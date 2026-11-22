package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/manovaspace/orbit-auth/internal/domain"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/sqlcgen"
)

// maxDisplayNameRunes bounds the display name copied from a Google profile.
const maxDisplayNameRunes = 100

var _ domain.OAuthStore = (*Store)(nil)

// CreateOAuthState stores one Google sign-in state by its hash, with its
// purpose and the invitation or account it carries. The ids are optional
// columns: the database checks which ones each purpose requires.
func (s *Store) CreateOAuthState(ctx context.Context, stateHash string, state domain.OAuthState, expiresAt time.Time) error {
	inviteID, err := optionalUUID(state.InviteID)
	if err != nil {
		return err
	}
	userID, err := optionalUUID(state.UserID)
	if err != nil {
		return err
	}
	return s.queries.CreateOAuthSignInState(ctx, sqlcgen.CreateOAuthSignInStateParams{
		StateHash:   stateHash,
		RedirectUri: state.RedirectURI,
		ReturnTo:    state.ReturnTo,
		ExpiresAt:   timestamptz(expiresAt),
		Purpose:     state.Purpose,
		InviteID:    inviteID,
		UserID:      userID,
	})
}

// ConsumeOAuthState deletes a live state and returns what it stored. A missing,
// used or expired state is domain.ErrInvalidGoogleState.
func (s *Store) ConsumeOAuthState(ctx context.Context, stateHash string, now time.Time) (domain.OAuthState, error) {
	row, err := s.queries.ConsumeOAuthSignInState(ctx, sqlcgen.ConsumeOAuthSignInStateParams{
		StateHash: stateHash,
		Now:       timestamptz(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OAuthState{}, domain.ErrInvalidGoogleState
	}
	if err != nil {
		return domain.OAuthState{}, err
	}
	return domain.OAuthState{
		RedirectURI: row.RedirectUri,
		ReturnTo:    row.ReturnTo,
		Purpose:     row.Purpose,
		InviteID:    row.InviteID,
		UserID:      row.UserID,
	}, nil
}

// DeleteExpiredOAuthStates removes states that expired at or before now.
func (s *Store) DeleteExpiredOAuthStates(ctx context.Context, now time.Time) error {
	_, err := s.queries.DeleteExpiredOAuthSignInStates(ctx, timestamptz(now))
	return err
}

// SignInWithIdentity returns the user for a verified Google identity. The rules
// are, in one transaction:
//
//  1. A known (provider, subject) returns its user and refreshes the identity's
//     last login and email. It is never relinked.
//  2. Otherwise, for an authoritative email, the accounts whose email matches
//     it case-insensitively are locked. One verified account without a Google
//     identity is linked, and identity.linked is written. An unverified account, or one that already has a
//     Google identity, is domain.ErrGoogleAccountNotAllowed. Two or more
//     matches (addresses that differ only by case) are also
//     domain.ErrGoogleAccountNotAllowed, because the holder would be a guess.
//  3. Otherwise a new active account is created when allowCreate is true, else
//     domain.ErrGoogleAccountNotAllowed. Its email is set and verified only when
//     the email is authoritative; user.created is emitted in the same
//     transaction.
//
// A unique violation, from a concurrent first sign-in of the same subject or
// email, rolls the transaction back and retries it once.
func (s *Store) SignInWithIdentity(ctx context.Context, provider string, id domain.GoogleIdentity, now time.Time, allowCreate bool) (domain.User, error) {
	user, err := s.signInWithIdentityOnce(ctx, provider, id, now, allowCreate)
	if isUniqueViolation(err) {
		user, err = s.signInWithIdentityOnce(ctx, provider, id, now, allowCreate)
	}
	return user, err
}

func (s *Store) signInWithIdentityOnce(ctx context.Context, provider string, id domain.GoogleIdentity, now time.Time, allowCreate bool) (domain.User, error) {
	email := domain.NormalizeEmail(id.Email)
	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, fmt.Errorf("postgres: begin google sign-in: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)

	identity := sqlcgen.UpdateGoogleIdentityLoginParams{
		Email:    emailPtr,
		Provider: provider,
		Subject:  id.Subject,
	}

	// 1. A known identity is never relinked to another account.
	userID, found, err := knownIdentityLogin(ctx, queries, identity)
	if err != nil {
		return domain.User{}, err
	}
	if found {
		return finishSignIn(ctx, tx, queries, userID)
	}

	// 2. An authoritative email links to a verified account that has no
	// identity of this provider yet.
	if domain.GoogleEmailIsAuthoritative(id) {
		holders, err := queries.LockUserByEmail(ctx, email)
		if err != nil {
			return domain.User{}, fmt.Errorf("postgres: lock user by email: %w", err)
		}
		if len(holders) > 0 {
			// The lock may have waited for, or rule 1 may have read before, a
			// concurrent first sign-in of the same subject. That sign-in committed
			// its identity, so the subject is known now and returns its user.
			userID, found, err := knownIdentityLogin(ctx, queries, identity)
			if err != nil {
				return domain.User{}, err
			}
			if found {
				return finishSignIn(ctx, tx, queries, userID)
			}
		}
		if len(holders) > 1 {
			return domain.User{}, domain.ErrGoogleAccountNotAllowed
		}
		if len(holders) == 1 {
			holder := holders[0]
			holderID, err := uuid.Parse(holder.ID)
			if err != nil {
				return domain.User{}, fmt.Errorf("postgres: user id %q: %w", holder.ID, err)
			}
			linked, err := queries.HasIdentityForProvider(ctx, sqlcgen.HasIdentityForProviderParams{
				Provider: provider,
				UserID:   holderID,
			})
			if err != nil {
				return domain.User{}, fmt.Errorf("postgres: check google identity: %w", err)
			}
			if !holder.EmailVerified || linked {
				return domain.User{}, domain.ErrGoogleAccountNotAllowed
			}
			if err := queries.InsertUserIdentity(ctx, sqlcgen.InsertUserIdentityParams{
				Provider: provider,
				Subject:  id.Subject,
				UserID:   holderID,
				Email:    emailPtr,
			}); err != nil {
				return domain.User{}, fmt.Errorf("postgres: link google identity: %w", err)
			}
			if err := identityLinkedAudit(ctx, queries, holder.ID, now); err != nil {
				return domain.User{}, err
			}
			return finishSignIn(ctx, tx, queries, holder.ID)
		}
	}

	// 3. Otherwise a new account, only while self-serve sign-up is open.
	if !allowCreate {
		return domain.User{}, domain.ErrGoogleAccountNotAllowed
	}
	newID := uuid.New()
	var newEmail *string
	if domain.GoogleEmailIsAuthoritative(id) {
		newEmail = emailPtr
	}
	if err := queries.CreateGoogleUser(ctx, sqlcgen.CreateGoogleUserParams{
		ID:          newID,
		Email:       newEmail,
		DisplayName: truncateRunes(strings.TrimSpace(id.Name), maxDisplayNameRunes),
	}); err != nil {
		return domain.User{}, fmt.Errorf("postgres: create google user: %w", err)
	}
	if err := queries.InsertUserIdentity(ctx, sqlcgen.InsertUserIdentityParams{
		Provider: provider,
		Subject:  id.Subject,
		UserID:   newID,
		Email:    emailPtr,
	}); err != nil {
		return domain.User{}, fmt.Errorf("postgres: insert google identity: %w", err)
	}
	if err := emitEvents(ctx, newOutboxSink(tx), domain.NewUserCreatedEvent(newID.String(), now)); err != nil {
		return domain.User{}, err
	}
	if err := userCreatedAudit(ctx, queries, newID.String(), domain.UserAuditActor(newID.String()), now); err != nil {
		return domain.User{}, err
	}
	return finishSignIn(ctx, tx, queries, newID.String())
}

// finishSignIn loads the user inside the transaction and commits it.
func finishSignIn(ctx context.Context, tx pgx.Tx, queries *sqlcgen.Queries, userID string) (domain.User, error) {
	parsed, err := uuid.Parse(userID)
	if err != nil {
		return domain.User{}, fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	row, err := queries.FindUserByID(ctx, parsed)
	if err != nil {
		return domain.User{}, fmt.Errorf("postgres: load google user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("postgres: commit google sign-in: %w", err)
	}
	return domain.User{
		ID:             row.ID,
		Email:          row.Email,
		Mobile:         row.Mobile,
		Username:       row.Username,
		PasswordHash:   row.PasswordHash,
		DisplayName:    row.DisplayName,
		Status:         row.Status,
		EmailVerified:  row.EmailVerified,
		MobileVerified: row.MobileVerified,
	}, nil
}

// knownIdentityLogin refreshes the login of an existing (provider, subject)
// identity and returns its user id. found is false when the identity does not
// exist yet.
func knownIdentityLogin(ctx context.Context, queries *sqlcgen.Queries, identity sqlcgen.UpdateGoogleIdentityLoginParams) (string, bool, error) {
	userID, err := queries.UpdateGoogleIdentityLogin(ctx, identity)
	switch {
	case err == nil:
		return userID, true, nil
	case errors.Is(err, pgx.ErrNoRows):
		return "", false, nil
	default:
		return "", false, fmt.Errorf("postgres: update google identity: %w", err)
	}
}

// isUniqueViolation reports whether err is a Postgres unique violation (23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// truncateRunes returns at most limit runes of value.
func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

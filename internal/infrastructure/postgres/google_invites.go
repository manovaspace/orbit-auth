package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/manovaspace/orbit-auth/internal/domain"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/sqlcgen"
)

// AcceptInviteWithIdentity accepts a pending email invitation for the account a
// verified Google identity signs in to, in one transaction. The lock order is
// the one every write uses: the account rows, then the organization, then the
// invitation. The invitation is read before any lock only to learn its
// organization and email; it is locked and checked again under the
// organization lock, so a concurrent acceptance cannot both succeed.
//
// The account rules are in resolveInviteAccount. A unique violation, from a
// concurrent first sign-in or sign-up of the same address, rolls the transaction
// back and retries it once.
func (s *Store) AcceptInviteWithIdentity(ctx context.Context, provider string, id domain.GoogleIdentity, inviteID string, now time.Time, newUserID string, allowSignup bool) (domain.InviteAcceptance, error) {
	acceptance, err := s.acceptInviteWithIdentityOnce(ctx, provider, id, inviteID, now, newUserID, allowSignup)
	if isUniqueViolation(err) {
		acceptance, err = s.acceptInviteWithIdentityOnce(ctx, provider, id, inviteID, now, newUserID, allowSignup)
	}
	return acceptance, err
}

func (s *Store) acceptInviteWithIdentityOnce(ctx context.Context, provider string, id domain.GoogleIdentity, inviteID string, now time.Time, newUserID string, allowSignup bool) (domain.InviteAcceptance, error) {
	invite, err := uuid.Parse(inviteID)
	if err != nil {
		return domain.InviteAcceptance{}, domain.ErrInviteNotFound
	}
	newID, err := uuid.Parse(newUserID)
	if err != nil {
		return domain.InviteAcceptance{}, fmt.Errorf("postgres: new user id %q: %w", newUserID, err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.InviteAcceptance{}, fmt.Errorf("postgres: begin google invite: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)

	pre, err := queries.GetOrganizationInviteByID(ctx, invite)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InviteAcceptance{}, domain.ErrInviteNotFound
	}
	if err != nil {
		return domain.InviteAcceptance{}, fmt.Errorf("postgres: read google invite: %w", err)
	}
	if !inviteOpen(pre.Status, pre.ExpiresAt, now) || domain.NormalizeEmail(pre.Email) == "" {
		return domain.InviteAcceptance{}, domain.ErrInviteNotFound
	}
	org, err := uuid.Parse(pre.OrgID)
	if err != nil {
		return domain.InviteAcceptance{}, fmt.Errorf("postgres: organization id %q: %w", pre.OrgID, err)
	}

	// The account is locked before the organization, as CreateInviteSignup does.
	holderID, found, err := knownGoogleAccount(ctx, queries, provider, id.Subject)
	if err != nil {
		return domain.InviteAcceptance{}, err
	}
	var holders []sqlcgen.LockUserByEmailRow
	if found {
		if _, err := queries.LockUserForUpdate(ctx, holderID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.InviteAcceptance{}, domain.ErrGoogleAccountNotAllowed
			}
			return domain.InviteAcceptance{}, fmt.Errorf("postgres: lock google account: %w", err)
		}
	} else {
		// An unknown subject is taken over or linked only through a verified
		// Google email that is the invited address. Nothing is locked before that
		// check passes.
		if !id.EmailVerified || domain.NormalizeEmail(id.Email) != domain.NormalizeEmail(pre.Email) {
			return domain.InviteAcceptance{}, domain.ErrInviteEmailMismatch
		}
		holders, err = queries.LockUserByEmail(ctx, domain.NormalizeEmail(pre.Email))
		if err != nil {
			return domain.InviteAcceptance{}, fmt.Errorf("postgres: lock accounts by invited email: %w", err)
		}
	}

	if err := lockOrganization(ctx, queries, org); err != nil {
		return domain.InviteAcceptance{}, err
	}
	locked, err := queries.LockOrganizationInviteForAccept(ctx, invite)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InviteAcceptance{}, domain.ErrInviteNotFound
	}
	if err != nil {
		return domain.InviteAcceptance{}, fmt.Errorf("postgres: lock google invite: %w", err)
	}
	if !inviteOpen(locked.Status, locked.ExpiresAt, now) || locked.Email != pre.Email || locked.OrgID != pre.OrgID {
		return domain.InviteAcceptance{}, domain.ErrInviteNotFound
	}

	accountID, err := resolveInviteAccount(ctx, tx, queries, inviteAccount{
		provider:    provider,
		identity:    id,
		invitedMail: domain.NormalizeEmail(pre.Email),
		known:       found,
		knownID:     holderID,
		holders:     holders,
		newID:       newID,
		displayName: truncateRunes(strings.TrimSpace(id.Name), maxDisplayNameRunes),
		allowSignup: allowSignup,
		at:          now,
	})
	if err != nil {
		return domain.InviteAcceptance{}, err
	}
	role, newMember, err := acceptInviteUnderLock(ctx, tx, queries, org, invite, accountID.String(), domain.MembershipRole(locked.Role), now)
	if err != nil {
		return domain.InviteAcceptance{}, err
	}
	user, err := loadUser(ctx, queries, accountID)
	if err != nil {
		return domain.InviteAcceptance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.InviteAcceptance{}, fmt.Errorf("postgres: commit google invite: %w", err)
	}
	return domain.InviteAcceptance{
		User:          user,
		OrgID:         org.String(),
		Role:          role,
		AlreadyMember: !newMember,
	}, nil
}

// inviteAccount is the input of resolveInviteAccount: the verified identity,
// the invited address, the accounts found for the subject or the address, and
// the policy inputs.
type inviteAccount struct {
	provider    string
	identity    domain.GoogleIdentity
	invitedMail string
	// known is true when the subject already has an identity; knownID is its
	// account and the account is already locked.
	known   bool
	knownID uuid.UUID
	// holders are the accounts with the invited address, locked by email.
	holders     []sqlcgen.LockUserByEmailRow
	newID       uuid.UUID
	displayName string
	allowSignup bool
	at          time.Time
}

// resolveInviteAccount returns the id of the account the invitation is accepted
// for, and writes what the account needs in the caller's transaction. Every
// refusal writes nothing.
//
// A known subject must belong to an active account whose verified email is the
// invited address, or whose Google email is verified and is the invited address
// (the identity's last login and email are then refreshed). Otherwise the
// subject is unknown: its verified Google email must be the invited address;
// then no account with the address is a new account (user.created), a
// signup_pending account is taken over (no user.created), an active verified
// account without a Google identity is linked when the Google email is
// authoritative, and everything else is ErrGoogleAccountNotAllowed.
func resolveInviteAccount(ctx context.Context, tx pgx.Tx, queries *sqlcgen.Queries, in inviteAccount) (uuid.UUID, error) {
	if in.known {
		return resolveKnownInviteAccount(ctx, queries, in)
	}
	if len(in.holders) > 1 {
		return uuid.Nil, domain.ErrGoogleAccountNotAllowed
	}
	emailPtr := &in.invitedMail
	if len(in.holders) == 0 {
		if !in.allowSignup {
			return uuid.Nil, domain.ErrInviteSignupDisabled
		}
		if err := queries.CreateGoogleUser(ctx, sqlcgen.CreateGoogleUserParams{
			ID:          in.newID,
			Email:       emailPtr,
			DisplayName: in.displayName,
		}); err != nil {
			return uuid.Nil, fmt.Errorf("postgres: create invited google user: %w", err)
		}
		if err := insertGoogleIdentity(ctx, queries, in, in.newID); err != nil {
			return uuid.Nil, err
		}
		if err := emitEvents(ctx, newOutboxSink(tx), domain.NewUserCreatedEvent(in.newID.String(), in.at)); err != nil {
			return uuid.Nil, err
		}
		if err := userCreatedAudit(ctx, queries, in.newID.String(), domain.UserAuditActor(in.newID.String()), in.at); err != nil {
			return uuid.Nil, err
		}
		return in.newID, nil
	}

	holderID, err := uuid.Parse(in.holders[0].ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("postgres: user id %q: %w", in.holders[0].ID, err)
	}
	holder, err := loadUser(ctx, queries, holderID)
	if err != nil {
		return uuid.Nil, err
	}
	linked, err := queries.HasIdentityForProvider(ctx, sqlcgen.HasIdentityForProviderParams{
		Provider: in.provider,
		UserID:   holderID,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("postgres: check google identity: %w", err)
	}
	switch {
	case holder.Status == domain.UserSignupPending && !linked:
		if !in.allowSignup {
			return uuid.Nil, domain.ErrInviteSignupDisabled
		}
		if _, err := queries.ActivatePendingUserForGoogle(ctx, sqlcgen.ActivatePendingUserForGoogleParams{
			ID:          holderID,
			DisplayName: in.displayName,
		}); err != nil {
			return uuid.Nil, fmt.Errorf("postgres: take over pending google account: %w", err)
		}
		if err := insertGoogleIdentity(ctx, queries, in, holderID); err != nil {
			return uuid.Nil, err
		}
		if err := identityLinkedAudit(ctx, queries, holderID.String(), in.at); err != nil {
			return uuid.Nil, err
		}
		return holderID, nil
	case holder.Status == domain.UserActive && holder.EmailVerified && !linked && domain.GoogleEmailIsAuthoritative(in.identity):
		if err := insertGoogleIdentity(ctx, queries, in, holderID); err != nil {
			return uuid.Nil, err
		}
		if err := identityLinkedAudit(ctx, queries, holderID.String(), in.at); err != nil {
			return uuid.Nil, err
		}
		return holderID, nil
	default:
		return uuid.Nil, domain.ErrGoogleAccountNotAllowed
	}
}

// resolveKnownInviteAccount is the known-subject branch of resolveInviteAccount.
func resolveKnownInviteAccount(ctx context.Context, queries *sqlcgen.Queries, in inviteAccount) (uuid.UUID, error) {
	holderID := in.knownID
	holder, err := loadUser(ctx, queries, holderID)
	if err != nil {
		return uuid.Nil, err
	}
	if holder.Status != domain.UserActive {
		return uuid.Nil, domain.ErrGoogleAccountNotAllowed
	}
	verifiedMatch := holder.EmailVerified && domain.NormalizeEmail(holder.Email) == in.invitedMail
	googleMatch := in.identity.EmailVerified && domain.NormalizeEmail(in.identity.Email) == in.invitedMail
	if !verifiedMatch && !googleMatch {
		return uuid.Nil, domain.ErrInviteEmailMismatch
	}
	if _, err := queries.UpdateGoogleIdentityLogin(ctx, sqlcgen.UpdateGoogleIdentityLoginParams{
		Email:    nullableEmail(in.identity.Email),
		Provider: in.provider,
		Subject:  in.identity.Subject,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("postgres: refresh google identity: %w", err)
	}
	return holderID, nil
}

// insertGoogleIdentity writes the identity row of a verified subject for
// userID. The Google email is stored as the identity's email.
func insertGoogleIdentity(ctx context.Context, queries *sqlcgen.Queries, in inviteAccount, userID uuid.UUID) error {
	if err := queries.InsertUserIdentity(ctx, sqlcgen.InsertUserIdentityParams{
		Provider: in.provider,
		Subject:  in.identity.Subject,
		UserID:   userID,
		Email:    nullableEmail(in.identity.Email),
	}); err != nil {
		return fmt.Errorf("postgres: insert google identity: %w", err)
	}
	return nil
}

// nullableEmail normalises a Google email, or returns nil when it is empty.
func nullableEmail(email string) *string {
	normalized := domain.NormalizeEmail(email)
	if normalized == "" {
		return nil
	}
	return &normalized
}

// inviteOpen reports whether an invitation is pending and not expired at now.
func inviteOpen(status string, expiresAt pgtype.Timestamptz, now time.Time) bool {
	return status == string(domain.InvitePending) && now.Before(expiresAt.Time)
}

// knownGoogleAccount returns the account a Google subject is linked to. found is
// false when the subject has no identity.
func knownGoogleAccount(ctx context.Context, queries *sqlcgen.Queries, provider, subject string) (uuid.UUID, bool, error) {
	id, err := queries.FindGoogleIdentityUser(ctx, sqlcgen.FindGoogleIdentityUserParams{
		Provider: provider,
		Subject:  subject,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("postgres: find google identity: %w", err)
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("postgres: user id %q: %w", id, err)
	}
	return parsed, true, nil
}

// loadUser reads the account userID inside the caller's transaction.
func loadUser(ctx context.Context, queries *sqlcgen.Queries, userID uuid.UUID) (domain.User, error) {
	row, err := queries.FindUserByID(ctx, userID)
	if err != nil {
		return domain.User{}, fmt.Errorf("postgres: load user: %w", err)
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

// identityLinkedAudit writes identity.linked for a Google identity added to an
// existing account. The account is both the actor and the subject.
func identityLinkedAudit(ctx context.Context, queries *sqlcgen.Queries, userID string, at time.Time) error {
	return writeAudit(ctx, queries, domain.AuditEntry{
		Action:        domain.AuditIdentityLinked,
		Actor:         domain.UserAuditActor(userID),
		SubjectUserID: userID,
		Details:       map[string]any{"provider": domain.GoogleProviderName},
		OccurredAt:    at,
	})
}

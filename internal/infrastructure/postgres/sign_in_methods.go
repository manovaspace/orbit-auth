package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/manovaspace/orbit-auth/internal/domain"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/sqlcgen"
)

var _ domain.SignInMethodStore = (*Store)(nil)

// GoogleLinkForUser returns the Google identity linked to userID, without its
// subject. found is false when the account has none.
func (s *Store) GoogleLinkForUser(ctx context.Context, provider, userID string) (domain.GoogleLink, bool, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return domain.GoogleLink{}, false, fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	row, err := s.queries.FindGoogleIdentityForUser(ctx, sqlcgen.FindGoogleIdentityForUserParams{
		Provider: provider,
		UserID:   uid,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.GoogleLink{}, false, nil
	}
	if err != nil {
		return domain.GoogleLink{}, false, fmt.Errorf("postgres: find google identity: %w", err)
	}
	return domain.GoogleLink{
		Email:       textValue(row.Email),
		LinkedAt:    row.CreatedAt.Time,
		LastLoginAt: row.LastLoginAt.Time,
	}, true, nil
}

// LinkGoogleIdentity links a verified Google subject to an active account in one
// transaction. The account row is locked first. A subject held by another
// account is ErrGoogleIdentityTaken; an account that already has a Google
// identity, or a subject it already holds, is ErrGoogleAlreadyLinked. A unique
// violation on the insert, from a concurrent link of the same subject, is
// ErrGoogleIdentityTaken.
func (s *Store) LinkGoogleIdentity(ctx context.Context, provider string, id domain.GoogleIdentity, userID string, at time.Time) (string, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("postgres: begin link google: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)

	status, err := queries.LockUserForUpdate(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("postgres: lock account for google link: %w", err)
	}
	if status != domain.UserActive {
		return "", domain.ErrAccountNotActive
	}
	owner, found, err := knownGoogleAccount(ctx, queries, provider, id.Subject)
	if err != nil {
		return "", err
	}
	if found {
		if owner != uid {
			return "", domain.ErrGoogleIdentityTaken
		}
		return "", domain.ErrGoogleAlreadyLinked
	}
	linked, err := queries.HasIdentityForProvider(ctx, sqlcgen.HasIdentityForProviderParams{
		Provider: provider,
		UserID:   uid,
	})
	if err != nil {
		return "", fmt.Errorf("postgres: check google identity: %w", err)
	}
	if linked {
		return "", domain.ErrGoogleAlreadyLinked
	}
	if err := queries.InsertUserIdentity(ctx, sqlcgen.InsertUserIdentityParams{
		Provider: provider,
		Subject:  id.Subject,
		UserID:   uid,
		Email:    nullableEmail(id.Email),
	}); err != nil {
		if isUniqueViolation(err) {
			return "", domain.ErrGoogleIdentityTaken
		}
		return "", fmt.Errorf("postgres: link google identity: %w", err)
	}
	if err := identityLinkedAudit(ctx, queries, userID, at); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("postgres: commit link google: %w", err)
	}
	return domain.NormalizeEmail(id.Email), nil
}

// UnlinkGoogleIdentity removes the Google identity of an active account in one
// transaction. It is ErrGoogleNotLinked when there is none. It is
// ErrLastSignInMethod when the account has no usable password afterwards, so
// the account always keeps one way to sign in. identity.unlinked is written with
// the delete.
func (s *Store) UnlinkGoogleIdentity(ctx context.Context, provider, userID string, by domain.AuditActor, at time.Time) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin unlink google: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)

	status, err := queries.LockUserForUpdate(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: lock account for google unlink: %w", err)
	}
	if status != domain.UserActive {
		return domain.ErrAccountNotActive
	}
	if _, err := queries.FindGoogleIdentityForUser(ctx, sqlcgen.FindGoogleIdentityForUserParams{
		Provider: provider,
		UserID:   uid,
	}); errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrGoogleNotLinked
	} else if err != nil {
		return fmt.Errorf("postgres: find google identity: %w", err)
	}
	user, err := loadUser(ctx, queries, uid)
	if err != nil {
		return err
	}
	if !domain.HasUsablePassword(user) {
		return domain.ErrLastSignInMethod
	}
	affected, err := queries.DeleteGoogleIdentityForUser(ctx, sqlcgen.DeleteGoogleIdentityForUserParams{
		Provider: provider,
		UserID:   uid,
	})
	if err != nil {
		return fmt.Errorf("postgres: delete google identity: %w", err)
	}
	if affected == 0 {
		return domain.ErrGoogleNotLinked
	}
	if err := writeAudit(ctx, queries, domain.AuditEntry{
		Action:        domain.AuditIdentityUnlinked,
		Actor:         by,
		SubjectUserID: userID,
		Details:       map[string]any{"provider": domain.GoogleProviderName},
		OccurredAt:    at,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit unlink google: %w", err)
	}
	return nil
}

// SetPassword stores a new password hash for an active account, revokes its
// other live refresh tokens and writes password.set, in one transaction. The
// account row is locked first, so a concurrent disable either commits first
// and the update finds no active account, or waits.
func (s *Store) SetPassword(ctx context.Context, userID, passwordHash, keepTokenHash string, first bool, by domain.AuditActor, at time.Time) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin set password: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)

	if err := lockActiveAccount(ctx, queries, uid); err != nil {
		return err
	}
	affected, err := queries.SetUserPasswordHash(ctx, sqlcgen.SetUserPasswordHashParams{
		PasswordHash: passwordHash,
		ID:           uid,
	})
	if err != nil {
		return fmt.Errorf("postgres: set password: %w", err)
	}
	if affected == 0 {
		return domain.ErrAccountNotActive
	}
	if _, err := queries.RevokeUserRefreshTokensExcept(ctx, sqlcgen.RevokeUserRefreshTokensExceptParams{
		UserID:   uid,
		KeepHash: keepTokenHash,
	}); err != nil {
		return fmt.Errorf("postgres: revoke other sessions: %w", err)
	}
	if err := writeAudit(ctx, queries, domain.AuditEntry{
		Action:        domain.AuditPasswordSet,
		Actor:         by,
		SubjectUserID: userID,
		Details:       map[string]any{"first": first},
		OccurredAt:    at,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit set password: %w", err)
	}
	return nil
}

// RevokeSessions revokes every live refresh token of an active account except
// keepTokenHash, and writes sessions.revoked, in one transaction.
func (s *Store) RevokeSessions(ctx context.Context, userID, keepTokenHash string, by domain.AuditActor, at time.Time) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin revoke sessions: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)

	if err := lockActiveAccount(ctx, queries, uid); err != nil {
		return err
	}
	if _, err := queries.RevokeUserRefreshTokensExcept(ctx, sqlcgen.RevokeUserRefreshTokensExceptParams{
		UserID:   uid,
		KeepHash: keepTokenHash,
	}); err != nil {
		return fmt.Errorf("postgres: revoke sessions: %w", err)
	}
	if err := writeAudit(ctx, queries, domain.AuditEntry{
		Action:        domain.AuditSessionsRevoked,
		Actor:         by,
		SubjectUserID: userID,
		Details:       map[string]any{"kept_current": keepTokenHash != ""},
		OccurredAt:    at,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit revoke sessions: %w", err)
	}
	return nil
}

// lockActiveAccount locks the account row and returns domain.ErrAccountNotActive
// unless it is active, or domain.ErrUserNotFound when it does not exist.
func lockActiveAccount(ctx context.Context, queries *sqlcgen.Queries, userID uuid.UUID) error {
	status, err := queries.LockUserForUpdate(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: lock account: %w", err)
	}
	if status != domain.UserActive {
		return domain.ErrAccountNotActive
	}
	return nil
}

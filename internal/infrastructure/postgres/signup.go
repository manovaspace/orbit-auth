package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/manovaspace/orbit-auth/internal/domain"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/sqlcgen"
)

// CreateSignupUser writes a self-serve account and, in the same transaction,
// either creates the organization it owns or accepts the invitation that
// proved the address. ON CONFLICT (email) DO NOTHING makes duplicate
// registrations a no-op without revealing that the address exists.
func (s *Store) CreateSignupUser(ctx context.Context, user domain.User, org *domain.Organization, invite *domain.Invite, acceptedAt time.Time) (bool, error) {
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		return false, fmt.Errorf("postgres: user id %q: %w", user.ID, err)
	}

	var verifiedAt pgtype.Timestamptz
	if user.EmailVerified {
		verifiedAt = timestamptz(acceptedAt)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("postgres: begin signup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	created, err := queries.CreateSignupUser(ctx, sqlcgen.CreateSignupUserParams{
		ID:              userID,
		Email:           user.Email,
		PasswordHash:    user.PasswordHash,
		DisplayName:     user.DisplayName,
		Status:          user.Status,
		EmailVerifiedAt: verifiedAt,
	})
	if err != nil {
		return false, fmt.Errorf("postgres: create signup user: %w", err)
	}
	if created == 0 {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("postgres: commit duplicate signup: %w", err)
		}
		return false, nil
	}

	occurredAt := acceptedAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	sink := newOutboxSink(tx)
	if err := emitEvents(ctx, sink,
		domain.NewUserCreatedEvent(user.ID, occurredAt),
	); err != nil {
		return false, err
	}

	if invite != nil {
		orgID, err := uuid.Parse(invite.OrgID)
		if err != nil {
			return false, fmt.Errorf("postgres: organization id %q: %w", invite.OrgID, err)
		}
		inviteID, err := uuid.Parse(invite.ID)
		if err != nil {
			return false, fmt.Errorf("postgres: invite id %q: %w", invite.ID, err)
		}
		if err := lockOrganization(ctx, queries, orgID); err != nil {
			return false, err
		}
		affected, err := queries.AcceptOrganizationInvite(ctx, sqlcgen.AcceptOrganizationInviteParams{
			ID:         inviteID,
			OrgID:      orgID,
			AcceptedAt: timestamptz(acceptedAt),
		})
		if err != nil {
			return false, fmt.Errorf("postgres: accept signup invite: %w", err)
		}
		if affected == 0 {
			return false, domain.ErrInviteNotFound
		}
		if err := queries.AddActiveOrganizationMembership(ctx, sqlcgen.AddActiveOrganizationMembershipParams{
			OrgID:  orgID,
			UserID: userID,
			Role:   string(invite.Role),
		}); err != nil {
			return false, fmt.Errorf("postgres: add signup invite member: %w", err)
		}
		if err := emitEvents(ctx, sink,
			domain.NewMembershipAddedEvent(invite.OrgID, user.ID, domain.MembershipActive, invite.Role, occurredAt),
		); err != nil {
			return false, err
		}
	}

	if org != nil {
		orgID, err := uuid.Parse(org.ID)
		if err != nil {
			return false, fmt.Errorf("postgres: organization id %q: %w", org.ID, err)
		}
		if err := createOrganizationTx(ctx, queries, sink, *org, orgID, userID, occurredAt); err != nil {
			return false, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("postgres: commit signup: %w", err)
	}
	return true, nil
}

// CreatePhoneInviteSignup writes an active account for a phone invitation and,
// in the same transaction, emits user.created, accepts the invitation and adds
// the active membership with the invited role. The account's mobile number is
// the invited number and stays unverified. ON CONFLICT (mobile) DO NOTHING makes
// a number that already belongs to an account a no-op (created=false), and a
// non-pending invitation rolls the whole transaction back, so no user row is
// left behind without its membership.
func (s *Store) CreatePhoneInviteSignup(ctx context.Context, user domain.User, invite domain.Invite, acceptedAt time.Time) (bool, error) {
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		return false, fmt.Errorf("postgres: user id %q: %w", user.ID, err)
	}
	orgID, inviteID, err := inviteIDs(invite.OrgID, invite.ID)
	if err != nil {
		return false, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("postgres: begin phone invite signup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	created, err := queries.CreatePhoneSignupUser(ctx, sqlcgen.CreatePhoneSignupUserParams{
		ID:           userID,
		Mobile:       user.Mobile,
		PasswordHash: user.PasswordHash,
		DisplayName:  user.DisplayName,
		Status:       domain.UserActive,
	})
	if err != nil {
		return false, fmt.Errorf("postgres: create phone signup user: %w", err)
	}
	if created == 0 {
		return false, nil
	}

	sink := newOutboxSink(tx)
	if err := emitEvents(ctx, sink,
		domain.NewUserCreatedEvent(user.ID, acceptedAt),
	); err != nil {
		return false, err
	}
	if err := lockOrganization(ctx, queries, orgID); err != nil {
		return false, err
	}
	affected, err := queries.AcceptOrganizationInvite(ctx, sqlcgen.AcceptOrganizationInviteParams{
		ID:         inviteID,
		OrgID:      orgID,
		AcceptedAt: timestamptz(acceptedAt),
	})
	if err != nil {
		return false, fmt.Errorf("postgres: accept phone signup invite: %w", err)
	}
	if affected == 0 {
		return false, domain.ErrInviteNotFound
	}
	if err := queries.AddActiveOrganizationMembership(ctx, sqlcgen.AddActiveOrganizationMembershipParams{
		OrgID:  orgID,
		UserID: userID,
		Role:   string(invite.Role),
	}); err != nil {
		return false, fmt.Errorf("postgres: add phone signup member: %w", err)
	}
	if err := emitEvents(ctx, sink,
		domain.NewMembershipAddedEvent(invite.OrgID, user.ID, domain.MembershipActive, invite.Role, acceptedAt),
	); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("postgres: commit phone invite signup: %w", err)
	}
	return true, nil
}

// ReissuePendingSignup replaces the password and display name of an existing
// unverified sign-up. A non-nil challenge rotates the address's email OTP in
// the same transaction; a non-nil invite activates the account and accepts the
// invitation instead. An active or missing account is left untouched and
// returns reissued=false.
func (s *Store) ReissuePendingSignup(ctx context.Context, user domain.User, challenge *domain.OTPChallenge, invite *domain.Invite, acceptedAt time.Time) (string, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("postgres: begin reissue signup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	userID, err := queries.ReissueSignupUser(ctx, sqlcgen.ReissueSignupUserParams{
		Email:        user.Email,
		PasswordHash: user.PasswordHash,
		DisplayName:  user.DisplayName,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("postgres: reissue signup user: %w", err)
	}

	if challenge != nil {
		if err := queries.DeleteChallengesForIdentifier(ctx, sqlcgen.DeleteChallengesForIdentifierParams{
			Identifier: challenge.Identifier,
			Channel:    challenge.Channel,
		}); err != nil {
			return "", false, fmt.Errorf("postgres: invalidate signup challenges: %w", err)
		}
		challengeID, err := uuid.Parse(challenge.ID)
		if err != nil {
			return "", false, fmt.Errorf("postgres: challenge id %q: %w", challenge.ID, err)
		}
		createdAt := challenge.CreatedAt
		if createdAt.IsZero() {
			createdAt = acceptedAt
		}
		if err := queries.UpsertChallenge(ctx, sqlcgen.UpsertChallengeParams{
			ID:         challengeID,
			Identifier: challenge.Identifier,
			Channel:    challenge.Channel,
			CodeHash:   challenge.CodeHash,
			ExpiresAt:  timestamptz(challenge.ExpiresAt),
			CreatedAt:  timestamptz(createdAt),
		}); err != nil {
			return "", false, fmt.Errorf("postgres: write signup challenge: %w", err)
		}
	}

	if invite != nil {
		userUUID, err := uuid.Parse(userID)
		if err != nil {
			return "", false, fmt.Errorf("postgres: user id %q: %w", userID, err)
		}
		orgID, err := uuid.Parse(invite.OrgID)
		if err != nil {
			return "", false, fmt.Errorf("postgres: organization id %q: %w", invite.OrgID, err)
		}
		inviteID, err := uuid.Parse(invite.ID)
		if err != nil {
			return "", false, fmt.Errorf("postgres: invite id %q: %w", invite.ID, err)
		}
		affected, err := queries.VerifySignupUser(ctx, sqlcgen.VerifySignupUserParams{
			ID:              userUUID,
			EmailVerifiedAt: timestamptz(acceptedAt),
		})
		if err != nil {
			return "", false, fmt.Errorf("postgres: activate signup invite: %w", err)
		}
		if affected == 0 {
			return "", false, nil
		}
		if err := lockOrganization(ctx, queries, orgID); err != nil {
			return "", false, err
		}
		affected, err = queries.AcceptOrganizationInvite(ctx, sqlcgen.AcceptOrganizationInviteParams{
			ID:         inviteID,
			OrgID:      orgID,
			AcceptedAt: timestamptz(acceptedAt),
		})
		if err != nil {
			return "", false, fmt.Errorf("postgres: accept signup invite: %w", err)
		}
		if affected == 0 {
			return "", false, domain.ErrInviteNotFound
		}
		if err := queries.AddActiveOrganizationMembership(ctx, sqlcgen.AddActiveOrganizationMembershipParams{
			OrgID:  orgID,
			UserID: userUUID,
			Role:   string(invite.Role),
		}); err != nil {
			return "", false, fmt.Errorf("postgres: add signup invite member: %w", err)
		}
		if err := emitEvents(ctx, newOutboxSink(tx),
			domain.NewMembershipAddedEvent(invite.OrgID, userID, domain.MembershipActive, invite.Role, acceptedAt),
		); err != nil {
			return "", false, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("postgres: commit reissue signup: %w", err)
	}
	return userID, true, nil
}

// VerifySignup activates one pending sign-up account. It only updates a row
// that is still signup_pending and unverified, so a second verification cannot
// revive a different account state.
func (s *Store) VerifySignup(ctx context.Context, userID string, verifiedAt time.Time) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	affected, err := s.queries.VerifySignupUser(ctx, sqlcgen.VerifySignupUserParams{
		ID:              id,
		EmailVerifiedAt: timestamptz(verifiedAt),
	})
	if err != nil {
		return fmt.Errorf("postgres: verify signup: %w", err)
	}
	if affected == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

// PurgeUnverifiedSignups deletes pending, unverified sign-ups created before
// the cutoff and returns how many rows were removed. The membership cascades
// with the user; organizations left without any member (the ones created by a
// sign-up with org_name) are removed in the same transaction.
func (s *Store) PurgeUnverifiedSignups(ctx context.Context, before time.Time) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin purge signups: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	removed, err := queries.PurgeUnverifiedSignups(ctx, timestamptz(before))
	if err != nil {
		return 0, fmt.Errorf("postgres: purge unverified signups: %w", err)
	}
	if _, err := queries.DeleteEmptyOrganizations(ctx); err != nil {
		return 0, fmt.Errorf("postgres: purge empty signup organizations: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres: commit purge signups: %w", err)
	}
	return removed, nil
}

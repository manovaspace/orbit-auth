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
	self := domain.UserAuditActor(user.ID)
	if err := userCreatedAudit(ctx, queries, user.ID, self, occurredAt); err != nil {
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
		if err := inviteAcceptedAudit(ctx, queries, invite.OrgID, invite.ID, user.ID, invite.Role, true, self, occurredAt); err != nil {
			return false, err
		}
		if err := emitEvents(ctx, sink,
			domain.NewMembershipAddedEvent(invite.OrgID, user.ID, domain.MembershipActive, invite.Role, occurredAt),
		); err != nil {
			return false, err
		}
	}

	if org != nil {
		if err := createOrganizationTx(ctx, queries, sink, *org, userID, occurredAt); err != nil {
			return false, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("postgres: commit signup: %w", err)
	}
	return true, nil
}

// CreateInviteSignup writes the account for an invitation and, in one
// transaction, accepts the invitation and adds the active membership with the
// invited role. A phone invitation inserts the account with the invited number,
// with mobile_verified_at set to acceptedAt only when user.MobileVerified is
// true; ON CONFLICT (mobile) DO NOTHING makes a number that already belongs to
// an account a no-op. An email invitation inserts the account with
// email_verified_at set; if the email belongs to a signup_pending account that
// account is taken over (password, display name, status active, verification
// time replaced) and no user.created is emitted, as for the existing pending
// sign-up path. An active account with the email is a no-op. A no-op returns
// created=false before anything else is written, and a non-pending invitation
// rolls the whole transaction back, so no account is left without its
// membership.
func (s *Store) CreateInviteSignup(ctx context.Context, user domain.User, invite domain.Invite, acceptedAt time.Time) (string, bool, error) {
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		return "", false, fmt.Errorf("postgres: user id %q: %w", user.ID, err)
	}
	orgID, inviteID, err := inviteIDs(invite.OrgID, invite.ID)
	if err != nil {
		return "", false, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("postgres: begin invite signup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	holderID, inserted, err := writeInviteAccount(ctx, queries, user, invite, userID, acceptedAt)
	if err != nil {
		return "", false, err
	}
	if holderID == "" {
		return "", false, nil
	}
	holderUUID, err := uuid.Parse(holderID)
	if err != nil {
		return "", false, fmt.Errorf("postgres: account id %q: %w", holderID, err)
	}

	sink := newOutboxSink(tx)
	self := domain.UserAuditActor(holderID)
	if inserted {
		if err := emitEvents(ctx, sink,
			domain.NewUserCreatedEvent(holderID, acceptedAt),
		); err != nil {
			return "", false, err
		}
		if err := userCreatedAudit(ctx, queries, holderID, self, acceptedAt); err != nil {
			return "", false, err
		}
	}
	if err := lockOrganization(ctx, queries, orgID); err != nil {
		return "", false, err
	}
	affected, err := queries.AcceptOrganizationInvite(ctx, sqlcgen.AcceptOrganizationInviteParams{
		ID:         inviteID,
		OrgID:      orgID,
		AcceptedAt: timestamptz(acceptedAt),
	})
	if err != nil {
		return "", false, fmt.Errorf("postgres: accept invite signup: %w", err)
	}
	if affected == 0 {
		return "", false, domain.ErrInviteNotFound
	}
	newMember, err := isNewMember(ctx, queries, orgID, holderUUID)
	if err != nil {
		return "", false, err
	}
	if err := queries.AddActiveOrganizationMembership(ctx, sqlcgen.AddActiveOrganizationMembershipParams{
		OrgID:  orgID,
		UserID: holderUUID,
		Role:   string(invite.Role),
	}); err != nil {
		return "", false, fmt.Errorf("postgres: add invite signup member: %w", err)
	}
	if err := inviteAcceptedAudit(ctx, queries, invite.OrgID, invite.ID, holderID, invite.Role, newMember, self, acceptedAt); err != nil {
		return "", false, err
	}
	if err := emitEvents(ctx, sink,
		domain.NewMembershipAddedEvent(invite.OrgID, holderID, domain.MembershipActive, invite.Role, acceptedAt),
	); err != nil {
		return "", false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("postgres: commit invite signup: %w", err)
	}
	return holderID, true, nil
}

// writeInviteAccount writes the account row for CreateInviteSignup. It returns
// the id of the account that holds the invitation, and inserted=true only when
// that row was newly written. An empty id means nothing was written: the phone
// number or an active email already belongs to an account.
func writeInviteAccount(ctx context.Context, queries *sqlcgen.Queries, user domain.User, invite domain.Invite, userID uuid.UUID, acceptedAt time.Time) (string, bool, error) {
	if invite.Phone != "" {
		var mobileVerifiedAt pgtype.Timestamptz
		if user.MobileVerified {
			mobileVerifiedAt = timestamptz(acceptedAt)
		}
		created, err := queries.CreatePhoneSignupUser(ctx, sqlcgen.CreatePhoneSignupUserParams{
			ID:               userID,
			Mobile:           user.Mobile,
			PasswordHash:     user.PasswordHash,
			DisplayName:      user.DisplayName,
			Status:           domain.UserActive,
			MobileVerifiedAt: mobileVerifiedAt,
		})
		if err != nil {
			return "", false, fmt.Errorf("postgres: create phone signup user: %w", err)
		}
		if created == 0 {
			return "", false, nil
		}
		return user.ID, true, nil
	}

	created, err := queries.CreateSignupUser(ctx, sqlcgen.CreateSignupUserParams{
		ID:              userID,
		Email:           user.Email,
		PasswordHash:    user.PasswordHash,
		DisplayName:     user.DisplayName,
		Status:          domain.UserActive,
		EmailVerifiedAt: timestamptz(acceptedAt),
	})
	if err != nil {
		return "", false, fmt.Errorf("postgres: create invite signup user: %w", err)
	}
	if created > 0 {
		return user.ID, true, nil
	}
	holderID, err := queries.TakeOverPendingSignupUser(ctx, sqlcgen.TakeOverPendingSignupUserParams{
		PasswordHash:    user.PasswordHash,
		DisplayName:     user.DisplayName,
		EmailVerifiedAt: timestamptz(acceptedAt),
		Email:           user.Email,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("postgres: take over pending signup: %w", err)
	}
	return holderID, false, nil
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
		newMember, err := isNewMember(ctx, queries, orgID, userUUID)
		if err != nil {
			return "", false, err
		}
		if err := queries.AddActiveOrganizationMembership(ctx, sqlcgen.AddActiveOrganizationMembershipParams{
			OrgID:  orgID,
			UserID: userUUID,
			Role:   string(invite.Role),
		}); err != nil {
			return "", false, fmt.Errorf("postgres: add signup invite member: %w", err)
		}
		if err := inviteAcceptedAudit(ctx, queries, invite.OrgID, invite.ID, userID, invite.Role, newMember, domain.UserAuditActor(userID), acceptedAt); err != nil {
			return "", false, err
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

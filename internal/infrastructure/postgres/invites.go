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

// CreateInvite revokes any pending invite for the same organization and
// recipient (email or phone), then writes the new pending invite in one
// transaction. The organization row is locked so two concurrent invitations
// for the same recipient cannot both survive the partial unique indexes. The
// database check keeps exactly one of email and phone set.
func (s *Store) CreateInvite(ctx context.Context, invite domain.Invite) error {
	id, err := uuid.Parse(invite.ID)
	if err != nil {
		return fmt.Errorf("postgres: invite id %q: %w", invite.ID, err)
	}
	orgID, err := uuid.Parse(invite.OrgID)
	if err != nil {
		return fmt.Errorf("postgres: organization id %q: %w", invite.OrgID, err)
	}
	inviterID, err := uuid.Parse(invite.InviterID)
	if err != nil {
		return fmt.Errorf("postgres: inviter id %q: %w", invite.InviterID, err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin create invite: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	if err := lockOrganization(ctx, queries, orgID); err != nil {
		return err
	}
	if invite.Phone != "" {
		err = queries.RevokePendingOrganizationInvitesForPhone(ctx, sqlcgen.RevokePendingOrganizationInvitesForPhoneParams{
			OrgID: orgID,
			Phone: invite.Phone,
		})
	} else {
		err = queries.RevokePendingOrganizationInvitesForEmail(ctx, sqlcgen.RevokePendingOrganizationInvitesForEmailParams{
			OrgID: orgID,
			Email: invite.Email,
		})
	}
	if err != nil {
		return fmt.Errorf("postgres: revoke previous pending invite: %w", err)
	}
	if err := queries.CreateOrganizationInvite(ctx, sqlcgen.CreateOrganizationInviteParams{
		ID:        id,
		OrgID:     orgID,
		Email:     nullableText(invite.Email),
		Phone:     nullableText(invite.Phone),
		Role:      string(invite.Role),
		TokenHash: invite.TokenHash,
		InviterID: inviterID,
		Status:    string(invite.Status),
		ExpiresAt: timestamptz(invite.ExpiresAt),
		CreatedAt: timestamptz(invite.CreatedAt),
	}); err != nil {
		return fmt.Errorf("postgres: create invite: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit create invite: %w", err)
	}
	return nil
}

// FindInviteByTokenHash returns the invite matching a stored token hash, or
// domain.ErrInviteNotFound.
func (s *Store) FindInviteByTokenHash(ctx context.Context, tokenHash string) (domain.Invite, error) {
	row, err := s.queries.FindOrganizationInviteByTokenHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invite{}, domain.ErrInviteNotFound
	}
	if err != nil {
		return domain.Invite{}, err
	}
	return domain.Invite{
		ID:         row.ID,
		OrgID:      row.OrgID,
		Email:      textValue(row.Email),
		Phone:      textValue(row.Phone),
		Role:       domain.MembershipRole(row.Role),
		TokenHash:  row.TokenHash,
		InviterID:  row.InviterID,
		Status:     domain.InviteStatus(row.Status),
		ExpiresAt:  row.ExpiresAt.Time,
		AcceptedAt: timePtr(row.AcceptedAt),
		CreatedAt:  row.CreatedAt.Time,
	}, nil
}

// ListInvites returns every invite of orgID. The token hash is not selected,
// so a listing cannot leak a token.
func (s *Store) ListInvites(ctx context.Context, orgID string) ([]domain.Invite, error) {
	parsed, err := uuid.Parse(orgID)
	if err != nil {
		return nil, fmt.Errorf("postgres: organization id %q: %w", orgID, err)
	}
	rows, err := s.queries.ListOrganizationInvites(ctx, parsed)
	if err != nil {
		return nil, err
	}
	var out []domain.Invite
	for _, row := range rows {
		out = append(out, domain.Invite{
			ID:         row.ID,
			OrgID:      row.OrgID,
			Email:      textValue(row.Email),
			Phone:      textValue(row.Phone),
			Role:       domain.MembershipRole(row.Role),
			InviterID:  row.InviterID,
			Status:     domain.InviteStatus(row.Status),
			ExpiresAt:  row.ExpiresAt.Time,
			AcceptedAt: timePtr(row.AcceptedAt),
			CreatedAt:  row.CreatedAt.Time,
		})
	}
	return out, nil
}

// RevokeInvite revokes one pending invite or returns domain.ErrInviteNotFound.
func (s *Store) RevokeInvite(ctx context.Context, orgID, inviteID string) error {
	org, id, err := inviteIDs(orgID, inviteID)
	if err != nil {
		return err
	}
	affected, err := s.queries.RevokeOrganizationInvite(ctx, sqlcgen.RevokeOrganizationInviteParams{
		ID:    id,
		OrgID: org,
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		return domain.ErrInviteNotFound
	}
	return nil
}

// AcceptInvite marks one pending invite accepted and makes the user an active
// member in one transaction. A new membership gets role, the tier the invite
// carries; an existing active member keeps theirs. The conditional update is the
// single-use gate: a second acceptance affects no rows.
func (s *Store) AcceptInvite(ctx context.Context, inviteID, orgID, userID string, role domain.MembershipRole, acceptedAt time.Time) error {
	org, id, err := inviteIDs(orgID, inviteID)
	if err != nil {
		return err
	}
	user, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	if _, err := domain.ParseMembershipRole(string(role)); err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin accept invite: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	// The organization row is the same lock membership suspension and removal
	// take, so a membership cannot be suspended between this check and the
	// acceptance write.
	if err := lockOrganization(ctx, queries, org); err != nil {
		return err
	}
	current, err := getMembership(ctx, queries, org, user)
	switch {
	case err == nil && current.Status == domain.MembershipSuspended:
		// A suspended member cannot use an invitation to reactivate
		// themselves. The invitation stays pending for an admin to resolve.
		return domain.ErrInvalidInvite
	case err != nil && !errors.Is(err, domain.ErrNotMember):
		return err
	}
	affected, err := queries.AcceptOrganizationInvite(ctx, sqlcgen.AcceptOrganizationInviteParams{
		ID:         id,
		OrgID:      org,
		AcceptedAt: timestamptz(acceptedAt),
	})
	if err != nil {
		return fmt.Errorf("postgres: accept invite: %w", err)
	}
	if affected == 0 {
		return domain.ErrInviteNotFound
	}
	if err := queries.AddActiveOrganizationMembership(ctx, sqlcgen.AddActiveOrganizationMembershipParams{
		OrgID:  org,
		UserID: user,
		Role:   string(role),
	}); err != nil {
		return fmt.Errorf("postgres: add invited member: %w", err)
	}
	// An owner or admin who accepts an invitation sent to their own address is
	// already an active member and keeps their role, so report the stored role
	// rather than assuming a new plain member.
	joined, err := getMembership(ctx, queries, org, user)
	if err != nil {
		return err
	}
	if err := emitEvents(ctx, newOutboxSink(tx),
		domain.NewMembershipAddedEvent(orgID, userID, domain.MembershipActive, joined.Role, acceptedAt),
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit accept invite: %w", err)
	}
	return nil
}

func inviteIDs(orgID, inviteID string) (uuid.UUID, uuid.UUID, error) {
	org, err := uuid.Parse(orgID)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("postgres: organization id %q: %w", orgID, err)
	}
	id, err := uuid.Parse(inviteID)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("postgres: invite id %q: %w", inviteID, err)
	}
	return org, id, nil
}

// nullableText maps the empty string to SQL NULL, for the invite email and
// phone columns where exactly one is set.
func nullableText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// textValue reads a nullable text column, where NULL is the empty string.
func textValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

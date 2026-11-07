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

// CreateOrg writes the organization and the creator's active owner membership
// in one transaction, so a failure leaves no organization without its owner.
func (s *Store) CreateOrg(ctx context.Context, org domain.Organization, creatorID string) error {
	orgID, err := uuid.Parse(org.ID)
	if err != nil {
		return fmt.Errorf("postgres: organization id %q: %w", org.ID, err)
	}
	userID, err := uuid.Parse(creatorID)
	if err != nil {
		return fmt.Errorf("postgres: user id %q: %w", creatorID, err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin create organization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	if err := queries.CreateOrganization(ctx, sqlcgen.CreateOrganizationParams{
		ID:        orgID,
		Name:      org.Name,
		CreatedAt: timestamptz(org.CreatedAt),
		UpdatedAt: timestamptz(org.UpdatedAt),
	}); err != nil {
		return fmt.Errorf("postgres: create organization: %w", err)
	}
	if err := queries.AddOrganizationMembership(ctx, sqlcgen.AddOrganizationMembershipParams{
		OrgID:  orgID,
		UserID: userID,
		Status: string(domain.MembershipActive),
		Role:   string(domain.RoleOwner),
	}); err != nil {
		return fmt.Errorf("postgres: add organization creator: %w", err)
	}
	occurredAt := org.CreatedAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	if err := emitEvents(ctx, newOutboxSink(tx),
		domain.NewOrgCreatedEvent(org.ID, org.Name, occurredAt),
		domain.NewMembershipAddedEvent(org.ID, creatorID, domain.MembershipActive, domain.RoleOwner, occurredAt),
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit create organization: %w", err)
	}
	return nil
}

// GetOrg returns one organization or domain.ErrOrgNotFound.
func (s *Store) GetOrg(ctx context.Context, orgID string) (domain.Organization, error) {
	parsed, err := uuid.Parse(orgID)
	if err != nil {
		return domain.Organization{}, fmt.Errorf("postgres: organization id %q: %w", orgID, err)
	}
	row, err := s.queries.GetOrganization(ctx, parsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Organization{}, domain.ErrOrgNotFound
	}
	if err != nil {
		return domain.Organization{}, err
	}
	return domain.Organization{
		ID:        row.ID,
		Name:      row.Name,
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}, nil
}

// ListOrgsForUser returns the organizations where userID is active.
func (s *Store) ListOrgsForUser(ctx context.Context, userID string) ([]domain.Organization, error) {
	parsed, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	rows, err := s.queries.ListOrganizationsForUser(ctx, parsed)
	if err != nil {
		return nil, err
	}
	var out []domain.Organization
	for _, row := range rows {
		out = append(out, domain.Organization{
			ID:        row.ID,
			Name:      row.Name,
			CreatedAt: row.CreatedAt.Time,
			UpdatedAt: row.UpdatedAt.Time,
		})
	}
	return out, nil
}

// ListMembers returns every membership of orgID with its lifecycle status.
func (s *Store) ListMembers(ctx context.Context, orgID string) ([]domain.Membership, error) {
	parsed, err := uuid.Parse(orgID)
	if err != nil {
		return nil, fmt.Errorf("postgres: organization id %q: %w", orgID, err)
	}
	rows, err := s.queries.ListOrganizationMembers(ctx, parsed)
	if err != nil {
		return nil, err
	}
	var out []domain.Membership
	for _, row := range rows {
		out = append(out, domain.Membership{
			OrgID:     row.OrgID,
			UserID:    row.UserID,
			Status:    domain.MembershipStatus(row.Status),
			Role:      domain.MembershipRole(row.Role),
			CreatedAt: row.CreatedAt.Time,
			UpdatedAt: row.UpdatedAt.Time,
		})
	}
	return out, nil
}

// AddMember inserts a membership and writes auth.membership.added in the same
// transaction. An existing membership is left untouched, so its status can only
// change through UpdateMemberStatus, its role through UpdateMemberRole, and no
// event is written for a no-op.
func (s *Store) AddMember(ctx context.Context, orgID, userID string, status domain.MembershipStatus, role domain.MembershipRole) error {
	org, user, err := membershipIDs(orgID, userID)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin add membership: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	if err := lockOrganization(ctx, queries, org); err != nil {
		return err
	}
	_, err = getMembership(ctx, queries, org, user)
	switch {
	case err == nil:
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres: commit existing membership: %w", err)
		}
		return nil
	case !errors.Is(err, domain.ErrNotMember):
		return err
	}

	if err := queries.AddOrganizationMembership(ctx, sqlcgen.AddOrganizationMembershipParams{
		OrgID:  org,
		UserID: user,
		Status: string(status),
		Role:   string(role),
	}); err != nil {
		return err
	}
	if err := emitEvents(ctx, newOutboxSink(tx),
		domain.NewMembershipAddedEvent(orgID, userID, status, role, time.Now().UTC()),
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit add membership: %w", err)
	}
	return nil
}

// UpdateMemberStatus changes one existing membership. The organization row is
// locked for the transaction so concurrent demotions cannot remove the last
// active member.
func (s *Store) UpdateMemberStatus(ctx context.Context, orgID, userID string, status domain.MembershipStatus) error {
	org, user, err := membershipIDs(orgID, userID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin update membership: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	if err := lockOrganization(ctx, queries, org); err != nil {
		return err
	}
	current, err := getMembership(ctx, queries, org, user)
	if err != nil {
		return err
	}
	if status != domain.MembershipActive && current.Status == domain.MembershipActive {
		count, err := queries.CountActiveOrganizationMembers(ctx, org)
		if err != nil {
			return err
		}
		if count <= 1 {
			return domain.ErrLastActiveMember
		}
		if err := requireAnotherActiveOwner(ctx, queries, org, current); err != nil {
			return err
		}
	}
	if err := queries.UpdateOrganizationMembershipStatus(ctx, sqlcgen.UpdateOrganizationMembershipStatusParams{
		Status: string(status),
		OrgID:  org,
		UserID: user,
	}); err != nil {
		return err
	}
	if status == domain.MembershipSuspended && current.Status != domain.MembershipSuspended {
		if err := emitEvents(ctx, newOutboxSink(tx),
			domain.NewMembershipSuspendedEvent(orgID, userID, status, time.Now().UTC()),
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit update membership: %w", err)
	}
	return nil
}

// RemoveMember deletes one membership. The organization row is locked for the
// transaction so concurrent removals cannot remove the last active member.
func (s *Store) RemoveMember(ctx context.Context, orgID, userID string) error {
	org, user, err := membershipIDs(orgID, userID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin remove membership: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	if err := lockOrganization(ctx, queries, org); err != nil {
		return err
	}
	current, err := getMembership(ctx, queries, org, user)
	if err != nil {
		return err
	}
	if current.Status == domain.MembershipActive {
		count, err := queries.CountActiveOrganizationMembers(ctx, org)
		if err != nil {
			return err
		}
		if count <= 1 {
			return domain.ErrLastActiveMember
		}
		if err := requireAnotherActiveOwner(ctx, queries, org, current); err != nil {
			return err
		}
	}
	if err := queries.DeleteOrganizationMembership(ctx, sqlcgen.DeleteOrganizationMembershipParams{
		OrgID:  org,
		UserID: user,
	}); err != nil {
		return err
	}
	if err := emitEvents(ctx, newOutboxSink(tx),
		domain.NewMembershipRemovedEvent(orgID, userID, current.Status, time.Now().UTC()),
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit remove membership: %w", err)
	}
	return nil
}

// UpdateMemberRole changes one member's administration tier and writes
// auth.membership.role_changed in the same transaction. The organization row is
// locked so concurrent demotions cannot remove the last active owner. Setting
// the role a member already has changes nothing and writes no event.
func (s *Store) UpdateMemberRole(ctx context.Context, orgID, userID string, role domain.MembershipRole, actorID string) error {
	if _, err := domain.ParseMembershipRole(string(role)); err != nil {
		return err
	}
	org, user, err := membershipIDs(orgID, userID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin update membership role: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	if err := lockOrganization(ctx, queries, org); err != nil {
		return err
	}
	current, err := getMembership(ctx, queries, org, user)
	if err != nil {
		return err
	}
	if current.Role == role {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres: commit unchanged membership role: %w", err)
		}
		return nil
	}
	if role != domain.RoleOwner {
		if err := requireAnotherActiveOwner(ctx, queries, org, current); err != nil {
			return err
		}
	}
	if err := queries.UpdateOrganizationMembershipRole(ctx, sqlcgen.UpdateOrganizationMembershipRoleParams{
		Role:   string(role),
		OrgID:  org,
		UserID: user,
	}); err != nil {
		return err
	}
	if err := emitEvents(ctx, newOutboxSink(tx),
		domain.NewMembershipRoleChangedEvent(orgID, userID, role, current.Role, actorID, time.Now().UTC()),
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit membership role: %w", err)
	}
	return nil
}

// requireAnotherActiveOwner refuses a change to current that would leave the
// organization without an active owner. It is a no-op unless current is itself
// an active owner. The caller must hold the organization lock.
func requireAnotherActiveOwner(ctx context.Context, queries *sqlcgen.Queries, orgID uuid.UUID, current domain.Membership) error {
	if current.Role != domain.RoleOwner || current.Status != domain.MembershipActive {
		return nil
	}
	owners, err := queries.CountActiveOrganizationOwners(ctx, orgID)
	if err != nil {
		return err
	}
	if owners <= 1 {
		return domain.ErrLastOwner
	}
	return nil
}

// GetMembership returns one membership or domain.ErrNotMember.
func (s *Store) GetMembership(ctx context.Context, orgID, userID string) (domain.Membership, error) {
	org, user, err := membershipIDs(orgID, userID)
	if err != nil {
		return domain.Membership{}, err
	}
	return getMembership(ctx, s.queries, org, user)
}

func getMembership(ctx context.Context, queries *sqlcgen.Queries, orgID, userID uuid.UUID) (domain.Membership, error) {
	row, err := queries.GetOrganizationMembership(ctx, sqlcgen.GetOrganizationMembershipParams{
		OrgID:  orgID,
		UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, domain.ErrNotMember
	}
	if err != nil {
		return domain.Membership{}, err
	}
	return domain.Membership{
		OrgID:     row.OrgID,
		UserID:    row.UserID,
		Status:    domain.MembershipStatus(row.Status),
		Role:      domain.MembershipRole(row.Role),
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}, nil
}

func lockOrganization(ctx context.Context, queries *sqlcgen.Queries, orgID uuid.UUID) error {
	if _, err := queries.LockOrganization(ctx, orgID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrOrgNotFound
		}
		return err
	}
	return nil
}

func membershipIDs(orgID, userID string) (uuid.UUID, uuid.UUID, error) {
	org, err := uuid.Parse(orgID)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("postgres: organization id %q: %w", orgID, err)
	}
	user, err := uuid.Parse(userID)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	return org, user, nil
}

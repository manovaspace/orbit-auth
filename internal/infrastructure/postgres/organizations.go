package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/manovaspace/orbit-auth/internal/domain"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/sqlcgen"
)

// CreateOrg writes the organization and the creator's active membership in one
// transaction, so a failure leaves no organization without its owner.
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
	}); err != nil {
		return fmt.Errorf("postgres: add organization creator: %w", err)
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

// AddMember inserts a membership. An existing membership is left untouched, so
// its status can only change through UpdateMemberStatus.
func (s *Store) AddMember(ctx context.Context, orgID, userID string, status domain.MembershipStatus) error {
	org, user, err := membershipIDs(orgID, userID)
	if err != nil {
		return err
	}
	return s.queries.AddOrganizationMembership(ctx, sqlcgen.AddOrganizationMembershipParams{
		OrgID:  org,
		UserID: user,
		Status: string(status),
	})
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
	}
	if err := queries.UpdateOrganizationMembershipStatus(ctx, sqlcgen.UpdateOrganizationMembershipStatusParams{
		Status: string(status),
		OrgID:  org,
		UserID: user,
	}); err != nil {
		return err
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
	}
	if err := queries.DeleteOrganizationMembership(ctx, sqlcgen.DeleteOrganizationMembershipParams{
		OrgID:  org,
		UserID: user,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit remove membership: %w", err)
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

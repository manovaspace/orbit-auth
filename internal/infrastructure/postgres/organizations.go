package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/manovaspace/orbit-auth/internal/domain"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/sqlcgen"
)

// CreateOrg writes the organization and the creator's active owner membership
// in one transaction, so a failure leaves no organization without its owner.
func (s *Store) CreateOrg(ctx context.Context, org domain.Organization, creatorID string) error {
	if _, err := uuid.Parse(org.ID); err != nil {
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
	occurredAt := org.CreatedAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	if err := createOrganizationTx(ctx, queries, newOutboxSink(tx), org, userID, occurredAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit create organization: %w", err)
	}
	return nil
}

// createOrganizationTx writes a new organization with its creator as the first
// owner, and the creation events. It is shared by CreateOrg and sign-up with an
// organization so both allocate slugs the same way.
func createOrganizationTx(ctx context.Context, queries *sqlcgen.Queries, sink domain.EventSink, org domain.Organization, userID uuid.UUID, occurredAt time.Time) error {
	if err := insertOrganizationTx(ctx, queries, sink, org, occurredAt); err != nil {
		return err
	}
	orgID, err := uuid.Parse(org.ID)
	if err != nil {
		return fmt.Errorf("postgres: organization id %q: %w", org.ID, err)
	}
	if err := queries.AddOrganizationMembership(ctx, sqlcgen.AddOrganizationMembershipParams{
		OrgID:  orgID,
		UserID: userID,
		Status: string(domain.MembershipActive),
		Role:   string(domain.RoleOwner),
	}); err != nil {
		return fmt.Errorf("postgres: add organization creator: %w", err)
	}
	return emitEvents(ctx, sink,
		domain.NewMembershipAddedEvent(org.ID, userID.String(), domain.MembershipActive, domain.RoleOwner, occurredAt),
	)
}

// insertOrganizationTx writes the organization row, its slug registry row and
// auth.org.created, without any membership. Staff provisioning uses it directly
// because staff are never members. A slug that another organization holds or
// has held is domain.ErrSlugTaken.
func insertOrganizationTx(ctx context.Context, queries *sqlcgen.Queries, sink domain.EventSink, org domain.Organization, occurredAt time.Time) error {
	orgID, err := uuid.Parse(org.ID)
	if err != nil {
		return fmt.Errorf("postgres: organization id %q: %w", org.ID, err)
	}
	if err := domain.ValidateSlug(org.Slug); err != nil {
		return err
	}
	if err := queries.CreateOrganization(ctx, sqlcgen.CreateOrganizationParams{
		ID:        orgID,
		Name:      org.Name,
		Slug:      org.Slug,
		CreatedAt: timestamptz(org.CreatedAt),
		UpdatedAt: timestamptz(org.UpdatedAt),
	}); err != nil {
		return slugConflict(fmt.Errorf("postgres: create organization: %w", err))
	}
	if err := queries.InsertOrganizationSlug(ctx, sqlcgen.InsertOrganizationSlugParams{Slug: org.Slug, OrgID: orgID}); err != nil {
		return slugConflict(fmt.Errorf("postgres: register organization slug: %w", err))
	}
	return emitEvents(ctx, sink,
		domain.NewOrgCreatedEvent(org.ID, org.Name, org.Slug, occurredAt),
	)
}

// slugConflict turns a unique violation on a slug into domain.ErrSlugTaken and
// leaves every other error as it is.
func slugConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return domain.ErrSlugTaken
	}
	return err
}

// SlugAvailable reports whether no organization holds or has held slug.
func (s *Store) SlugAvailable(ctx context.Context, slug string) (bool, error) {
	exists, err := s.queries.OrganizationSlugExists(ctx, slug)
	if err != nil {
		return false, err
	}
	return !exists, nil
}

// SetOrgSlug makes slug the organization's current slug for a member, under the
// organization lock where the policy is rechecked. The event names the caller.
func (s *Store) SetOrgSlug(ctx context.Context, orgID, slug string, by domain.MembershipActor) error {
	return s.changeOrgSlug(ctx, orgID, slug, by, by.CallerID, "")
}

// SetOrgSlugByStaff makes slug the organization's current slug for a staff
// write. Staff are not members, so no membership is checked. The event names the
// staff subject as its actor and carries the reason.
func (s *Store) SetOrgSlugByStaff(ctx context.Context, orgID, slug string, by domain.StaffActor) error {
	return s.changeOrgSlug(ctx, orgID, slug, domain.MembershipActor{}, by.Subject, by.Reason)
}

// changeOrgSlug makes slug the organization's current slug in one transaction
// under the organization lock. The previous slug is retired, stays reserved for
// this organization and keeps resolving, so a rename never frees an address for
// someone else. Re-claiming one of the organization's own retired slugs is
// allowed. actorID and reason are written to auth.org.slug_changed.
func (s *Store) changeOrgSlug(ctx context.Context, orgID, slug string, by domain.MembershipActor, actorID, reason string) error {
	if err := domain.ValidateSlug(slug); err != nil {
		return err
	}
	org, err := uuid.Parse(orgID)
	if err != nil {
		return fmt.Errorf("postgres: organization id %q: %w", orgID, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin set organization slug: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	if err := lockOrganization(ctx, queries, org); err != nil {
		return err
	}
	if err := authorizeUnderLock(ctx, queries, org, by, nil, ""); err != nil {
		return err
	}
	current, err := queries.GetOrganization(ctx, org)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrOrgNotFound
		}
		return err
	}
	if current.Slug == slug {
		return tx.Commit(ctx)
	}
	if err := queries.RetireCurrentOrganizationSlug(ctx, org); err != nil {
		return err
	}
	revived, err := queries.ReviveOwnOrganizationSlug(ctx, sqlcgen.ReviveOwnOrganizationSlugParams{Slug: slug, OrgID: org})
	if err != nil {
		return err
	}
	if revived == 0 {
		if err := queries.InsertOrganizationSlug(ctx, sqlcgen.InsertOrganizationSlugParams{Slug: slug, OrgID: org}); err != nil {
			return slugConflict(fmt.Errorf("postgres: register organization slug: %w", err))
		}
	}
	if err := queries.UpdateOrganizationSlug(ctx, sqlcgen.UpdateOrganizationSlugParams{Slug: slug, ID: org}); err != nil {
		return slugConflict(fmt.Errorf("postgres: update organization slug: %w", err))
	}
	if err := emitEvents(ctx, newOutboxSink(tx),
		domain.NewOrgSlugChangedEvent(orgID, slug, current.Slug, actorID, reason, time.Now().UTC()),
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit organization slug: %w", err)
	}
	return nil
}

// ResolveSlug finds the organization that holds or once held slug. The result
// carries the organization's current slug.
func (s *Store) ResolveSlug(ctx context.Context, slug string) (domain.Organization, error) {
	row, err := s.queries.ResolveOrganizationSlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Organization{}, domain.ErrSlugNotFound
	}
	if err != nil {
		return domain.Organization{}, err
	}
	return domain.Organization{
		ID: row.ID, Name: row.Name, Slug: row.Slug,
		Status: domain.OrganizationStatus(row.Status), SuspendedAt: timePtr(row.SuspendedAt), SuspendedReason: row.SuspendedReason,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}, nil
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
		ID:              row.ID,
		Name:            row.Name,
		Slug:            row.Slug,
		Status:          domain.OrganizationStatus(row.Status),
		SuspendedAt:     timePtr(row.SuspendedAt),
		SuspendedReason: row.SuspendedReason,
		CreatedAt:       row.CreatedAt.Time,
		UpdatedAt:       row.UpdatedAt.Time,
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
			ID:              row.ID,
			Name:            row.Name,
			Slug:            row.Slug,
			Status:          domain.OrganizationStatus(row.Status),
			SuspendedAt:     timePtr(row.SuspendedAt),
			SuspendedReason: row.SuspendedReason,
			CreatedAt:       row.CreatedAt.Time,
			UpdatedAt:       row.UpdatedAt.Time,
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
func (s *Store) UpdateMemberStatus(ctx context.Context, orgID, userID string, status domain.MembershipStatus, by domain.MembershipActor) error {
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
	if err := authorizeUnderLock(ctx, queries, org, by, &current, ""); err != nil {
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
func (s *Store) RemoveMember(ctx context.Context, orgID, userID string, by domain.MembershipActor) error {
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
	if err := authorizeUnderLock(ctx, queries, org, by, &current, ""); err != nil {
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
	if err := authorizeUnderLock(ctx, queries, org, domain.MembershipActor{CallerID: actorID, Action: domain.ActionSetRole}, &current, role); err != nil {
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

// authorizeUnderLock reruns the membership policy with the caller's and the
// target's current rows. The caller must hold the organization lock, so neither
// row can change between this read and the write that follows. A zero actor is
// a system change and is not checked. The caller's own removal of themselves is
// allowed by the policy.
func authorizeUnderLock(ctx context.Context, queries *sqlcgen.Queries, orgID uuid.UUID, by domain.MembershipActor, target *domain.Membership, newRole domain.MembershipRole) error {
	if by.CallerID == "" {
		return nil
	}
	callerID, err := uuid.Parse(by.CallerID)
	if err != nil {
		return fmt.Errorf("postgres: caller id %q: %w", by.CallerID, err)
	}
	caller, err := getMembership(ctx, queries, orgID, callerID)
	if err != nil {
		return err
	}
	return domain.AuthorizeMembershipAction(caller, by.Action, target, newRole)
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

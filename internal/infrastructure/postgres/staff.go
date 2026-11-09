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

// likeEscaper escapes the LIKE metacharacters of a user's search text, so the
// text matches literally. Postgres uses backslash as the default escape.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// ListOrganizationsForStaff returns one keyset page of organizations, oldest
// first. The query matches the name or the slug case-insensitively.
func (s *Store) ListOrganizationsForStaff(ctx context.Context, query domain.StaffOrgListQuery) ([]domain.StaffOrgSummary, error) {
	params := sqlcgen.ListOrganizationsForStaffParams{
		StatusFilter: string(query.Status),
		RowLimit:     int32(query.Limit),
	}
	if trimmed := strings.TrimSpace(query.Query); trimmed != "" {
		params.Pattern = "%" + likeEscaper.Replace(trimmed) + "%"
	}
	if query.After != nil {
		afterID, err := uuid.Parse(query.After.ID)
		if err != nil {
			return nil, fmt.Errorf("postgres: cursor organization id %q: %w", query.After.ID, err)
		}
		params.HasAfter = true
		params.AfterCreatedAt = timestamptz(query.After.CreatedAt)
		params.AfterID = afterID
	}
	rows, err := s.queries.ListOrganizationsForStaff(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]domain.StaffOrgSummary, 0, len(rows))
	for _, row := range rows {
		owners := row.OwnerUserIds
		if owners == nil {
			owners = []string{}
		}
		out = append(out, domain.StaffOrgSummary{
			Organization: domain.Organization{
				ID:              row.ID,
				Name:            row.Name,
				Slug:            row.Slug,
				Status:          domain.OrganizationStatus(row.Status),
				SuspendedAt:     timePtr(row.SuspendedAt),
				SuspendedReason: row.SuspendedReason,
				CreatedAt:       row.CreatedAt.Time,
				UpdatedAt:       row.UpdatedAt.Time,
			},
			MemberCount:  int(row.MemberCount),
			OwnerUserIDs: owners,
		})
	}
	return out, nil
}

// ProvisionedOrganization returns what an idempotency key recorded: the
// organization and the owner target it was provisioned for.
func (s *Store) ProvisionedOrganization(ctx context.Context, idempotencyKey string) (domain.OrganizationProvision, bool, error) {
	row, err := s.queries.FindOrganizationProvision(ctx, idempotencyKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OrganizationProvision{}, false, nil
	}
	if err != nil {
		return domain.OrganizationProvision{}, false, err
	}
	org, err := s.GetOrg(ctx, row.OrgID.String())
	if err != nil {
		return domain.OrganizationProvision{}, false, err
	}
	return domain.OrganizationProvision{
		Organization: org,
		OwnerTarget:  domain.InviteTarget{Email: textValue(row.OwnerEmail), Phone: textValue(row.OwnerPhone)},
	}, true, nil
}

// ProvisionOrganization writes the organization, its slug, the owner invitation
// and auth.org.created in one transaction. The organization has no members. A
// repeated idempotency key rolls the attempt back and returns the organization
// the key created, so a repeat never leaves a second organization behind. Attempts
// with one key are serialised by an advisory lock, so a concurrent repeat waits
// for the first attempt and then returns its organization.
func (s *Store) ProvisionOrganization(ctx context.Context, org domain.Organization, owner domain.Invite, idempotencyKey string) (domain.OrganizationProvision, bool, error) {
	orgID, err := uuid.Parse(org.ID)
	if err != nil {
		return domain.OrganizationProvision{}, false, fmt.Errorf("postgres: organization id %q: %w", org.ID, err)
	}
	if _, err := uuid.Parse(owner.ID); err != nil {
		return domain.OrganizationProvision{}, false, fmt.Errorf("postgres: invite id %q: %w", owner.ID, err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.OrganizationProvision{}, false, fmt.Errorf("postgres: begin provision organization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	if idempotencyKey != "" {
		// Attempts with one key run one after another. A concurrent repeat waits
		// here until the first commits, then finds the organization that key made.
		if err := queries.LockProvisionKey(ctx, idempotencyKey); err != nil {
			return domain.OrganizationProvision{}, false, fmt.Errorf("postgres: lock provision key: %w", err)
		}
		if _, err := queries.FindOrganizationProvision(ctx, idempotencyKey); err == nil {
			return s.repeatedProvision(ctx, tx, idempotencyKey)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return domain.OrganizationProvision{}, false, fmt.Errorf("postgres: find provision key: %w", err)
		}
	}
	occurredAt := org.CreatedAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	if err := insertOrganizationTx(ctx, queries, newOutboxSink(tx), org, occurredAt); err != nil {
		return domain.OrganizationProvision{}, false, err
	}
	if idempotencyKey != "" {
		affected, err := queries.InsertOrganizationProvision(ctx, sqlcgen.InsertOrganizationProvisionParams{
			IdempotencyKey: idempotencyKey,
			OrgID:          orgID,
			OwnerEmail:     nullableText(owner.Email),
			OwnerPhone:     nullableText(owner.Phone),
		})
		if err != nil {
			return domain.OrganizationProvision{}, false, fmt.Errorf("postgres: record provision key: %w", err)
		}
		if affected == 0 {
			// Unreachable while the lock above holds; kept as a safety net, so a
			// key that was recorded without the lock still returns its organization.
			return s.repeatedProvision(ctx, tx, idempotencyKey)
		}
	}
	if err := insertOwnerInviteTx(ctx, queries, owner); err != nil {
		return domain.OrganizationProvision{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OrganizationProvision{}, false, fmt.Errorf("postgres: commit provision organization: %w", err)
	}
	return domain.OrganizationProvision{
		Organization: org,
		OwnerTarget:  domain.InviteTarget{Email: owner.Email, Phone: owner.Phone},
	}, true, nil
}

// repeatedProvision ends a provisioning attempt that found its idempotency key
// already used. It writes nothing and returns what the key recorded.
func (s *Store) repeatedProvision(ctx context.Context, tx pgx.Tx, idempotencyKey string) (domain.OrganizationProvision, bool, error) {
	if err := tx.Rollback(ctx); err != nil {
		return domain.OrganizationProvision{}, false, fmt.Errorf("postgres: rollback repeated provision: %w", err)
	}
	recorded, found, err := s.ProvisionedOrganization(ctx, idempotencyKey)
	if err != nil {
		return domain.OrganizationProvision{}, false, err
	}
	if !found {
		return domain.OrganizationProvision{}, false, fmt.Errorf("postgres: idempotency key %q has no organization", idempotencyKey)
	}
	return recorded, false, nil
}

// SuspendOrganization suspends one organization under its row lock and emits
// auth.org.suspended. An organization that is already suspended is unchanged.
func (s *Store) SuspendOrganization(ctx context.Context, orgID string, by domain.StaffActor) error {
	org, err := uuid.Parse(orgID)
	if err != nil {
		return fmt.Errorf("postgres: organization id %q: %w", orgID, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin suspend organization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	if err := lockOrganization(ctx, queries, org); err != nil {
		return err
	}
	current, err := queries.GetOrganization(ctx, org)
	if err != nil {
		return err
	}
	if domain.OrganizationStatus(current.Status) == domain.OrgStatusSuspended {
		return tx.Commit(ctx)
	}
	now := time.Now().UTC()
	if err := queries.SetOrganizationStatus(ctx, sqlcgen.SetOrganizationStatusParams{
		Status:          string(domain.OrgStatusSuspended),
		SuspendedAt:     timestamptz(now),
		SuspendedReason: by.Reason,
		ID:              org,
	}); err != nil {
		return fmt.Errorf("postgres: suspend organization: %w", err)
	}
	if err := emitEvents(ctx, newOutboxSink(tx), domain.NewOrgSuspendedEvent(orgID, by, now)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit suspend organization: %w", err)
	}
	return nil
}

// ReactivateOrganization lifts a suspension under the row lock and emits
// auth.org.reactivated. An active organization is unchanged.
func (s *Store) ReactivateOrganization(ctx context.Context, orgID string, by domain.StaffActor) error {
	org, err := uuid.Parse(orgID)
	if err != nil {
		return fmt.Errorf("postgres: organization id %q: %w", orgID, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin reactivate organization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	if err := lockOrganization(ctx, queries, org); err != nil {
		return err
	}
	current, err := queries.GetOrganization(ctx, org)
	if err != nil {
		return err
	}
	if domain.OrganizationStatus(current.Status) != domain.OrgStatusSuspended {
		return tx.Commit(ctx)
	}
	now := time.Now().UTC()
	if err := queries.SetOrganizationStatus(ctx, sqlcgen.SetOrganizationStatusParams{
		Status:          string(domain.OrgStatusActive),
		SuspendedAt:     pgtype.Timestamptz{},
		SuspendedReason: "",
		ID:              org,
	}); err != nil {
		return fmt.Errorf("postgres: reactivate organization: %w", err)
	}
	if err := emitEvents(ctx, newOutboxSink(tx), domain.NewOrgReactivatedEvent(orgID, by, now)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit reactivate organization: %w", err)
	}
	return nil
}

// insertOwnerInviteTx writes one pending owner-tier invitation in the caller's
// transaction. The organization has no other invitation for the recipient yet,
// so nothing is revoked here; rotation goes through CreateInvite.
func insertOwnerInviteTx(ctx context.Context, queries *sqlcgen.Queries, invite domain.Invite) error {
	id, err := uuid.Parse(invite.ID)
	if err != nil {
		return fmt.Errorf("postgres: invite id %q: %w", invite.ID, err)
	}
	orgID, err := uuid.Parse(invite.OrgID)
	if err != nil {
		return fmt.Errorf("postgres: organization id %q: %w", invite.OrgID, err)
	}
	inviterID, err := inviterParam(invite.InviterID)
	if err != nil {
		return err
	}
	if err := queries.CreateOrganizationInvite(ctx, sqlcgen.CreateOrganizationInviteParams{
		ID:                  id,
		OrgID:               orgID,
		Email:               nullableText(invite.Email),
		Phone:               nullableText(invite.Phone),
		Role:                string(invite.Role),
		TokenHash:           invite.TokenHash,
		InviterID:           inviterID,
		InviterStaffSubject: nullableText(invite.InviterStaffSubject),
		Status:              string(invite.Status),
		ExpiresAt:           timestamptz(invite.ExpiresAt),
		CreatedAt:           timestamptz(invite.CreatedAt),
	}); err != nil {
		return fmt.Errorf("postgres: create owner invite: %w", err)
	}
	return nil
}

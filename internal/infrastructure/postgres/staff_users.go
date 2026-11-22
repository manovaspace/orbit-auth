package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/manovaspace/orbit-auth/internal/domain"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/sqlcgen"
)

// ListUsersForStaff returns one keyset page of accounts, oldest first. The query
// matches the email, mobile number, username or display name case-insensitively.
func (s *Store) ListUsersForStaff(ctx context.Context, query domain.StaffUserListQuery) ([]domain.StaffUser, error) {
	params := sqlcgen.ListUsersForStaffParams{
		StatusFilter: query.Status,
		RowLimit:     int32(query.Limit),
	}
	if trimmed := strings.TrimSpace(query.Query); trimmed != "" {
		params.Pattern = "%" + likeEscaper.Replace(trimmed) + "%"
	}
	if query.After != nil {
		afterID, err := uuid.Parse(query.After.ID)
		if err != nil {
			return nil, fmt.Errorf("postgres: cursor user id %q: %w", query.After.ID, err)
		}
		params.HasAfter = true
		params.AfterCreatedAt = timestamptz(query.After.CreatedAt)
		params.AfterID = afterID
	}
	rows, err := s.queries.ListUsersForStaff(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]domain.StaffUser, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.StaffUser{
			ID:              row.ID,
			Email:           row.Email,
			Mobile:          row.Mobile,
			Username:        row.Username,
			DisplayName:     row.DisplayName,
			Status:          row.Status,
			EmailVerified:   row.EmailVerified,
			MobileVerified:  row.MobileVerified,
			HasPassword:     row.HasPassword,
			GoogleLinked:    row.GoogleLinked,
			CreatedAt:       row.CreatedAt.Time,
			LastLoginAt:     timePtr(row.LastLoginAt),
			DisabledAt:      timePtr(row.DisabledAt),
			DisabledReason:  row.DisabledReason,
			MembershipCount: int(row.MembershipCount),
		})
	}
	return out, nil
}

// GetUserForStaff returns one account and its memberships. A missing account is
// domain.ErrUserNotFound.
func (s *Store) GetUserForStaff(ctx context.Context, userID string) (domain.StaffUser, []domain.StaffUserMembership, error) {
	parsed, err := uuid.Parse(userID)
	if err != nil {
		return domain.StaffUser{}, nil, fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	row, err := s.queries.GetUserForStaff(ctx, parsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StaffUser{}, nil, domain.ErrUserNotFound
	}
	if err != nil {
		return domain.StaffUser{}, nil, err
	}
	user := domain.StaffUser{
		ID:              row.ID,
		Email:           row.Email,
		Mobile:          row.Mobile,
		Username:        row.Username,
		DisplayName:     row.DisplayName,
		Status:          row.Status,
		EmailVerified:   row.EmailVerified,
		MobileVerified:  row.MobileVerified,
		HasPassword:     row.HasPassword,
		GoogleLinked:    row.GoogleLinked,
		CreatedAt:       row.CreatedAt.Time,
		LastLoginAt:     timePtr(row.LastLoginAt),
		DisabledAt:      timePtr(row.DisabledAt),
		DisabledReason:  row.DisabledReason,
		MembershipCount: int(row.MembershipCount),
	}
	rows, err := s.queries.ListMembershipsForStaffUser(ctx, parsed)
	if err != nil {
		return domain.StaffUser{}, nil, err
	}
	memberships := make([]domain.StaffUserMembership, 0, len(rows))
	for _, m := range rows {
		memberships = append(memberships, domain.StaffUserMembership{
			OrgID:     m.OrgID,
			OrgName:   m.OrgName,
			OrgSlug:   m.OrgSlug,
			OrgStatus: domain.OrganizationStatus(m.OrgStatus),
			Role:      domain.MembershipRole(m.Role),
			Status:    domain.MembershipStatus(m.Status),
			JoinedAt:  m.JoinedAt.Time,
		})
	}
	return user, memberships, nil
}

// DisableUser disables an active account in one transaction. The account row is
// locked first, so a concurrent sign-in cannot store a refresh token between the
// status change and the revocation. Every live refresh token of the account is
// revoked, and user.disabled and auth.user.disabled are written with the staff
// actor. A disabled account is unchanged. A pending account is
// ErrAccountNotActive.
func (s *Store) DisableUser(ctx context.Context, userID string, by domain.StaffActor) (bool, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return false, fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("postgres: begin disable user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	status, err := lockAccount(ctx, queries, id)
	if err != nil {
		return false, err
	}
	switch status {
	case domain.UserDisabled:
		return false, tx.Commit(ctx)
	case domain.UserActive:
	default:
		return false, domain.ErrAccountNotActive
	}
	now := time.Now().UTC()
	if _, err := queries.SetUserDisabled(ctx, sqlcgen.SetUserDisabledParams{Reason: by.Reason, ID: id}); err != nil {
		return false, fmt.Errorf("postgres: disable user: %w", err)
	}
	if _, err := queries.RevokeUserRefreshTokens(ctx, id); err != nil {
		return false, fmt.Errorf("postgres: revoke user refresh tokens: %w", err)
	}
	if err := writeAudit(ctx, queries, domain.AuditEntry{
		Action:        domain.AuditUserDisabled,
		Actor:         domain.StaffAuditActor(by),
		SubjectUserID: userID,
		OccurredAt:    now,
	}); err != nil {
		return false, err
	}
	if err := emitEvents(ctx, newOutboxSink(tx), domain.NewUserDisabledEvent(userID, by, now)); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("postgres: commit disable user: %w", err)
	}
	return true, nil
}

// EnableUser makes a disabled account active again in one transaction. An active
// account is unchanged. A pending account is ErrAccountNotActive. The change
// writes user.enabled and auth.user.enabled with the staff actor. Refresh tokens
// revoked by the disable stay revoked: the user signs in again.
func (s *Store) EnableUser(ctx context.Context, userID string, by domain.StaffActor) (bool, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return false, fmt.Errorf("postgres: user id %q: %w", userID, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("postgres: begin enable user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := s.queries.WithTx(tx)
	status, err := lockAccount(ctx, queries, id)
	if err != nil {
		return false, err
	}
	switch status {
	case domain.UserActive:
		return false, tx.Commit(ctx)
	case domain.UserDisabled:
	default:
		return false, domain.ErrAccountNotActive
	}
	now := time.Now().UTC()
	if _, err := queries.SetUserEnabled(ctx, id); err != nil {
		return false, fmt.Errorf("postgres: enable user: %w", err)
	}
	if err := writeAudit(ctx, queries, domain.AuditEntry{
		Action:        domain.AuditUserEnabled,
		Actor:         domain.StaffAuditActor(by),
		SubjectUserID: userID,
		OccurredAt:    now,
	}); err != nil {
		return false, err
	}
	if err := emitEvents(ctx, newOutboxSink(tx), domain.NewUserEnabledEvent(userID, by, now)); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("postgres: commit enable user: %w", err)
	}
	return true, nil
}

// lockAccount locks the account row for update and returns its status. A
// missing account is domain.ErrUserNotFound.
func lockAccount(ctx context.Context, queries *sqlcgen.Queries, id uuid.UUID) (string, error) {
	status, err := queries.LockUserForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("postgres: lock user: %w", err)
	}
	return status, nil
}

// ListAuditEvents returns one page of audit rows, newest first. The filters are
// the organization and the subject account; at least one is required.
func (s *Store) ListAuditEvents(ctx context.Context, query domain.AuditEventQuery) ([]domain.AuditEvent, error) {
	params := sqlcgen.ListAuditEventsParams{RowLimit: int32(query.Limit)}
	if query.OrgID == "" && query.SubjectUserID == "" {
		return nil, domain.ErrAuditFilterRequired
	}
	var err error
	if params.OrgID, err = optionalUUID(query.OrgID); err != nil {
		return nil, err
	}
	if params.SubjectUserID, err = optionalUUID(query.SubjectUserID); err != nil {
		return nil, err
	}
	if query.After != nil {
		after := query.After.ID
		params.AfterID = &after
	}
	rows, err := s.queries.ListAuditEvents(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AuditEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.AuditEvent{
			ID:            row.ID,
			OccurredAt:    row.OccurredAt.Time,
			Action:        row.Action,
			ActorKind:     row.ActorKind,
			ActorID:       row.ActorID,
			OrgID:         row.OrgID,
			SubjectUserID: row.SubjectUserID,
			InviteID:      row.InviteID,
			Reason:        row.Reason,
			Details:       row.Details,
		})
	}
	return out, nil
}

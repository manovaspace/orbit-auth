package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/manovaspace/orbit-auth/internal/domain"
	"github.com/manovaspace/orbit-auth/internal/infrastructure/postgres/sqlcgen"
)

// writeAudit writes one audit row in the caller's transaction, so the row and
// the change it records commit or roll back together. An empty Details is {}.
// The row takes the actor's reason as its own. Callers never pass an email,
// mobile number, password, token or hash in Details.
func writeAudit(ctx context.Context, queries *sqlcgen.Queries, entry domain.AuditEntry) error {
	details := entry.Details
	if details == nil {
		details = map[string]any{}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("postgres: marshal audit %s details: %w", entry.Action, err)
	}
	orgID, err := optionalUUID(entry.OrgID)
	if err != nil {
		return err
	}
	subjectID, err := optionalUUID(entry.SubjectUserID)
	if err != nil {
		return err
	}
	inviteID, err := optionalUUID(entry.InviteID)
	if err != nil {
		return err
	}
	occurredAt := entry.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	if err := queries.InsertAuditEvent(ctx, sqlcgen.InsertAuditEventParams{
		OccurredAt:    timestamptz(occurredAt),
		Action:        entry.Action,
		ActorKind:     entry.Actor.Kind,
		ActorID:       entry.Actor.ID,
		OrgID:         orgID,
		SubjectUserID: subjectID,
		InviteID:      inviteID,
		Reason:        entry.Actor.Reason,
		Details:       raw,
	}); err != nil {
		return fmt.Errorf("postgres: write audit %s: %w", entry.Action, err)
	}
	return nil
}

// optionalUUID maps the empty string to SQL NULL and parses any other value.
func optionalUUID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return inviterParam(value)
}

// userCreatedAudit writes auth.user.created's audit row for an account created
// by actor. The subject is the new account.
func userCreatedAudit(ctx context.Context, queries *sqlcgen.Queries, userID string, actor domain.AuditActor, at time.Time) error {
	return writeAudit(ctx, queries, domain.AuditEntry{
		Action:        domain.AuditUserCreated,
		Actor:         actor,
		SubjectUserID: userID,
		OccurredAt:    at,
	})
}

// inviteAcceptedAudit writes the audit rows of an accepted invitation: the
// acceptance itself and, when the membership is new, membership.added.
func inviteAcceptedAudit(ctx context.Context, queries *sqlcgen.Queries, orgID, inviteID, userID string, role domain.MembershipRole, newMember bool, actor domain.AuditActor, at time.Time) error {
	if err := writeAudit(ctx, queries, domain.AuditEntry{
		Action:        domain.AuditInviteAccepted,
		Actor:         actor,
		OrgID:         orgID,
		SubjectUserID: userID,
		InviteID:      inviteID,
		Details:       map[string]any{"role": string(role)},
		OccurredAt:    at,
	}); err != nil {
		return err
	}
	if !newMember {
		return nil
	}
	return writeAudit(ctx, queries, domain.AuditEntry{
		Action:        domain.AuditMembershipAdded,
		Actor:         actor,
		OrgID:         orgID,
		SubjectUserID: userID,
		Details:       map[string]any{"role": string(role)},
		OccurredAt:    at,
	})
}

// isNewMember reports whether user has no membership in org yet. It runs under
// the organization lock, so the answer holds until the transaction commits.
func isNewMember(ctx context.Context, queries *sqlcgen.Queries, org, user uuid.UUID) (bool, error) {
	_, err := getMembership(ctx, queries, org, user)
	if errors.Is(err, domain.ErrNotMember) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

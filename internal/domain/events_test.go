package domain

import (
	"testing"
	"time"

	"github.com/manovaspace/orbit-message-bus/bustest"
)

func TestLifecycleEventSubjectsAndVersion(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"user created", SubjectUserCreated, "auth.user.created"},
		{"org created", SubjectOrgCreated, "auth.org.created"},
		{"membership added", SubjectMembershipAdded, "auth.membership.added"},
		{"membership removed", SubjectMembershipRemoved, "auth.membership.removed"},
		{"membership suspended", SubjectMembershipSuspended, "auth.membership.suspended"},
		{"membership role changed", SubjectMembershipRoleChanged, "auth.membership.role_changed"},
		{"org slug changed", SubjectOrgSlugChanged, "auth.org.slug_changed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("subject = %q, want %q", tc.got, tc.want)
			}
		})
	}
	if EventVersion != "1" {
		t.Fatalf("EventVersion = %q, want 1", EventVersion)
	}
	if EventSource != "orbit-auth" {
		t.Fatalf("EventSource = %q, want orbit-auth", EventSource)
	}
}

func TestLifecycleEventPayloadSchemas(t *testing.T) {
	at := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		payload any
	}{
		{"user.created", UserCreatedPayload{UserID: "u-1", OccurredAt: at}},
		{"org.created", OrgCreatedPayload{OrgID: "o-1", Name: "Acme", Slug: "acme", OccurredAt: at}},
		{"org.slug_changed", OrgSlugChangedPayload{OrgID: "o-1", Slug: "acme-cafe", PreviousSlug: "acme", ActorID: "u-1", OccurredAt: at}},
		{"membership.added", MembershipAddedPayload{OrgID: "o-1", UserID: "u-1", Status: MembershipActive, Role: RoleMember, OccurredAt: at}},
		{"membership.removed", MembershipRemovedPayload{OrgID: "o-1", UserID: "u-1", Status: MembershipActive, OccurredAt: at}},
		{"membership.suspended", MembershipSuspendedPayload{OrgID: "o-1", UserID: "u-1", Status: MembershipSuspended, OccurredAt: at}},
		{"membership.role_changed", MembershipRoleChangedPayload{OrgID: "o-1", UserID: "u-1", Role: RoleAdmin, PreviousRole: RoleMember, ActorID: "u-2", OccurredAt: at}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bustest.CheckEventSchema(t, tc.payload, "testdata/"+tc.name+".schema.json")
		})
	}
}

func TestLifecycleEventConstructors(t *testing.T) {
	at := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		event   Event
		subject string
	}{
		{"user", NewUserCreatedEvent("u-1", at), SubjectUserCreated},
		{"org", NewOrgCreatedEvent("o-1", "Acme", "acme", at), SubjectOrgCreated},
		{"added", NewMembershipAddedEvent("o-1", "u-1", MembershipActive, RoleMember, at), SubjectMembershipAdded},
		{"removed", NewMembershipRemovedEvent("o-1", "u-1", MembershipSuspended, at), SubjectMembershipRemoved},
		{"suspended", NewMembershipSuspendedEvent("o-1", "u-1", MembershipSuspended, at), SubjectMembershipSuspended},
		{"slug changed", NewOrgSlugChangedEvent("o-1", "acme-cafe", "acme", "u-1", at), SubjectOrgSlugChanged},
		{"role changed", NewMembershipRoleChangedEvent("o-1", "u-1", RoleAdmin, RoleMember, "u-2", at), SubjectMembershipRoleChanged},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.event.Subject != tc.subject {
				t.Fatalf("subject = %q, want %q", tc.event.Subject, tc.subject)
			}
			if !tc.event.OccurredAt.Equal(at) {
				t.Fatalf("occurred_at = %v, want %v", tc.event.OccurredAt, at)
			}
		})
	}
}

func TestMembershipAddedAndRoleChangedPayloadsCarryRole(t *testing.T) {
	at := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	added, ok := NewMembershipAddedEvent("o-1", "u-1", MembershipActive, RoleOwner, at).Payload.(MembershipAddedPayload)
	if !ok || added.Role != RoleOwner {
		t.Fatalf("added payload = %+v, want role owner", added)
	}
	changed, ok := NewMembershipRoleChangedEvent("o-1", "u-1", RoleAdmin, RoleMember, "u-2", at).Payload.(MembershipRoleChangedPayload)
	if !ok || changed.Role != RoleAdmin || changed.PreviousRole != RoleMember || changed.ActorID != "u-2" {
		t.Fatalf("role changed payload = %+v", changed)
	}
}

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
		{"org.created", OrgCreatedPayload{OrgID: "o-1", Name: "Acme", OccurredAt: at}},
		{"membership.added", MembershipAddedPayload{OrgID: "o-1", UserID: "u-1", Status: MembershipActive, OccurredAt: at}},
		{"membership.removed", MembershipRemovedPayload{OrgID: "o-1", UserID: "u-1", Status: MembershipActive, OccurredAt: at}},
		{"membership.suspended", MembershipSuspendedPayload{OrgID: "o-1", UserID: "u-1", Status: MembershipSuspended, OccurredAt: at}},
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
		{"org", NewOrgCreatedEvent("o-1", "Acme", at), SubjectOrgCreated},
		{"added", NewMembershipAddedEvent("o-1", "u-1", MembershipActive, at), SubjectMembershipAdded},
		{"removed", NewMembershipRemovedEvent("o-1", "u-1", MembershipSuspended, at), SubjectMembershipRemoved},
		{"suspended", NewMembershipSuspendedEvent("o-1", "u-1", MembershipSuspended, at), SubjectMembershipSuspended},
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

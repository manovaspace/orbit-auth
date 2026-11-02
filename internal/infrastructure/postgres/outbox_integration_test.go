package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	messagebus "github.com/manovaspace/orbit-message-bus"
	"github.com/manovaspace/orbit-message-bus/inproc"
	"github.com/manovaspace/orbit-message-bus/outbox"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// TestOutboxRollbackWritesNoEvent proves the event writer is transaction-bound:
// a rolled-back change leaves no outbox row and the relay publishes nothing.
func TestOutboxRollbackWritesNoEvent(t *testing.T) {
	_, db := migrationPool(t)
	ctx := context.Background()
	bus := inproc.New(messagebus.Config{})
	t.Cleanup(func() { _ = bus.Close() })

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := newOutboxSink(tx).Emit(ctx, domain.NewUserCreatedEvent(uuid.NewString(), time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	relay := &outbox.Relay{Pool: db.Pool, Bus: bus}
	published, err := relay.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if published != 0 {
		t.Fatalf("relayed %d events after rollback, want 0", published)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("outbox rows = %d, want 0", count)
	}
}

// TestRolledBackSignupWritesNoEvent covers a store transaction that writes the
// user.created row and then fails while accepting the invite. The rollback must
// remove both the user and the event.
func TestRolledBackSignupWritesNoEvent(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()

	inviter, err := store.FindOrCreateByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	org := domain.Organization{ID: uuid.NewString(), Name: "Acme", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOrg(ctx, org, inviter.ID); err != nil {
		t.Fatal(err)
	}
	invite := pendingInvite(org.ID, inviter.ID, "new@example.com", "rollback-token")
	if err := store.CreateInvite(ctx, invite); err != nil {
		t.Fatal(err)
	}
	// Revoking leaves the invite id valid but no longer pending, so acceptance
	// inside CreateSignupUser affects no rows after user.created was written.
	if err := store.RevokeInvite(ctx, org.ID, invite.ID); err != nil {
		t.Fatal(err)
	}

	user := signupUser("new@example.com", domain.UserActive)
	user.EmailVerified = true
	created, err := store.CreateSignupUser(ctx, user, nil, &invite, now)
	if !errors.Is(err, domain.ErrInviteNotFound) {
		t.Fatalf("CreateSignupUser err = %v, want ErrInviteNotFound", err)
	}
	if created {
		t.Fatal("rolled-back signup reported created")
	}
	if _, err := store.FindByEmail(ctx, user.Email); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("rolled-back user err = %v, want no rows", err)
	}
	for _, subject := range []string{domain.SubjectUserCreated, domain.SubjectMembershipAdded} {
		var count int
		if err := db.Pool.QueryRow(ctx,
			`SELECT count(*) FROM outbox_events
			 WHERE subject = $1 AND envelope->'payload'->>'user_id' = $2`,
			subject, user.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows for the rolled-back user = %d, want 0", subject, count)
		}
	}
}

// TestOutboxCommitPublishesOnceAndRestartRedeliversNothing is the L5 message-bus
// check against a real Postgres and the in-process bus. AddMember is one
// committed change and must produce exactly one membership.added; a new relay
// instance must not publish a row that was already published.
func TestOutboxCommitPublishesOnceAndRestartRedeliversNothing(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	bus := inproc.New(messagebus.Config{})
	t.Cleanup(func() { _ = bus.Close() })

	added := subscribeEvents(t, bus, domain.SubjectMembershipAdded)

	owner, err := store.FindOrCreateByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.FindOrCreateByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	org := domain.Organization{ID: uuid.NewString(), Name: "Acme", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOrg(ctx, org, owner.ID); err != nil {
		t.Fatal(err)
	}

	relay := &outbox.Relay{Pool: db.Pool, Bus: bus}
	// Two user.created, org.created and the creator's membership.added.
	published, err := relay.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if published != 4 {
		t.Fatalf("initial relay published %d events, want 4", published)
	}
	first := waitEvent(t, added)
	if first.Type != domain.SubjectMembershipAdded {
		t.Fatalf("first membership event type = %q", first.Type)
	}
	if first.Metadata["source"] != domain.EventSource || first.Metadata["version"] != domain.EventVersion {
		t.Fatalf("metadata = %v, want source %q version %q", first.Metadata, domain.EventSource, domain.EventVersion)
	}
	assertNoEvent(t, added)

	// One committed change: adding a member writes exactly one outbox row and
	// the relay publishes exactly one event.
	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending outbox rows = %d after AddMember, want 1", pending)
	}

	published, err = relay.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if published != 1 {
		t.Fatalf("relay published %d events for AddMember, want 1", published)
	}
	env := waitEvent(t, added)
	if env.Type != domain.SubjectMembershipAdded {
		t.Fatalf("event type = %q, want %q", env.Type, domain.SubjectMembershipAdded)
	}
	if env.Metadata["source"] != domain.EventSource || env.Metadata["version"] != domain.EventVersion {
		t.Fatalf("metadata = %v, want source %q version %q", env.Metadata, domain.EventSource, domain.EventVersion)
	}
	var payload domain.MembershipAddedPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.OrgID != org.ID || payload.UserID != member.ID || payload.Status != domain.MembershipActive {
		t.Fatalf("payload = %+v, want org %s user %s status active", payload, org.ID, member.ID)
	}
	if payload.OccurredAt.IsZero() {
		t.Fatal("payload occurred_at is zero")
	}
	assertNoEvent(t, added)

	// A restarted relay reads only pending rows, so nothing is published twice.
	restarted := &outbox.Relay{Pool: db.Pool, Bus: bus}
	published, err = restarted.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if published != 0 {
		t.Fatalf("restarted relay republished %d events, want 0", published)
	}
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("pending outbox rows after restart = %d, want 0", pending)
	}
	assertNoEvent(t, added)
}

// TestLifecycleEventSubjectsInOneTransaction checks the subjects written by the
// user, organization and membership store methods.
func TestLifecycleEventSubjectsInOneTransaction(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()
	bus := inproc.New(messagebus.Config{})
	t.Cleanup(func() { _ = bus.Close() })

	userCreated := subscribeEvents(t, bus, domain.SubjectUserCreated)
	orgCreated := subscribeEvents(t, bus, domain.SubjectOrgCreated)
	added := subscribeEvents(t, bus, domain.SubjectMembershipAdded)
	suspended := subscribeEvents(t, bus, domain.SubjectMembershipSuspended)
	removed := subscribeEvents(t, bus, domain.SubjectMembershipRemoved)

	owner, err := store.FindOrCreateByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.FindOrCreateByEmail(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	org := domain.Organization{ID: uuid.NewString(), Name: "Acme", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOrg(ctx, org, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMember(ctx, org.ID, member.ID, domain.MembershipActive); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemberStatus(ctx, org.ID, member.ID, domain.MembershipSuspended); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveMember(ctx, org.ID, member.ID); err != nil {
		t.Fatal(err)
	}

	relay := &outbox.Relay{Pool: db.Pool, Bus: bus}
	published, err := relay.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if published != 7 {
		t.Fatalf("published %d events, want 7", published)
	}
	if env := waitEvent(t, userCreated); env.Type != domain.SubjectUserCreated {
		t.Fatalf("first user event = %q", env.Type)
	}
	if env := waitEvent(t, userCreated); env.Type != domain.SubjectUserCreated {
		t.Fatalf("second user event = %q", env.Type)
	}
	if env := waitEvent(t, orgCreated); env.Type != domain.SubjectOrgCreated {
		t.Fatalf("org event = %q", env.Type)
	}
	if env := waitEvent(t, added); env.Type != domain.SubjectMembershipAdded {
		t.Fatalf("first added event = %q", env.Type)
	}
	if env := waitEvent(t, added); env.Type != domain.SubjectMembershipAdded {
		t.Fatalf("second added event = %q", env.Type)
	}
	if env := waitEvent(t, suspended); env.Type != domain.SubjectMembershipSuspended {
		t.Fatalf("suspended event = %q", env.Type)
	}
	if env := waitEvent(t, removed); env.Type != domain.SubjectMembershipRemoved {
		t.Fatalf("removed event = %q", env.Type)
	}
}

func subscribeEvents(t *testing.T, bus messagebus.Bus, subject string) <-chan messagebus.Envelope {
	t.Helper()
	ch := make(chan messagebus.Envelope, 32)
	if err := bus.Subscribe(context.Background(), subject, func(_ context.Context, env messagebus.Envelope) error {
		ch <- env
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return ch
}

func waitEvent(t *testing.T, ch <-chan messagebus.Envelope) messagebus.Envelope {
	t.Helper()
	select {
	case env := <-ch:
		return env
	case <-time.After(5 * time.Second):
		t.Fatal("event was not delivered")
		return messagebus.Envelope{}
	}
}

func assertNoEvent(t *testing.T, ch <-chan messagebus.Envelope) {
	t.Helper()
	select {
	case env := <-ch:
		t.Fatalf("unexpected event %q", env.Type)
	case <-time.After(300 * time.Millisecond):
	}
}

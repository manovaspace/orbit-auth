package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

func ownerActor(id string) domain.MembershipActor {
	return domain.MembershipActor{CallerID: id, Action: domain.ActionSetSlug}
}

func TestCreateOrgRegistersItsSlugAndRejectsADuplicate(t *testing.T) {
	store, org, ids := ownerFixture(t)
	ctx := context.Background()

	got, err := store.GetOrg(ctx, org.ID)
	if err != nil || got.Slug != org.Slug {
		t.Fatalf("GetOrg slug = %q, %v; want %q", got.Slug, err, org.Slug)
	}
	listed, err := store.ListOrgsForUser(ctx, ids["owner"])
	if err != nil || len(listed) != 1 || listed[0].Slug != org.Slug {
		t.Fatalf("ListOrgsForUser = %+v, %v", listed, err)
	}
	resolved, err := store.ResolveSlug(ctx, org.Slug)
	if err != nil || resolved.ID != org.ID || resolved.Slug != org.Slug {
		t.Fatalf("ResolveSlug = %+v, %v", resolved, err)
	}
	if ok, err := store.SlugAvailable(ctx, org.Slug); err != nil || ok {
		t.Fatalf("SlugAvailable(taken) = %v, %v", ok, err)
	}
	if ok, err := store.SlugAvailable(ctx, "free-slug"); err != nil || !ok {
		t.Fatalf("SlugAvailable(free) = %v, %v", ok, err)
	}

	clash := testOrg("Clash", time.Now().UTC())
	clash.Slug = org.Slug
	if err := store.CreateOrg(ctx, clash, ids["owner"]); !errors.Is(err, domain.ErrSlugTaken) {
		t.Fatalf("duplicate slug err = %v, want ErrSlugTaken", err)
	}
	if _, err := store.GetOrg(ctx, clash.ID); !errors.Is(err, domain.ErrOrgNotFound) {
		t.Fatalf("the failed creation left an organization behind: %v", err)
	}
	invalid := testOrg("Invalid", time.Now().UTC())
	invalid.Slug = "WWW"
	if err := store.CreateOrg(ctx, invalid, ids["owner"]); !errors.Is(err, domain.ErrInvalidSlug) {
		t.Fatalf("invalid slug err = %v, want ErrInvalidSlug", err)
	}
	if _, err := store.ResolveSlug(ctx, "nobody-here"); !errors.Is(err, domain.ErrSlugNotFound) {
		t.Fatalf("unknown slug err = %v, want ErrSlugNotFound", err)
	}
}

func TestSetOrgSlugRetiresTheOldSlugWithoutFreeingIt(t *testing.T) {
	store, org, ids := ownerFixture(t)
	ctx := context.Background()
	old := org.Slug

	if err := store.SetOrgSlug(ctx, org.ID, "new-home", ownerActor(ids["owner"])); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetOrg(ctx, org.ID)
	if err != nil || got.Slug != "new-home" {
		t.Fatalf("slug after rename = %q, %v", got.Slug, err)
	}
	viaOld, err := store.ResolveSlug(ctx, old)
	if err != nil || viaOld.ID != org.ID || viaOld.Slug != "new-home" {
		t.Fatalf("old slug resolves to %+v, %v; want the same organization with its new slug", viaOld, err)
	}
	if ok, err := store.SlugAvailable(ctx, old); err != nil || ok {
		t.Fatalf("a retired slug is available again (%v, %v): the host could be taken over", ok, err)
	}
	other := testOrg("Other", time.Now().UTC())
	other.Slug = old
	if err := store.CreateOrg(ctx, other, ids["owner"]); !errors.Is(err, domain.ErrSlugTaken) {
		t.Fatalf("another organization claimed a retired slug: %v", err)
	}

	// Setting the current slug again is a no-op and writes no event.
	var before int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE subject = $1`, domain.SubjectOrgSlugChanged).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := store.SetOrgSlug(ctx, org.ID, "new-home", ownerActor(ids["owner"])); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE subject = $1`, domain.SubjectOrgSlugChanged).Scan(&after); err != nil || after != before {
		t.Fatalf("events after a no-op = %d (was %d), %v", after, before, err)
	}

	// The organization may take its own retired slug back.
	if err := store.SetOrgSlug(ctx, org.ID, old, ownerActor(ids["owner"])); err != nil {
		t.Fatalf("re-claiming an own retired slug: %v", err)
	}
	back, err := store.ResolveSlug(ctx, "new-home")
	if err != nil || back.Slug != old {
		t.Fatalf("the slug renamed away from resolves to %+v, %v; want the restored slug", back, err)
	}
	var current int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM organization_slugs WHERE org_id = $1::uuid AND retired_at IS NULL`, org.ID).Scan(&current); err != nil || current != 1 {
		t.Fatalf("current slug rows = %d, %v; want exactly 1", current, err)
	}

	var raw []byte
	if err := store.pool.QueryRow(ctx, `SELECT envelope -> 'payload' FROM outbox_events WHERE subject = $1 ORDER BY id LIMIT 1`, domain.SubjectOrgSlugChanged).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload domain.OrgSlugChangedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.OrgID != org.ID || payload.Slug != "new-home" || payload.PreviousSlug != old || payload.ActorID != ids["owner"] {
		t.Fatalf("event payload = %+v", payload)
	}
}

func TestSetOrgSlugChecksTheActorUnderTheLockAndTheSlug(t *testing.T) {
	store, org, ids := ownerFixture(t)
	ctx := context.Background()

	if err := store.SetOrgSlug(ctx, org.ID, "by-admin", ownerActor(ids["admin"])); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin rename err = %v, want ErrForbidden", err)
	}
	if err := store.SetOrgSlug(ctx, org.ID, "by-member", ownerActor(ids["member"])); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member rename err = %v, want ErrForbidden", err)
	}
	if err := store.SetOrgSlug(ctx, org.ID, "api", ownerActor(ids["owner"])); !errors.Is(err, domain.ErrInvalidSlug) {
		t.Fatalf("reserved slug err = %v, want ErrInvalidSlug", err)
	}
	taken := testOrg("Taken", time.Now().UTC())
	taken.Slug = "taken-name"
	if err := store.CreateOrg(ctx, taken, ids["owner"]); err != nil {
		t.Fatal(err)
	}
	if err := store.SetOrgSlug(ctx, org.ID, "taken-name", ownerActor(ids["owner"])); !errors.Is(err, domain.ErrSlugTaken) {
		t.Fatalf("taken slug err = %v, want ErrSlugTaken", err)
	}
	got, err := store.GetOrg(ctx, org.ID)
	if err != nil || got.Slug != org.Slug {
		t.Fatalf("slug after refused renames = %q, %v; want unchanged %q", got.Slug, err, org.Slug)
	}
	var current int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM organization_slugs WHERE org_id = $1::uuid AND retired_at IS NULL`, org.ID).Scan(&current); err != nil || current != 1 {
		t.Fatalf("a refused rename left %d current slugs, %v; want 1", current, err)
	}
}

func TestConcurrentClaimsOfOneSlugHaveOneWinner(t *testing.T) {
	store, first, ids := ownerFixture(t)
	ctx := context.Background()
	second := testOrg("Second", time.Now().UTC())
	if err := store.CreateOrg(ctx, second, ids["owner"]); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, orgID := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			errs <- store.SetOrgSlug(ctx, id, "contested", ownerActor(ids["owner"]))
		}(orgID)
	}
	close(start)
	wg.Wait()
	close(errs)

	wins, taken := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, domain.ErrSlugTaken):
			taken++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 || taken != 1 {
		t.Fatalf("wins = %d, taken = %d; want 1 and 1", wins, taken)
	}
}

func TestSignupWithAnOrganizationRegistersItsSlug(t *testing.T) {
	store, _ := migrationPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	user := signupUser("owner@example.com", domain.UserSignupPending)
	org := testOrg("Acme", now)
	if created, err := store.CreateSignupUser(ctx, user, &org, nil, now); err != nil || !created {
		t.Fatalf("signup = %v, %v", created, err)
	}
	resolved, err := store.ResolveSlug(ctx, org.Slug)
	if err != nil || resolved.ID != org.ID {
		t.Fatalf("signup organization slug resolves to %+v, %v", resolved, err)
	}
	var raw []byte
	if err := store.pool.QueryRow(ctx, `SELECT envelope -> 'payload' FROM outbox_events WHERE subject = $1 AND envelope -> 'payload' ->> 'org_id' = $2`, domain.SubjectOrgCreated, org.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload domain.OrgCreatedPayload
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Slug != org.Slug {
		t.Fatalf("org.created payload = %+v, %v; want slug %q", payload, err, org.Slug)
	}
}

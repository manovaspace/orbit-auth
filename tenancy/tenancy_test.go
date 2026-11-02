package tenancy_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/tenancy"
)

func TestOrgContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := tenancy.OrgFrom(ctx); ok {
		t.Fatal("OrgFrom on a bare context reported an organization")
	}

	orgID := uuid.New()
	ctx = tenancy.WithOrg(ctx, orgID)
	got, ok := tenancy.OrgFrom(ctx)
	if !ok || got != orgID {
		t.Fatalf("OrgFrom = %v, %v, want %v, true", got, ok, orgID)
	}
	if got := tenancy.MustOrg(ctx); got != orgID {
		t.Fatalf("MustOrg = %v, want %v", got, orgID)
	}

	// A later value replaces the earlier one instead of stacking.
	other := uuid.New()
	got, ok = tenancy.OrgFrom(tenancy.WithOrg(ctx, other))
	if !ok || got != other {
		t.Fatalf("nested OrgFrom = %v, %v, want %v, true", got, ok, other)
	}
}

func TestWithOrgNilClearsOrg(t *testing.T) {
	ctx := tenancy.WithOrg(context.Background(), uuid.New())
	if _, ok := tenancy.OrgFrom(tenancy.WithOrg(ctx, uuid.Nil)); ok {
		t.Fatal("WithOrg(uuid.Nil) kept an organization in context")
	}
}

func TestMustOrgPanicsWithoutOrg(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustOrg did not panic without an organization")
		}
	}()
	_ = tenancy.MustOrg(context.Background())
}

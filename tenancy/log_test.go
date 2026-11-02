package tenancy_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/manovaspace/orbit-auth/tenancy"
)

func TestLogAttrsWithoutOrg(t *testing.T) {
	if attrs := tenancy.LogAttrs(context.Background()); len(attrs) != 0 {
		t.Fatalf("LogAttrs = %v, want none", attrs)
	}
}

func TestLogAttrsWithOrg(t *testing.T) {
	orgID := uuid.New()
	attrs := tenancy.LogAttrs(tenancy.WithOrg(context.Background(), orgID))
	if len(attrs) != 1 {
		t.Fatalf("LogAttrs = %v, want one attr", attrs)
	}
	if attrs[0].Key != "org_id" {
		t.Fatalf("attr key = %q, want org_id", attrs[0].Key)
	}
	if got := attrs[0].Value.String(); got != orgID.String() {
		t.Fatalf("attr value = %q, want %q", got, orgID)
	}
	if attrs[0].Value.Kind() != slog.KindString {
		t.Fatalf("attr kind = %v, want string", attrs[0].Value.Kind())
	}
}

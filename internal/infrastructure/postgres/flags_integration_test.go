package postgres

import (
	"context"
	"testing"
	"time"
)

func TestListFeatureFlagsOrdersAndReturnsValues(t *testing.T) {
	store, db := migrationPool(t)
	ctx := context.Background()

	// The isolated migration schema seeds one flag; clear it so the test
	// asserts only the two rows it inserts.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM feature_flags`); err != nil {
		t.Fatal(err)
	}

	updatedAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO feature_flags (name, enabled, description, updated_at)
		VALUES
			('zeta.flag', FALSE, 'last', $1),
			('alpha.flag', TRUE, 'first', $1)`, updatedAt); err != nil {
		t.Fatal(err)
	}

	flags, err := store.ListFeatureFlags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 2 {
		t.Fatalf("flags = %+v, want 2", flags)
	}
	if flags[0].Name != "alpha.flag" || flags[1].Name != "zeta.flag" {
		t.Fatalf("flags out of order: %+v", flags)
	}
	if !flags[0].Enabled || flags[0].Description != "first" {
		t.Fatalf("alpha.flag = %+v", flags[0])
	}
	if flags[1].Enabled || flags[1].Description != "last" {
		t.Fatalf("zeta.flag = %+v", flags[1])
	}
	if !flags[0].UpdatedAt.Equal(updatedAt) || !flags[1].UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated_at = %v and %v, want %v", flags[0].UpdatedAt, flags[1].UpdatedAt, updatedAt)
	}
}

package tenancy_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/manovaspace/orbit-auth/internal/testutil"
	"github.com/manovaspace/orbit-auth/tenancy"
)

const currentOrgSetting = "SELECT COALESCE(current_setting('app.org_id', true), '')"

func TestSetLocalOrg(t *testing.T) {
	db := testutil.OpenPostgres(t)
	ctx := context.Background()

	sqlDB, err := sql.Open("pgx", db.URL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	orgID := uuid.New()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := tenancy.SetLocalOrg(tenancy.WithOrg(ctx, orgID), tx); err != nil {
		t.Fatalf("SetLocalOrg: %v", err)
	}

	var inside string
	if err := tx.QueryRowContext(ctx, currentOrgSetting).Scan(&inside); err != nil {
		t.Fatalf("read app.org_id inside transaction: %v", err)
	}
	if inside != orgID.String() {
		t.Fatalf("app.org_id inside transaction = %q, want %q", inside, orgID)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var outside string
	if err := sqlDB.QueryRowContext(ctx, currentOrgSetting).Scan(&outside); err != nil {
		t.Fatalf("read app.org_id after commit: %v", err)
	}
	if outside != "" {
		t.Fatalf("app.org_id after commit = %q, want empty", outside)
	}
}

func TestSetLocalOrgRequiresOrg(t *testing.T) {
	execer := &recordingExecer{}

	err := tenancy.SetLocalOrg(context.Background(), execer)
	if !errors.Is(err, tenancy.ErrOrgRequired) {
		t.Fatalf("SetLocalOrg error = %v, want ErrOrgRequired", err)
	}
	if execer.calls != 0 {
		t.Fatalf("SetLocalOrg executed %d statements without an organization, want 0", execer.calls)
	}
}

type recordingExecer struct {
	calls int
}

func (e *recordingExecer) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	e.calls++
	return nil, nil
}

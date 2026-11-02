package tenancy

import (
	"context"
	"database/sql"
)

// Execer is the part of *sql.Tx or *sql.DB that SetLocalOrg needs. It is
// driver-neutral: any Postgres adapter exposing ExecContext can satisfy it.
type Execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

// SetLocalOrg runs SET LOCAL app.org_id inside tx, using the organization in
// ctx. The value is passed as a bind parameter, never built into the SQL. It
// returns ErrOrgRequired when ctx has no organization.
//
// Postgres scopes the setting to the transaction, so row-level security sees
// it for the rest of the transaction and the setting is gone after commit or
// rollback.
func SetLocalOrg(ctx context.Context, tx Execer) error {
	orgID, ok := OrgFrom(ctx)
	if !ok {
		return ErrOrgRequired
	}
	_, err := tx.ExecContext(ctx, "SELECT set_config('app.org_id', $1, true)", orgID.String())
	return err
}

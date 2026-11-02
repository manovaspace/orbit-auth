# Orbit tenancy package

`github.com/manovaspace/orbit-auth/tenancy` carries the active organization
from a request into the request context and into a Postgres transaction. It is
the shared piece every product uses so row-level tenancy has one answer
([ADR-045 D3–D4](/decisions/045-tenancy-and-access)).

The package does **not** verify tokens, resolve memberships, or decide
permissions. Products verify the access token first (with
`github.com/manovaspace/orbit-auth/token` or the gateway) and give tenancy the
verified `token.AccessClaims`.

## Context

```go
ctx = tenancy.WithOrg(ctx, orgID)
orgID, ok := tenancy.OrgFrom(ctx)
orgID = tenancy.MustOrg(ctx) // only behind RequireOrg or UnaryServerInterceptor
```

`WithOrg(ctx, uuid.Nil)` clears the organization. `MustOrg` panics when no
organization is present.

## HTTP middleware

`RequireOrg` is `net/http` middleware. It reads already-verified claims through
a `ClaimsFunc`, so the package never parses a token itself.

```go
func claims(r *http.Request) (token.AccessClaims, bool) {
    return productAuth.VerifiedClaims(r) // put here by the auth middleware
}

mux.Handle("/api/", tenancy.RequireOrg(claims)(productHandler))
```

| Request | Result |
| --- | --- |
| verified claims with a valid `org` | next handler runs, `OrgFrom` returns the org |
| verified claims without `org`, no fallback | `403` `{"error":"org_required"}` |
| verified claims without `org`, fallback configured | next handler runs with the fallback |
| malformed `org` | `401` `{"error":"invalid_org"}` |
| no verified claims | `401` `{"error":"unauthenticated"}` |

A malformed claim is never replaced by the fallback.

Fiber v3 users adapt this `net/http` middleware at the edge, as
[ADR-041 D3](/decisions/041-go-service-architecture) describes. For example:

```go
app.Use(adaptor.HTTPMiddleware(tenancy.RequireOrg(claims)))
```

This package imports no Fiber package.

## gRPC interceptor

```go
server := grpc.NewServer(
    grpc.UnaryInterceptor(tenancy.UnaryServerInterceptor(verifiedClaimsFromContext)),
)
```

The rules match HTTP: missing `org` without a fallback is
`codes.PermissionDenied` with message `org_required`, a malformed `org` is
`codes.Unauthenticated`, and no verified claims is `codes.Unauthenticated`.

## Postgres transaction

```go
tx, err := pool.Begin(ctx)
if err != nil {
    return err
}
defer func() { _ = tx.Rollback(ctx) }()

if err := tenancy.SetLocalOrg(ctx, tx); err != nil {
    return err // ErrOrgRequired when the context has no organization
}

// Run queries in tx; RLS policy current_setting('app.org_id') sees the org.
return tx.Commit(ctx)
```

`SetLocalOrg` runs
`SELECT set_config('app.org_id', $1, true)`. The organization is a bind
parameter, never string-built SQL, and Postgres scopes it to the transaction,
so it is gone after commit or rollback. `Execer` only requires `ExecContext`,
so adapt the pgx transaction (or use `database/sql`) at the call site.

A product that has not set `app.org_id` in a transaction gets no rows from an
RLS-protected table (ADR-045 consequences), which is the desired fail-closed
answer.

## Logging

`LogAttrs(ctx)` returns `org_id` when the context carries an organization:

```go
attrs := append(baseAttrs, tenancy.LogAttrs(ctx)...)
```

Pass those attributes to the product's own `observability.RegisterContextAttrs`
or logger. The package does not import `orbit-observability`.

## The fallback is temporary

`WithFallbackOrg` exists only so a single-tenant product can keep serving while
its tokens and data gain an `org`. It is not a feature and not a default:

- Remove it once every request that reaches tenancy has a verified `org` claim.
- Never use it to paper over a token that should have carried an organization.
- A malformed claim fails closed even with a fallback.
- Track the removal; leaving it in eventually turns a tenancy bug into silent
  data access for the wrong organization.

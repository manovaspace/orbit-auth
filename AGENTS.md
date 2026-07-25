# orbit-auth

Tier 2 product user authentication — OTP login, password login, JWT sessions, API tokens. Staff SSO is separate from this product auth service.

## Commands

```bash
export DEPLOYMENT_ENVIRONMENT=dev
export DATABASE_URL=postgres://orbit:orbit@localhost:10332/auth?sslmode=disable
export NOTIFICATIONS_GRPC_ADDR=localhost:10110
go run ./cmd/auth
go test ./...
./scripts/generate-proto.sh   # after proto changes
```

gRPC: **10100**. Requires `orbit-notifications` on **10110** and dev Postgres.

## Stack

- Go 1.26, gRPC + Postgres
- Migrations in `migrations/`
- Proto in `api/proto/auth/v1/`

## Docs

| Topic | Path |
| --- | --- |

## Structure

| Path | Role |
| --- | --- |
| `cmd/auth/` | gRPC server entry |
| `internal/domain/` | Ports, entities |
| `internal/application/` | OTP, password, token use cases |
| `internal/infrastructure/postgres/` | Store adapter |
| `internal/infrastructure/grpc/` | gRPC handlers |

## Do / don't

- Use `log/slog` — not `log.Printf`
- Notification delivery via `orbit-notifications` gRPC — not direct SMTP
- Migrations are sequential — do not skip numbers
- No commit unless user asks

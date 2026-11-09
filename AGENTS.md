---
read_with:
  - handbook/docs/orbit/architecture/go-toolchain.md
  - handbook/docs/orbit/architecture/library-design.md
  - handbook/docs/orbit/decisions/011-platform-auth-notifications.md
  - handbook/docs/orbit/decisions/016-orbit-auth-password-and-api-tokens.md
  - handbook/docs/orbit/guides/platform-dev-quickstart.md
---

# orbit-auth

Tier 2 product user authentication — OTP login, password login, JWT sessions, API tokens. Staff SSO is Keycloak workforce identity (ADR-027).

## Commands

```bash
export DEPLOYMENT_ENVIRONMENT=dev
export DATABASE_URL=postgres://auth:<password>@127.0.0.1:10000/auth?sslmode=disable
export NOTIFICATIONS_GRPC_ADDR=localhost:10012
export GRPC_PORT=10010
export HEALTH_PORT=10011
export AUTH_AUTO_SELECT_SINGLE_ORG=true
export AUTH_SIGNUP=off
go run ./cmd/auth
go run ./cmd/auth migrate status   # up, down, status
go test ./...
TEST_DATABASE_URL='postgres://postgres:postgres@localhost:55440/test?sslmode=disable' go test ./... -race -count=1
TEST_NATS_URL=nats://localhost:4222 go test ./...   # runs the NATS durable-consumer test
./scripts/generate-proto.sh   # after proto changes
```

Ports: gRPC on **10010** (`GRPC_PORT`), HTTP health on **10011** (`HEALTH_PORT`, `/healthz`, `/readyz`). HTTP runs on Fiber v3; gRPC is unchanged.
Requires `orbit-notifications` on **10012** and dev Postgres.

Start with orbit-infra: `orbit/orbit-infra/scripts/start-tier2.sh`

## Architecture

- `internal/domain` — user, OTP and API-token types, lifecycle event payloads, pinned token rules and the ports: `OTPStore`, `UserStore`, `RefreshTokenStore`, `APITokenStore`, `Notifier`, `Flags`, `Clock`, `IDGenerator`, `TokenSigner`, `Hasher`, `CodeGenerator`, `SecretGenerator`, `EventSink`. It imports no infrastructure, driver, HTTP framework or process package.
- `internal/application` — OTP, password, refresh-token, API-token and demo-seed use cases. It receives `application.Deps` and reads no environment variable.
- `internal/infrastructure` — adapters: `postgres` (sqlc-generated queries), `postgres/migrations` (embedded goose), `grpc`, `httpapi` (Fiber v3 probes), `notifications` (gRPC client), `featureflags`, `credentials` (bcrypt, OTP codes, API secrets), `jwt` (RS256 signer), `clock`, `ids`.
- `cmd/auth` — wiring only, including `migrate` and demo-mode parsing.
- `.golangci.yml` `depguard` rules enforce both boundaries. Do not weaken them to land a change.

## Non-negotiables

1. **Tenant isolation is sacred** — all user queries and data operations must be scoped by tenant/user ID. Zero cross-tenant leakage.
2. **JWT verification before mutations** — never trust unauthenticated claims or bypass token validation.
3. **Notifications strictly via gRPC** — call `orbit-notifications` on port 10012; never implement direct SMTP or raw mail calls in orbit-auth.
4. **`log/slog` only** — `log.Printf` and `fmt.Println` logging are strictly forbidden.
5. **Sequential embedded migrations** — files in `internal/infrastructure/postgres/migrations/` are numbered sequentially without gaps. Never alter previously applied migrations. Adoption of the old `schema_migrations` state is documented in [docs/migrations.md](./docs/migrations.md).
6. **Zero credential leakage** — never log passwords, plaintext OTP codes, or JWT signing keys in stdout/stderr.
7. **Credential formats are contracts** — bcrypt cost, OTP rules, refresh-token shape, `oat_` API-token format and RS256 claims are pinned by characterization tests. Changing one needs a migration and an ADR, not a refactor.

## Engineering gotchas

- **gRPC interceptor order:** Panic recovery must wrap the outermost boundary; auth token validation must precede tenant context extraction in the interceptor chain.
- **Proto code regeneration:** When editing `api/proto/auth/v1/`, run `./scripts/generate-proto.sh`. Never manually modify generated `*.pb.go` files.
- **Unleash flag fallback:** When Unleash flags (`manova.auth.email_otp`, `manova.auth.mobile_otp`) are toggled off or unreachable, handlers return typed errors (`ErrEmailOTPDisabled`). Unit tests must mock the flag provider.
- **Demo mode guard:** `DEMO_MODE=true` is permitted only when `DEPLOYMENT_ENVIRONMENT=dev`. `cmd/auth` fails closed before the service starts if demo mode is enabled elsewhere.
- **Migration adoption:** `migrate up` maps the old `schema_migrations` rows to goose versions without running SQL and without touching `schema_migrations`. Do not drop that table in this release.

## Organization administration

Memberships carry an administration tier `role` (`owner`, `admin`, `member`; migration 010). It decides who may change memberships and invitations in orbit-auth itself; products keep their own roles and permissions (ADR-045).

- The rule is one pure function, `domain.AuthorizeMembershipAction`. The caller must be an active owner or admin; an admin acts only on plain members; only an owner grants `owner` or `admin`; any active member may remove themselves.
- Every write RPC (`AddMember`, `RemoveMember`, `SuspendMember`, `SetMemberRole`, `InviteMember`, `RevokeInvite`) and `ListInvites` is authorized in `internal/application`. A caller who is not an active member gets `NotFound` (existence is not revealed); a member without authority gets `PermissionDenied`.
- The store keeps at least one active owner (`ErrLastOwner` → `FailedPrecondition`) under the organization row lock, so concurrent demotions cannot orphan an organization. The creator of an organization (`CreateOrg`, sign-up with `org_name`) is its first owner; invited and added members start as `member`.
- Migration 010 backfilled the earliest active member of each organization as its owner.

## Staff organization administration

`StaffOrgService` is the Manova staff surface over organizations. It is not for the public edge: the gateway authenticates staff through Keycloak and is the only caller.

- **Metadata:** every call carries `x-orbit-service-token`, compared in constant time with `AUTH_STAFF_SERVICE_TOKEN`, and `x-staff-subject`. Every write also carries `x-staff-reason`, recorded with the change. `StaffServiceUnaryServerInterceptor` checks all three before any handler runs. An empty `AUTH_STAFF_SERVICE_TOKEN` refuses every call with `Unavailable` ("staff service disabled"); a missing or wrong token is `Unauthenticated`. The token is never logged.
- **RPCs:** `ListOrganizations` (query, status, `page_size` up to 100, keyset `page_token`), `GetOrganization` (members and pending invitations, never token hashes), `ProvisionOrganization` (no member and one pending owner invitation by email or phone, idempotent on `idempotency_key` for the same name and owner, where a repeat rotates the owner invitation; any other repeat is `AlreadyExists`), `SuspendOrganization` and `ReactivateOrganization`, `SetOrganizationSlug` (the `SetOrgSlug` registry rules), `ReinviteOwner`.
- **Staff are never members.** Staff operations skip `AuthorizeMembershipAction`. Each write is logged with the staff subject and reason.
- **Suspension:** a suspended organization refuses `SelectOrg` and refresh into it (`FailedPrecondition`, "organization suspended"). Its members keep their memberships, and `ResolveTenant` reports `suspended`.
- **Migration 014** adds `organizations.status` (`active` or `suspended`), `suspended_at`, `suspended_reason`, the `organization_provisions` idempotency table (key, organization and owner target), and `organization_invites.inviter_staff_subject`. `inviter_id` becomes nullable, and exactly one of the two inviter columns is set.

## Organization slugs and tenant hosts

Every organization has a `slug`, the label of its tenant host (`<slug>.falak.<env>`; migration 011).

- Format (`domain.ValidateSlug`): 3 to 30 lowercase ASCII letters, digits and single hyphens, starting with a letter and ending with a letter or digit, not in the reserved list (`www`, `api`, `app`, `auth`, `admin`, `demo`, `internal`, `login`, `falak`, ...). The check constraint pins the format; the reserved list is code.
- Allocation (`CreateOrg`, sign-up with `org_name`): an explicit `slug` must be valid and free, otherwise the request fails. Without one the slug is derived from the name, then from the organization id (`org-` and 12 hex digits) when the name has no usable ASCII (for example a Persian name) or the derived slug is taken.
- `organization_slugs` is the registry of every slug an organization has held; its primary key makes a slug unique across organizations **and across time**. `SetOrgSlug` (owner only, rechecked under the organization lock) retires the old slug, which stays reserved for the organization and keeps resolving, so a rename never frees an address for someone else. An organization can take its own retired slug back.
- `InviteService` is public (`GetInvitePreview`, `AcceptInviteWithSignup`) and, like `TenantService`, sits outside `OrgService`, which is the only service the access-token interceptor guards. `GetInvitePreview` shows the organization, tier and expiry of a token and never the recipient. `AcceptInviteWithSignup` creates the account for a **phone** invitation; it is refused with `FailedPrecondition` "invite sign-up is disabled" only while `AUTH_INVITE_SIGNUP` is `off` or `false` (default on; it does not depend on `AUTH_SIGNUP`), `InvalidArgument` for a password under eight characters after trimming, `AlreadyExists` when an account already uses the number (that person signs in and calls `AcceptInvite`), and `NotFound` for any unusable token. The account's mobile is the invited number with `users.mobile_verified_at` NULL: invite sign-up does not verify the number, and only a successful mobile OTP sets the column (`VerifyOTP` on the `sms` channel, and demo seeding). Sign-in does not depend on the column.
- `OrgService.InviteMember` takes `email` or `phone` (exactly one) and an optional `role` (empty means `member`). Only a phone invitation returns the raw `token` in its response, because no email carries it there; an email invitation never returns it.
- `TenantService.ResolveTenant` is the only unauthenticated organization call. It answers the organization id, name and current slug, whether the requested slug is retired and whether the organization is suspended, never members. Unknown, invalid and reserved slugs are the same `NotFound`.

## Feature Flags & Demo Mode

- **Unleash flags:** `manova.auth.email_otp` and `manova.auth.mobile_otp` (`UNLEASH_URL`, `UNLEASH_API_TOKEN`, `UNLEASH_APP_NAME`). When disabled, `RequestOTP` returns `ErrEmailOTPDisabled` or `ErrMobileOTPDisabled`.
- **Organization auto-select:** `AUTH_AUTO_SELECT_SINGLE_ORG` (default `true`) adds the optional `org` access-token claim at login when the user has exactly one active membership. Zero or many memberships omit it, and a suspended organization is never chosen. `SelectOrg` rotates the refresh token and issues a pair carrying the selected org.
- **Refresh tokens:** `RefreshToken` and `SelectOrg` consume the presented token atomically (`RefreshTokenStore.Consume`), so a token can be redeemed once even under concurrent requests. Refreshing into or selecting a suspended organization fails with `FailedPrecondition` (`organization suspended`) and leaves the token unspent. `RevokeRefreshToken` revokes a token on logout and is idempotent: unknown, revoked or expired tokens succeed.
- **Demo seeding:** `DEMO_MODE=true` (dev only) and `AUTH_DEMO_USERS` JSON array seeds test user accounts into Postgres on startup. `cmd/auth` parses the JSON; the application validates and hashes each entry.
- **Invite sign-up:** `AUTH_INVITE_SIGNUP` defaults to on; only an explicit `off` or `false` disables `InviteService.AcceptInviteWithSignup`. It needs a token an owner or admin issued, so it is independent of `AUTH_SIGNUP`.
- **Self-serve sign-up:** `AUTH_SIGNUP` is `open` or `off` (default `off`), read in `cmd/auth` and injected through `application.Deps`. While off, `SignupService` returns `codes.FailedPrecondition` "sign-up is disabled" and writes nothing. A sign-up account is `signup_pending` until `VerifySignup` receives both the `signup_verify` OTP and the pending account password; password and OTP login refuse it with the generic invalid-credentials error. A retry for a still-pending address replaces its credentials and reissues the code (at most once per 60 seconds); an active account is left untouched. Admin and demo accounts are verified from the start, and a valid invite token verifies the address without an OTP.

## Events

Lifecycle events are written to the Postgres outbox (`outbox_events`, migration 008) in the same transaction as the change and relayed by `outbox.Relay` to `orbit-message-bus`:

- `auth.user.created` — a user is created by OTP login, sign-up, invite-sign-up or demo seeding
- `auth.org.created` — `CreateOrg`, sign-up with `org_name`, or staff `ProvisionOrganization`
- `auth.membership.added` — `AddMember`, invite acceptance (including phone invite sign-up), or the creator membership
- `auth.membership.removed` — `RemoveMember`
- `auth.membership.suspended` — `SuspendMember`
- `auth.membership.role_changed` — `SetMemberRole` (carries `role`, `previous_role` and `actor_id`)
- `auth.org.slug_changed` — `SetOrgSlug` or staff `SetOrganizationSlug` (carries `slug`, `previous_slug` and `actor_id`: the caller, or for a staff change the staff subject, with `reason`); `auth.org.created` carries the new organization's `slug`
- `auth.org.suspended` — staff `SuspendOrganization` (carries `org_id`, `staff_subject` and `reason`)
- `auth.org.reactivated` — staff `ReactivateOrganization` (same payload)

Subjects are the three-segment `{domain}.{entity}.{action}` shape. The version is envelope metadata `version="1"`, not a subject suffix. Payloads carry ids, the organization name for `org.created`, the membership status, the organization administration tier (`role`: owner, admin or member, never a product role) on `membership.added` and `occurred_at`; they never carry emails, tokens or secrets.

`BUS_TRANSPORT` selects the transport: `inproc` (default; relayed and logged, no subscribers), `nats` with `BUS_NATS_URL`, or `redis` with `BUS_REDIS_URL`. The relay runs in `cmd/auth` for the life of the process and stops on graceful shutdown; publish failures are logged with `slog` and retried, never crashing the gRPC server.

## Stack

- Go 1.27.3, gRPC + Fiber v3 health + Postgres
- Embedded goose migrations in `internal/infrastructure/postgres/migrations/`
- sqlc queries in `internal/infrastructure/postgres/queries/`, generated package committed
- Proto in `api/proto/auth/v1/`

## Docs

| Topic | Path |
| --- | --- |
| Migrations, adoption, rollback | `docs/migrations.md` |
| Platform auth ADR | `handbook/docs/orbit/decisions/011-platform-auth-notifications.md` |
| Password + API tokens | `handbook/docs/orbit/decisions/016-orbit-auth-password-and-api-tokens.md` |
| Dev quickstart | `handbook/docs/orbit/guides/platform-dev-quickstart.md` |
| Go toolchain | `handbook/docs/orbit/architecture/go-toolchain.md` |

## Structure

| Path | Role |
| --- | --- |
| `cmd/auth/` | gRPC server entry, wiring and `migrate` |
| `internal/domain/` | Entities, pinned rules, ports |
| `internal/application/` | OTP, password, token and demo use cases |
| `internal/infrastructure/postgres/` | Store adapter, sqlc queries and embedded goose migrations |
| `internal/infrastructure/grpc/` | gRPC handlers |
| `internal/infrastructure/httpapi/` | Fiber v3 health probes |
| `internal/infrastructure/credentials/` | bcrypt, OTP code and API-secret adapters |
| `internal/infrastructure/jwt/` | RS256 signer adapter |
| `internal/testutil/` | disposable-schema integration-test helper |

## Session pre-flight

1. `rtk o port list` — verify 10010 (gRPC) and 10011 (health) allocations
2. `rtk o status orbit` — check dependency state (`orbit-notifications`, dev Postgres)
3. `rtk git log --oneline -6 origin/main` — inspect recently landed changes

## Definition of done

- [ ] `go test ./...` exits 0
- [ ] `go test ./... -race -count=1` with `TEST_DATABASE_URL` exits 0
- [ ] `go vet ./...` exits 0 and `golangci-lint run` reports 0 issues
- [ ] `./scripts/generate-proto.sh` leaves no unstaged proto drift
- [ ] New migrations include clean rollback statements
- [ ] Docs updated in `handbook/docs/orbit/` if auth contracts or endpoints changed

## Do / don't

- Use `log/slog` — not `log.Printf`
- Notification delivery via `orbit-notifications` gRPC — not direct SMTP
- Migrations are sequential — do not skip numbers
- Never edit `internal/infrastructure/postgres/sqlcgen` by hand
- No commit unless user asks

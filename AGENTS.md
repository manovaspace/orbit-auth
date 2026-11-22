---
see_also:
  - handbook/docs/orbit/architecture/go-toolchain.md
  - handbook/docs/orbit/architecture/library-design.md
  - handbook/docs/orbit/decisions/069-identity.md
  - handbook/docs/orbit/guides/platform-dev-quickstart.md
---

# orbit-auth

Tier 2 product user authentication — OTP login, password login, JWT sessions, API tokens. Staff SSO is Keycloak workforce identity (ADR-027).

## Commands

```bash
export DEPLOYMENT_ENVIRONMENT=dev
# The `auth` database is production; never use it for development.
export DATABASE_URL=postgres://auth_dev:<password>@127.0.0.1:10000/auth_dev?sslmode=disable
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
- **Feature flag fallback:** When flags (`manova.auth.email_otp`, `manova.auth.mobile_otp`) read from orbit-auth `/v1/flags` through OpenFeature are toggled off or unreachable, handlers return typed errors (`ErrEmailOTPDisabled`). Unit tests must mock the flag provider.
- **Demo mode guard:** `DEMO_MODE=true` is permitted only when `DEPLOYMENT_ENVIRONMENT=dev`. `cmd/auth` fails closed before the service starts if demo mode is enabled elsewhere.
- **Migration adoption:** `migrate up` maps the old `schema_migrations` rows to goose versions without running SQL and without touching `schema_migrations`. Do not drop that table in this release.

## Organization administration

Memberships carry an administration tier `role` (`owner`, `admin`, `member`; migration 010). It decides who may change memberships and invitations in orbit-auth itself; products keep their own roles and permissions (ADR-045).

- The rule is one pure function, `domain.AuthorizeMembershipAction`. The caller must be an active owner or admin; an admin acts only on plain members; only an owner grants `owner` or `admin`; any active member may remove themselves.
- Every write RPC (`AddMember`, `RemoveMember`, `SuspendMember`, `ReactivateMember`, `SetMemberRole`, `InviteMember`, `RevokeInvite`) and `ListInvites` is authorized in `internal/application`. A caller who is not an active member gets `NotFound` (existence is not revealed); a member without authority gets `PermissionDenied`.
- The store keeps at least one active owner (`ErrLastOwner` → `FailedPrecondition`) under the organization row lock, so concurrent demotions cannot orphan an organization. The creator of an organization (`CreateOrg`, sign-up with `org_name`) is its first owner; invited and added members start as `member`.
- Migration 010 backfilled the earliest active member of each organization as its owner.

## Staff organization administration

`StaffOrgService` is the Manova staff surface over organizations. It is not for the public edge: the gateway authenticates staff through Keycloak and is the only caller.

- **Metadata:** every call carries `x-orbit-service-token`, compared in constant time with `AUTH_STAFF_SERVICE_TOKEN`, and `x-staff-subject`. Every write also carries the reason, recorded with the change: `x-staff-reason-bin` holds it as UTF-8 text of at most 500 characters (gRPC base64-encodes binary metadata, so any language works); `x-staff-reason` (ASCII) is still read for older callers. `StaffServiceUnaryServerInterceptor` checks all three before any handler runs. An empty `AUTH_STAFF_SERVICE_TOKEN` refuses every call with `Unavailable` ("staff service disabled"); a missing or wrong token is `Unauthenticated`. The token is never logged.
- **RPCs:** `ListOrganizations` (query, status, `page_size` up to 100, keyset `page_token`), `GetOrganization` (members and pending invitations, never token hashes), `ProvisionOrganization` (no member and one pending owner invitation by email or phone, idempotent on `idempotency_key` for the same name and owner, where a repeat rotates the owner invitation; any other repeat is `AlreadyExists`), `SuspendOrganization` and `ReactivateOrganization`, `SetOrganizationSlug` (the `SetOrgSlug` registry rules), `ReinviteOwner`.
- **Staff are never members.** Staff operations skip `AuthorizeMembershipAction`. Each write is logged with the staff subject and reason.
- **Suspension:** a suspended organization refuses `SelectOrg` and refresh into it (`FailedPrecondition`, "organization suspended"). Its members keep their memberships, and `ResolveTenant` reports `suspended`.
- **Migration 014** adds `organizations.status` (`active` or `suspended`), `suspended_at`, `suspended_reason`, the `organization_provisions` idempotency table (key, organization and owner target), and `organization_invites.inviter_staff_subject`. `inviter_id` becomes nullable, and exactly one of the two inviter columns is set.

## Staff user administration

`StaffUserService` is the staff surface over accounts. It follows the `StaffOrgService` edge rules: the gateway is the only caller, every call carries `x-orbit-service-token` and `x-staff-subject`, and every write also carries the reason.

- **RPCs:** `ListUsers` (query over email, mobile, username and display name, case-insensitive, `LIKE` wildcards escaped; `status` filter; keyset `page_token` on `(created_at, id)`, `page_size` up to 100), `GetUser` (one account with its organization memberships), `DisableUser`, `EnableUser` and `ListAuditEvents`. `GetUser` and the listings never return a password hash, a token or a hash of either.
- **Disable:** `DisableUser` locks the account row (`FOR UPDATE`), sets `disabled`, revokes every live refresh token of the account, writes `user.disabled` to `audit_events` and `auth.user.disabled` to the outbox, all in one transaction. Sign-in takes the account row `FOR SHARE` before it stores a refresh token, so a sign-in either commits before the disable and is revoked, or sees `disabled` and gets no token. A disabled account returns `changed=false` and writes nothing. A `signup_pending` account is `FailedPrecondition` "account is not active".
- **Enable:** `EnableUser` restores `active` and writes `user.enabled` and `auth.user.enabled`. Refresh tokens revoked by the disable stay revoked; the user signs in again. An active account returns `changed=false`.
- **Reason:** both writes need `x-staff-reason`. `ListUsers`, `GetUser` and `ListAuditEvents` need none.

## Audit trail

`audit_events` (migration 017) is a durable, append-only record of account, membership, organization and invitation changes. Each row is written in the same transaction as the change it records, through `writeAudit` in `internal/infrastructure/postgres/audit.go`.

- Columns: `occurred_at`, `action` (for example `invite.created`, `membership.suspended`, `org.reactivated`, `user.disabled`, `identity.linked`, `password.set`), `actor_kind` (`user`, `staff` or `system`), `actor_id` (the user id, or the staff subject, or empty), `org_id`, `subject_user_id`, `invite_id`, `reason` (the staff reason) and `details` (JSON). Rows carry no foreign keys, so they outlive the organization and the user.
- A row never holds an email address, a mobile number, a password, a token or a hash. An invitation is identified by `invite_id`.
- `ListAuditEvents` (`StaffUserService`) pages newest first and requires an `org_id`, a `user_id` or both. A `user_id` filter matches `subject_user_id` only: rows where the user was the actor, and not the subject, are not listed by it.

## Organization slugs and tenant hosts

Every organization has a `slug`, the label of its tenant host (`<slug>.falak.<env>`; migration 011).

- Format (`domain.ValidateSlug`): 3 to 30 lowercase ASCII letters, digits and single hyphens, starting with a letter and ending with a letter or digit, not in the reserved list (`www`, `api`, `app`, `auth`, `admin`, `demo`, `internal`, `login`, `falak`, ...). The check constraint pins the format; the reserved list is code.
- Allocation (`CreateOrg`, sign-up with `org_name`): an explicit `slug` must be valid and free, otherwise the request fails. Without one the slug is derived from the name, then from the organization id (`org-` and 12 hex digits) when the name has no usable ASCII (for example a Persian name) or the derived slug is taken.
- `organization_slugs` is the registry of every slug an organization has held; its primary key makes a slug unique across organizations **and across time**. `SetOrgSlug` (owner only, rechecked under the organization lock) retires the old slug, which stays reserved for the organization and keeps resolving, so a rename never frees an address for someone else. An organization can take its own retired slug back.
- `InviteService` is public (`GetInvitePreview`, `AcceptInviteWithSignup`) and, like `TenantService`, sits outside `OrgService`, which is the only service the access-token interceptor guards. `GetInvitePreview` shows the organization, tier and expiry of a token and never the recipient. `GetInvitePreview` also returns the `channel` (`email` or `phone`) and `account_exists` (an active account already uses the invited number or email; the preview never says whose). `AcceptInviteWithSignup` creates the account for a **phone or email** invitation and returns `user_id`, `org_id`, `invite_id`, `role` and `identifier` (the number or the email the person signs in with). It is refused with `FailedPrecondition` "invite sign-up is disabled" only while `AUTH_INVITE_SIGNUP` is `off` or `false` (default on; it does not depend on `AUTH_SIGNUP`), `InvalidArgument` for a password under eight characters after trimming, `AlreadyExists` when an active account already uses the number or email (that person signs in and calls `AcceptInvite`), and `NotFound` for any unusable token. A phone account's mobile is the invited number with `users.mobile_verified_at` NULL: invite sign-up does not verify the number, and only a successful mobile OTP sets the column (`VerifyOTP` on the `sms` channel, and demo seeding). Sign-in does not depend on the column. An email account has `email_verified_at` set at acceptance, because the token reached only that mailbox. A `signup_pending` account with the invited email is taken over: its password and display name are replaced and it becomes active, with no new `auth.user.created` event (the same as a pending sign-up retry).
- `OrgService.InviteMember` takes `email` or `phone` (exactly one) and an optional `role` (empty means `member`). Only a phone invitation returns the raw `token` in its response, because no email carries it there; an email invitation never returns it.
- `TenantService.ResolveTenant` is the only unauthenticated organization call. It answers the organization id, name and current slug, whether the requested slug is retired and whether the organization is suspended, never members. Unknown, invalid and reserved slugs are the same `NotFound`.

## Feature Flags & Demo Mode

- **Feature flags:** `manova.auth.email_otp` and `manova.auth.mobile_otp`, evaluated with OpenFeature over orbit-auth `/v1/flags` (`FLAGS_URL`). When disabled, `RequestOTP` returns `ErrEmailOTPDisabled` or `ErrMobileOTPDisabled`.
- **Organization auto-select:** `AUTH_AUTO_SELECT_SINGLE_ORG` (default `true`) adds the optional `org` access-token claim at login when the user has exactly one active membership. Zero or many memberships omit it, and a suspended organization is never chosen. `SelectOrg` rotates the refresh token and issues a pair carrying the selected org.
- **Account states:** `users.status` is `active`, `disabled` or `signup_pending` (migration 017). Only an active account gets tokens or acts. A disabled account is refused on every path with the caller's generic error: password login with `invalid credentials` (the password is compared first, so the refusal costs the same as a wrong password), OTP with `invalid or expired code`, `RefreshToken` and `SelectOrg` with `invalid refresh token`, API tokens with `invalid api token`, Google sign-in with `ErrGoogleAccountNotAllowed`, and `CreateOrg`, `SetUsername` and every membership action with `PermissionDenied` "account is not active". A still-valid access token keeps working at most 15 minutes, because access tokens are not checked against the account; the refresh tokens are revoked at once.
- **Refresh tokens:** `RefreshToken` and `SelectOrg` consume the presented token atomically (`RefreshTokenStore.Consume`), so a token can be redeemed once even under concurrent requests. Refreshing into or selecting a suspended organization fails with `FailedPrecondition` (`organization suspended`) and leaves the token unspent. `RevokeRefreshToken` revokes a token on logout and is idempotent: unknown, revoked or expired tokens succeed.
- **Demo seeding:** `DEMO_MODE=true` (dev only) and `AUTH_DEMO_USERS` JSON array seeds test user accounts into Postgres on startup. `cmd/auth` parses the JSON; the application validates and hashes each entry.
- **Invite sign-up:** `AUTH_INVITE_SIGNUP` defaults to on; only an explicit `off` or `false` disables `InviteService.AcceptInviteWithSignup`. It needs a token an owner or admin issued, so it is independent of `AUTH_SIGNUP`.
- **Invite accept links:** `AUTH_INVITE_ACCEPT_HOSTS` is a comma-separated list of hosts (default empty). An `accept_url` on `InviteMember`, `ProvisionOrganization` or `ReinviteOwner` must be `https` (or `http` for `localhost`), have no userinfo, query or fragment, and have a host equal to, or a subdomain of, one entry. `{slug}` is replaced with the organization's slug and is allowed only in the host. The domain check is `domain.ResolveAcceptURL`. An email invitation's `org_invite` carries `accept_url` = the resolved URL with the raw token in its fragment; the token is never logged. With no hosts configured, any non-empty `accept_url` is `InvalidArgument` "invalid accept_url". `application.Deps` receives the list from `cmd/auth`; the application reads no environment.
- **Self-serve sign-up:** `AUTH_SIGNUP` is `open` or `off` (default `off`), read in `cmd/auth` and injected through `application.Deps`. While off, `SignupService` returns `codes.FailedPrecondition` "sign-up is disabled" and writes nothing. A sign-up account is `signup_pending` until `VerifySignup` receives both the `signup_verify` OTP and the pending account password; password and OTP login refuse it with the generic invalid-credentials error. A retry for a still-pending address replaces its credentials and reissues the code (at most once per 60 seconds); an active account is left untouched. Admin and demo accounts are verified from the start, and a valid invite token verifies the address without an OTP.

## Google sign-in, invitations and sign-in methods

- **Expired invitations:** `expired` is a derived status only. `domain.EffectiveInviteStatus(status, expires_at, now)` reports a pending invitation as `expired` at and after `expires_at`; the stored status and the database check never hold `expired`. `ListInvites` and `StaffGetOrganization` return the effective status with the service clock. Token preview and accept are unchanged and keep their generic error.
- **Invitation with Google:** `AuthService.StartGoogleSignIn` takes an optional `invite_token`. It must resolve to a pending, unexpired invitation (`ErrInvalidInvite`, `InvalidArgument`, generic). A phone invitation is refused with `ErrInviteNeedsPassword` (`FailedPrecondition`, "google cannot accept a phone invitation"). The state is stored with purpose `invite` and the invitation id; the token is never stored.
- **Completion:** `CompleteGoogleSignIn` consumes the state and returns `GoogleSignInResult`. A `link` state is refused with `ErrInvalidGoogleState`. For an `invite` state, `AcceptInviteWithIdentity` accepts the invitation in one transaction. A suspended organization gets tokens without the organization, as `SelectOrg` into it is refused. The response carries `invite_id`, `org_id`, `org_slug`, `role` and `already_member`; these are empty or false for a plain sign-in.
- **Matching rule:** a known Google subject must match the invited email (verified account email, or verified Google email). An unknown subject needs a verified Google email equal to the invited email. Accounts are then created, taken over (`signup_pending`) or linked, only when `GoogleEmailIsAuthoritative` holds for the link. A disabled account is always refused. A mismatch is `ErrInviteEmailMismatch` (`FailedPrecondition`, "invite email mismatch") and writes nothing. Invite sign-up off (`AUTH_INVITE_SIGNUP`) refuses a new account or a takeover with `ErrInviteSignupDisabled`.
- **Lock order:** account row, then organization row, then invitation row. `AcceptInvite` and the Google path use the same helper (`acceptInviteUnderLock`).
- **`AccountService`** (`/auth.v1.AccountService/`, guarded by the access-token interceptor). The caller is the verified token subject; no request names an account. Every RPC needs an active account.
  - `GetSignInMethods`: `has_password`, at most one `google` link (`email`, `linked_at`, `last_login_at`), `username`, `email`, `email_verified`, `mobile`, `mobile_verified`. Never subjects or hashes.
  - `SetPassword(current_password, new_password, refresh_token)`: a wrong current password is `Unauthenticated` "invalid credentials" when a password exists; ignored otherwise. Needs a verified email, a mobile or a username (`ErrNoPasswordIdentifier`, `FailedPrecondition`). Revokes every other live refresh token; the presented one stays.
  - `StartGoogleLink` and `CompleteGoogleLink`: link purpose only. An account with a Google identity is `FailedPrecondition` "google is already linked"; a subject held by another account is `AlreadyExists`. A state of another caller is `ErrInvalidGoogleState`. No tokens are issued.
  - `UnlinkGoogle`: refused as `FailedPrecondition` when no usable password remains (`ErrLastSignInMethod`); no identity is `NotFound`.
  - `RevokeAllSessions(refresh_token)`: revokes every live refresh token except the presented one.
- **Audit actions added:** `identity.linked` (invite acceptance with a new identity, sign-in link, link RPC; details `{"provider":"google"}`), `identity.unlinked` (same details), `password.set` (details `{"first": bool}`), `sessions.revoked` (details `{"kept_current": bool}`). Subject is the user. No audit row or log attribute holds an email.
- **Events:** no new outbox events. Invitation sign-up with Google emits `auth.user.created` when it creates an account and `auth.membership.added` for a new membership, as the existing invitation paths do.

## Events

Lifecycle events are written to the Postgres outbox (`outbox_events`, migration 008) in the same transaction as the change and relayed by `outbox.Relay` to `orbit-kit/messagebus`:

- `auth.user.created` — a user is created by OTP login, sign-up, invite-sign-up or demo seeding
- `auth.org.created` — `CreateOrg`, sign-up with `org_name`, or staff `ProvisionOrganization`
- `auth.membership.added` — `AddMember`, invite acceptance (including phone invite sign-up), or the creator membership
- `auth.membership.removed` — `RemoveMember`
- `auth.membership.suspended` — `SuspendMember`
- `auth.membership.reactivated` — `ReactivateMember` (carries `status` and `role`)
- `auth.user.disabled` — staff `DisableUser` (carries `user_id`, `staff_subject` and `reason`)
- `auth.user.enabled` — staff `EnableUser` (same payload)
- `auth.membership.role_changed` — `SetMemberRole` (carries `role`, `previous_role` and `actor_id`)
- `auth.org.slug_changed` — `SetOrgSlug` or staff `SetOrganizationSlug` (carries `slug`, `previous_slug` and `actor_id`: the caller, or for a staff change the staff subject, with `reason`); `auth.org.created` carries the new organization's `slug`
- `auth.org.suspended` — staff `SuspendOrganization` (carries `org_id`, `staff_subject` and `reason`)
- `auth.org.reactivated` — staff `ReactivateOrganization` (same payload)

Subjects are the three-segment `{domain}.{entity}.{action}` shape. The version is envelope metadata `version="1"`, not a subject suffix. Payloads carry ids, the organization name for `org.created`, the membership status, the organization administration tier (`role`: owner, admin or member, never a product role) on `membership.added` and `occurred_at`; they never carry emails, tokens or secrets.

`BUS_TRANSPORT` selects the transport: `inproc` (default; relayed and logged, no subscribers), `nats` with `BUS_NATS_URL`, or `redis` with `BUS_REDIS_URL`. The relay runs in `cmd/auth` for the life of the process and stops on graceful shutdown; publish failures are logged with `slog` and retried, never crashing the gRPC server.

## Stack

- Go 1.27.4, gRPC + Fiber v3 health + Postgres
- Embedded goose migrations in `internal/infrastructure/postgres/migrations/`
- sqlc queries in `internal/infrastructure/postgres/queries/`, generated package committed
- Proto in `api/proto/auth/v1/`

## Docs

| Topic | Path |
| --- | --- |
| Migrations, adoption, rollback | `docs/migrations.md` |
| Platform auth ADR | `handbook/docs/orbit/decisions/069-identity.md` |
| Password + API tokens | `handbook/docs/orbit/decisions/069-identity.md` |
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

1. `o port list` — verify 10010 (gRPC) and 10011 (health) allocations
2. `o status orbit` — check dependency state (`orbit-notifications`, dev Postgres)
3. `git log --oneline -6 origin/main` — inspect recently landed changes

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

<!-- manova:conventions v1 — generated from manovaspace/ci conventions/core.md; edit it there -->
## Shared conventions

These rules apply to every Manova repository. They are copied from `manovaspace/ci` (`conventions/core.md`); change them there, not here.

### Delivery

1. Never push to `main`. Every change goes on a topic branch (`feat/<name>`, `fix/<name>`, `refactor/<name>`, `docs/<name>`, `chore/<name>`) and reaches `main` through a pull request.
2. One pull request does one thing. An unrelated problem you find on the way becomes an issue, not part of this change.
3. Pull request titles and commit messages follow Conventional Commits (`feat:`, `fix:`, `docs:`, `refactor:`, `chore:`, `test:`; `!` for a breaking change). Releases are computed from them.
4. Merge with squash once the checks are green. Do not use `--no-verify`; if you ever must, say so in the pull request.

### Checks

5. CI runs `manovaspace/ci@v1`. Run the same gate locally before you push; a red check is fixed, not skipped.
6. Tool versions (Go, Node, Bun, linters) come from `ci/versions.env` in `manovaspace/ci`. Do not pin a different version in this repository.
7. A behaviour change comes with a test that fails without it. Do not delete or weaken a test to make a change pass.

### Secrets

8. Never commit a secret, token, password, private key or `.env` file. Commit `.env.example` with placeholders instead.
9. Never print a secret in logs, test output, error messages or pull requests. Read secrets from the environment or a file at run time.

### Runtime

10. Containers talk to each other by name on a Docker network. Do not publish a port on the host unless a person or a host process needs it, and then bind `127.0.0.1`, never `0.0.0.0`.
11. Configuration comes from the environment. A service with a missing required setting refuses to start; it never falls back to a built-in secret or a development default in production.
12. Port numbers come from the workspace port registry. Do not invent a port.

### Documentation

13. This repository explains itself: `README.md` says how to set up, run, test and deploy it; `AGENTS.md` holds its binding rules; repository decisions live in `docs/decisions/`.
14. Write plain, short sentences. Keep exact names, paths, commands and versions. Do not document what the code already says.
<!-- /manova:conventions -->

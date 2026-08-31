# Nightshift Identity + Onboarding — Implementation Plan

**Date:** 2026-08-31
**Spec:** [`../specs/2026-08-30-nightshift-identity-design.md`](../specs/2026-08-30-nightshift-identity-design.md)
**Tree:** written against `main` @ c0cb7e4 (Plan 3 merged; delta sheet in
[`2026-08-31-parallel-sessions.md`](./2026-08-31-parallel-sessions.md)
re-verified against the real tree).

## Intended outcome

Magic-link signup/login is the production auth path: `POST /v1/auth/magic-link`
emails a link, `GET /auth/verify` renders a consuming-nothing interstitial,
`POST /v1/auth/verify` atomically claims the token, mints tenant + KEK + owner
on first login, and issues a DB-backed opaque session in a
`__Host-ns_session` cookie. `NIGHTSHIFT_SESSION_KEY` is gone; every `/v1`
mutating route enforces the Origin policy; `dev-session` mints session rows.
`GET /v1/me` and `POST /v1/auth/logout` exist. All existing behavior
(workflows, runs, connections, scheduler, reaper, meter wiring) is untouched.

## Evidence and constraints

- Migrations end at `00008_scheduling_runs.sql`; identity takes `00009`.
  `app_user` already has `UNIQUE (tenant_id, id)` (`00002_app_user.sql`), so
  the session composite FK per `00003_workflow.sql`'s pattern works as-is.
- `httpapi.Deps` is `{Store, SessionKey, Engine, Vault}`; the session surface
  is `SignSession`/`VerifySession`/`SessionCookie`/`RequireSession` in
  `internal/httpapi/session.go`. `ClaimsFrom` and handlers stay untouched.
- `serve()` (`cmd/nightshift/main.go`) starts scheduler + reaper goroutines
  and wires the meter as the proxy `Hook`; identity edits wiring inputs only.
- `store.CreateTenant` opens its own transaction; `UpsertUser` runs on the
  pool — the atomic verify needs one transaction-scoped store operation
  (spec §Verifying).
- Blast radius of the session replacement (verified): `httpapi_test.newEnv`
  (`workflows_test.go:38`), `session_test.go` (entirely about signing), three
  `SessionCookie` call sites in `e2e_test.go`, `devSession` in `main.go`.
- No mail infrastructure exists in `server/`; the provider choice is an open
  question in the spec, so v1 ships a seam plus a dev sender.
- The `src/` prototype has no URL routing, so redirect targets are
  server-side constants the frontend claims later.
- Route namespace is clean: `/v1/*`, `/internal/*`, `/proxy/*` — the new
  `/auth/verify` and `/v1/auth/*` collide with nothing.

### Spec defects resolved here (not inherited)

1. **Lookup-then-consume is not single-use at READ COMMITTED** — two
   concurrent verifies can both pass the "unconsumed" check before either
   commits. Replaced by a conditional claim: the transaction opens with

   ```sql
   UPDATE login_token
   SET consumed_at = now()
   WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > now()
   RETURNING email, next_path
   ```

   Zero rows means expired-or-reused (one sentinel; the interstitial renders
   one friendly page for both). The row lock taken by the winning UPDATE
   serializes the loser, whose re-evaluated WHERE then sees `consumed_at`
   set. Session insert and first-login writes happen only after a successful
   claim, in the same transaction.

2. **`NIGHTSHIFT_PUBLIC_BASE_URL` had no scheme constraint** while carrying
   magic-link tokens, defining the trusted Origin, and pairing with a
   `Secure` cookie. Startup validation requires `https://`, with an explicit
   exception only for hosts `localhost`, `127.0.0.1`, and `::1` (dev). Also
   rejected: path, query, fragment, trailing slash, userinfo.

## Decisions for review

### 1. Observable behavior

- **Endpoints.** `POST /v1/auth/magic-link` (JSON `{"email","next"?}`, always
  202 with an identical body); `GET /auth/verify?token=…` (HTML interstitial
  — valid token renders the continue button, anything else renders the
  friendly "link expired" page; consumes nothing); `POST /v1/auth/verify`
  (form-encoded `token` from the interstitial; on success sets the cookie
  and 303-redirects; on failure 303s back to the expired page);
  `POST /v1/auth/logout` (deletes the row, clears the cookie, 204);
  `GET /v1/me` (current user + tenant JSON).
- **Redirects.** First login → `/build`; returning login → stored
  `next_path` joined to the public base URL, `/` when NULL. `next` is read
  only at request time and stored only if it parses as a relative path
  (starts with a single `/`, no `\`, not `//`); the verify POST accepts no
  redirect input.
- **Rate budgets** (anti-abuse, not UX): at most 3 outstanding
  (unconsumed, unexpired) tokens per email — checked in the DB — and an
  in-memory fixed-window cap of 10 magic-link requests per source IP
  (`RemoteAddr`, never proxy headers) per hour. Over budget → same 202, no
  email.
- **Tenant naming.** First login names the tenant from the email local part
  (`pat@acme.test` → tenant `pat`); the user is never asked.

### 2. Architecture and boundaries

- **`internal/httpapi` keeps the session surface**; `session.go` is
  rewritten around opaque tokens (random 32 bytes; cookie carries
  base64url(raw), DB stores SHA-256). `RequireSession(store, next)` replaces
  `RequireSession(key, next)`. `SessionClaims{UserID, TenantID, Role}` and
  `ClaimsFrom` keep their shapes (Exp drops).
- **New `internal/mail`**: `type Sender interface { Send(ctx, to, subject,
body string) error }` plus `LogSender` (slog-based dev sender — the log
  line _is_ the dev login flow). The spec left the provider open; mid-plan
  the grading + alerting spec (PR #13) chose **Postmark** for the whole
  platform, so `internal/mail` also ships a Postmark sender, selected when
  `NIGHTSHIFT_POSTMARK_TOKEN` + `NIGHTSHIFT_MAIL_FROM` are set, with the
  log sender as the dev fallback.
- **Atomic signup lives in the store** as one operation (below); the handler
  pre-generates the wrapped KEK (the tenant name derives from the claimed
  email's local part inside the store) and passes it in, so the
  store does not grow a vault dependency. The KEK is wasted bytes when the
  user already exists — no side effect.
- **Origin policy is httpapi middleware** applied to every mutating `/v1`
  route (the auth POSTs included): `Origin` absent → allow; exactly equal to
  the configured public origin → allow; anything else → 403 before the
  handler.

### 3. Data model and interfaces

Migration `00009_identity.sql`:

```sql
-- +goose Up
-- One canonical email representation, then the v1 one-tenant-per-email
-- constraint. Dev databases holding cross-tenant duplicates fail here and
-- are recreated (spec migration note); no production data exists.
UPDATE app_user SET email = lower(btrim(email));
CREATE UNIQUE INDEX app_user_email_global ON app_user (lower(email));

CREATE TABLE login_token (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash bytea NOT NULL UNIQUE,
    email text NOT NULL,
    next_path text,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);

CREATE TABLE session (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash bytea NOT NULL UNIQUE,
    user_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    -- Same-tenant parentage, enforced by the composite-FK pattern every
    -- child table uses; cascade means a deleted user takes its sessions.
    FOREIGN KEY (tenant_id, user_id)
        REFERENCES app_user (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX session_by_user ON session (tenant_id, user_id);

-- +goose Down
DROP TABLE session;
DROP TABLE login_token;
DROP INDEX app_user_email_global;
```

New store surface (`store/auth.go`, `store/session.go`):

- `NormalizeEmail(string) string` — `lower(trim)`; applied at every boundary.
- `CreateLoginToken(ctx, tokenHash, email, nextPath, expiresAt)` — also
  opportunistically deletes long-expired rows (the spec's sweep).
- `LoginTokenValid(ctx, tokenHash) (bool, error)` — interstitial GET's
  existence check (unconsumed and unexpired), reads only.
- `CountActiveLoginTokens(ctx, email) (int, error)` — the per-email budget.
- `ConsumeLoginToken(ctx, tokenHash, sessionTokenHash, tenantName,
wrappedKEK) (ConsumeResult, error)` — the one transaction: conditional
  claim (zero rows → `ErrLoginTokenInvalid`), resolve `lower(email)` → user,
  else create tenant + KEK + user via tx-scoped helpers shared with
  `CreateTenant`, insert the session row. `ConsumeResult{User, Tenant,
Session, NextPath, FirstLogin}`.
- `CreateSession(ctx, tokenHash, tenantID, userID) (Session, error)` —
  30-day `expires_at`; used by `dev-session` and tests.
- `SessionUser(ctx, tokenHash) (userID, tenantID, role, error)` — one query:
  a CTE touches `last_seen_at` when it is older than an hour, the main
  SELECT joins `session` to `app_user` on `(tenant_id, user_id)` and applies
  `last_seen_at > now() - interval '7 days' AND expires_at > now()`. No row
  (including a vanished user) → `ErrNotFound` → 401. Role therefore always
  reflects the current `app_user` row.
- `DeleteSession(ctx, tokenHash)`; `DeleteUserSessions(ctx, tenantID,
userID)` — logout and the log-out-everywhere / support-revocation lever.
- `UserByEmail(ctx, email) (User, error)` — `dev-session` resolution.
- `CreateTenant` is refactored onto the shared tx helpers; its signature and
  behavior do not change.

`httpapi.Deps` becomes
`{Store, Engine, Vault, PublicBaseURL *url.URL, Mailer mail.Sender}`
(`SessionKey` deleted). `RegisterRoutes` adds the auth routes and wraps
mutating routes in the Origin middleware.

### 4. Compatibility and migration

- Nothing external depends on the old cookie: sessions were 24 h dev-minted
  cookies; every holder re-runs `dev-session`. No dual-read window.
- The email backfill + global unique index can fail on dev databases with
  cross-tenant duplicate emails (repeated default `dev-session` runs made
  them); per spec the fix is a fresh database, and the migration comment
  says so.
- `dev-session` reuse semantics change per spec: resolve email first — if it
  exists, reuse its tenant; `-tenant-id` naming a different tenant than the
  email's is an error; only a genuinely new email mints a tenant.

### 5. Security and failure handling

- Tokens (login and session) are 256-bit `crypto/rand`; only SHA-256 hashes
  are stored; the raw value exists in the email link / cookie alone.
- Cookie: `__Host-ns_session`, `Secure`, `HttpOnly`, `SameSite=Lax`,
  `Path=/`, no Domain, `Max-Age` = 30 d.
- Enumeration resistance: byte-identical 202 for known/unknown/over-budget
  emails; the only observable difference is whether mail arrives.
- Failure of anything inside the verify transaction rolls back the claim
  too, so the link stays valid and the user retries by clicking again
  (spec §Failure handling).
- Public-origin trust: `NIGHTSHIFT_PUBLIC_BASE_URL` (validated as above) is
  the single source for email links, the Origin comparison, and redirect
  joining. Host/proxy headers are never consulted.
- The in-memory IP limiter resets on restart and is per-process; accepted
  for v1 (single-process deployment; the limit is anti-abuse only).

### 6. Deployment and operations

- Env: `NIGHTSHIFT_SESSION_KEY` removed (serve and dev-session);
  `NIGHTSHIFT_PUBLIC_BASE_URL` required for serve. Mail defaults to the log
  sender until a provider is chosen (open question; shared with Plan 4
  alerting). `serve()`'s scheduler/reaper/meter wiring is preserved
  verbatim.
- Expired-row hygiene is opportunistic deletes on the write paths, not a new
  background loop.

### 7. Testing and verification

Everything in the spec's Testing section, mapped:

- `store`: concurrent consumption (two goroutines, one wins — the loser gets
  `ErrLoginTokenInvalid`); signup atomicity (force the session insert to
  collide on `token_hash` → whole transaction rolls back, no tenant / KEK /
  user / consumed token); global email uniqueness across tenants; composite
  FK rejects user-A/tenant-B; idle and absolute expiry boundaries (crafted
  timestamps via direct SQL); `last_seen_at` touch throttling; delete-user
  cascades sessions; log-out-everywhere.
- `httpapi`: byte-identical 202s; per-email and per-IP budgets; GET
  interstitial consumes nothing; Set-Cookie contract; the three Origin
  cases on a mutating route; stored `next_path` honored and verify-time
  `next` ignored; **`ClaimsFrom(ctx).Role` populated through the
  session→`app_user` join, and a session whose user row was deleted → 401**
  (deleting via SQL that bypasses the cascade is impossible, so the test
  deletes the user and proves the session vanished / lookup 401s).
- e2e: fresh email → captured link from a recording `mail.Sender` → GET
  interstitial → POST verify over `httptest.NewTLSServer` with an
  `http.CookieJar` client (cookie proven to round-trip under its real
  attributes) → tenant + KEK + owner exist → `/v1/me` → logout → 401.
- Existing tests migrate mechanically: `newEnv` and the e2e helpers mint a
  session row (`CreateSession`) and build the cookie from the raw token;
  `session_test.go` is replaced by store-backed `RequireSession` tests.

## Alternatives and tradeoffs

- **Conditional claim vs `SELECT … FOR UPDATE`**: both serialize correctly;
  the single UPDATE is fewer round-trips and makes "expired" and "reused"
  one code path, which is what the UX wants anyway.
- **Passing `wrappedKEK` into the store vs a store→vault dependency**: keeps
  the store dependency-free at the cost of generating a KEK that is
  discarded on returning logins. Accepted; generation is cheap and
  side-effect-free.
- **Form-POST verify with 303 vs JSON fetch from the interstitial**: the
  interstitial is a server-rendered page before any SPA is loaded; a plain
  form needs no JS and the 303 carries Set-Cookie naturally.
- **In-memory IP limiter vs DB-backed**: DB-backed survives restarts but
  adds a table and sweep for an anti-abuse budget the spec calls an
  implementation detail. In-memory chosen; revisit with real deployment.
- **`/build` first-login constant vs `/`**: the UX spec's entry point is the
  build conversation; the prototype has no routes yet either way, so the
  constant documents intent and the frontend claims it later.

## Ordered implementation steps (TDD per step)

1. Migration `00009_identity.sql`; store email normalization + global
   uniqueness test.
2. Store: login tokens (`CreateLoginToken`, `LoginTokenValid`,
   `CountActiveLoginTokens`) + tests.
3. Store: shared tx helpers under `CreateTenant`; `ConsumeLoginToken` with
   claim/mint/session semantics + concurrency and atomicity tests.
4. Store: sessions (`CreateSession`, `SessionUser`, `DeleteSession`,
   `DeleteUserSessions`, `UserByEmail`) + expiry/touch/cascade tests.
5. `internal/mail`: `Sender`, `LogSender`, test recorder.
6. httpapi: rewrite `session.go` (opaque tokens, store-backed
   `RequireSession`, cookie constants); update `Deps`; migrate `newEnv` and
   `session_test.go`.
7. httpapi: auth handlers (magic-link + limiter, interstitial GET, verify
   POST, logout, me) + handler tests.
8. httpapi: Origin middleware on mutating routes + tests.
9. `cmd/nightshift`: base-URL validation (+ HTTPS rule), serve wiring,
   `dev-session` rework, doc comment; e2e helper migration + new TLS e2e
   test.
10. Docs: README env table, parallel-sessions delta note for the
    connector/escalation sessions.

## Adaptation points

- If the CTE touch-and-select interacts badly with pgx (visibility
  surprises), fall back to a fire-and-forget throttled UPDATE after the
  SELECT — semantics identical, two round-trips at most once per hour.
- If `httptest.NewTLSServer` + cookiejar fights the `__Host-` prefix
  anywhere, the e2e asserts the Set-Cookie attributes explicitly and keeps
  the jar for round-tripping.
- If forcing the atomicity failure via `token_hash` collision proves flaky,
  substitute any in-transaction failure with the same rollback assertion.

## Explicit exclusions

Per spec: multi-user tenants, invitations, roles beyond `owner`, OAuth,
passkeys, SSO, billing identity, settings UI, connector OAuth, email
provider integration (seam only), email change / account recovery, tenant
deletion, support impersonation. Also excluded: any `src/` change (the
interstitial is server-rendered; the SPA claims `/build` later).

# Tomte P1 — Subtraction and Floor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove OAuth and login/mail end-to-end, add the local-session mint + `/local/handoff`, the wake-aware scheduler window, the endpoint record (presets / custom / local), the reworked pricing gate (user-entered prices, local-$0), the budget rename, and `serve()` as a library entry point with the loopback hardening.

**Architecture:** Mostly subtraction (`internal/oauth`, `internal/mail`, magic-link surface, refresh/epoch machinery), one migration (`00012`), one new root-package library entry (`package server` at `server/`), and endpoint-awareness threaded through approval → compiled doc → harness so the proxy and cost paths never need a runtime price lookup: **approval records the endpoint identity and the resolved prices in the compiled document**, and switching endpoints recompiles.

**Tech Stack:** Go 1.26, Postgres (goose migrations, testpg/testcontainers), existing `internal/{store,permit,llm,proxy,proxyadapter,token,compute,engine,meter,httpapi,internalapi,catalog,steps,harness}`.

**Spec:** `docs/superpowers/specs/2026-08-31-tomte-pivot-design.md`, roadmap item P1 (sections: "Estate triage" connectors row, "Identity at its floor", "The sleeping machine", "Endpoint agnosticism", "The priced-pair gate, reworked", "Vault and metering", "The shell", "Loopback security posture"). Coordination rules: `docs/superpowers/plans/2026-08-31-parallel-sessions.md`.

## Global Constraints

- Work in the `p1` worktree, branch `feat/p1-subtraction-floor` off `main` @ `16fa315`. PRs target `main`; no pre-stacked bases.
- **One implementation PR.** P1 is one coherent floor with one migration; splitting into serial PRs would serialize on review latency for no isolation gain (every later piece touches the files the removal reshapes). The plan itself is a separate docs PR.
- Migration is exactly one file: `server/internal/db/migrations/00012_pivot_floor.sql` (verified next free number; goose auto-embeds).
- **Never rename the derived-key labels `tomte:run-jwt` and `tomte-oauth-state`.** The OAuth _state signer_ dies, so the `tomte-oauth-state` derivation in `main.go:185-189` is deleted along with its consumer — deletion is allowed, renaming is not. `tomte:run-jwt` (internal/token) is untouched.
- `tenant.monthly_cap_cents` **keeps its column name** — the rename is semantics and copy only (spec: "same enforcement, new copy").
- The `api_key` half of `store/connection.go` and the connector op-invocation gateway survive untouched (except where OAuth columns force the shared column list to shrink). `needs_reauth` (`connection.status`) **survives** — the spec's estate triage keeps it because a pasted token can be revoked upstream; only the epoch CAS around it dies.
- Fail closed everywhere, as today: unpriced remote pair → 400; meter store failure → denied; credential-resolution failure → 500 — except a `local` endpoint, where skipping credentials is the contract, not a failure.
- Verification before the PR, from `server/`: `gofmt -l .` (prints nothing), `go vet ./...`, `go build ./...`, `go test ./...` (Docker available), **plus a real `tomte serve` boot** against a live Postgres. Docs get `npx prettier --write` from the repo root. Conventional commits.

## Blind-spot findings (from four symbol-level exploration passes)

Confirmed facts the plan builds on:

- `PublicBaseURL` is not magic-link-only: it feeds `checkOrigin` (`httpapi.go:73`) and dies nowhere; only its magic-link/OAuth roles die. `httpapi.IsLocalhost` loses its last caller (delete).
- `firstLoginPath = "/build"` lives in dying code (`auth.go:22`) and must move, not vanish.
- The catalog gate's `equalDefs` compares def _counts_ against the baseline: `defs/google-calendar.json` and `baseline/google-calendar.json` must be deleted **together** or boot fails.
- Two approve-path tests are stranded in `httpapi/oauth_test.go` (`TestApproveChecksConnections`, `TestApproveChecksNamedLLMConnection`) and must be rescued into `workflows_test.go`, re-seeded via `api_key`.
- After OAuth removal **no HTTP path writes an `api_key`-kind connector credential** (`putConnection` hard-codes `llm_api_key`), and the connector e2e seeds Slack via `UpsertConnectionBundle`. P1 re-seeds the e2e at store level (`UpsertConnection`, kind `api_key`); the HTTP capture surface is P2.
- The provider _name_ is load-bearing in five sites (llm factory, proxy route table, inbound token header, outbound credential header, platform-key env map). `permit.llm.providers` stays name-keyed (spec: the harness names a provider, never a URL).
- There is **no settings/config table, no tenant-level event table** (`run_event` is FK'd to `run`), and `SetTenantMonthlyCap` has no HTTP caller — endpoint, prices, budget, and governance events all need new persistence + API.
- `mostRecentDue` is a free function taking `window` as a parameter — the wake-aware change is its call site plus heartbeat load/save. `TestTickStalenessWindowSkipsOldOccurrences` asserts the _current_ (broken-for-wake) behavior and is re-framed as the no-heartbeat case.
- `serve()` today: `os.Exit(2)` config helpers, hardcoded `Addr` (no `:0`), loops on a `context.Background()`-derived ctx (ignores serve's own ctx), no shutdown, no readiness. Every e2e test hand-wires the same graph — they are the natural first consumer of the library shape.
- `store/auth_test.go`'s `hash()` helper is used by surviving `session_test.go` tests; `login_token` reads `monthly_cap_cents` in a fourth SELECT (`store/auth.go:85-87`) that dies with the file.

## Decisions taken (recorded, autonomous, spec-derived)

1. **Endpoint record persists in the DB** (`llm_endpoint`, one row per tenant), not a config file: the server enforces routing, pricing, and the switch-time gate, and the frontend needs an API. The shell's non-secret config file may mirror it later.
2. **No endpoint row = legacy env mode**: `approveVersion` and the proxy behave exactly as on `main` today (`RunProvider`/`RunModel` env, bundled pricing, static route table). First-run always creates a row; this keeps dev workflows and ~all existing tests valid.
3. **Approval bakes endpoint identity _and_ resolved prices into the compiled doc** (`steps.Compiled` gains `endpoint`, `endpoint_preset`, `price_in_cents_per_1m`, `price_out_cents_per_1m`; `CompilerV = 2`). The harness costs runs from the compiled prices (v<2 docs fall back to the bundled table), so no runtime price plumbing exists and "switching re-runs the gate" is simply "switching recompiles".
4. **Endpoint switch = gate + recompile + governance event**: every approved version is recompiled against the new endpoint (the `ApproveVersion` doc comment's recompile-not-copy rule), and a `tenant_event` row `endpoint.switched` records it.
5. **Handoff is a GET that consumes**: `GET /local/handoff?token=…` atomically consumes (login_token's single-use UPDATE shape), mints the session in the same tx, sets the cookie, 303-redirects. The scanner-proof interstitial existed for _emailed_ links; a loopback URL minted seconds earlier by the shell has no scanner in the path.
6. `connection.metadata` is dropped with `epoch` (only OAuth ever wrote it); `status` stays.
7. **Five presets** (board amendment, 2026-08-31, relayed by the coordinating session): Anthropic, OpenAI, OpenRouter, **GitHub Models**, **Azure AI Foundry**, plus custom and local. Verified against current vendor docs:
   - **GitHub Models**: fixed base URL `https://models.github.ai/inference`, OpenAI-compatible chat/completions, auth `Authorization: Bearer <PAT>` (fine-grained PAT with `models:read`). Billing is **two-mode** (board correction, 2026-08-31): the free included quota is subscription-included/rate-limited, but **opt-in pay-as-you-go exists** (token units × per-model multipliers; personal accounts default to a $0 spending limit but can raise it), so a blanket $0 classification would be silent unmetered spend — the exact thing the gate exists to prevent. Resolution: **zero-cost is an explicit per-endpoint flag, never preset-inferred.** The github preset asks "free included quota, or paid usage enabled?": free → `zero_cost = true`, gate skipped, cost recorded 0, copy "included with your GitHub plan — if you later enable paid usage, update this in settings"; paid → `zero_cost = false` and the normal user-entered (base URL, model) price path (link GitHub's pricing page). Flipping the flag later is an ordinary endpoint switch through `PUT /v1/settings/endpoint` — a recorded governance event that re-runs the gate and recompiles. `local` is always `zero_cost = true`. Sources: GitHub REST models-inference docs; GitHub changelog 2025-06-24 (paid usage beyond free limits); docs.github.com "About billing for GitHub Models".
   - **Azure AI Foundry**: **per-resource** base URL — Tomte pins the **v1 API**: `https://<resource>.openai.azure.com/openai/v1` or `https://<resource>.services.ai.azure.com/openai/v1`, which is OpenAI-compatible chat/completions with **no `api-version` parameter** (v1 GA). API keys are sent in an **`api-key` header** (Bearer is Entra-token-only, out of scope). Validation: HTTPS, host suffix ∈ {`.openai.azure.com`, `.services.ai.azure.com`}, path exactly `/openai/v1`. No bundled prices (Azure pricing is per-deployment) — the user-entered price path is Azure's pricing story. Source: MS Learn "Azure OpenAI v1 API" (api-version-lifecycle).

---

## File structure

```
server/serve.go                        NEW package server — Options, Start, Server (library entry)
server/serve_test.go                   NEW boot/host-allowlist/local-session tests
server/internal/db/migrations/00012_pivot_floor.sql   NEW (full DDL in Task 1)
server/internal/endpoint/endpoint.go   NEW endpoint record type, validation, canonical URL, provider name, route
server/internal/endpoint/endpoint_test.go
server/internal/store/{endpoint.go,handoff.go,heartbeat.go}  NEW store methods (+_test)
server/internal/httpapi/{settings.go,local.go}               NEW settings API + /local/handoff (+_test)
DELETE: server/internal/oauth/ (4 files), server/internal/mail/ (4 files),
        server/internal/httpapi/oauth.go(+_test after rescue),
        server/internal/proxyadapter/refresh_test.go,
        server/internal/catalog/{defs,baseline}/google-calendar.json
SHRINK: httpapi/{auth.go,auth_test.go,httpapi.go,connections.go,catalog.go,workflows.go,baseurl.go},
        store/{auth.go,auth_test.go,connection.go}, proxyadapter/adapter.go,
        proxy/{proxy.go,handler.go,connector.go}, llm/{factory.go,pricing.go}, steps/steps.go,
        engine/scheduler.go, harness/harness.go, cmd/tomte/main.go
```

### Task 1: Migration 00012 + store methods for the new tables

**Files:** Create `00012_pivot_floor.sql`, `store/heartbeat.go`, `store/handoff.go`, `store/endpoint.go` + tests. Modify `store/connection.go` (column list), `store/workflow.go` (recompile helpers).

**DDL (verbatim):**

```sql
-- +goose Up
DROP TABLE login_token;

ALTER TABLE connection DROP COLUMN metadata, DROP COLUMN epoch;
ALTER TABLE connection DROP CONSTRAINT connection_kind_check;
ALTER TABLE connection ADD CONSTRAINT connection_kind_check
  CHECK (kind IN ('llm_api_key', 'api_key'));

-- System table: single row, no tenant scope (scheduler catch-up across restarts).
CREATE TABLE scheduler_heartbeat (
  id int PRIMARY KEY CHECK (id = 1),
  last_tick_at timestamptz NOT NULL
);

-- Single-use, short-TTL browser handoff (login_token's consume shape, minus email).
CREATE TABLE handoff_token (
  token_hash text PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
  user_id uuid NOT NULL,
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (tenant_id, user_id) REFERENCES app_user (tenant_id, id) ON DELETE CASCADE
);

-- One configured LLM endpoint per tenant (spec: one endpoint, switchable).
CREATE TABLE llm_endpoint (
  tenant_id uuid PRIMARY KEY REFERENCES tenant (id) ON DELETE CASCADE,
  preset text NOT NULL CHECK (preset IN ('anthropic', 'openai', 'openrouter', 'github', 'azure', 'custom', 'local')),
  kind text NOT NULL CHECK (kind IN ('anthropic', 'openai_compatible')),
  base_url text NOT NULL,
  connection_name text,
  run_model text NOT NULL,
  -- Explicit $0 classification, never inferred (local always; github only on the
  -- free included quota — paid GitHub usage takes the user-entered price path).
  zero_cost boolean NOT NULL DEFAULT false,
  updated_at timestamptz NOT NULL DEFAULT now(),
  -- A local endpoint carries no credential, ever ("Endpoint agnosticism").
  CONSTRAINT llm_endpoint_local_no_connection CHECK (preset <> 'local' OR connection_name IS NULL),
  CONSTRAINT llm_endpoint_local_zero_cost CHECK (preset <> 'local' OR zero_cost),
  CONSTRAINT llm_endpoint_zero_cost_presets CHECK (NOT zero_cost OR preset IN ('local', 'github'))
);

-- User-entered prices, keyed by the endpoint's canonical base URL ("The priced-pair gate, reworked").
CREATE TABLE model_price (
  tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
  base_url text NOT NULL,
  model text NOT NULL,
  input_cents_per_1m int NOT NULL CHECK (input_cents_per_1m >= 0),
  output_cents_per_1m int NOT NULL CHECK (output_cents_per_1m >= 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, base_url, model)
);

-- Tenant-scoped governance events (run_event is FK'd to run and cannot hold these).
CREATE TABLE tenant_event (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
  type text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);
-- +goose Down (recreate login_token exactly as 00009; re-add metadata/epoch + old kind check; drop the five new tables)
```

**Produces (store API):**

- `GetSchedulerHeartbeat(ctx) (*time.Time, error)` / `SetSchedulerHeartbeat(ctx, t time.Time) error` (`INSERT … ON CONFLICT (id) DO UPDATE`) — `// System query:` comments; the single documented exception to the tenant-scoping rule for tables (precedent: the three System-query list methods).
- `CreateHandoffToken(ctx, tokenHash string, tenantID, userID uuid.UUID, ttl time.Duration) error` — inline sweep `DELETE FROM handoff_token WHERE expires_at < now() - interval '1 hour'` on the write path (login_token's trick).
- `ConsumeHandoffToken(ctx, tokenHash, sessionTokenHash string) (tenantID, userID uuid.UUID, err error)` — one tx: claim via `UPDATE … SET consumed_at = now() WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > now() RETURNING tenant_id, user_id`, then the existing unexported `createSession`, commit. `pgx.ErrNoRows` → `ErrHandoffTokenInvalid` (new sentinel).
- `GetLLMEndpoint(ctx, tenantID) (*LLMEndpoint, error)` (`ErrNotFound` when unset) / `PutLLMEndpoint(ctx, tenantID, e LLMEndpoint) error` (upsert) with `type LLMEndpoint struct { Preset, Kind, BaseURL string; ConnectionName *string; RunModel string; ZeroCost bool }`.
- `GetModelPrice(ctx, tenantID, baseURL, model string) (inCents, outCents int, err error)` / `UpsertModelPrice(ctx, tenantID, baseURL, model string, inCents, outCents int) error` / `ListModelPrices(ctx, tenantID) ([]ModelPrice, error)`.
- `AppendTenantEvent(ctx, tenantID, eventType string, payload json.RawMessage) error`.
- `ListApprovedVersions(ctx, tenantID) ([]Version, error)` and `UpdateApprovedCompiled(ctx, tenantID, workflowID uuid.UUID, version int, compiled json.RawMessage) error` (`WHERE status = 'approved'`, `ErrNotFound` otherwise) — for switch-time recompilation.
- `MarkConnectionNeedsReauth(ctx, tenantID, connectionID uuid.UUID) error` — **re-signed without the epoch CAS** (plain `UPDATE … SET status = 'needs_reauth'`).

Steps: write store tests first (`store/heartbeat_test.go`: set/get roundtrip + upsert; `store/handoff_test.go`: consume-once wins / expired / reuse all → `ErrHandoffTokenInvalid`, session row minted on success; `store/endpoint_test.go`: endpoint upsert + local-no-connection CHECK violation + price roundtrip + tenant isolation), run red, implement, run green, commit.

### Task 2: OAuth removal

**Files:** Delete `internal/oauth/*`, `httpapi/oauth.go`, `proxyadapter/refresh_test.go`, `catalog/defs/google-calendar.json`, `catalog/baseline/google-calendar.json`. Modify `httpapi/httpapi.go` (Deps loses `OAuth`, `StateSigner`; routes `POST /v1/connections/oauth/{connector}/start` and `GET /auth/oauth/callback` go), `httpapi/connections.go` (drop `Metadata` from `connectionJSON`, `revokeOAuth` call; `DeleteConnectionLocked` → `DeleteConnection`; reword the 409 guard), `httpapi/catalog.go` (unchanged — `status`/`needs_reauth` reporting survives), `store/connection.go` (delete `UpsertConnectionBundle`, `BundleUpdate`, `WithConnectionLock`, `DeleteConnectionLocked`, `connLockKey`; `Connection` loses `Metadata`/`Epoch`, keeps `Status`; `UpsertConnection` stops writing `status='ok', epoch=epoch+1` on conflict — sets `status = 'ok'` only, so a re-pasted key clears `needs_reauth`), `proxyadapter/adapter.go` (delete oauth field, singleflight, `refreshSkew`, `needsRefresh`, `refresh`, `secretFor`, oauth branch of `resolve`; `New` loses the `oauthSvc` param; `MarkBroken` now calls the simplified `MarkConnectionNeedsReauth` — the 401-demotion path is the surviving revoked-pasted-token detector), `proxy/connector.go` (401 demote/retry logic stays; rename the `fakeoauth` test provider), `cmd/tomte/main.go` (drop oauth imports, state-key derivation, `EnvClients`, Deps wiring, `TOMTE_OAUTH_*` doc block), `catalog/catalog_test.go` (`[]string{"slack"}`), `httpapi/catalog_test.go`, e2e (`seedOAuthConnection`-style seeding → `store.UpsertConnection(…, "api_key", "slack", …)` with vault-encrypted token).

Rescue first: move `TestApproveChecksConnections` and `TestApproveChecksNamedLLMConnection` into `workflows_test.go`, seeding via `UpsertConnection` kind `api_key` (connected case) and a row with `status='needs_reauth'` (approve still refuses — the check at `workflows.go:263-266` survives).

Steps: rescue tests + re-seed e2e (green against current code), then delete in dependency order (httpapi/oauth.go → main.go wiring → proxyadapter → store → internal/oauth → catalog defs+baseline together), `go build ./... && go test ./...` after each, commit per coherent unit.

### Task 3: Login + mail removal (session core untouched)

**Files:** Delete `internal/mail/*`. Modify `httpapi/auth.go` (keep only `logout`, `me`; move `firstLoginPath` to `httpapi/local.go` in Task 4; delete `interstitialPage`, `expiredPage`, `magicLink`, `tryToSendLink`, `verifyPage`, `verify`, `ipLimiter` and its constants; **keep `isSafeRelativePath`** — handoff's `next` param reuses it), `httpapi/httpapi.go` (Deps loses `Mailer`; routes `/v1/auth/magic-link`, `/auth/verify`, `/v1/auth/verify` go; `PublicBaseURL` doc comment rewritten as "the loopback origin, auto-configured by serve"), `httpapi/baseurl.go` (delete `IsLocalhost`; keep `ParsePublicBaseURL` with the justification rewritten to Secure-cookies + Origin), `store/auth.go` (delete `ErrLoginTokenInvalid`, `NewSignup`, `tenantNameFromEmail`, `ConsumeResult`, `ConsumeLoginToken`, `CreateLoginToken`, `LoginTokenValid`, `CountActiveLoginTokens` — the whole file except nothing survives → **delete the file**; move the `hash()` test helper into `store/session_test.go`), `cmd/tomte/main.go` (drop mail import, `TOMTE_POSTMARK_TOKEN`/`TOMTE_MAIL_FROM` reads + doc block), tests (`httpapi/auth_test.go` keep only logout test; `store/auth_test.go` keep the two user tests, moved or kept with `hash` relocated; delete `TestEndToEndMagicLinkLogin` from e2e; re-point `TestOriginPolicyOnAuthRoutes` at `POST /v1/auth/logout`; strip `env.mailer` from `newEnv`/e2e), `server/README.md` + `docs/api/v1.md` (drop magic-link rows).

Steps: tests-first where behavior changes (origin re-point), delete, suite green, commit.

### Task 4: Local-session mint + `/local/handoff`

**Files:** Create `httpapi/local.go` + `local_test.go`. Modify `httpapi/httpapi.go` (route), `cmd/tomte/main.go` (`dev-session` refactors onto the shared helper).

**Produces:**

- `httpapi/local.go`: `const firstLoginPath = "/build"` (moved home); `const handoffTTL = time.Minute`; route `GET /local/handoff` (registered **without** session middleware; not a mutating `/v1` route, token is the credential): reads `token` + optional `next` (validated by `isSafeRelativePath`), `HashToken`, `Store.ConsumeHandoffToken` with a freshly minted session token, on success `SessionCookie` + `303 See Other` → `next` or `firstLoginPath`; invalid/expired/reused → `403` plain page, no cookie.
- `httpapi.EnsureLocalOwner(ctx, s *store.Store, v *vault.Master) (tenantID, userID uuid.UUID, err error)`: `UserByEmail("owner@tomte.local")`; on `ErrNotFound` → `vault.NewTenantKEK` + `CreateTenant("local", …)` + `UpsertUser` (dev-session's exact shape, fixed names). The placeholder local identity from "Identity at its floor".
- `httpapi.MintLocalSession(ctx, s *store.Store, tenantID, userID uuid.UUID) (*http.Cookie, error)` — `NewOpaqueToken` + `CreateSession` + `SessionCookie`.
- `httpapi.NewHandoffToken(ctx, s *store.Store, tenantID, userID uuid.UUID) (rawToken string, err error)` — `NewOpaqueToken` + `CreateHandoffToken` with `handoffTTL`.

Tests (`local_test.go`): handoff happy path sets cookie + redirects to `/build`; `next=/settings` honored, `next=https://evil` rejected to `/build`; second GET with same token → 403 and no cookie; expired token → 403; `RequireSession` still guards `/v1/*` (no unauthenticated surface beyond the one exchange).

`dev-session` keeps its CLI shape but its mint body calls `MintLocalSession` (custom email/tenant flags preserved).

### Task 5: Wake-aware scheduler window

**Files:** Modify `engine/scheduler.go`, `engine/scheduler_test.go`.

`Tick` becomes:

```go
func (s *Scheduler) Tick(ctx context.Context) {
    defer func() { /* existing recover */ }()
    now := s.now()
    lookback := s.window()
    if last, err := s.Store.GetSchedulerHeartbeat(ctx); err != nil {
        slog.Error("scheduler: heartbeat load", "err", err) // fall back to the default window
    } else if last != nil {
        if gap := now.Sub(*last) + s.interval(); gap > lookback {
            lookback = gap // sleep/downtime gap plus one interval of margin ("The sleeping machine")
        }
    }
    s.createDue(ctx, now, lookback)
    s.dispatchPending(ctx)
    if err := s.Store.SetSchedulerHeartbeat(ctx, now); err != nil {
        slog.Error("scheduler: heartbeat save", "err", err)
    }
}
```

`createDue` gains `(now, lookback)` params (drops its own `s.now()`/`s.window()` calls); `mostRecentDue` unchanged. Idempotence stays index-enforced (`run_workflow_firetime_unique`), so a long lookback cannot double-fire.

Tests: **`TestTickFiresOccurrenceHoursOldAfterWake`** — the case that never fires today: daily `0 3 * * *` schedule, heartbeat seeded at 02:59, `Now` fixed at 07:42 → exactly one run with `fire_time` = 03:00; a second `Tick` creates nothing. `TestTickStalenessWindowSkipsOldOccurrences` re-framed as `TestTickNoHeartbeatKeepsDefaultWindow` (first-ever tick, no heartbeat row → old occurrence still outside the 5-minute window). Existing tests get a heartbeat-free setup (nil heartbeat ⇒ behavior identical to today).

### Task 6: Endpoint record + proxy endpoint-awareness + local skip

**Files:** Create `internal/endpoint/endpoint.go` + test. Modify `proxy/proxy.go`, `proxy/handler.go`, `llm/factory.go`, `cmd/tomte/main.go` (later folded into serve.go), proxy tests.

**`internal/endpoint`:**

```go
type Endpoint struct {
    Preset  string // anthropic | openai | openrouter | github | azure | custom | local
    Kind    string // anthropic | openai_compatible
    BaseURL string // canonical; fixed for anthropic/openai/openrouter/github, user-entered for azure/custom/local
    Connection string // vault llm_api_key connection name; empty for local
    RunModel   string
    ZeroCost   bool   // explicit classification: local always true; github by user choice (free quota vs paid usage); all others false
}
func (e Endpoint) Provider() string   // presets → their name ("github", "azure", …); custom → "custom"; local → "local"
func (e Endpoint) Route() (base, method, path string) // kind anthropic → POST v1/messages; openai_compatible → POST chat/completions (path allowlist unchanged; Azure v1 API needs no api-version)
func (e Endpoint) CredentialHeader() string // "x-api-key" (anthropic) | "api-key" (azure) | "Authorization: Bearer" (rest) | none (local)
func Canonical(raw string) (string, error) // lowercase scheme+host, strip trailing slash
func Validate(preset, rawBaseURL string) (Endpoint, error)
```

`Validate`: fixed-base presets pin their base URLs (`https://api.anthropic.com`, `https://api.openai.com/v1`, `https://openrouter.ai/api/v1` — today's `proxy.DefaultConfig` values — and `https://models.github.ai/inference` for `github`); `azure` parses the user's per-resource URL (HTTPS, host suffix ∈ {`.openai.azure.com`, `.services.ai.azure.com`}, path exactly `/openai/v1`, no userinfo/query/fragment); `custom`/`local` parse the user URL: **HTTPS required except loopback hosts** (`localhost`, `127.0.0.1`, `::1` — reuse `isLocalhost`'s list), **no userinfo**, no query/fragment; `local` additionally requires a loopback host (necessary, never sufficient — classification stays explicit).

**Proxy:** `proxy.Deps` gains `Endpoints EndpointSource` (`interface { EndpointFor(ctx context.Context, tenantID uuid.UUID) (*endpoint.Endpoint, error) }`, nil-tolerant). `authorize`: if `Endpoints` returns a record and `provider == e.Provider()`, the route is `e.Route()`; otherwise fall back to `Config.Providers` (legacy env mode + existing tests). `forward` on a `local` endpoint: **skip credential resolution and injection entirely** — inbound `Authorization`/`x-api-key` still deleted, nothing set, request forwarded bare. For all other endpoint routes, outbound injection uses `e.CredentialHeader()` (Azure gets `api-key`, anthropic `x-api-key`, github/openai/openrouter/custom `Authorization: Bearer`) instead of the provider-name switch. The permit's `llm.connection` resolution must tolerate the no-credential endpoint rather than 500 — **the contract worth its own test** (spec, "Endpoint agnosticism"):

- `TestProxyLocalEndpointSkipsCredentials` (proxy test with `Endpoints` fake returning a `local` record pointed at an `httptest` upstream): request forwarded with **no** auth header, 200 relayed, `proxy.request` audited — and no `proxy.error{stage:"credential"}` even though no connection exists.
- `TestProxyCustomEndpointInjectsBearer`: `custom` record + `llm_api_key` connection → `Authorization: Bearer` at the upstream, path allowlist still 403s a wrong path.

**Factory:** `llm.NewFactory(cfg Config)` is replaced by `llm.NewProxyFactory(proxyBase string) Factory` — `anthropic` → anthropic SDK, everything else (openai, openrouter, custom, local) → OpenAI-compatible SDK, both pointed at `proxyBase + "/proxy/llm/" + name`. (Inbound token extraction in `handler.go:32-40` already treats non-anthropic names as Bearer.)

`proxyadapter` implements `EndpointSource` over `store.GetLLMEndpoint` (`ErrNotFound` → `nil, nil`).

### Task 7: Pricing-gate rework + endpoint switch + settings API

**Files:** Modify `steps/steps.go`, `llm/pricing.go`, `httpapi/workflows.go`, `harness/harness.go`, `internalapi` (only if compile shape assertions exist). Create `httpapi/settings.go` + `settings_test.go`.

**Compiled doc** (`steps.Compiled`, `CompilerV = 2`; additive fields, v0/v1 docs stay loadable):

```go
Endpoint          string `json:"endpoint,omitempty"`        // canonical base URL — approval records the endpoint identity
EndpointPreset    string `json:"endpoint_preset,omitempty"` // "" on legacy env-mode approvals
PriceInCentsPer1M int    `json:"price_in_cents_per_1m,omitempty"`
PriceOutCentsPer1M int   `json:"price_out_cents_per_1m,omitempty"`
```

`steps.Platform` gains the same four fields, copied through by `Compile`.

**`llm/pricing.go`:** add `Price(provider, model string) (inCents, outCents int, ok bool)` (table lookup) and `MaxTokensForOutPrice(outCentsPer1M, perRunCents int) int` (the existing clamp math, price passed in; `outCentsPer1M <= 0` → `defaultBudgetTokens` — the local fallback). Existing `Priced`/`CostCents`/`MaxTokensForBudget` stay for legacy-mode and v<2 docs.

**`approveVersion`** price resolution (replaces the flat `llm.Priced` check):

1. `Store.GetLLMEndpoint`; `ErrNotFound` → legacy mode, exactly today's code path.
2. `provider, model := e.Provider(), e.RunModel`; permit `AllowsProvider(provider)` check as today.
3. `e.ZeroCost` (the explicit flag: `local` always; `github` only in free-quota mode) → prices 0/0, `MaxTokens = defaultBudgetTokens`, **gate skipped** (`local`: "runs on your computer — free"; `github`: "included with your GitHub plan").
4. Priced preset → `llm.Price(provider, model)`; miss falls through to 5 (azure/custom — and github in paid mode — always land here: no bundled rows).
5. `Store.GetModelPrice(tenant, e.BaseURL, model)`; miss → **400 with a machine-readable body** `{"error": "unpriced_model", "model": model, "base_url": e.BaseURL}` — the frontend's inline two-number form contract.
6. Resolved prices + endpoint fields go into `steps.Platform`; `MaxTokens = llm.MaxTokensForOutPrice(outCents, perRunCents)`.
7. Connection check: `local` endpoints skip the `llm.connection` existence check (nothing to look up); others check `e.Connection` (falling back to today's `"default"` tolerance in legacy mode).

**Harness cost:** in `harness.Run`, cost per turn becomes: `EndpointPreset != ""` → cost from the compiled prices, `(in*PriceIn + out*PriceOut) / 1e6` floored once (today's floor-once semantics, `costFromCompiled` helper; a zero-cost approval compiled 0/0, so it yields 0 with no preset special-casing); else (v<2 / legacy doc) → `llm.CostCents` as today.

**`httpapi/settings.go`** (all session-authed; mutations Origin-wrapped via `mut(auth(...))`):

- `GET /v1/settings/endpoint` → `{preset, kind, base_url, connection, run_model, zero_cost, connected}` or 404 when unset.
- `PUT /v1/settings/endpoint` `{preset, base_url, connection, run_model, zero_cost}` (`zero_cost` accepted only for `github` — `local` forces true, everything else forces false; a flip of the flag alone is still a full switch) → `endpoint.Validate`; then the **switch-time gate**: for every approved version, resolve the new (provider, model) price exactly as approveVersion does; any miss → `409 {"error": "unpriced_models", "models": [{"model": …, "base_url": …}]}` and **no write**. On success: `PutLLMEndpoint`, recompile every approved version (`steps.Parse` the stored draft → `steps.Compile` with the new Platform → `UpdateApprovedCompiled`), `AppendTenantEvent(…, "endpoint.switched", {"from": old.BaseURL|null, "to": new.BaseURL, "preset": new.Preset})`. The recorded governance act.
- `GET /v1/settings/prices` / `PUT /v1/settings/prices` `{base_url, model, input_cents_per_1m, output_cents_per_1m}` (explicit `base_url` so switch-time pricing can target the _new_ endpoint before switching).
- `GET /v1/settings/budget` → `{monthly_cap_cents}`; `PUT /v1/settings/budget` `{monthly_cap_cents}` → `SetTenantMonthlyCap` (first HTTP caller; the budget is user-owned now).

Tests (`settings_test.go`): endpoint CRUD + validation rejects (`http://` non-loopback, userinfo, local-with-connection); switch with unpriced approved version → 409 and endpoint unchanged; switch after `PUT prices` → 200, compiled doc re-read shows new endpoint + prices, `tenant_event` row exists; local switch → approved version recompiled with 0/0 prices and default max_tokens. `workflows_test.go`: approve on custom endpoint 400s `unpriced_model` until a price row exists, then compiles prices in; approve on local endpoint skips gate and records preset. Harness test: cost computed from compiled prices; local → 0.

### Task 8: Budget rename (copy only)

**Files:** Modify `store/tenant.go` (field/comment copy), `meter/meter.go` (comments), `cmd/tomte/main.go` + `server/README.md` (env docs: "the user's local budget — how much Tomte may spend from your key per month"), `docs/api/v1.md` (settings endpoints documented). No column rename, no enforcement change (Task 7 already added the API).

### Task 9: `serve()` as a library + loopback hardening

**Files:** Create `server/serve.go`, `server/serve_test.go`. Modify `cmd/tomte/main.go` (serve → env-to-Options translation; env helpers stay in main where `os.Exit(2)` is appropriate).

```go
package server // module root, alongside e2e_test.go

type Options struct {
    DatabaseURL   string
    ListenAddr    string            // default "127.0.0.1:8080"; "127.0.0.1:0" supported
    PublicBaseURL string            // optional override (Vite dev origin); default: derived from the bound listener
    RunnerKey, VaultKey []byte      // 32 bytes each
    StateDir      string
    RunProvider, RunModel string    // legacy env-mode fallback (defaults as today)
    RunTokenTTL, RunDeadline time.Duration
    DefaultMonthlyCapCents int
    PlatformKeys  map[string]string // dev/headless fallback keys
    LogHandler    slog.Handler      // optional; always wrapped in redact.Handler
}

func Start(ctx context.Context, o Options) (*Server, error)

type Server struct{ /* unexported */ }
func (s *Server) Addr() string                    // bound address (real port even for :0)
func (s *Server) BaseURL() string                 // "http://127.0.0.1:<port>" — the auto-configured loopback origin
func (s *Server) MintLocalSession(ctx context.Context) (*http.Cookie, error) // EnsureLocalOwner + MintLocalSession
func (s *Server) HandoffURL(ctx context.Context) (string, error)             // BaseURL + "/local/handoff?token=…"
func (s *Server) Shutdown(ctx context.Context) error
func (s *Server) Err() <-chan error               // serve-loop exit
```

`Start`, in order: migrate → pool → **`net.Listen("tcp", o.ListenAddr)` first**, derive `baseURL`/`publicBase` from the bound address (override honored) → catalog, token signer, vault, redacted slog (`slog.SetDefault` stays — redaction is a security control; the handler sink is the caller's) → engine/meter/mux wiring exactly as today's `serve()` → **background loops on a ctx-derived `loopCtx`** (fixes the `context.Background()` detachment) → `httpServer.Serve(ln)` in a goroutine → return. `Shutdown` cancels loops + `http.Server.Shutdown`.

**Host allowlist** (spec, "Loopback security posture"): `hostAllowlist(allowed map[string]bool, next http.Handler)` wrapping the **whole mux** (covers `/v1`, `/proxy/*`, `/internal/*` — `checkOrigin` only covers `/v1` mutations). Allowed: the bound `host:port`, plus the `PublicBaseURL` host when overridden. Mismatch → 403 "unknown host". DNS-rebinding requests arrive with the attacker's Host and die here.

`cmd/tomte serve`: reads the same env vars (TOMTE_PUBLIC_BASE_URL now **optional**), builds `Options`, `Start`, waits on `Err()`/signal. `dev-session` unchanged beyond Task 4.

Tests (`serve_test.go`, testpg-backed): `TestStartBootsAndServes` — `Start` with `ListenAddr: "127.0.0.1:0"`, `GET BaseURL()+"/v1/me"` without cookie → 401 (routes live), correct Host accepted, `Host: evil.example` → 403 (**the allowlist test**); `TestMintLocalSessionAndHandoff` — `MintLocalSession` cookie authenticates `/v1/me`; `HandoffURL` GET (no redirect-follow) sets cookie + 303; `TestShutdownStopsLoops` — `Shutdown` returns, `Err()` yields. These are the packaging lane's consumption proof.

### Task 10: Verification + delta sheet

- [ ] `gofmt -l .` (nothing), `go vet ./...`, `go build ./...`, full `go test ./...` from `server/` — including `TestEndToEndConnectorToolRun` on the api_key seed.
- [ ] Real boot: `docker run` a Postgres (or reuse the dev one), `DATABASE_URL=… TOMTE_RUNNER_KEY=… TOMTE_VAULT_KEY=… go run ./cmd/tomte serve` — confirm bind log, `GET /v1/me` → 401, `Host: evil` → 403, clean Ctrl-C. **No `TOMTE_PUBLIC_BASE_URL`, `TOMTE_POSTMARK_TOKEN`, or `TOMTE_OAUTH_*` set** — proving they're gone/optional.
- [ ] `npx prettier --write` on touched docs; sweep for stale `docs/api/v1.md` rows (magic-link, oauth) and add settings/handoff rows.
- [ ] `my:polish-core --fix`, re-verify, `my:change-explainer`; PR to `main` with the delta sheet below in the description.

## Delta sheet (published to consuming lanes on completion)

**Packaging lane** — the library seam: `server.Options` / `server.Start(ctx, Options) (*Server, error)` / `Server.{Addr, BaseURL, MintLocalSession, HandoffURL, Shutdown, Err}` exactly as Task 9. `TOMTE_PUBLIC_BASE_URL` optional; Host allowlist automatic.

**Frontend lane** — API shapes: settings endpoints + error contracts exactly as Task 7 (`unpriced_model` / `unpriced_models` bodies are the price-form triggers); preset enum `anthropic|openai|openrouter|github|azure|custom|local` (azure/custom/local take a user base URL; `zero_cost` is an explicit per-endpoint flag — local always, github by free-vs-paid choice on the capture card, togglable later via the endpoint settings switch); login/magic-link routes gone; `GET /local/handoff?token=&next=` sets the session cookie; `/v1/me`, logout, Origin rules unchanged.

Final numbers (migration verified as 00012, exact signatures as merged) are re-verified against the tree at PR time and reported to the board.

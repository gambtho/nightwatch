# P2 — Connectors Main Road: Implementation Plan

**Status:** Awaiting coordination review; `server/` lock taken only after the
hygiene pass merges
**Date:** 2026-08-31
**Author:** gambtho
**Spec:** [`2026-08-31-tomte-pivot-design.md`](../specs/2026-08-31-tomte-pivot-design.md)
("Credentials without OAuth", "First run", "Endpoint agnosticism") over
[`2026-08-30-nightshift-connectors-design.md`](../specs/2026-08-30-nightshift-connectors-design.md)
(remote MCP, SSRF surface, discovery), sequenced by the board's P2 prompt.
**Baseline tree:** `main` @ `8f7c434` (post-P1). Written lock-free; every
file/line claim re-verified at branch time against the post-hygiene tree.

## Intended outcome

Four PRs, in order, each independently green on `main`:

- **A — Slack token capture + verify.** The catalog's Slack entry carries a
  structured capture guide; `PUT /v1/connections/{name}` verifies a pasted
  Slack token live (control-plane, session-authed, never a run token) before
  storing it, and returns a scope warning derived from Slack's
  `x-oauth-scopes` response header.
- **B — First-run LLM key verification.** A session-authed endpoint makes the
  spec's disclosed, metered "one live, minimal call" with a candidate key at
  paste time; its cost is recorded so it counts against the monthly budget
  once one is set.
- **C — Remote MCP registration + discovery** (old connectors phase 5): the
  `mcp_server` record, canonicalization, the hardened dialer with the full
  SSRF defense set, probe-verified registration, `refresh-tools` snapshots,
  and the `GET /v1/catalog` MCP extension.
- **D — Remote MCP proxy enforcement** (old phase 6, strictly after C):
  `/proxy/mcp/{serverID}`, permit `remote_mcp` entries with snapshot pinning,
  and the harness `mcp_{shortid}__{tool}` projection.

Old phase 3 (options client) is explicitly **not** scheduled — it starts only
when the build-conversation lane asks, confirmed through the coordinating
session.

## Evidence and constraints

Verified against `main` @ `8f7c434`:

- **Catalog:** one def, `internal/catalog/defs/slack.json`, mirrored by
  `baseline/slack.json`; `Auth` is `{Provider string}` only
  (`catalog.go:52-56`); decoding uses `DisallowUnknownFields`
  (`catalog.go:130`), so any new def key needs a Go field first, and any def
  edit must update the baseline in the same PR or boot fails
  (`CheckAgainstBaseline`, called from `serve.go:115`). The gate
  (`gate.go:26-146`, `cmd/catalog-gate`) fails only on reach-widenings; op
  **additions are append-only-legal**, and non-widening drift is a
  baseline-update, not a violation.
- **Connections API:** `PUT /v1/connections/{name}` hardcodes
  `kind="llm_api_key"` (`httpapi/connections.go:70`) and performs **no
  verification**; `api_key` is legal at the DB level (migration
  `00012_pivot_floor.sql` kept it in the kind CHECK) but unreachable over
  HTTP. `GET /v1/catalog` already joins per-connector `connected`/`status`
  (`httpapi/catalog.go:38-58`), with the `"default"` connection name winning
  in a provider namespace.
- **The proxy route cannot host paste-time verify.** The connector gateway
  requires a run token (`proxy/connector.go:29`), and its response-header
  allowlist passes only `Content-Type` and `Location` back
  (`connector.go:225-238`) — `x-oauth-scopes` is dropped. Compilation is
  reusable without it: `catalog.Compile(opDef, args)`
  (`internal/catalog/compile.go`) is the proxy-independent half.
- **No non-run spend path exists.** Monthly spend is a sum over
  `run.cost_cents` (`store/run.go:256`, `MonthSpendCents`); the meter
  (`internal/meter/meter.go`) consults only that. Recording a paste-time
  verify call's cost requires a new persisted surface — the only new-table
  decision in phases A/B.
- **Endpoint record + prices are live:** `store.LLMEndpoint`
  (`store/endpoint.go:16`), domain `endpoint.Endpoint` with
  `Validate`/`Canonical`/`Route`/`CredentialHeader`
  (`internal/endpoint/endpoint.go`), settings handlers with
  `resolvePrices` (`httpapi/settings.go:44`) and the preset enum
  `anthropic|openai|openrouter|github|azure|custom|local` with DB-checked
  `zero_cost`.
- **Phase 4's harness tool loop is fully merged**: `llm.Message.IsError`
  (`llm/provider.go:34`), tools projection (`internalapi.go:161`), the
  `ChatTurn` loop with parallel dispatch and `tool.call.*` events
  (`harness/harness.go:214-256`). Phases 3, 5, 6 are confirmed absent (no
  MCP table, no hardened dialer, no options client).
- **Migrations:** through `00012_pivot_floor.sql`; **next free is 00013**
  (re-verify at branch time — the board's standing instruction).
- **Frontend contracts** (merged PR #47, fake seams at
  `web/src/local/connections.ts` and `web/src/local/config.ts`):
  `ConnectionState = ok|needs_reauth|missing`; `connectWithToken(id, token)`
  is **one verify-then-store call** returning `{ok:true} | {ok:false,
message}`; `CaptureGuide` carries `startUrl?/startLabel?/steps/
secretPrefix?/placeholder?` (two fields beyond the spec's JSON example);
  `registerMcpServer(name, url, key)` includes a display **name** the
  connectors spec's `mcp_server` row does not have; `verifyEndpointKey(
preset, key)` runs **before** the endpoint is saved.
- **Board decisions binding here:** five-preset enum and explicit
  `zero_cost`; the Slack `manifest_url` has no hosted home — ship
  plain-create copy, claim no pre-fill; each phase PRs to `main` and waits
  for its predecessor (no pre-stacked bases); `mut(auth(...))` for every new
  mutating `/v1` route (`httpapi.go:57-91`).

Conflict flagged rather than silently resolved: `docs/api/v1.md` still says
permit `connections` "must be empty in v1" — stale since connectors phase 1
merged. Phase A's doc update corrects the connections/catalog sections it
touches; the wholesale doc refresh belongs to the docs lane.

## Decisions for review

Ordered by review risk.

### 1. Verify-then-store lives inside `PUT /v1/connections/{name}`

The frontend contract is a single call that verifies and stores. The route
widens: body gains optional `kind` (default `llm_api_key` for
compatibility; `api_key` accepted). When the named provider matches a
catalog connector whose capture guide declares a `verify_op`, the handler
runs the live verify **with the pasted value, before storing**; a failed
verify is a `422` with a user-facing `message` and **nothing is stored**. A
successful verify stores the connection (existing upsert, `status='ok'`)
and the `200` response gains an optional `missing_scopes: [...]` warning —
warn-and-store, per the spec ("warns immediately … instead of failing at
first run").

Fail-closed sequencing (the old plan's decision 2, carried forward):
phase A accepts `kind: api_key` only for providers present in the catalog;
`mcp:{uuid}` namespaces are accepted from phase C, where the row they
qualify exists.

- **Rejected — separate `POST …/verify` endpoint:** two round-trips, a
  TOCTOU gap between verify and store, and a second place holding a raw
  secret in flight. The frontend seam is also shaped as one call.
- **Consequence if wrong:** an API reshape at frontend wire-up time; cheap
  to revisit, so the simpler contract wins.

### 2. `auth_test` ships as a normal catalog read op; verify success requires body-level `ok`

The spec's guide names `verify_op: "auth_test"`. Slack's `auth.test` is not
in the catalog today, so phase A **appends** it as an ordinary op
(`effect: read`, empty scope set — `auth.test` requires none, which is also
why it can verify any bot token — empty args schema, `POST
slack.com/api/auth.test`). Append is narrow-only-legal; validation gains a
rule that `capture.verify_op` names an existing `read` op of the same
connector.

**The success rule is not HTTP-status-only.** Slack's Web API returns HTTP
200 with `{"ok": false, "error": "invalid_auth"}` for a bad token — a
status-code check would verify garbage. The contract: non-2xx fails; a JSON
body containing a boolean `ok` field must have it `true`, else the verify
fails with the body's `error` code mapped to user-facing copy. This is a
documented generic rule (Slack Web API envelope convention), not a
Slack-special-case in code.

- **Rejected — a private verify binding inside `auth.capture`** (not an
  op): duplicates the binding/validation machinery for one URL and hides an
  invocable HTTP call from the narrow-only gate's scrutiny. As a real op it
  is diffable, validated, and permit-grantable (harmlessly — it reads the
  bot's own identity).
- **Consequence if wrong:** invalid tokens stored as `ok` — the exact 3 AM
  failure the surface exists to prevent. This decision is the phase's
  correctness core; test it first.

### 3. The control-plane verify client is a direct HTTP client, not a proxy hop

A new small package (working name `internal/captureverify`, final name at
implementation) builds the request via `catalog.Compile` on the `verify_op`
with empty args, injects `Authorization: Bearer <candidate>` — the pasted
value, which is deliberately **not in the vault yet** — and calls upstream
directly: explicit `http.Transport` with `Proxy: nil`, no redirects
(`CheckRedirect` errors), 10s timeout, response body capped. It reads
`x-oauth-scopes` from the raw response and compares against the union of
the connector's ops' scope sets to produce `missing_scopes`.

It cannot ride `/proxy/connector/…`: that route requires a run token, reads
credentials only from the vault, and strips `x-oauth-scopes` from
responses. Reusing `catalog.Compile` keeps the one compile path the spec
requires; the proxy's own header allowlist is untouched.

- **Rejected — widening the proxy's response-header allowlist and calling
  the route in-process:** widens the run path's response surface for a
  control-plane-only need, and still can't work pre-storage (vault-only
  credential resolution).
- **Consequence if wrong:** a second compile path drifting from the proxy's
  — mitigated by sharing `catalog.Compile` and asserting in tests that both
  paths produce identical upstream requests for the same op.

### 4. Capture guide: catalog structure + Go types, exposed via `GET /v1/catalog`

`Auth` gains an optional `Capture` struct:

```go
type Capture struct {
	StartURL     string   `json:"start_url,omitempty"`
	StartLabel   string   `json:"start_label,omitempty"`
	Steps        []string `json:"steps"`
	SecretPrefix string   `json:"secret_prefix,omitempty"`
	Placeholder  string   `json:"placeholder,omitempty"`
	VerifyOp     string   `json:"verify_op,omitempty"`
}
```

`start_label` and `placeholder` come from the frontend's merged
`CaptureGuide` shape — the spec's JSON example lacks them, and the frontend
contract wins (additive). The Slack def gains the guide with **plain-create
copy** (`https://api.slack.com/apps?new_app=1`, no `manifest_url`, no
pre-fill claim — board open question). Baseline updates in the same commit
(`catalog-gate -update-baseline`); the gate reports this as non-widening
drift, which is the designed path. `GET /v1/catalog` serializes `capture`
per connector; `secret_prefix` gives the frontend its instant
wrong-string-paste check.

### 5. LLM key verify: `POST /v1/settings/endpoint/verify`, direct provider call

Session-authed, `mut(auth(...))`. Body:
`{preset, base_url?, connection_value?, run_model?}` — the **candidate**
endpoint (validated by `endpoint.Validate`, exactly as `PUT
/v1/settings/endpoint` does) plus the candidate key, since first-run
verifies before anything is saved (the frontend calls `verifyEndpointKey`
before `saveEndpoint`). The handler makes one minimal call — `max_tokens:
1`, one-word prompt — through the `llm` provider layer against
`endpoint.Route()`, injecting the candidate key per
`endpoint.CredentialHeader()`. `local` presets send no credential and the
call doubles as a reachability check. Response: `{ok:true,
cost_cents, recorded}` or `{ok:false, message}` (mapped upstream auth
errors, timeouts, connectivity).

**Spec-phrase divergence, stated plainly:** the spec says "through the
proxy path". The proxy's LLM route requires a run identity and permit and
resolves credentials from the vault — none exist at paste time. This plan
reads "proxy path" as _the same validation and injection semantics_
(`endpoint.Validate`, `Route`, `CredentialHeader` — shared code, one
source of truth), with the egress made directly by the server process. If
coordination reads the spec as requiring the literal proxy hop, this
decision reverts to minting an internal synthetic identity — noted as the
alternative, rejected for widening the run-token trust surface.

Budget posture: the verify consults `Meter.CapCents`/`OverCap` first and
refuses (`{ok:false, message: budget copy}`) when the budget is exhausted —
fail-closed consistency with every other spend path. At first run no budget
exists yet (serve default applies), matching the spec's disclosed-call
framing.

### 6. Recording verify cost: a `spend_entry` ledger table (migration 00013)

No non-run spend path exists, and a synthetic `run` row would violate run
invariants (workflow FK, one-active-run index). Migration 00013 adds a
minimal append-only ledger:

```sql
CREATE TABLE spend_entry (
    id            uuid PRIMARY KEY,
    tenant_id     uuid NOT NULL REFERENCES tenant(id),
    kind          text NOT NULL CHECK (kind IN ('endpoint_verify')),
    cost_cents    integer NOT NULL CHECK (cost_cents >= 0),
    input_tokens  integer NOT NULL,
    output_tokens integer NOT NULL,
    base_url      text NOT NULL,
    model         text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
```

`store.MonthSpendCents` widens to `run` sum **plus** ledger sum — the one
metering-path change, and the mechanism by which the verify call "counts
against the month once the budget exists" (entries recorded pre-budget are
already in the sum when a budget arrives). Cost follows the existing
rounding convention (`llm.CostCents`: round once on the combined
numerator); `zero_cost` endpoints record a 0-cent row with real token
counts. On an endpoint whose (base_url, model) has no bundled or
user-entered price yet — a custom endpoint at first run, before the price
form — the row records the tokens with `cost_cents = 0`: honest about
usage, and the budget error is bounded by one 1-token call. The `kind`
CHECK is deliberately single-valued; future non-run spenders (the build
agent, the grader) widen it when they arrive.

- **Rejected — synthetic run rows** (invariant damage) and **tenant_event
  with a cost payload** (events are not a ledger; summing JSON payloads in
  the meter's hot path is the wrong shape).
- **Consequence if wrong:** a metering-path change is security-adjacent —
  reviewed first, tested against `Meter.OverCap` behavior.

### 7. MCP registration (phase C): spec's phase 5 + a display name + registry-shaped-but-empty

Migration 00014: `mcp_server` (id, tenant_id, **display name** — the
frontend contract adds it to the spec's row — canonical URL, auth mode
`api_key|none`, origin `registry|custom`, timestamps) and
`mcp_tools_snapshot` (server FK, content hash, immutable JSON revision,
created_at; prior revisions kept while pinned).

- **`POST /v1/mcp-servers`** with the spec's no-orphan ordering:
  canonicalize (reject before persisting) → create row → store the secret
  under `mcp:{server-uuid}` (`api_key` auth) → probe (`initialize` +
  `tools/list`) through the hardened dialer with that credential → on probe
  failure, delete row and secret and return the error.
- **The hardened dialer is a new shared package** (`internal/egress`),
  implementing the spec's full SSRF set verbatim: canonicalization rules
  (HTTPS only, port 443 or ≥1024, no userinfo/fragment, IDN punycoding,
  IP-literal rejection), per-attempt resolution filtering against the full
  denied set including v4-mapped-v6, pinned-IP dialing, explicit transport
  with `Proxy: nil`, no redirect following, TLS verified for the canonical
  hostname, composed proxy-owned timers. Phase D's proxy route consumes the
  same package — one dialer, three touch points, as the spec demands.
- **`POST /v1/mcp-servers/{id}/refresh-tools`**: control-plane MCP client
  (session-authed, never run tokens), new content-hashed snapshot revision.
- **`GET /v1/catalog`** gains the registered-servers slice (id, name, URL,
  state, snapshot summary). `DELETE /v1/mcp-servers/{id}` removes server +
  credential (frontend `removeMcpServer`).
- **Registry entries ship as schema, not data.** The `origin: registry`
  shape and the capture-guide generalization (`capture` on a registry
  entry) land; no vendor list is settled and the frontend seam has no
  registry browser. One-click registry adds become a data addition later —
  flagged to the board, not silently dropped.
- MCP protocol scope: tools only (`initialize`, `tools/list`, `tools/call`,
  `ping`), per the spec.

### 8. MCP enforcement (phase D): permit + proxy + projection, pinned snapshots

Exactly the spec's phase 6, unchanged in substance: `permit.Parse` accepts
`remote_mcp` entries only now (fail-closed sequencing held since the old
plan); approval pins a snapshot revision; `/proxy/mcp/{serverID}` parses
the JSON-RPC envelope, enumerates methods, enforces the tool allowlist,
forwards SSE with idle-timeout semantics via the shared dialer;
`internalapi.projectTools` joins pinned revisions to project
`mcp_{shortid}__{tool}`; unknown tools 403; every rendering surface labels
MCP tools unverified.

### 9. Frontend divergences flagged, not silently coded

- `missing_scopes` on the PUT response has no field in the seam's
  `VerifyResult` — additive; the wire-up maps it to the card's warning copy.
- `registerMcpServer` returns only `VerifyResult`; the server returns the
  created row (id included) — additive.
- Connection state: `missing`/`ok`/`needs_reauth` derive from today's
  catalog join (`connected` + `status`) — no new state machinery needed;
  the seam's overlay simply retires at wire-up.

These go in the plan-PR description for the board; none blocks.

## Ordered implementation steps

House style per phase: worktree branch off current `main` → failing tests
first (table-driven; Postgres via `internal/testpg`) → implementation →
`my:polish-core --fix` → full verification
(`go build ./... && go vet ./... && gofmt -l . && go test ./...`, connector
e2e, real `tomte serve` boot) → `my:change-explainer` → PR to `main` → wait
for merge before the next phase branches.

- **PR A — Slack capture + verify.** `catalog`: `Capture` type, validation
  (`verify_op` names an existing same-connector read op; steps non-empty),
  `auth_test` op appended to `defs/slack.json` + guide, baseline updated;
  `internal/captureverify` client (compile-reuse, Bearer injection,
  ok-envelope rule, scope diff); `httpapi.putConnection` widening (`kind`,
  catalog-provider gating, verify-then-store, 422 + `missing_scopes`);
  `GET /v1/catalog` capture serialization; `docs/api/v1.md` connections +
  catalog rows. Tests: ok-envelope matrix (2xx+ok:false fails, non-2xx
  fails, 2xx+ok:true stores), nothing-stored-on-failure, scope-warning
  diff, `llm_api_key` compatibility path untouched, compile-parity with the
  proxy path. No migration.
- **PR B — LLM verify + ledger.** Migration 00013 (`spend_entry`);
  `store.MonthSpendCents` widening + `store.RecordSpend`; the verify
  handler + route; provider-layer minimal-call helper; `docs/api/v1.md`.
  Tests: cost recording (priced, zero-cost, unpriced-custom), meter
  integration (`OverCap` sees ledger entries; verify refused over cap),
  candidate-endpoint validation reuse, local-preset no-credential path,
  upstream 401 mapped to `{ok:false}`.
- **PR C — MCP registration.** Migration 00014; `internal/egress`;
  `mcp-servers` endpoints + probe ordering; control-plane MCP client +
  snapshots; catalog extension; `PUT /v1/connections` accepts
  `mcp:{uuid}` rotation. Tests: the spec's full SSRF suite with a fake
  resolver (every denied range, v4-mapped-v6, rebinding pin, redirect
  refusal, TLS-name mismatch), registration no-orphan ordering, snapshot
  immutability + content-hash dedup, cross-tenant isolation.
- **PR D — MCP enforcement.** Permit `remote_mcp` + `AllowsTool` +
  approval-time snapshot pinning; proxy route (envelope, method
  enumeration, allowlist, SSE idle timeout, 64 KiB scan cap); harness
  projection. Tests: enforcement matrix MCP rows, method/batch rejection,
  pinned-revision projection (refresh changes nothing until re-approval),
  e2e against a fake MCP server.

## Testing and verification

The connectors spec's Testing section remains the acceptance checklist for
C/D; each row is assigned to exactly one PR in its description. Phases A/B
add their matrices above. Every PR: full server suite + connector e2e green,
CI green, real `tomte serve` boot; A and B also get a manual first-run pass
against the real web app where the seams exist to exercise.

## Adaptation points

- **Hygiene-pass drift:** every `file:line` here predates the hygiene merge;
  re-verify at branch time. If hygiene renamed or removed a seam this plan
  names, the plan's structure holds and the references update.
- **Migration numbers** (00013/00014) re-verified at each branch time.
- **Slack envelope rule:** if a future curated connector's verify endpoint
  breaks the `ok`-envelope convention, the rule graduates to a per-capture
  declaration (e.g. `verify_ok_field`) — a catalog data change, not a
  reshape.
- **"Proxy path" reading** (decision 5): reverts to a synthetic-identity
  proxy hop if coordination rules the spec literal.
- **Phase 3 (options client):** unscheduled; slots between B and C or after
  D if the build lane asks — its old-plan design (session-authed
  compile-reuse, `effect: write` rejected) is unchanged by anything here.

## Explicit exclusions

The build conversation (P3); grading/alerting on `connection.broken` (P4);
the options client (on demand only); MCP registry vendor data and any hosted
`manifest_url` (board open questions); OAuth in any form; MCP
resources/prompts/sampling/elicitation; webhook triggers; rate limiting;
`web/` wire-up of these APIs (frontend lane, currently idle); packaging
(`app/`, paused); anything under `tomtectl/`.

# Connector Catalog — Implementation Plan

**Status:** Awaiting coordination review before execution
**Date:** 2026-08-31
**Author:** gambtho
**Spec:** [`2026-08-30-nightshift-connectors-design.md`](../specs/2026-08-30-nightshift-connectors-design.md)
**Settled inputs:** build-conversation spec (merged, PR #14) server/ items 4–5
resolve the constraint-elicitation open question — a session-authed
control-plane connector client sharing the proxy's compile-and-inject path,
never accepting run tokens, rejecting `effect: write`; `options_from` on
constraint bindings; `key_hint` on MCP registry entries. Built to, not
reopened.

## Intended outcome

The spec's six subsystems land as seven independently reviewable PRs, ordered
so that (a) each PR is enforceable on its own — no phase grants permit reach a
later phase would have to make real, and (b) the build-conversation lane's
server dependencies (its checklist items 4, 5, 7, 8) unblock as early as the
dependency graph allows, because the frontend lane is downstream of them.

## Phase breakdown

| #   | PR                      | Contents                                                                                                                                                                                                                                                                                                                                                                                                                                                  | Unblocks                                      |
| --- | ----------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------- |
| 0   | serve deadlock fix      | Redact handler over explicit `slog.NewTextHandler`. **Shipped: PR #24.**                                                                                                                                                                                                                                                                                                                                                                                  | everyone                                      |
| 1   | Enforcement core        | `internal/catalog` (embedded JSON, startup validation, the universal three), permit `connections` for `kind: http` only + `AllowsOp` + `ValidateConnections`, `/proxy/connector/{connector}/{op}` gateway (schema validation, constraint bindings, compile-and-inject, header allowlist, path-arg encoding), `HookRequest.Connector/Op`, `GET /v1/catalog` (curated slice), approval-time run-provider cross-check, `cmd/catalog-gate` narrow-only differ | build lane discovery; everything below        |
| 2   | Vault OAuth             | Migration 00011 (kind CHECK widens, `metadata`, `status`, `epoch`), connect flow (`start` + public callback, signed state with front-end return path — build item 7), refresh (singleflight + advisory lock + epoch CAS), `needs_reauth`, `connection.broken`, revocation                                                                                                                                                                                 | credentialed curated ops; build item 7        |
| 3   | Options client          | Control-plane connector client for `options_from` (build item 4, settled semantics), `options_from` on catalog constraint bindings (build item 5, curated half)                                                                                                                                                                                                                                                                                           | build-conversation `select` elicitation       |
| 4   | Harness tool loop       | `llm.Message.IsError` + provider mapping, run-context `tools` projection (server-derived), harness `ChatTurn` loop (`{connector}__{op}`, parallel dispatch, 60 s per-tool, 20-turn cap), `tool.call.*` events, `provider_tool_unsupported`, curated e2e vs fake Slack                                                                                                                                                                                     | runs that actually use connectors             |
| 5   | Remote MCP registration | Migration 00012 (`mcp_server`, immutable snapshot revisions), canonicalization, the hardened dialer + full SSRF defense set, registration probe ordering, control-plane MCP client + `refresh-tools` (build item 8), registry + `key_hint` (build item 5, MCP half), `api_key` capture/rotation, `/v1/catalog` MCP extension                                                                                                                              | MCP servers visible to the build conversation |
| 6   | Remote MCP enforcement  | `/proxy/mcp/{serverID}` (envelope parsing, method enumeration, tool allowlist, SSE/session forwarding), permit `remote_mcp` entries + `AllowsTool` + snapshot pinning, harness `mcp_{shortid}__{tool}` projection from pinned revisions                                                                                                                                                                                                                   | the pluggable fourth connector, end to end    |

Deviations from the starting decomposition given to this lane:

- **The discovery API moves from last (phase 5) into phases 1 and 5.** The
  curated `GET /v1/catalog` slice is nearly free once the catalog package
  exists, and the frontend/build lane — 10 of 11 checklist items stalled — is
  downstream of it. Holding it to the end starves the dependent lane for no
  engineering reason.
- **Remote MCP splits in two** (registration/discovery vs. proxy
  enforcement/permit). Together they are the largest subsystem plus the entire
  SSRF surface; one PR would be unreviewable at the standard the surface
  deserves. The split line is clean: phase 5 is control-plane only (session
  auth), phase 6 is the run path (run tokens).
- **The options client is its own small PR** (phase 3) rather than folded into
  OAuth: settled semantics, distinct reviewers' concerns (session-vs-run auth
  boundary), and it can slot between larger phases.

## Evidence and constraints

- `permit.Parse` rejects any non-empty `connections`
  (`server/internal/permit/permit.go:56`); `AllowsProvider` is the only
  enforcement method. All connector permits are impossible until phase 1.
- Proxy extension seams exist as designed: narrow `Deps` interfaces and the
  one-exact-tuple route posture (`server/internal/proxy/proxy.go`,
  `handler.go:84`). `CredentialSource.Credential(ctx, tenant, name, provider)`
  survives unchanged (`server/internal/proxyadapter/adapter.go`).
- Vault schema: `kind` CHECK is `('llm_api_key')` only, no
  `metadata`/`status`/epoch (`00006_vault.sql`); `store.UpsertConnection`
  already takes `kind`. Migrations run through `00010_steps_v1.sql`; new ones
  start at 00011.
- `llm.ToolCapableProvider.ChatTurn` is ported and tested for anthropic and
  openai, but tool results hard-code `is_error: false`
  (`server/internal/llm/anthropic.go:122`) and `llm.Message` has no error
  field — the `IsError` interface change is confirmed real, not optional.
- `harness.Steps` is the tool-less compiled shape
  (`server/internal/harness/harness.go:31`); the run context that serves it is
  `internalapi.runContext`. The `tools` array is a projection change there
  plus a store join, exactly as the spec says.
- `approveVersion` checks `llm.Priced(provider, model)` but never
  `p.AllowsProvider(provider)` for the platform run model
  (`server/internal/httpapi/workflows.go:205-232`) — the gap steps v1 left.
- New mutating routes take the `mut(auth(...))` wrapper
  (`server/internal/httpapi/httpapi.go:56-87`); the OAuth callback is a GET
  outside it; tests mint session rows directly.
- `store.ApproveVersion` takes trailing compiled `json.RawMessage`; a DB CHECK
  enforces approved ⇒ compiled object. House tx pattern for multi-statement
  writes: `store.CreateWorkflow` (`workflow.go:60-88`).
- **There is no CI in this repository** (no `.github/`). The spec's
  "narrow-only rule enforced by CI" cannot be wired here yet.

## Decisions for review

1. **Approval run-provider gap: closed in phase 1.** `approveVersion` gains a
   400 when the platform run provider is not in the permit's `llm.providers`.
   Safe to close: the proxy already denies such runs at request time
   (`handler.go:77`), so the check only moves an inevitable failure from
   run time to approval time; nothing that works today stops working. The
   error message names both the platform provider and the permit's list.
2. **Fail-closed phase sequencing for permit reach.** Phase 1's
   `permit.Parse` accepts `kind: http` entries only; `mcp:` keys and
   `kind: remote_mcp` stay rejected until phase 6, where enforcement exists.
   At no commit on main can an approved permit name reach the proxy cannot
   enforce. Same principle gates the API: `PUT /v1/connections` accepts
   `kind: api_key` only from phase 5, though the CHECK widens in 00011.
3. **Catalog narrow-only gate: Go tool + startup baseline check, honestly
   framed.** `server/cmd/catalog-gate` diffs the embedded catalog between two
   git revs and fails on reach-widening edits (host, method, path template,
   removed constraint, loosened schema); it is runnable locally and in
   whatever CI this repo grows. Additionally (raised by the escalation lane,
   whose merged spec's first anti-injection control leans on the narrow-only
   rule): the catalog embeds a **committed baseline snapshot** of itself, and
   startup validation runs the narrow-only diff against it, refusing to boot
   on a widening — converting "nobody ran the tool" into "the binary won't
   start". Stated plainly: the baseline updates in the same PR that edits the
   catalog, so this is **tamper-evident, not tamper-proof** — a widening
   becomes a deliberate, reviewable two-file edit instead of a silent one.
   Only a PR-time CI diff against the merge base closes it fully; until CI
   exists, an unenforced narrow-only rule weakens a documented security
   control in the escalation design, and the board entry says so.
4. **Gmail scopes are late-binding data.** Catalog scope sets are JSON; the
   CASA decision (full read vs. `gmail.metadata`) changes catalog data and
   consent copy, not code. Hard deadline: **phase 2 merge** — the first time
   the connect flow requests Google scopes. Phase 1 ships the `google-gmail`
   ops with their scope sets marked pending that decision; the decision is
   owed by coordination (asked, not decided here).
5. **One migration per phase that needs one.** 00011 (vault widening) in
   phase 2, 00012 (`mcp_server` + snapshots) in phase 5. No speculative
   columns ahead of their consumer except the kind CHECK widening, which is a
   single list edit.
6. **The options client reuses the gateway's compile-and-inject internals as
   a package-level function**, not a second HTTP hop through the proxy: same
   code path (spec requirement), different auth front door (session, never
   run tokens), `effect: write` rejected before compilation.
7. **Hook per-run-cap consult (build item 10) is explicitly deferred** to the
   Plan 3/4 follow-up lane, per the build spec's own assignment ("items 9–10
   … can land with Plan 3/4 follow-ups"). Item 9 (approval-time connection
   re-validation) lands in phase 2, where connection status exists to check.

## Alternatives rejected

- **Single mega-PR or per-subsystem-order-as-written:** unreviewable;
  enforcement before catalog is meaningless, catalog without enforcement is
  reach without teeth.
- **Discovery API last (starting decomposition):** starves the downstream
  frontend lane; rejected as above.
- **Accepting `remote_mcp` permit entries in phase 1** (schema complete from
  day one): violates fail-closed sequencing; a permit could be approved
  naming tools nothing enforces.
- **Wiring GitHub Actions for the catalog gate from this lane:** `.github/`
  is repo-global and unowned; flagged to coordination instead.

## Ordered implementation steps

Within each phase: worktree branch → failing tests first for enforcement
behavior (house style: table-driven, testcontainers via `internal/testpg`) →
implementation → `my:polish-core --fix` → full `server/` verification
(`go build ./... && go vet ./... && gofmt -l . && go test ./...`) →
`my:change-explainer` → PR. Each PR message names its spec section and its
slice of the spec's testing matrix.

- **Phase 1** — `internal/catalog` types + embedded defs + validation +
  baseline snapshot with startup narrow-only check; permit changes; proxy
  `connector.go` (authorize/compile/inject/forward); `httpapi` catalog
  handler; approval check; `cmd/catalog-gate`. Tests: the enforcement matrix
  (curated rows), compilation hygiene rows, catalog validation rows
  (including boot refusal on a widened baseline), approval-gap regression.
- **Phase 2** — migration 00011; `store` connection widening (metadata,
  status, epoch, advisory-lock helpers); `httpapi` oauth start/callback;
  refresh in the proxy credential path; revocation. Tests: the spec's OAuth
  matrix (refresh race, stale-401 CAS, revoke-mid-refresh, needs_reauth
  surfacing).
- **Phase 3** — `internal/catalog` `options_from`; control-plane options
  endpoint + client. Tests: write-op rejection, run-token rejection,
  cross-tenant 404, option-value exactness.
- **Phase 4** — `llm` IsError + adapters; `internalapi` tools projection;
  `harness` loop + client; e2e. Tests: loop unit tests against `llmtest`,
  IsError mapping fixtures, the curated e2e.
- **Phase 5** — migration 00012; canonicalization + hardened dialer
  (`internal/egress` or similar, shared); `mcp-servers` endpoints + probe;
  control-plane MCP client + snapshots; registry. Tests: the full SSRF suite
  with fake resolver; registration ordering (no orphaned state); discovery
  isolation.
- **Phase 6** — proxy MCP route; permit `remote_mcp`; harness MCP tool
  projection. Tests: enforcement matrix (MCP rows), envelope/method
  enumeration, snapshot pinning.

## Testing and verification

Per phase as above; the spec's Testing section is the acceptance checklist,
each row assigned to exactly one phase (no row unassigned; assignment recorded
in each PR description). Full `server/` suite green before every PR; `serve`
smoke against local Postgres for phases touching `main.go` wiring.

## Adaptation points

- If the CASA decision lands as `gmail.metadata`-first, phase 1's Gmail op
  set may shrink (an op whose scope is unavailable should not ship as
  approvable copy).
- If the build lane needs `GET /v1/catalog` response-shape changes for the
  verdict's computed access block, phase 1's handler adapts — copy
  verification is build item 5's second half and lands there.
- If MCP auth breadth (OAuth discovery) becomes needed for a registry entry,
  phase 5 grows the connect-flow extension the spec sketches; the vault
  bundle shape already fits.

## Explicit exclusions

The build conversation itself (resource, agent loop, verdict — build items
2, 3, 6 belong to that lane; this lane only unblocks them); alerting on
`connection.broken` (Plan 4); metering tool calls and per-run cap consult
(Plan 3/4 follow-ups, build items 9's meter half and 10); BYO OAuth app
(designed-for seam only); MCP resources/prompts/sampling/elicitation;
webhook triggers; rate limiting; key rotation jobs; CI wiring for the
catalog gate; anything under `web/` or `src/`.

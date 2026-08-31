# Nightshift Connector Catalog — Design

**Status:** Design approved in conversation; spec awaiting review
**Date:** 2026-08-30
**Author:** gambtho
**Parent:** [`2026-08-30-nightshift-platform-design.md`](./2026-08-30-nightshift-platform-design.md).
This is the "connector catalog" spec both parent specs name as the top product
risk. It extends
[`2026-08-30-nightshift-egress-proxy-design.md`](./2026-08-30-nightshift-egress-proxy-design.md):
it defines the entries of permit v1's reserved `connections` map, owns the
user-controlled-destination security surface that spec explicitly scoped out,
adds OAuth to the vault (whose `kind` column and per-provider uniqueness
anticipate it), and designs the MCP transport rework the
[roadmap](../plans/2026-08-30-nightshift-platform-roadmap.md) assigns here.
The reference survey of CronFoundry's `internal/mcp` (stdio-subprocess-only,
no permission model) informs what is harvested and what is replaced.

## Scope decisions

| Decision                                                  | Why                                                                                                                                                                                                                                                                                                                                             |
| --------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Hybrid connector model from day one**                   | Curated `http` connectors carry the universal three (inbox, calendar, chat) with full per-operation enforcement; a `remote-mcp` kind answers the pluggable system-of-record without us authoring every connector. The permit degrades honestly, per kind, and the approval diagram says so.                                                     |
| **Per-tool allowlist for remote MCP**                     | MCP over streamable HTTP puts the tool name inside the JSON-RPC body, so the proxy parses the envelope (shallow, size-capped, fail-closed) and enforces a tool-name allowlist. Per-server-only grants would let a vendor adding a dangerous tool silently widen reach; read/write classification of vendor tools is unknowable and not claimed. |
| **Platform OAuth apps, BYO designed-for**                 | The target user will not register an OAuth app. Nightshift owns one app per provider (and the Google CASA verification burden that follows); the schema and credential-resolution path carry an optional per-tenant client override from day one so enterprise BYO-app lands later without migration. Only the platform path is built.          |
| **MCP registry + custom URLs**                            | A curated registry of known vendor MCP endpoints gives one-click adds; tenant-typed custom URLs keep "pluggable" genuine (including self-hosted) and activate the full SSRF defense set this spec designs. Registry entries get the identical defenses — trust does not fork the code path.                                                     |
| **Resource-level constraints via declarative extractors** | The UX spec's flagship permit example — "only our support channel" — lives in a request body, not a URL. Curated write operations may declare a constraint binding (an argument field checked against a permit resource list, exact match only); the proxy enforces it. Without this the diagram's best promise is unenforced.                  |
| **Op-invocation gateway, not REST pass-through**          | For curated connectors the harness posts `{args}` naming an operation; the proxy validates, permit-checks, and **compiles** the upstream HTTP request from the catalog binding. Enforcement operates on exactly the vocabulary the approval diagram showed — no path-template matching against model-authored URLs.                             |
| **In-repo declarative catalog, append-only reach**        | Curated definitions are JSON files embedded at build, validated at startup. Operations are append-only; an edit may only narrow or preserve reach — anything wider ships as a new op name, so an approved permit can never silently mean more than it did at approval. No DB catalog, no admin API in v1.                                       |

## The connector model

A **connector** is a catalog entry of one of two kinds.

### Kind `http` — curated, first-party

Authored by us, in the repo. An entry declares:

- **Identity and copy** — id (`slack`), display name, and plain-language
  descriptions written for the approval diagram and the build conversation,
  not for developers. The catalog is where product copy for "I'd need access
  to" lives, written once.
- **Auth** — the OAuth provider (`google`, `slack`) and named scope sets.
  Gmail and Calendar are separate connectors sharing the `google` credential
  namespace, so one consent covers both.
- **Hosts** — fixed upstream hosts, or at most one templated tenant parameter
  (`{workspace}.zendesk.com`) validated against a strict charset
  (`^[a-z0-9][a-z0-9-]{0,62}$`) at connection time. Catalog-authored hosts are
  not user-controlled destinations.
- **Operations** — the atomic unit everywhere. Each has:
  - `name` — stable, unique within the connector.
  - `description` — plain language, surfaced to the LLM and the diagram.
  - `effect` — `read` or `write`; drives the diagram's green/amber.
  - `args_schema` — JSON Schema; this **is** the LLM tool definition.
  - `scopes` — the OAuth scopes this op requires.
  - `binding` — method, path template, and arg placement (path / query /
    JSON body) that the proxy compiles the upstream request from. Every
    path-placed arg is **component-encoded after validation**: percent-encode
    as a single path segment, so `/`, `?`, and `#` cannot alter the compiled
    path or query, and a value that is (or decodes to) `.` or `..` is
    rejected outright — a traversal segment can never reach the upstream
    URL.
  - `constraints` (optional, write ops) — bindings of the form "arg field
    `channel` must appear in the permit's resource list for this op". Exact
    string match only; no predicate language.

**v1 curated set:** `google-gmail`, `google-calendar`, `slack` — the UX
spec's universal three. The pluggable fourth is served by `remote-mcp`; no
fourth curated connector ships in v1.

### Kind `remote-mcp`

An MCP server reached over streamable HTTP, from one of two origins:

- **Registry entry** — vendor endpoints we list (id, canonical URL, auth
  mode). One-click add.
- **Custom URL** — tenant-typed, including self-hosted. Registration runs the
  full canonicalization and probe pipeline below.

Either way the result is a per-tenant `mcp_server` row (id, tenant, canonical
URL, auth mode) plus **immutable discovered-tools snapshot revisions** (see
Discovery) — remote servers are tenant state, not shared catalog content. Its "operations" are whatever `tools/list`
reports; we cannot author or verify them, every rendering surface labels them
as unverified, and the permit treats them at tool-name granularity.

MCP protocol scope: **tools only.** `initialize`, `tools/list`, `tools/call`,
`ping`. Resources, prompts, sampling, and elicitation are out of scope; the
proxy rejects their methods.

### Catalog authoring, versioning, and drift

Curated definitions are declarative JSON under `server/` (embedded via
`go:embed`), validated at startup: unique ids, unique op names, well-formed
schemas, constraint bindings referencing real args, scope sets resolvable.

The stale-permit risk gets a stated policy instead of machinery:

- **Operations are append-only.** Removing an op turns matching permit
  entries into 403s (fail closed, visible in the audit trail) — acceptable,
  never silent widening.
- **An edit may only narrow or preserve an op's reach** (host fix, schema
  tightening, added constraint). Anything that widens reach — new host, new
  method, loosened constraint — ships as a **new op name**, which no approved
  permit contains until a re-approved version lists it.
- **The narrow-only rule is enforced mechanically, not by convention:** a CI
  check diffs the embedded catalog against the previous commit and fails on
  any reach-widening edit to an existing op (host, method, path template,
  removed constraint, loosened arg schema). Policy backed by a gate, matching
  the repo's immutable-version posture.
- The permit records the catalog build version at approval time for audit;
  enforcement always uses the running catalog, which the CI gate guarantees
  is never wider than what was approved.

## Permit `connections` — the reserved map, defined

Permit stays `v: 1`; the egress-proxy spec reserved exactly this. Keys are
connector ids for curated entries and `mcp:{server-uuid}` for remote servers:

```json
{
  "v": 1,
  "llm": { "providers": ["anthropic"], "connection": "default" },
  "connections": {
    "slack": {
      "kind": "http",
      "connection": "default",
      "ops": ["list_channels", "read_messages", "post_message"],
      "resources": { "post_message": { "channel": ["C0123ABC"] } }
    },
    "mcp:7f3a2c10-…": {
      "kind": "remote_mcp",
      "connection": "default",
      "tools": ["search_tickets", "get_ticket"],
      "snapshot": "sha256:9c41…"
    }
  }
}
```

Validation stays in `permit.Parse`, same fail-closed style as v1:

- Unknown fields, unknown kinds, and empty `ops`/`tools` rejected (an entry
  allowing nothing is a mistake, not a deny-all — deny-all is the entry's
  absence).
- `resources` keys must name listed ops; values are non-empty string lists.
- At version-write time, structural checks stay in `permit.Parse` (via
  `httpapi.decodeDoc`), but catalog and ownership checks need the
  authenticated tenant and the store, so they live in a separate
  **`ValidateConnections(ctx, tenantID, permit)`** helper called by both
  `createWorkflow` and `addVersion` after authentication and before
  `CreateWorkflow`/`AddVersion`: every `http` key must exist in the running
  catalog with every listed op present, and every `mcp:` key must name an
  `mcp_server` row **belonging to that tenant** (another tenant's server id
  is `ErrNotFound`, the house pattern — a cross-tenant reference is a test
  case, not an assumption). A permit cannot reference a connector that does
  not exist.
- Each `remote_mcp` entry carries a required `snapshot` — the content hash of
  the discovered-tools snapshot revision the permit was approved against; the
  named revision must exist for that server, and every listed tool must
  appear in it.
- Existing approved permits (`connections` absent or empty) parse unchanged.

`connection` names the vault credential per entry with the same `"default"`
convention as `llm.connection`, resolved per connector namespace (see Vault).

Enforcement API grows two methods beside `AllowsProvider`:
`AllowsOp(connector, op) (Constraints, bool)` and
`AllowsTool(serverID, tool) bool`.

## Proxy enforcement

Two new route families reuse the existing pipeline verbatim — authenticate
run token → resolve permit per request, no cache → `Hook.Before` → inject →
forward streaming → append event. Fail closed at every step.

```text
/proxy/connector/{connector}/{op}     curated op invocation (POST, JSON args)
/proxy/mcp/{serverID}                 remote MCP pass-through (JSON-RPC)
```

The MCP route carries **no path remainder**: the proxy forwards every request
to the registered server's exact canonical endpoint URL, mirroring the LLM
routes' one-exact-tuple posture (`server/internal/proxy/handler.go:82-89`). A
harness cannot steer a request to another path on the approved origin.

Differences from the LLM routes:

- **Auth.** The run token rides in a plain `Authorization: Bearer` — no SDK
  forces a provider-native slot here. Same `AuthSource.VerifyRunToken`, same
  active-run and cleared-hash semantics.
- **Authorize, curated.** Connector present in permit → op listed → args
  parse against `args_schema` → each constraint binding's extracted value ∈
  the permit's resource list. Any miss → 403 + `proxy.denied` naming
  connector, op, and the failing check. Denials are audit content: they are
  what lets a future alert name the boundary that was hit.
- **Compile and inject, curated.** The proxy builds the upstream request from
  the op's binding (method, path template, arg placement), attaches the
  credential per the connector's auth spec (OAuth bearer; per-connector
  header placement replaces the current hard-coded provider switch in
  `forward`), and forwards. The upstream request's headers are **constructed
  from an allowlist, not forwarded**: the proxy sets what the binding needs
  (content type, accept, the injected credential) and drops every inbound
  header — cookies, auth headers, and anything harness-supplied never reach
  the upstream, so an inbound header can never conflict with or override the
  injected credential. The harness never constructs upstream URLs.
- **Authorize, remote MCP.** The JSON-RPC envelope is parsed under a 64 KiB
  scan cap: batch requests rejected, unparseable envelopes rejected. Allowed
  methods are an **exhaustive enumeration** — `initialize`, `tools/list`,
  `tools/call`, `ping`, `notifications/initialized`,
  `notifications/cancelled` — and nothing else; there is no
  `notifications/*` wildcard, so a vendor-defined notification with side
  effects is 403 like any other unknown method. `tools/call` additionally
  requires `params.name` in the permit's tool allowlist. The supported
  transport is pinned: **streamable HTTP only** (MCP protocol revision
  `2025-06-18`; the deprecated HTTP+SSE transport is not supported). HTTP
  methods are enumerated like everything else — `POST` carries JSON-RPC and
  gets the envelope checks above; `GET` opens the server-initiated SSE
  stream (no envelope, permitted as-is); `DELETE` terminates the session;
  any other method is 405. `Mcp-Session-Id` and SSE responses are forwarded
  untouched — the proxy parses the envelope for enforcement but is not a
  stateful MCP participant.
- **Hook widening.** `HookRequest` gains optional `Connector`/`Op` fields
  (additive), so Plan 3 metering can price tool calls without reopening the
  proxy. `Provider` stays for LLM routes.
- **Events.** `proxy.request` / `proxy.denied` / `proxy.error` gain
  connector/op (or server/tool) fields — still never bodies, never headers.

## User-controlled destinations — the deeded security surface

Applies to remote MCP URLs at three touch points: registration, build-time
discovery, and every proxied request. All three share **one hardened dialer**;
registry entries get the identical treatment.

- **Canonicalization at registration.** HTTPS only; port 443 or an explicit
  port ≥ 1024; no userinfo, no fragment; hostname lowercased, IDN punycoded,
  trailing dot stripped; **IP-literal hosts rejected outright**. The
  canonical form is what is stored and what the permit means.
- **Resolution filtering and pinning, per connection attempt.** The proxy
  resolves via its own resolver and filters every A/AAAA answer against the
  denied set — loopback, RFC 1918, link-local (`169.254.0.0/16`,
  `fe80::/10`), CGNAT (`100.64.0.0/10`), unique-local, multicast,
  unspecified, cloud metadata (`169.254.169.254` and its v6 mapping), and the
  **v4-mapped-v6 forms of all of these**. A vetted IP is pinned into
  `DialContext` and dialed directly, so a rebinding TTL flip between check
  and connect has nothing to flip. Every request re-resolves and re-vets;
  nothing caches an approval of an IP. The dialer lives in an **explicit
  `http.Transport` with proxy selection disabled** (`Proxy: nil`) — never
  `http.DefaultTransport` — so `HTTP_PROXY`/`HTTPS_PROXY` environment
  variables cannot route a vetted, pinned dial through an unvetted
  intermediary.
- **Redirects.** Nothing follows them. The proxy relays a 3xx verbatim —
  the injected credential was attached only to the original vetted request
  (same posture as the LLM routes) — and the **control-plane discovery
  client refuses redirects outright** (`CheckRedirect` returns an error),
  since it is a real HTTP client that would otherwise chase absolute or
  relative targets past the vetting. If the harness's MCP client follows a
  relayed 3xx, the new request can only re-enter the proxy and is vetted
  from scratch; a redirect target that is not a registered server has no
  route at all.
- **TLS.** Verified against system roots **for the canonical hostname** —
  SNI and SAN checks use the registered name even though the dial is by
  pinned IP, so a vetted address serving a different certificate fails.
- **Response hygiene.** All timers are **proxy-owned and composed**: a
  connect/TLS timeout and response-header timeout on the transport, a
  per-request deadline for ordinary responses, and an idle timeout (time
  since last event) instead of a total deadline for SSE. The harness's
  per-tool timeout rides the request context, and **downstream cancellation
  propagates**: when the client goes away or a timer fires, the upstream
  request context is cancelled and its response body closed — a hostile
  server cannot hold a run's resources open past its caller.
- The in-cluster denies (this platform's own control plane, proxy, and
  actor addresses) are covered by the private-range filters plus
  NetworkPolicy under Plan 5; the conformance test there gains a case: an
  actor registering its own service address as an MCP URL must fail.

Curated `http` connectors are outside this surface — their hosts are catalog
constants (the `{workspace}` template is charset-validated, dot-free, and
interpolated into a fixed suffix, so it cannot change the registrable
domain).

## Vault — OAuth and connector credentials

### Schema (migration; pre-release, widen in place)

- `kind` CHECK widens: `('llm_api_key', 'oauth', 'api_key')`.
  - `oauth` — ciphertext holds one encrypted JSON bundle: access token,
    refresh token, expiry, granted scopes.
  - `api_key` — static bearer/header secrets for remote MCP servers.
- New columns: `metadata jsonb` (non-secret: granted scopes, account label,
  provider hints — what `GET /v1/connections` may show) and
  `status text NOT NULL DEFAULT 'ok'` (`ok` | `needs_reauth`).
- The `provider` column becomes the **credential namespace**: for curated
  connectors, the connector's auth provider (`google`, `slack`); for remote
  MCP, `mcp:{server-uuid}`. The `(tenant_id, provider, name)` unique key and
  the `"default"` resolution convention carry over unchanged — which is why
  `CredentialSource.Credential(ctx, tenantID, name, provider)` survives with
  its signature intact.

### Capturing static secrets (`api_key`)

OAuth is not the only capture path. The existing write endpoint hard-codes
`kind = "llm_api_key"` (`server/internal/httpapi/connections.go:29-61`), so it
**widens**: `PUT /v1/connections/{name}` gains an optional `kind` in the body
(default `llm_api_key`, for compatibility) and accepts `api_key` with a
`provider` of `mcp:{server-uuid}` — this is how a remote MCP server's bearer
secret is **rotated**. Initial capture rides registration: `POST
/v1/mcp-servers` carries the secret for `auth: api_key` servers and stores it
before the probe (see Discovery), so a registered server always has its
credential. Connection
identity is **always provider-qualified** — the unique key is
`(tenant_id, provider, name)` and every route that names a connection takes
the provider (the existing `DELETE …?provider=` shape), so `{name}` alone is
never ambiguous.

### Connect flow (platform apps)

- `POST /v1/connections/oauth/{connector}/start` (session-authed) → provider
  auth URL. `state` is a signed, expiring nonce carrying tenant, user,
  connector, and requested scopes.
- One public callback route exchanges the code and writes the bundle;
  `redirect_uri` comes from deployment config.
- **Requested scopes** are the union of the scopes needed by the ops being
  granted plus whatever the connection already holds. A later workflow
  needing more triggers **re-consent on the same shared connection**
  (incremental auth where the provider supports it). This re-consent trigger
  is the concrete landing point for the UX spec's deferred "credential
  capture UX"; this spec defines the trigger and API, the UX owns the screen.
- **BYO app, designed for and not built:** client credentials resolve through
  a lookup that consults an optional per-tenant OAuth-client record before
  the platform's env-configured app. v1 builds only the platform path; the
  resolution seam and the schema slot are the design commitment.

### Refresh

At injection time, if the access token is within a 60-second expiry skew, the
proxy refreshes before forwarding — under a per-connection singleflight plus
a **transaction-scoped** Postgres advisory lock keyed on the connection id,
so concurrent runs (or proxy replicas) do not race a rotating refresh token.
The expiry decision made before the lock is **discarded on acquisition**: the
holder re-reads the connection inside the transaction and re-checks — if a
winner already refreshed, it uses the stored bundle and refreshes nothing; if
the row is gone or marked `needs_reauth`, it fails the tool call rather than
retrying with a stale (possibly rotated) refresh token. The new bundle is
persisted in the same transaction that holds the lock, before use. This is the one place the vault gains a **write on the
proxy request path** — stated plainly, extending the parent spec's
decrypt-only claim.

State transitions are guarded by a **credential epoch**: every bundle write
bumps an integer epoch on the connection, and injection records the epoch of
the token it used. Refresh failure, or an upstream 401, marks the connection
`needs_reauth` **only via compare-and-swap on that epoch** — a 401 earned by
stale token A cannot demote a connection already refreshed to token B (the
CAS misses and the request simply retries against the current bundle once).
Deletion takes the same per-connection advisory lock, so a revoke that lands
mid-refresh cannot be followed by the refresh persisting credentials for a
row that no longer exists. When the demotion does apply, the tool call fails
as a **tool-level error** (the model sees it; the run can finish degraded)
and a `connection.broken` run event is appended — the alerting hook for
Plan 4.

### Revocation

`DELETE /v1/connections/{name}?provider=…` (provider-qualified, matching the
existing API shape) calls the provider's revoke endpoint best-effort for
`oauth` kinds, then deletes the row. In-flight runs lose the credential on
their next request — consistent with the parent spec's "nothing outlives the
database check". Provider-side revocation (a user revoking from Google's
security page) surfaces as the 401 → `needs_reauth` path above.

## Discovery — how the build conversation learns what exists

- **`GET /v1/catalog`** (session-authed): curated connectors and their ops
  (names, plain-language descriptions, effect class, constraint slots,
  required scopes), per-tenant connection status for each, and the tenant's
  registered MCP servers with their discovered-tools snapshots. This grounds
  the verdict's "I'd need access to" and the build agent's step proposals.
- **`POST /v1/mcp-servers`** registers a registry or custom URL. Ordering is
  defined so no orphaned state survives a failure: canonicalization first
  (reject before anything persists); then the row is created and, for
  `auth: api_key`, the request-supplied secret is stored in the vault under
  the freshly minted `mcp:{server-uuid}` namespace; then the connect probe
  runs through the hardened dialer **with that credential**. A failed probe
  deletes both the row and the stored secret and returns the error — a
  registration either exists probe-verified with its credential in place, or
  not at all.
- **`POST /v1/mcp-servers/{id}/refresh-tools`** runs `initialize` +
  `tools/list` via a **control-plane MCP client** — session-authed, not the
  run-token proxy path, same hardened dialer — and stores a **new immutable
  snapshot revision** (content-hashed; prior revisions are kept while any
  approved permit pins them). Build-time discovery is not a run and must not
  be reachable with a run token.
- **The permit pins a snapshot revision at approval, not just tool names.**
  Run-context tool projection reads the pinned revision, so a refresh can
  never change the schema or description an approved workflow's model sees —
  adopting a newer revision requires a new approved version, mirroring the
  repo's immutable-version model (`store.VersionDoc`,
  `workflow_version.status`). A vendor adding tools changes nothing until
  re-approval (unknown names are 403 regardless); a vendor removing or
  breaking a tool surfaces as tool-level errors at call time, never as
  silent widening.

## Harness — the tool loop and the transport rework

CronFoundry's **contract** is harvested; its transport is not. What ports:
the runner-side manager interface shape (`Start/Tools/DispatchAll/Shutdown`),
the `ToolUse`/`CallResult`/`FatalError` error split (tool-level errors return
to the model; transport failures are fatal with named kinds), and
`{namespace}__{tool}` flat naming. What is replaced: stdio subprocesses,
newline framing, env-as-auth, arbitrary `command` execution — none of it
exists here. There is no MCP subprocess anywhere; "transport" is HTTP to the
proxy routes.

- The loop drives `llm.ToolCapableProvider.ChatTurn` (already ported and
  tested for anthropic and openai); a permit with connections and a
  non-tool-capable provider fails the run with `provider_tool_unsupported`.
- Tool names: `{connector}__{op}` for curated, `mcp_{shortid}__{tool}` for
  remote. Parallel dispatch within a turn; per-tool timeout 60 s; max turns
  default 20 (per-workflow override is a later feature).
- **`GET /internal/runs/{id}/context` grows a `tools` array**: tool
  definitions **server-derived** from the approved permit joined with the
  catalog (curated `args_schema`) and the permit-pinned snapshot revisions
  (remote tool schemas). The harness stays a dumb executor: it never sees the permit,
  never holds a credential, and cannot grant itself a tool the control plane
  did not project.
- **The provider contract widens to carry tool errors.** Today a tool-result
  message is content plus a tool-use id (`server/internal/llm/provider.go:23-29`),
  and the Anthropic adapter hard-codes `is_error: false`
  (`server/internal/llm/anthropic.go:121-124`) — so "tool-level errors return
  to the model" is an **interface change, not free**: the tool-result message
  gains an explicit `IsError`, mapped to Anthropic's `tool_result.is_error`
  and, for providers with no error flag (OpenAI-shaped APIs), to a stated
  error-prefix convention in the result content. Denials, refresh failures,
  and upstream connector errors all set it; without this they would present
  to the model as successful results.
- Run events mirror the harvested vocabulary: `tool.call.ok` /
  `tool.call.fail` with tool name and duration only — no args, no results.

## Failure handling

Fail closed throughout, extending the parent spec's table: connector or op
absent from permit → 403 + `proxy.denied`; constraint miss → 403 +
`proxy.denied` (names the failing check, not the offending value); malformed
or batch JSON-RPC → 403; tool not allowlisted → 403; MCP URL failing
resolution vetting → connection refused with `proxy.denied`; vault decrypt or
refresh failure → tool-level error + `connection.broken` + `needs_reauth`;
upstream connector errors pass through as tool results (the model decides
what to do), while transport-level failures to reach the proxy are fatal to
the run. Unknown/finalized run tokens → 401, unchanged.

## Testing

- **Enforcement matrix, per kind:** op not in permit; constraint miss;
  unknown connector; args failing schema; tool not in allowlist; batch and
  oversized envelopes; any MCP method outside the enumerated set (including
  vendor-defined notifications) — every one 403 + a recorded `proxy.denied`;
  and the MCP route reaches only the registered endpoint URL regardless of
  request path.
- **Compilation hygiene:** path args containing `/`, `?`, `#`, or `..`
  arrive upstream component-encoded or rejected — never as extra path
  segments; inbound requests carrying cookies or credential-shaped headers
  reach the upstream with only allowlisted headers and the injected
  credential winning.
- **Snapshot pinning:** after a `refresh-tools` that changes a tool's schema,
  a run fired from a previously approved version still projects the pinned
  revision's schemas; adopting the new revision requires a re-approved
  version.
- **SSRF suite** with a fake resolver: rebinding flip between check and dial
  (must be inert — the dial is pinned); metadata IP; v4-mapped v6 private
  ranges; IP-literal registration; redirect relayed not followed by the
  proxy, and the discovery client refusing both absolute and relative
  redirect targets; TLS name mismatch against a vetted IP; a pinned dial
  ignoring `HTTP_PROXY`/`HTTPS_PROXY` set in the environment; a client abort
  mid-SSE cancelling the upstream request and closing its body.
- **OAuth:** refresh race under concurrent runs (singleflight + advisory
  lock: one refresh, both proceed); expired-token refresh persisted before
  use; refresh failure marks `needs_reauth` and the API shows it; a stale
  401 (earned by an epoch already superseded) does **not** demote the
  connection; a revoke landing mid-refresh leaves neither row nor secret
  behind; revocation kills the credential for in-flight runs on their next
  request.
- **Discovery isolation:** the control-plane MCP client's endpoints reject
  run tokens; `refresh-tools` for another tenant's server is `ErrNotFound`
  (the house pattern).
- **Catalog:** startup validation rejects duplicate ops, dangling constraint
  bindings, malformed schemas; version-write rejects permits naming unknown
  connectors/ops, and a permit naming **another tenant's** `mcp_server` is
  rejected as `ErrNotFound` (cross-tenant reference test).
- **e2e:** a run against a fake Slack upstream completes a read op and a
  channel-constrained write op with zero credentials in the harness, and a
  write to an unlisted channel is denied and audited.

## Explicitly out of scope

The build conversation itself (UX spec's successor owns the screens; this
spec provides `GET /v1/catalog` and the re-consent trigger); alerting on
`connection.broken` (Plan 4); metering tool calls (Plan 3, via the widened
`Hook`); BYO OAuth app implementation (designed-for only); MCP resources,
prompts, sampling, elicitation; webhook/event triggers (the UX spec's
non-users); per-connector rate limiting; additional curated connectors
beyond the universal three; key/KEK rotation jobs (unchanged from parent).

## Open questions

- **Google CASA timing.** Gmail restricted scopes require a security
  assessment of the platform app owner. Lead time is months; whether v1
  launches with full Gmail read scopes or starts with `gmail.metadata` +
  narrower scopes is a business decision that should be taken before the
  implementation plan, not during it.
- **MCP auth breadth.** v1 supports static `api_key` bearer and unauthenticated
  servers. The MCP authorization spec (OAuth discovery via protected-resource
  metadata) is where vendor servers are heading; when a registry entry needs
  it, the connect flow above extends — the vault bundle shape already fits.
- **Constraint elicitation.** The build conversation must learn channel IDs
  (etc.) to fill `resources` — likely by calling read ops (`list_channels`)
  during build with the user's session. Whether build-time op invocation is
  session-authed through a dedicated path or deferred until first run is a
  UX-spec question this spec leaves open; the proxy only ever accepts run
  tokens.
- **Snapshot staleness UX.** Discovered-tools snapshots age; when the build
  conversation should auto-refresh versus warn is a product call.

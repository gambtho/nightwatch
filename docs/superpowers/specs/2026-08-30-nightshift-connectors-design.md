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
| **Per-tool allowlist for remote MCP**                     | MCP over streamable HTTP puts the tool name inside the JSON-RPC body, so the proxy parses the envelope (shallow, size-capped, fail closed) and enforces a tool-name allowlist. Per-server-only grants would let a vendor adding a dangerous tool silently widen reach; read/write classification of vendor tools is unknowable and not claimed. |
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
    JSON body) that the proxy compiles the upstream request from.
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
URL, auth mode, discovered-tools snapshot) — remote servers are tenant state,
not shared catalog content. Its "operations" are whatever `tools/list`
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
- The permit records the catalog build version at approval time for audit;
  enforcement always uses the running catalog.

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
      "tools": ["search_tickets", "get_ticket"]
    }
  }
}
```

Validation stays in `permit.Parse`, same fail-closed style as v1:

- Unknown fields, unknown kinds, and empty `ops`/`tools` rejected (an entry
  allowing nothing is a mistake, not a deny-all — deny-all is the entry's
  absence).
- `resources` keys must name listed ops; values are non-empty string lists.
- At version-write time (`httpapi.decodeDoc`) every `http` key must exist in
  the running catalog with every listed op present, and every `mcp:` key must
  name an `mcp_server` row belonging to the tenant. A permit cannot reference
  a connector that does not exist.
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

```
/proxy/connector/{connector}/{op}     curated op invocation (POST, JSON args)
/proxy/mcp/{serverID}/{path...}       remote MCP pass-through (JSON-RPC)
```

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
  `forward`), and forwards. The harness never constructs upstream URLs.
- **Authorize, remote MCP.** The JSON-RPC envelope is parsed under a 64 KiB
  scan cap: batch requests rejected, unparseable envelopes rejected,
  non-tool methods rejected. `initialize`, `tools/list`, `ping`, and client
  `notifications/*` pass; `tools/call` requires `params.name` in the permit's
  tool allowlist. The proxy forwards `Mcp-Session-Id` and SSE responses
  untouched — it parses the envelope for enforcement but is not a stateful
  MCP participant.
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
  nothing caches an approval of an IP.
- **Redirects.** The proxy never follows them — a 3xx is relayed verbatim
  and the injected credential was attached only to the original vetted
  request (same posture as the LLM routes). If the harness's MCP client
  follows one, the new request can only re-enter the proxy and is vetted
  from scratch; a redirect target that is not a registered server has no
  route at all.
- **TLS.** Verified against system roots **for the canonical hostname** —
  SNI and SAN checks use the registered name even though the dial is by
  pinned IP, so a vetted address serving a different certificate fails.
- **Response hygiene.** Per-request timeout, response size cap, and an SSE
  idle timeout, so a hostile server cannot hold a run's resources open
  indefinitely.
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
a Postgres advisory lock keyed on the connection id, so concurrent runs (or
proxy replicas) do not race a rotating refresh token. The new bundle is
persisted before use. This is the one place the vault gains a **write on the
proxy request path** — stated plainly, extending the parent spec's
decrypt-only claim. Refresh failure, or an upstream 401 on a request, marks
the connection `needs_reauth`, fails the tool call as a **tool-level error**
(the model sees it; the run can finish degraded), and appends a
`connection.broken` run event — the alerting hook for Plan 4.

### Revocation

`DELETE /v1/connections/{name}` calls the provider's revoke endpoint
best-effort, then deletes the row. In-flight runs lose the credential on
their next request — consistent with the parent spec's "nothing outlives the
database check". Provider-side revocation (a user revoking from Google's
security page) surfaces as the 401 → `needs_reauth` path above.

## Discovery — how the build conversation learns what exists

- **`GET /v1/catalog`** (session-authed): curated connectors and their ops
  (names, plain-language descriptions, effect class, constraint slots,
  required scopes), per-tenant connection status for each, and the tenant's
  registered MCP servers with their discovered-tools snapshots. This grounds
  the verdict's "I'd need access to" and the build agent's step proposals.
- **`POST /v1/mcp-servers`** registers a registry or custom URL — running
  canonicalization and a connect probe through the hardened dialer before
  the row exists.
- **`POST /v1/mcp-servers/{id}/refresh-tools`** runs `initialize` +
  `tools/list` via a **control-plane MCP client** — session-authed, not the
  run-token proxy path, same hardened dialer — and stores the snapshot.
  Build-time discovery is not a run and must not be reachable with a run
  token.
- The permit pins tool names at approval. A vendor adding tools changes
  nothing until a new version is approved (unknown names are 403 regardless);
  a vendor removing a tool surfaces as tool-level errors, never as silent
  widening.

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
  catalog (curated `args_schema`) and the MCP snapshots (remote tool
  schemas). The harness stays a dumb executor: it never sees the permit,
  never holds a credential, and cannot grant itself a tool the control plane
  did not project.
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
  oversized envelopes; non-tool MCP methods — every one 403 + a recorded
  `proxy.denied`.
- **SSRF suite** with a fake resolver: rebinding flip between check and dial
  (must be inert — the dial is pinned); metadata IP; v4-mapped v6 private
  ranges; IP-literal registration; redirect relayed not followed; TLS name
  mismatch against a vetted IP.
- **OAuth:** refresh race under concurrent runs (singleflight + advisory
  lock: one refresh, both proceed); expired-token refresh persisted before
  use; refresh failure marks `needs_reauth` and the API shows it; revocation
  kills the credential for in-flight runs on their next request.
- **Discovery isolation:** the control-plane MCP client's endpoints reject
  run tokens; `refresh-tools` for another tenant's server is `ErrNotFound`
  (the house pattern).
- **Catalog:** startup validation rejects duplicate ops, dangling constraint
  bindings, malformed schemas; version-write rejects permits naming unknown
  connectors/ops.
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

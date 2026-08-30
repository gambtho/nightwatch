# Nightshift Egress Proxy + Credential Vault — Design

**Status:** Design approved in conversation; spec awaiting review
**Date:** 2026-08-30
**Author:** gambtho
**Parent:** [`2026-08-30-nightshift-platform-design.md`](./2026-08-30-nightshift-platform-design.md) — this designs governance
primitives 1 (permit enforcement) and 5 (credential vaulting), Plan 2 of the
[roadmap](../plans/2026-08-30-nightshift-platform-roadmap.md). It resolves the
roadmap's "egress proxy design detail" prerequisite (actor authentication,
canonicalization, TLS handling, proof of no direct egress).

## Scope decisions

| Decision                                            | Why                                                                                                                                                                                                                                                                                                                                                                                            |
| --------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **LLM provider traffic is the first governed flow** | Connectors don't exist yet, so LLM calls are the only real egress. Governing them exercises the full permit + injection path with production traffic, and moves API keys out of the sandbox today. Connector rules slot into the same permit later.                                                                                                                                            |
| **Vault now, keys first**                           | The per-tenant-DEK vault is built in Plan 2 with LLM API keys as its first contents. This makes credential injection real (not static config) and enables per-tenant BYO keys. OAuth refresh/revocation arrive with connectors.                                                                                                                                                                |
| **Grow-in-place gateway, designed for the split**   | The proxy is a package (`server/internal/proxy`) mounted in the `nightshift` binary, behind narrow interfaces, importing neither `httpapi` nor `internalapi`. Splitting it into a standalone service is a Plan 5 deployment change, not a redesign. A standalone service today would front-load service-to-service auth and a second deploy surface before any sandbox exists to justify them. |
| **Gateway semantics, not a forward proxy**          | Per the parent spec: the actor sends a request naming its destination class; the proxy substitutes the real credential at egress. No CONNECT tunneling, no TLS interception, no CA inside the sandbox. Actor→proxy is in-cluster traffic; proxy→provider is ordinary verified TLS.                                                                                                             |

## Enforcement posture — stated plainly

The permit's guarantee depends on the compute backend, and the product must
never claim more than the backend delivers:

- **Local compute (now):** the harness shares the control plane's process and
  network, so the proxy is **policy-complete but advisory** — a hostile harness
  could bypass it. What Plan 2 genuinely delivers on Local: credentials leave
  the harness (no key in `Input`, none in actor memory), and every LLM call is
  permit-checked and audit-logged.
- **Substrate / Kubernetes compute (Plan 5):** default-deny egress
  NetworkPolicy with the proxy as the sole permitted destination makes this
  same proxy **the guarantee**. Nothing in the proxy changes at that point —
  only the network around it. "No direct egress" is proven by the NetworkPolicy
  plus a conformance test that attempts a direct connection from inside an
  actor and must fail.
- **The internal run-record channel gets a concrete route now**:
  `/proxy/internal/{path...}` is an always-allowed pass-through to the
  internal API, authenticated by the same run token (forwarded as the
  bearer the internal API already expects). On Local the harness continues
  to call the internal API directly; under Plan 5 the harness base URL for
  its client flips to the proxy route, and the NetworkPolicy model is
  exactly one allowed destination — the proxy. Without this route,
  "sole allowed destination" would be an assertion with no path for run
  records; with it, Plan 5 is a config flip, not a redesign.

## Architecture

```
harness (in actor)
   │  run token rides in the provider-native auth-header slot (see below)
   │  base URLs from llm.Config point here
   ▼
proxy (ours)  /proxy/llm/{provider}/{path...}   and   /proxy/internal/{path...}
   │  1 authenticate (run JWT from the auth-header slot:
   │       signature + stored-hash + active-run check)   ── AuthSource
   │  2 resolve permit (per-run cache)      ── PermitSource
   │  3 Hook.Before(...)                    ── no-op now; Plan 3 metering
   │  4 inject credential                   ── CredentialSource (vault)
   │  5 reverse-proxy, streaming            ── httputil.ReverseProxy, FlushInterval -1
   │  6 append run event (request/denied/error) ── EventSink
   ▼
provider (TLS, system roots)   /   internal API (pass-through)
```

**How the run token travels — the SDKs cannot send two auth headers.** The
provider SDKs emit exactly one credential header (`x-api-key` for anthropic,
`Authorization: Bearer` for openai/openrouter) and no other channel, so a
separate bearer-JWT header is impossible without a custom transport. Instead,
**the run token IS the placeholder**: the harness sets
`CallOptions.APIKey = runToken`, the SDK puts it in its native auth-header
slot, and the proxy extracts the token from whichever slot the provider route
uses, verifies it, strips it, and injects the real key. Provider code stays
untouched, and no request ever carries both a run token and a real credential.

`Deps` interfaces (the seam that makes the later split cheap):

```go
type AuthSource interface {
    // VerifyRunToken checks signature and expiry, compares the bearer's
    // sha256 against the run's stored runner_token_hash, and requires the
    // run to be active (status pending|running). Implemented over
    // token.Signer + the store today; an introspection endpoint after the
    // standalone split.
    VerifyRunToken(ctx context.Context, bearer string) (RunIdentity, error)
}

type PermitSource interface {
    // PermitForRun resolves run -> approved version -> permit.
    PermitForRun(ctx context.Context, tenantID, runID uuid.UUID) (Permit, error)
}

type CredentialSource interface {
    // Credential returns the secret for a tenant's named connection for the
    // given provider; name "default" resolves to the tenant's BYO default
    // for that provider, else the platform key.
    Credential(ctx context.Context, tenantID uuid.UUID, name, provider string) (Secret, error)
}

type EventSink interface {
    // AppendEvent records proxy.request / proxy.denied / proxy.error on the
    // run's audit trail. Best-effort: see Observability for the failure policy.
    AppendEvent(ctx context.Context, tenantID, runID uuid.UUID, typ string, payload map[string]any) error
}

type Hook interface {
    // Before runs ahead of each forwarded request. Plan 3's spend metering
    // implements this; Plan 2 ships a no-op.
    Before(ctx context.Context, req HookRequest) error
}
```

## The permit — schema v1

The permit column stops being opaque. Version 1 covers only what Plan 2
enforces and reserves the connector shape:

```json
{
  "v": 1,
  "llm": { "providers": ["anthropic"], "connection": "default" },
  "connections": {}
}
```

- `llm.providers` — allowlist of provider names the workflow may call. Absent
  or empty means **no LLM egress** (fail closed).
- `llm.connection` — the vault connection name supplying the key, resolved
  **per provider**: `"default"` resolves to the tenant's BYO default for the
  requested provider if one exists, else that provider's platform key.
- `connections` — reserved, must be empty in v1; the connector-catalog spec
  defines its entries (host allowlists, verbs).
- **Validation moves to the workflow API**: a permit that does not parse as v1
  is a 400 on create/add-version. The v1 API is stamped unstable, so this
  tightening is permitted; test fixtures gain real permits.

**Distribution: resolved per run, not per request.** On a run's first proxied
request the proxy calls `PermitForRun` and caches the permit keyed by RunID
until the run token expires or the run finalizes. Runs fire only from approved
versions and versions are immutable, so the cache cannot go stale within a
run. Re-approval mid-run deliberately does not alter a running run's permit —
a run executes the version it was fired from.

## The credential vault

### Key hierarchy

```
NIGHTSHIFT_VAULT_KEY (env, base64 32B — third key domain per roadmap decision 5)
  └─ tenant KEK  — per-tenant AES-256 key, minted at tenant creation,
                   stored wrapped by the master           (tenant_kek)
      └─ per-secret DEK — wraps each secret value, AES-256-GCM,
                   stored wrapped by the tenant KEK       (connection)
```

This is cronfoundry's envelope shape with the missing tenant layer inserted
(its single process-wide master was the survey's flagged multi-tenant gap).
**Rotation is designed for, not built:** every wrapped blob carries
`master_version` / `kek_version`, so re-wrapping is a later background job, not
a schema migration. Building the rotation job now would be speculative.

### Schema

```sql
CREATE TABLE tenant_kek (
    tenant_id uuid PRIMARY KEY REFERENCES tenant (id) ON DELETE CASCADE,
    wrapped_kek bytea NOT NULL,
    version int NOT NULL DEFAULT 1,   -- bumped on KEK rotation; what
                                      -- connection.kek_version refers to
    master_version int NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE connection (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    name text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('llm_api_key')),
    provider text NOT NULL,
    dek_wrapped bytea NOT NULL,
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL,
    kek_version int NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    UNIQUE (tenant_id, provider, name)
);
```

`kind` starts with one value; connectors add `oauth` and friends. Uniqueness
is **per provider**: a permit may allow several providers while naming one
connection, so `default` must be resolvable independently for each — the
lookup key is `(tenant_id, provider, name)`, and a tenant can hold a BYO
`default` for anthropic while openai falls through to the platform key.
Rotation is resumable because each connection's `kek_version` names the
`tenant_kek.version` that wrapped its DEK.

### Contracts

- **Write-only API**: `PUT /v1/connections/{name}`, `DELETE
/v1/connections/{name}`, `GET /v1/connections` — responses carry
  name/kind/provider/timestamps, **never a secret value**.
- **Decryption happens only inside the vault package, only on the proxy's
  request path.** No `store` struct returned to a handler ever contains
  plaintext secret material.
- Tenant creation mints the KEK atomically with the tenant row —
  `CreateTenant` is a single query today (`store/tenant.go`), so it **gains a
  transaction** as part of this work. Pre-release, no deployed databases: dev
  databases are recreated, not backfilled.
- `internal/redact` is ported here (as the roadmap assigns): the proxy's
  logger is wrapped so key material and auth headers cannot reach logs.

## Request flow detail

1. **Authenticate** — extract the run token from the provider route's native
   auth-header slot; `AuthSource.VerifyRunToken` checks signature and expiry
   (same `token.Signer`), the stored-hash comparison, **and that the run is
   active** (`status IN ('pending','running')`). The active-run check is what
   the internal API's rules alone would miss: without it, a finalized run's
   token would keep authenticating until JWT expiry. In addition,
   **`FinalizeRun` now clears `runner_token_hash`** in the same UPDATE that
   sets the terminal status (the column becomes nullable — a migration), so
   finalization is atomic revocation and a cleared hash fails the comparison
   even before the status check. Same binary today means no key-distribution
   problem; the standalone split later chooses between shared-key config and
   an introspection endpoint, decided at split time and noted here so it
   isn't rediscovered.
2. **Authorize** — `{provider}` must appear in the run's `llm.providers`;
   otherwise **403, fail closed**, plus a `proxy.denied` run event. Denials
   are audit-trail content: they are what lets a future alert name the
   boundary that was hit.
3. **Hook** — `Hook.Before`; a non-nil error maps to 429/403 per the hook's
   typed error (Plan 3 defines the shapes; Plan 2's no-op never fires).
4. **Inject** — strip all inbound auth headers; set the provider's real shape
   (`x-api-key` for anthropic; `Authorization: Bearer` for openai/openrouter)
   from the resolved connection; update `last_used_at` async.
5. **Forward, streaming** — one `httputil.ReverseProxy` per provider,
   `FlushInterval: -1` so SSE streams immediately; upstream TLS verified
   against system roots; write timeouts sized for multi-minute LLM calls.
   Destination hosts are **fixed per provider route** (api.anthropic.com,
   api.openai.com, openrouter.ai) — the actor names a provider, never a URL,
   so there is no user-controlled destination to canonicalize.
   **Redirect/DNS-rebinding analysis for v1** (the roadmap prerequisite,
   answered rather than deferred): the proxy itself never follows redirects —
   `httputil.ReverseProxy` relays a 3xx to the client verbatim; if the SDK
   inside the actor follows one, the new request can only reach the proxy
   again (it is the sole egress under Plan 5, and its routes only serve the
   fixed hosts), so a redirect cannot exfiltrate a credential — the injected
   key was attached to the original upstream request only, never returned to
   the actor. DNS rebinding does not apply to fixed operator-configured
   hostnames resolved proxy-side. User-controlled destinations — where
   canonicalization and rebinding become real — arrive with connectors, and
   the connector-catalog spec owns that surface.
6. **Record** — a `proxy.request` run event (provider, status, duration —
   never bodies, never headers).

### Harness and wiring changes

- `llm.Config` base URLs point at the proxy prefix; the ported provider code
  is untouched. `harness.Input.APIKey` becomes `harness.Input.RunToken`: the
  harness sets `CallOptions.APIKey = runToken` (the run token it already
  holds for the internal-API client), the SDK carries it in its native
  auth-header slot, and the proxy verifies, strips, and replaces it. No
  custom transport, no second header, and the SDKs' its-a-required-value
  constraint is satisfied by something the harness genuinely possesses.
- The per-provider `*_API_KEY` env vars remain but are read **only by the
  proxy**, as the platform-default connection.
- The proxy accepts the same 1 h run token; the roadmap's Plan-3
  TTL-vs-queueing revisit now covers the proxy path too.

## Failure handling

Fail closed throughout: absent/unparseable permit → 403; provider not
allowlisted → 403 + `proxy.denied`; vault decryption failure → 500 +
`proxy.error` (message never includes key material); unknown/expired/revoked
run token → 401 (finalization clears the run's token hash, and the
active-run check rejects terminal runs, so a finished run's token is dead on
the next request — no cache eviction protocol needed). Upstream provider
errors and timeouts pass through untouched — the harness's existing
`llm_error` path and run finalization own them, so the proxy adds no new
harness error handling. The permit cache is per-run and TTL-bounded by token
expiry; it caches only the **permit** (authorization data), never the auth
decision — `AuthSource.VerifyRunToken` re-checks the run row on every
request, exactly as the internal API already does, so a stale cache entry
can never outlive a revoked or finalized run.

## Observability

The run-event stream is the audit surface: `proxy.request`, `proxy.denied`,
`proxy.error` with provider/status/duration only. **Audit-append failure
policy, stated honestly:** `EventSink.AppendEvent` is best-effort — an append
failure logs an error and never blocks or fails the proxied request (a
denial is still enforced even if recording it fails). The audit trail is
therefore near-complete, not guaranteed-complete; making it durable
(retry/outbox) is deliberately deferred until the alerting work (Plan 4)
depends on it. Redacted `slog` throughout the proxy and vault.
`connection.last_used_at` tells a tenant a key is in use.

## Testing

- **Vault**: encrypt/decrypt round-trip; tenant isolation (tenant B's KEK
  cannot decrypt A's secrets; a wrong master key fails everything); the API
  never returns secret values; cross-tenant connection access is
  `ErrNotFound` — the house pattern for every aggregate.
- **Proxy** (against an `httptest` fake provider): an allowed request arrives
  upstream with the injected header and without the placeholder; a
  non-allowlisted provider gets 403 and a recorded `proxy.denied` event; SSE
  bodies flush incrementally; a BYOK connection beats the platform default.
- **Permit validation**: malformed v1 permits rejected with 400 at the
  workflow API.
- **Lifecycle**: a finalized run's token is rejected by the proxy (both via
  the cleared hash and via the active-run check, tested separately); the
  `/proxy/internal/...` pass-through delivers events/finalization
  end-to-end with the same token.
- **e2e**: the existing end-to-end test reroutes through the proxy with a fake
  upstream, proving a run completes with zero credentials in the harness.

## Explicitly out of scope

Connector traffic and its permit entries (connector-catalog spec);
canonicalization and redirect defenses for **user-controlled** destinations
(v1's fixed-host analysis is in the request-flow section; the
connector-catalog spec owns the user-controlled surface); OAuth
refresh/revocation; key/KEK rotation jobs (designed for via version columns);
spend metering (Plan 3, via `Hook`); the standalone proxy deployment
(Plan 5); NetworkPolicy conformance tests (Plan 5, where a sandbox exists).

## Open questions

- **Anthropic/OpenAI SDK retry interaction** — the SDKs retry internally;
  behind a proxy each retry is a fresh proxied request. Fine for correctness;
  revisit when metering charges per request.
- **Platform-default keys per environment** — env vars suffice until there is
  more than one deployment; a config file question, not a design one.
- **BYOK validation** — should `PUT /v1/connections` verify a key against the
  provider before accepting it? Deferred; a bad key surfaces as `llm_error`
  on the next run, which is survivable but unfriendly.

# Nightshift server

The Nightshift control plane: a Go module exposing a session-authenticated
public API for workflows/versions/runs and a run-JWT-authenticated internal
API that the run harness pushes events and results back over. See the
[platform design spec](../docs/superpowers/specs/2026-08-30-nightshift-platform-design.md)
and the [foundation implementation plan](../docs/superpowers/plans/2026-08-30-nightshift-platform-foundation.md)
for the full architecture, and [`docs/api/v1.md`](../docs/api/v1.md) for the
`/v1` contract this module serves.

## Running it

Requires Postgres 16 (e.g. via Docker) and a `DATABASE_URL`:

```bash
docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:16-alpine
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"

go run ./cmd/nightshift migrate

export NIGHTSHIFT_SESSION_KEY=$(openssl rand -base64 32)
export NIGHTSHIFT_RUNNER_KEY=$(openssl rand -base64 32)
export NIGHTSHIFT_VAULT_KEY=$(openssl rand -base64 32)
go run ./cmd/nightshift serve
```

`nightshift dev-session` mints a tenant, owner user, and session cookie for
local use (no production login flow exists yet). Other env vars — listen
address, actor state directory, platform model credentials — are documented
in `cmd/nightshift/main.go`'s package comment. The `*_API_KEY`-shaped vars
are gone: `NIGHTSHIFT_PLATFORM_ANTHROPIC_KEY`,
`NIGHTSHIFT_PLATFORM_OPENAI_KEY`, and `NIGHTSHIFT_PLATFORM_OPENROUTER_KEY`
are the operator's platform-default keys, read only by the egress proxy and
injected at the boundary — no credential reaches the harness.

### The egress proxy

All LLM traffic from the harness is routed through `internal/proxy`, the
run's sole point of egress. The harness carries only its run token (in the
provider-native auth-header slot the SDK would otherwise put an API key
in); the proxy verifies that token, resolves the run's approved permit,
checks the requested provider against the permit's allowlist and against
one hardcoded (method, path) per provider, then injects the real
credential — either a tenant's stored connection or the operator's
platform-default key — and forwards. Every accepted or denied request is
recorded as a run event. See
[`docs/superpowers/specs/2026-08-30-nightshift-egress-proxy-design.md`](../docs/superpowers/specs/2026-08-30-nightshift-egress-proxy-design.md)
for the full design, including this caveat: **on Local compute (today) the
proxy is policy-complete but advisory** — the harness shares the control
plane's process and network, so a hostile harness could in principle bypass
it. What Local compute genuinely delivers is that no credential ever lives
in harness memory, and every call is permit-checked and audit-logged. The
hard guarantee — no direct egress is possible at all — arrives with
sandboxed/Kubernetes compute, where a default-deny NetworkPolicy makes this
same proxy the only permitted destination.

Tests need Docker too (they spin up Postgres via `testcontainers-go`):

```bash
go test ./...
```

## Layout

```
server/
  go.mod, go.sum
  cmd/nightshift/main.go        serve | migrate | dev-session
  internal/db/                  pool, goose migrate, migrations/*.sql
  internal/testpg/              shared Postgres testcontainer helper
  internal/store/               hand-written pgx queries; one file per aggregate
  internal/httpapi/             public /v1 API + session auth
  internal/internalapi/         harness-facing /internal API (run-JWT auth)
  internal/token/                run-JWT signer (HKDF + HS256)
  internal/llm/                  ported providers + pricing; llmtest/ fake
  internal/harness/              the agent loop (tool-less) + HTTP client/sink
  internal/compute/             Compute interface + Local implementation
  internal/permit/              workflow permit document, schema v1
  internal/vault/               envelope crypto: tenant KEKs, per-connection DEKs
  internal/proxy/               egress gateway: run-token auth, permit enforcement, credential injection
  internal/proxyadapter/        proxy's narrow interfaces wired to the real store/signer/vault
  e2e_test.go                   session -> workflow -> approve -> fire -> harness -> finished run
```

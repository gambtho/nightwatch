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
go run ./cmd/nightshift serve
```

`nightshift dev-session` mints a tenant, owner user, and session cookie for
local use (no production login flow exists yet). Other env vars — listen
address, actor state directory, per-provider API keys — are documented in
`cmd/nightshift/main.go`'s package comment.

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
  e2e_test.go                   session -> workflow -> approve -> fire -> harness -> finished run
```

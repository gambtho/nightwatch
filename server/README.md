# Tomte server

The Tomte control plane: a Go module exposing a session-authenticated
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

go run ./cmd/tomte migrate

export TOMTE_RUNNER_KEY=$(openssl rand -base64 32)
export TOMTE_VAULT_KEY=$(openssl rand -base64 32)
go run ./cmd/tomte serve
```

The app's origin is derived automatically from the bound listener (the
loopback origin); `TOMTE_PUBLIC_BASE_URL` is an optional override for
dev topologies like Vite-as-origin (scheme + host, nothing else; HTTPS
required except loopback hosts). It defines the trusted `Origin` for
CSRF checks, the session cookie's attributes, and the Host allowlist —
Host and proxy headers are never used to derive any of those.

There is no login surface: one install is one user. The app shell mints
the session at launch, and "open in browser" exchanges a single-use
handoff token at `GET /local/handoff`. Sessions are opaque DB-backed
cookies with a 7-day idle window inside a 30-day cap.

`tomte dev-session` mints a tenant, owner user, and session row for
local use, printing the cookie. It reuses the tenant an existing email
already belongs to; only a genuinely new email mints a tenant. Other env
vars — listen
address, actor state directory, platform model credentials — are documented
in `cmd/tomte/main.go`'s package comment. The `*_API_KEY`-shaped vars
are gone: `TOMTE_PLATFORM_ANTHROPIC_KEY`,
`TOMTE_PLATFORM_OPENAI_KEY`, and `TOMTE_PLATFORM_OPENROUTER_KEY`
are the operator's platform-default keys, read only by the egress proxy and
injected at the boundary — no credential reaches the harness.

Three more govern scheduling and metering: `TOMTE_RUN_TOKEN_TTL`
(default `1h`) and `TOMTE_RUN_DEADLINE` (default `2h`) are Go
durations; `serve` refuses to start unless the deadline strictly exceeds
the token TTL, since a run whose token has already expired can never
finalize itself before the reaper would be allowed to sweep it.
`TOMTE_DEFAULT_MONTHLY_CAP_CENTS` (default `0`, meaning unlimited)
sets the default monthly budget in cents — how much Tomte may spend
from the user's key per month (Tomte meters only what goes through
Tomte). The user edits it at `PUT /v1/settings/budget`.

### Scheduler and reaper

`internal/engine.Scheduler` ticks on an interval, looking for workflows
whose versioned `schedule` artifact (see [`docs/api/v1.md`](../docs/api/v1.md))
is due. It creates and dispatches a run for the current occurrence only —
a schedule that was down for a while does not catch up on missed
occurrences — and admits at most one active run per workflow at a time,
via a store-level admission index rather than an in-process queue. A tick
that finds cap-exceeded or already-active workflows simply skips them.

`internal/engine.Reaper` sweeps runs stuck past `TOMTE_RUN_DEADLINE`
and finalizes them as failed, recovering from harness crashes and lost
dispatches. A run's deadline is measured from its latest dispatch
episode (`dispatched_at`, falling back to `created_at` for runs that
never dispatched), so by the time the reaper is allowed to sweep it, the
most recently signed token for it — minted at or before that episode,
with the shorter TTL — has already expired; the deadline>TTL invariant
above guarantees that for reaping itself.

That guarantee doesn't cover a narrower path one layer up, in
`internal/engine.Engine.dispatch`: if `Invoke` succeeds but the
following `MarkRunDispatched` write fails (and a same-run retry on a
cancel-free context also fails), the run is live under a valid,
unexpired token but still looks undispatched — `dispatched_at` is still
NULL. The scheduler's next tick then treats it as crashed-before-dispatch
and calls `Redispatch`, which invalidates that live token and re-invokes,
duplicating spend. This is mitigated (not eliminated) by the retry in
`dispatch`; the residual risk is a rare double-write, not a race the
reaper itself can see or prevent.

### Metering

`internal/meter.Meter` is wired as the egress proxy's `Hook`: every
provider call is checked against the user's monthly budget
(`TOMTE_DEFAULT_MONTHLY_CAP_CENTS`, UTC calendar month) before it is
allowed to reach the upstream — the cap is enforced at the proxy hook,
not before a run is admitted.

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
  cmd/tomte/main.go        serve | migrate | dev-session
  internal/db/                  pool, goose migrate, migrations/*.sql
  internal/testpg/              shared Postgres testcontainer helper
  internal/store/               hand-written pgx queries; one file per aggregate
  internal/engine/              shared fire path, scheduler, orphaned-run reaper
  internal/schedule/            cron + IANA timezone schedule artifact
  internal/meter/               the monthly budget meter, wired as the proxy Hook
  internal/httpapi/             public /v1 API: DB-backed sessions, local handoff, Origin policy
  internal/mail/                transactional email seam: Postmark sender + dev log sender
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

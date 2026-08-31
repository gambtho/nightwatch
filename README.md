# Tomte

Tomte is a click-install desktop app that lets a non-technical person safely
delegate recurring work to an AI agent. You describe a job in plain language,
approve exactly what the agent may touch and spend, and Tomte runs it on a
schedule — against any LLM endpoint you bring (Anthropic, OpenAI, OpenRouter,
any OpenAI-compatible base URL, including local models), with your own pasted
key.

## How enforcement works

Every credential and every call the agent makes passes through Tomte's
checkpoint. Credentials live on the proxy side of the process; the agent
harness holds a run token and nothing else, and every tool call and LLM call
is permit-checked, credential-injected, and audited at the proxy. The agent
can only act through Tomte's checkpoint, and every request is checked against
the permit the user approved.

This is a **software boundary, not a sandbox**: in v1 the harness shares a
process and an OS user with the proxy. What is guaranteed is that no
credential ever reaches the harness and no call escapes the audit; OS-level
process isolation is a named hardening path, not something shipped today.

## Status

Early development, pre-release. The project recently pivoted from a hosted,
multi-tenant platform to the click-install, self-contained shape described
above; the direction is set by the
[pivot design spec](docs/superpowers/specs/2026-08-31-tomte-pivot-design.md).
The control plane, enforcement proxy, scheduler, vault, and metering are
built and tested; the desktop packaging shell, the build conversation, and
the connections manager are designed but not yet built.

## Repository layout

- `server/` — the Go control plane: public `/v1` API, enforcement proxy,
  scheduler, vault, and metering. See [server/README.md](server/README.md).
- `web/` — the SPA frontend. See [web/README.md](web/README.md).
- `src/` + the root Vite files — the retired early prototype, kept for
  reference; not shared code.
- `docs/` — API contract, design specs, and coordination documents.

## Developing

Requires Go 1.26+, Node 20+, and Docker (for Postgres 16; tests also use it
via testcontainers).

```bash
docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:16-alpine
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"

cd server
go run ./cmd/tomte migrate

export TOMTE_PUBLIC_BASE_URL=http://localhost:8080
export TOMTE_RUNNER_KEY=$(openssl rand -base64 32)
export TOMTE_VAULT_KEY=$(openssl rand -base64 32)
go run ./cmd/tomte serve
```

`go run ./cmd/tomte dev-session` mints a tenant, owner user, and session
cookie for local use. For the frontend, run the server with
`TOMTE_PUBLIC_BASE_URL=http://localhost:5173` (Vite proxies `/v1` through),
then:

```bash
cd web
npm install
npm run dev
```

## License

MIT — see [LICENSE](LICENSE).

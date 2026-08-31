# Tomte

Tomte is about safely delegating recurring work to an AI agent — with the
agent's reach, spend, and schedule under its owner's explicit control.

**The current focus is the Kubernetes agent track.** An agent is defined as
code: one human-readable `agent.yaml` whose text is the agent's topology —
what it does (`steps:`), when it wakes (`schedule:`), and named slots where
the model (`llm:`, phase K2) and its reach (`connectors:`, phase K3) arrive.
A small CLI, `tomtectl`, runs that file on whatever cluster your kubeconfig
points at; hello world takes about five minutes on a local
[kind](https://kind.sigs.k8s.io/) cluster — see
[tomtectl/README.md](tomtectl/README.md). The phase-K1 runtime is a
deliberate placeholder that prints its steps on the schedule; the LLM
arrives in K2.

The K8s track runs with **no database of its own** — none is planned,
either. The cluster is the store: the agent file lives in a ConfigMap,
keys in Secrets, run state in Deployment/Job status, and Kubernetes
primitives cover scheduling. The durable store returns only with the
governed control plane at the K3+ transition — deployed into the cluster
as a normal workload, not something `tomtectl` carries.

The destination is the full Tomte experience: describe a job in plain
language, approve exactly what the agent may touch and spend, and Tomte
runs it on a schedule — against any LLM endpoint you bring (Anthropic,
OpenAI, OpenRouter, GitHub Models (included with GitHub Copilot plans),
Azure AI Foundry, any OpenAI-compatible base URL, including local models),
with your own pasted key. That governed stack — control plane, enforcement
proxy, scheduler, vault, and metering — is built and tested under
`server/`, and the K8s track transitions into it: connectors and permits
mount into the agent file's empty slots. Direction is set by the
[pivot design spec](docs/superpowers/specs/2026-08-31-tomte-pivot-design.md)
and the K8s-track sections of the
[coordination board](docs/superpowers/plans/2026-08-31-parallel-sessions.md).

## How enforcement works (the destination stack)

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

The K1 agent on Kubernetes deliberately ships **without** this governance —
no proxy, no permits — and gains it in the K3 transition.

## Status

Early development, pre-release. The direction changed twice on 2026-08-31:
first from a hosted multi-tenant platform to a click-install desktop app
(the pivot), then to the K8s-first agent track — CLI before UI. Built and
verified today: the `server/` stack above, and `tomtectl` running the
hello-world agent on a real cluster. Designed but not built: the K8s
agent's LLM phase (K2), the connectors and governance transition (K3), and
the build conversation. The desktop packaging shell is paused, not dead.
[docs/README.md](docs/README.md) maps every spec and plan — living vs
historical — in a five-minute read.

## Repository layout

- `tomtectl/` — the current focus: the K8s agent track's CLI. An
  agent-as-code `agent.yaml` plus `tomtectl init/up/status/logs/down` to run
  an agent on a Kubernetes cluster.
- `server/` — the Go control plane: public `/v1` API, enforcement proxy,
  scheduler, vault, and metering. See [server/README.md](server/README.md).
- `web/` — the SPA frontend. Idle: its pivot surfaces are built; wire-up
  resumes when the server work it consumes lands. See
  [web/README.md](web/README.md).
- `app/` — the desktop packaging shell. Paused by the K8s-first direction
  change; kept as the record of the Wails v3 decision and boot spike. See
  [app/README.md](app/README.md).
- `docs/` — API contract, design specs, and coordination documents. Start
  at [docs/README.md](docs/README.md).

The early React prototype that previously lived at the repo root (`src/`
plus the root Vite files) was removed after the pivots; it survives in git
history and on the demo branches (`demo/dev-persona`, `demo/tomte-pivot`).

## Developing

For the K8s agent track, [tomtectl/README.md](tomtectl/README.md) is the
whole quickstart (Go 1.26+, a kubeconfig; kind works for a local cluster).

The server requires Go 1.26+, Node 20+, and Docker (for Postgres 16; tests
also use it via testcontainers):

```bash
docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:16-alpine
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"

cd server
go run ./cmd/tomte migrate

export TOMTE_RUNNER_KEY=$(openssl rand -base64 32)
export TOMTE_VAULT_KEY=$(openssl rand -base64 32)
go run ./cmd/tomte serve
```

`TOMTE_PUBLIC_BASE_URL` is optional since P1 — it defaults to the bound
loopback origin. `go run ./cmd/tomte dev-session` mints a tenant, owner
user, and session cookie for local use. For the frontend, run the server
with `TOMTE_PUBLIC_BASE_URL=http://localhost:5173` (Vite proxies `/v1`
through), then:

```bash
cd web
npm install
npm run dev
```

## Working here

See [CONTRIBUTING.md](CONTRIBUTING.md): the lane model, the one-session
`server/` lock, where the coordination board lives, and a 15-minute path
from clone to a running agent on kind.

## License

MIT — see [LICENSE](LICENSE).

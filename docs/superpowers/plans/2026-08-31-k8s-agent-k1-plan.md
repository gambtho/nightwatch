# K8s agent track — phase K1 plan (hello world via agent-as-code YAML)

**Date:** 2026-08-31
**Lane:** K8s agent track (direction change 2 — the current focus)
**Scope:** phase K1 only. This plan leads the implementation PR; at this size
a separate plan PR earns nothing.

## Intended outcome

A newcomer with a kubeconfig runs four commands and watches a hello-world
agent, defined entirely by one human-readable `agent.yaml`, log from a real
cluster:

```sh
tomtectl init
tomtectl up
tomtectl status
tomtectl logs --follow
```

The `agent.yaml` is the deliverable leadership asked to hold: reading it shows
the agent's identity, behavior, schedule, and the named slots where K2's
`llm:` and K3's `connectors:` land.

## Decisions

1. **Name and layout: top-level `tomtectl/`**, its own Go module
   (`github.com/gambtho/tomte/tomtectl`), binary `tomtectl`. The name cannot
   collide with the future server-extending `tomte` CLI, and it reads as what
   it is — a kubectl-shaped control for Tomte agents. Layout mirrors
   `server/`: `main.go` plus `internal/` packages (`agentfile`, `manifest`,
   `kube`).
2. **Agent-as-code format** (board decision, not re-litigated): net-new schema
   in the Kubernetes resource envelope — `apiVersion: tomte.dev/v1alpha1`,
   `kind: Agent`, `metadata:`, `spec:`. Not a CRD; the CLI reads the file.
   The `spec:` nouns are Tomte's own: `steps: [{id, text}]` and
   `schedule.every`, plus empty-but-named `llm: {}` and `connectors: []`
   slots whose comments carry the K2 endpoint vocabulary already fixed by the
   board (`kind: anthropic | openai_compatible`, explicit `local`
   classification, key via K8s Secret reference). K1 rejects a non-empty
   `llm:` or `connectors:` rather than silently ignoring it.
3. **client-go, not kubectl shell-out.** One self-contained binary — the
   five-minute demo needs no kubectl installed; `status` and `logs` are real
   features rather than exec wrappers; K2/K3 (Secrets, watches) need the
   typed API anyway. Plain namespaced objects, Get→Create/Update, no
   server-side-apply machinery, no Helm (nothing here earns a chart).
4. **Derived objects: ConfigMap + Deployment**, both named after the agent,
   labeled `tomte.dev/agent: <name>` and
   `app.kubernetes.io/managed-by: tomtectl`. A loop agent is resident, so a
   Deployment (1 replica), not a Job.
5. **K1 runtime: stock `busybox` + a runtime script the CLI writes into the
   ConfigMap** next to `agent.yaml`. The script reads `schedule.every` and
   each step's `text` from the *mounted* `agent.yaml` and prints them on the
   loop — behavior comes from the YAML, never from the image, and editing the
   ConfigMap changes the agent with no CLI involved. A custom runtime image
   would force newcomers to build/push before hello world; the script is the
   explicitly-labeled placeholder that K2's real agent-runtime image
   replaces. Its YAML reading is deliberately scoped to K1's two scalar
   shapes and unit-tested.
6. **Commands:** `init` (scaffold `agent.yaml`), `up` (apply), `status`,
   `logs [--follow]`, `down` (delete). Every command resolves the agent from
   the file (`-f`, default `./agent.yaml`) — the YAML stays the single
   source of truth; nobody addresses manifests directly. `-n` overrides the
   kubeconfig context's namespace; `--context` overrides the context.
   Stdlib `flag` per subcommand; no CLI framework.

## Steps

1. Plan doc (this file) + `tomtectl` added to the root `.gitignore` binary
   list.
2. `internal/agentfile`: types, strict load (unknown fields error), validate
   (envelope, DNS-1123 name, ≥1 step, `every` a positive Go duration,
   empty K2/K3 slots). Embedded init template. Tests first.
3. `internal/manifest`: derive ConfigMap (exact file bytes + `run.sh`) and
   Deployment; runtime script behavior tested by executing it under `sh`
   against a sample file.
4. `internal/kube` + `main.go`: kubeconfig loading, create-or-update apply,
   status (deployment readiness + pods), logs (stream first pod), down
   (delete both, NotFound-tolerant).
5. `tomtectl/README.md`: the pitch — `agent.yaml` up front, then the
   five-minute kind walkthrough.
6. Verification on a real cluster: `kind create cluster`, then
   init → up → status → logs → edit the YAML → up again → down, transcript
   in the PR.

## Explicitly out of scope (K1)

No CRD/operator/controller, no Helm, no LLM call, no connectors, no egress
proxy or permits (user decision, recorded on the board — governance arrives
with the K3 transition, and the existing proxy/permit stack is that
destination). No CI job for `tomtectl/` — `.github/` belongs to the CI lane;
a `tomtectl.yml` workflow mirroring `server.yml`'s gofmt/vet/test shape is
reported to the coordinating session as owed.

## Risks / notes

- `busybox sleep` must accept the `30s` suffix — verified live on kind before
  completion is claimed.
- The runtime script's field extraction is a placeholder parser, not a YAML
  parser; the CLI's strict loader is the real gate, and K2 retires the
  script.

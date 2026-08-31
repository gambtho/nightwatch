# tomtectl — an agent, as code, on Kubernetes

One YAML file **is** the agent. Reading it shows the topology — what
the agent does, when it runs, what model it thinks with, and what it
may reach:

```yaml
apiVersion: tomte.dev/v1alpha1
kind: Agent
metadata:
  name: hello
spec:
  # What the agent does, in order, each time it wakes. In K1 a
  # placeholder runtime prints each step verbatim; K2 hands the same
  # steps to an LLM. The shape does not change.
  steps:
    - id: greet
      text: Hello, world — from the hello agent.
  # When it runs. The agent stays resident and wakes on this interval.
  schedule:
    every: 30s
  # K2 — the model this agent thinks with. Empty means no LLM: the
  # agent is deterministic.
  llm: {}
  # K3 — what the agent may reach. Tomte connectors and their permits
  # mount here; until then the agent has no reach beyond its own logs.
  connectors: []
```

`tomtectl` derives everything Kubernetes needs from that file — a
ConfigMap carrying the file itself and a Deployment whose pod reads it.
Nobody hand-edits manifests, and the behavior lives in the YAML, never
in the image.

## Five minutes to a running agent

You need a cluster your kubeconfig points at. Any cluster works; for a
local one, [kind](https://kind.sigs.k8s.io/) does:

```sh
kind create cluster
```

Build the CLI (Go 1.26+) and run the agent:

```sh
cd tomtectl && go build -o tomtectl .

./tomtectl init            # writes agent.yaml — read it first
./tomtectl up              # ConfigMap + Deployment, from the file
./tomtectl status          # 1/1 ready
./tomtectl logs --follow   # hello world, every 30s
```

```
tomte agent starting: waking every 30s
2026-08-31T21:04:11Z Hello, world — from the hello agent.
2026-08-31T21:04:41Z Hello, world — from the hello agent.
```

Change the file — the message, another step, a different `every` —
and `./tomtectl up` again; the agent restarts with the new behavior.
`./tomtectl down` removes it. `-f` points at a different agent file,
`-n` at a namespace, `--context` at a kubeconfig context.

## What this is (and is deliberately not, yet)

This is phase K1 of Tomte's Kubernetes agent track: the smallest honest
version of *agent-as-code on a cluster*.

- **Not a CRD.** The file wears the Kubernetes resource envelope
  (`apiVersion: tomte.dev/v1alpha1`, `kind: Agent`) so registering it
  as a CRD later is a step, not a schema rewrite — but today the CLI
  reads the file and nothing registers with the API server. No
  operator, no controller, no Helm.
- **The runtime is a placeholder.** The pod runs a stock `busybox`
  image with a small script (shipped in the ConfigMap, next to the
  file) that prints each step's text on the schedule. K2 replaces it
  with the real Tomte agent runtime, which hands the same `steps:` to
  an LLM — `llm:` gains an endpoint (`anthropic | openai_compatible`
  base URL) and a key drawn from a Kubernetes Secret.
- **No governance yet, by decision.** Tomte's egress proxy, permits,
  and connectors are the destination, arriving with K3's `connectors:`
  — the slots in the file are where they mount.

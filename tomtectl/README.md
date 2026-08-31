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
  # What the agent does, in order, each time it wakes. With no llm the
  # runtime prints each step verbatim; with one it hands the steps to
  # the model and logs the reply. The shape does not change.
  steps:
    - id: greet
      text: Hello, world — from the hello agent.
  # When it runs. The agent stays resident and wakes on this interval.
  schedule:
    every: 30s
  # K2 — the model this agent thinks with. Empty means no LLM: the
  # agent is deterministic. The API key lives in a Kubernetes Secret
  # (`tomtectl set-key`), never in this file.
  llm:
    kind: anthropic              # anthropic | openai_compatible
    base_url: https://api.anthropic.com
    model: claude-haiku-4-5
    secretRef: hello-key
  # K3 — what the agent may reach. Tomte connectors and their permits
  # mount here; until then the agent reaches nothing beyond its logs
  # and the one llm endpoint named above.
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

Build the CLI and the runtime image (Go 1.26.2+, Docker). No registry
publishes the image yet, so a local cluster loads it directly:

```sh
cd tomtectl && go build -o tomtectl .
docker build -t tomte-agent:0.2.0 .
kind load docker-image tomte-agent:0.2.0
```

Run the agent:

```sh
./tomtectl init            # writes agent.yaml — read it first
./tomtectl up              # ConfigMap + Deployment, from the file
./tomtectl status          # 1/1 ready
./tomtectl logs --follow   # hello world, on the schedule
```

```text
tomte agent starting: waking every 30s
2026-08-31T21:04:11Z Hello, world — from the hello agent.
2026-08-31T21:04:41Z Hello, world — from the hello agent.
```

Change the file — the message, another step, a different `every` —
and `./tomtectl up` again; the agent restarts with the new behavior.
`./tomtectl down` removes it. `-f` points at a different agent file,
`-n` at a namespace, `--context` at a kubeconfig context, and
`up --image` at a different runtime image.

## Give the agent a model

Fill in `llm:` (the template's comments carry the shape — Anthropic or
any OpenAI-compatible endpoint), then store the key in the Kubernetes
Secret the file names. The key travels stdin → Secret → pod env; it
never appears in the YAML, a ConfigMap, an argv, or logs:

```sh
./tomtectl set-key         # hidden prompt; or pipe the key in
./tomtectl up
./tomtectl logs --follow   # the model's reply, on the schedule
```

Every wake, the runtime re-reads the mounted file and sends the steps
to the configured endpoint. Anything but a well-formed positive
response — an error status, an unreadable body, malformed JSON, an
empty completion — is logged as `wake failed: …` and never printed as
a result; the schedule survives it. A keyless local endpoint (Ollama
and friends) is `local: true` with no `secretRef`. A keyed endpoint
must use `https` unless its host is loopback or cluster-local — a key
never travels in cleartext to the open internet.

`e2e/e2e.sh` proves the whole path on a real kind cluster with an
in-cluster stub endpoint: build, load, `set-key`, `up`, the stub's
reply in `logs`, and the fail-closed 401 with a wrong key.

## What this is (and is deliberately not, yet)

This is phase K2 of Tomte's Kubernetes agent track: agent-as-code on a
cluster, now thinking with a model.

- **Not a CRD.** The file wears the Kubernetes resource envelope
  (`apiVersion: tomte.dev/v1alpha1`, `kind: Agent`) so registering it
  as a CRD later is a step, not a schema rewrite — but today the CLI
  reads the file and nothing registers with the API server. No
  operator, no controller, no Helm.
- **The runtime is real but bare.** A small Go image
  (`cmd/agent-runtime`, K1's mount contract unchanged) reads the
  mounted `agent.yaml` each wake and talks to the endpoint directly.
- **No governance yet, by decision.** Tomte's egress proxy, permits,
  and connectors are the destination, arriving with K3's `connectors:`
  — the slot in the file is where they mount, and a non-empty
  `connectors:` still refuses to deploy.

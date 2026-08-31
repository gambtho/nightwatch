# K8s agent track — phase K2 plan (the llm: block comes alive)

**Date:** 2026-08-31
**Lane:** K8s agent track (direction change 2 — the current focus)
**Scope:** phase K2 only. This plan leads the implementation PR (K1
precedent; a separate plan PR earns nothing at this size).

## Intended outcome

The `llm:` slot in `agent.yaml` stops being a promise. A user writes the
model their agent thinks with into the file, puts the API key into a
Kubernetes Secret through `tomtectl set-key` (never into the YAML, a
ConfigMap, or logs), runs `tomtectl up`, and `tomtectl logs` shows the
model's answers to the agent's steps, on the agent's schedule. A real Go
runtime image replaces the busybox placeholder on the same mount
contract. Non-empty `connectors:` still rejects — governance mounts at
K3, not here.

Verified end to end on a real kind cluster: an in-cluster stub
OpenAI-compatible server proves the whole path automatically (including
the Secret round trip and the fail-closed negative), and the README
documents the real-endpoint path with a pasted key.

## Decisions

1. **`llm:` schema — flat, exactly the brief's vocabulary:**

   ```yaml
   llm:
     kind: openai_compatible   # anthropic | openai_compatible
     base_url: https://api.openai.com/v1
     model: gpt-4o-mini
     secretRef: hello-key      # K8s Secret holding the API key
   ```

   plus `local: true` as the explicit keyless marker ("on this
   computer" / in-cluster, aligned with the board's endpoint record: a
   loopback URL alone never implies local). `model` joins the brief's
   `{kind, base_url, secretRef}` because a chat call cannot be made
   without one. `secretRef` is required unless `local: true`, which
   forbids it. This flattens K1's commented sketch (`endpoint:` /
   `api_key_secret`) — the K2 brief's literal field list wins; reported
   as a divergence.
2. **Bare-runtime URL rule, not governance:** `base_url` must be a
   well-formed http(s) URL without userinfo. Plain `http` is allowed
   only when the endpoint is keyless (`local: true`) or the host is
   loopback / `localhost` or spelled in the cluster-local `.svc` /
   `.svc.cluster.local` form — a bare single-label service name is
   rejected, since a resolver search domain could route it off-cluster
   (review hardening). The in-cluster stub still exercises the real
   Secret path via its `.svc` name, while a key can never be sent in
   cleartext to the open internet. Allowlists, path validation, and
   the proxy stay K3+.
3. **Key path:** `tomtectl set-key` reads the agent file, requires
   `spec.llm.secretRef`, reads the key from stdin (hidden prompt on a
   TTY via `x/term`; piped input otherwise — never argv, never an env
   var), and creates/updates the Secret under key `api_key` with the
   same ownership labels and bystander refusal as every other object.
   The Deployment injects it as env `TOMTE_API_KEY` via `secretKeyRef`.
   `down` leaves the Secret in place (key material outlives a
   redeploy; the message says so).
4. **One runtime binary, one image, two modes.** `cmd/agent-runtime`
   in the same module imports `internal/agentfile` — the runtime and
   the CLI share one strict parser. Default mode: re-read the mounted
   `/tomte/agent.yaml` each wake; empty `llm:` keeps K1's exact
   behavior (print each step's text); non-empty sends the steps as one
   chat request — both the Anthropic `/v1/messages` and OpenAI
   `/chat/completions` shapes — and logs the model's text. `stub` mode
   serves a canned OpenAI-compatible endpoint that 401s unless the
   Bearer key matches its own Secret-mounted env, so the e2e needs no
   second image and the Secret round trip is proven on both ends.
5. **Fail closed, per the board's standing guidance:** a wake's call
   that returns non-2xx, an unreadable body, malformed JSON, or empty
   content logs `wake failed: …` (status + truncated body, never the
   key) and is never printed as a result; the loop keeps the schedule.
   Config-level faults — unparseable file, missing key, bad kind — are
   fatal at startup, a loud CrashLoopBackOff in `status`.
6. **Image distribution stays local at K2:** constant default
   `tomte-agent:0.2.0`, `imagePullPolicy: IfNotPresent`, `--image`
   override on `up`. No registry is published yet; the README documents
   `docker build` + `kind load docker-image`. Publishing an image is a
   release question, not a K2 question.

## Steps

1. `internal/agentfile`: typed `LLM` struct + validation (table-driven
   tests first).
2. `internal/manifest`: runtime image + env injection replace busybox +
   `run.sh`; `--image` plumbed through.
3. `internal/runtime`: wake loop, both request shapes, fail-closed
   parsing, stub handler — all against `httptest` servers.
4. `main.go`: `set-key`, `--image`; `internal/kube`: Secret apply with
   ownership gate (fake-clientset tests).
5. `Dockerfile` (multi-stage → distroless/static, CA certs included).
6. `e2e/`: stub manifest + script — build, load, deploy stub, set-key,
   up, assert logs show the stub's marker, assert a wrong key logs
   `wake failed`, clean up. Run for real on kind.
7. README + template comments updated; the real-endpoint path
   documented.

## Verification

`gofmt`, `go vet`, `go test ./...` (the CI workflow's exact gates,
self-contained — no cluster in CI), plus the kind e2e run end to end in
this session with its output recorded in the PR.

## Explicitly out

Connectors (K3), any proxy/permit/governance, CRDs/operator, image
registry/publishing, multi-step conversations or tool use, retries or
backoff policy beyond the schedule itself.

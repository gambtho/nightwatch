# Nightshift Platform — Design

**Status:** Design approved in outline; not yet planned or built
**Date:** 2026-08-30
**Author:** gambtho
**Companion:** [`2026-08-28-nightshift-design.md`](./2026-08-28-nightshift-design.md) — the UX this serves.
That document's "Substrate" section is superseded by this one; see
[Corrections to the UX spec](#corrections-to-the-ux-spec).

## What this is

The multi-tenant hosted backend that runs Nightshift workflows. One repo with the UX,
one product. Greenfield, harvesting packages from CronFoundry.

## Decisions already taken

| Decision | Why |
|---|---|
| **Hosted, multi-tenant, operated by us** | Answers the UX spec's deferred "whose machines". Non-technical users cannot self-host. |
| **Agent Substrate for compute** | Own the unit economics. 30× multiplexing of idle scheduled agents is the whole business case. |
| **Not Managed Agents** | CMA and Substrate are alternatives, not layers — both host compute. CMA would give ten governance primitives free but makes us Anthropic-only and hands Anthropic our margins. |
| **Greenfield, not a CronFoundry retrofit** | CronFoundry is multi-tenant in its schema and single-tenant in every layer above it: 25 non-test `GetFirstOrganization` call sites, and `SessionClaims{Login, Role, Exp}` carries no tenant at all. Each of those sites is currently an authorization no-op that would have to become a real authz decision. Zero users means no reason to preserve any of it. |
| **One repo with the UX** | One product, one spec tree. Splitting later is bounded; merging later is not. |
| **OSS boundary undecided** | So the UI↔server API is designed as a public contract: versioned, documented, no leaked internals. Costs discipline, forecloses nothing. |
| **Harness: harvest CronFoundry's runner initially** | Provider-neutrality is *why* we chose Substrate; the harness must not undo it. CronFoundry's `ToolCapableProvider` loop already spans four providers. Behind an interface, so a Claude Agent SDK actor image stays an option. |

## Constraints from Substrate

> **Verification status.** Read on 2026-08-30 from the project's `README.md`,
> `docs/architecture.md`, and `docs/threat-model.md` via fetched summaries — not from
> source, and not from a running cluster. Everything below marked *(inferred)* needs
> confirming against code before it is built on. The project states it is pre-1.0,
> "not ready for production use, and the APIs are almost guaranteed to change."

**Confirmed:**

- Control plane is a gRPC API on `ate-api-server`: CreateActor, ResumeActor,
  SuspendActor, DeleteActor. **No exec or attach.**
- An **ActorTemplate** is a Kubernetes CRD — "an immutable definition of an
  actor-version… container image, configuration, and environment required to generate a
  'golden' snapshot." CreateActor references a template.
- Snapshots capture "the exact RAM state of the process" plus "the files written to the
  container's writable layer", stored in **Google Cloud Storage**.
- Actors are HTTP-addressable at
  `<actor-name>.<atespace>.actors.resources.substrate.ate.dev`. `atenet-router` (Envoy)
  intercepts, asks the control plane to **resume the actor**, then tunnels to the worker.
- **`atespace` is not a security boundary** — it is a DNS/logical grouping. Tenant
  isolation is ours to enforce.
- The threat model targets "mutually-untrusting multi-tenant workloads", mandates
  gVisor/microVM ("traditional containers are not a secure sandbox"), default-deny
  ingress/egress, full state reset between actors sharing a worker, and "credentials are
  not exposed in sandboxes by default".
- `WorkerPool` CRD defines warm capacity and `spec.sandboxClass` (gVisor or micro-VM).

**Two findings that shape the architecture:**

1. **Egress policy is coarse.** "Egress from actors leverages standard Kubernetes
   NetworkPolicy at the WorkerPool boundary." NetworkPolicy at pool scope cannot express
   "workflow A reaches Slack only; workflow B reaches Zendesk only" — not without a
   WorkerPool per workflow, which destroys the multiplexing we chose Substrate for. The
   threat model's "selectively allow access to specific actors" reads as a requirement,
   not a shipped feature.
   **This directly threatens the product's core promise**, since the blast-radius permit
   is Nightshift's entire differentiator. See [The egress proxy](#the-egress-proxy).
2. **There is no log or event retrieval API.** The architecture doc calls it an open
   question: "With actors being multiplexed onto and off of workers all the time, it will
   be more difficult to understand what is happening." We cannot pull run output from
   Substrate. The harness must **push** it to us. See
   [Run records](#the-actor-run-lifecycle).

## Architecture

Three layers, deliberately separated so the pre-1.0 one is replaceable.

```
Control plane (ours)   tenancy, authz, scheduling, permits, grading, metering, API
       │
Egress proxy (ours)    the permit enforcement point; credential injection
       │
Harness (ours, in the actor)   the agent loop, tools, provider calls
       │
Compute (Substrate)    actors, workers, snapshots, isolation
```

**Invocation model.** Substrate resumes an actor when HTTP traffic arrives for its DNS
name. So "fire a workflow" is an authenticated HTTP request from our scheduler to the
actor's address — not a job submission. Resume is sub-second, and the actor comes back
with its memory and working volume intact.

**The Substrate seam.** One interface in our control plane, modelled on CronFoundry's
`cloud.JobDispatcher` (`Dispatch(ctx, DispatchRequest) (Handle, error)`) but
actor-shaped rather than one-shot-shaped:

```go
type Compute interface {
    EnsureActor(ctx context.Context, w WorkflowRef, tmpl TemplateRef) (ActorID, error)
    Invoke(ctx context.Context, a ActorID, payload InvokeRequest) (Handle, error)
    Suspend(ctx context.Context, a ActorID) error
    Destroy(ctx context.Context, a ActorID) error
}
```

A second implementation over plain Kubernetes Jobs (stateless, no suspend/resume) ships
alongside the Substrate one from day one. That is not gold-plating: it is the mitigation
for a pre-1.0 dependency on our critical execution path, and it keeps us honest about
what leaks into the domain model.

## The egress proxy

Because per-actor network policy is not available, **the permit is enforced at an egress
proxy we operate**, and actors get no direct egress at all.

- Every outbound request from an actor goes through the proxy.
- The proxy holds the workflow's permit and rejects anything outside it.
- **Credentials never enter the sandbox.** The actor sends a request naming a connection;
  the proxy substitutes the real token at egress. This satisfies the threat model's
  "credentials are not exposed in sandboxes by default" and matches the pattern CMA's
  vaults implement.
- WorkerPool NetworkPolicy stays as defence in depth — default-deny, with the proxy the
  only permitted destination.

Enforcing in the proxy rather than in the harness is deliberate: the harness shares a
sandbox with an LLM, so anything it enforces is advisory. The proxy is outside the blast
radius and is the only component that ever sees a customer credential.

## Tenancy and authz

The failure mode we are explicitly not repeating: a schema that is tenant-aware while
every caller above it ignores the tenant.

- **Tenant identity is in the session and in every request context.** No handler ever
  resolves "the tenant" by lookup.
- **Every query takes a tenant id** as a parameter, and it is not defaultable.
- **Authorization is a decision at every resource access**, not an implicit consequence of
  there being one tenant. Cross-tenant access is a test case, not an assumption.
- Per-tenant DEKs for secrets. CronFoundry's envelope encryption under a single
  per-deployment master key does not scale to tenants.
- Actors carry their tenant in their identity so proxy and control plane can both check it.

## The actor / run lifecycle

**A workflow is a long-lived actor. A run is an episode in its life.** This is the
substantive change from CronFoundry's model, where a run was a process that started and
died.

- **Memory becomes state, not a hack.** CronFoundry extracted a `<memory>` block from LLM
  output with a regex and git-committed it. Here, the actor's working volume and RAM
  persist across fires. The `<memory>` mechanism is not ported.
- **Run records are pushed, not pulled.** Since Substrate exposes no log or event API, the
  harness streams events to the control plane over the same authenticated channel it uses
  for egress. A run record is ours end to end.
- **Overlap policy changes meaning.** CronFoundry's `skip | queue | concurrent` assumed
  independent processes. With one actor per workflow, concurrent is not free — it implies
  either serialization inside the actor or multiple actors per workflow. Default to
  serialize; treat concurrency as a later, explicit feature.
- **Suspension is the normal state.** A weekly digest is suspended ~99.9% of the time.
  That is the economic premise, and it must be the default path, not an optimization.

## Governance primitives we must build

Managed Agents supplied these; Substrate supplies isolation and lifecycle only. Each is
now ours. Ordered by how much of the product breaks without it.

1. **Permit enforcement** — the egress proxy above. Without it Nightshift has no product.
2. **Rubric grading** — an independent grader scoring each run per criterion. The UX spec's
   entire "silence is good" alerting rests on it: it is what lets an alert name a *specific
   broken promise* instead of guessing. Expensive, and it was free on CMA.
3. **Spend metering and caps** — per-run and per-tenant, enforced before issuing model
   requests, across providers. CronFoundry's `internal/llm/pricing.go` is a starting point.
4. **Scheduling** — cron plus IANA timezone, per-tenant fairness and quotas. CronFoundry's
   single-process `Tick` does not survive multi-tenancy unchanged.
5. **Credential vaulting** — storage, per-tenant encryption, OAuth refresh and revocation.
6. **Run records** — pushed from the harness; the audit surface.
7. **Tool permissions** — which tools a workflow may use, enforced by the harness and
   bounded by the proxy.
8. **Versioning** — a workflow's permit and steps are versioned; edits require re-approval.
   Note this couples to ActorTemplate immutability: a new version is a new template and a
   new golden snapshot.

## Harvest from CronFoundry

**Port (clean boundaries, tenant-agnostic):**

| Package | LOC | Note |
|---|---|---|
| `internal/publish` | 779 | Multi-destination fan-out with isolated retries. The best thing in the repo. |
| `internal/llm` | 786 | Four providers plus pricing; underpins metering. |
| `internal/mcp` | 583 | Tool dispatch for the harvested harness. |
| `internal/runner` | 535 | The agent loop — initial harness. |
| `internal/secrets` | 523 | Envelope encryption; rework for per-tenant DEKs. |
| `internal/redact` | — | Log redaction. Security-relevant and already tested. |
| `internal/token`, `internal/template` | — | Small and useful. |
| DB schema shape | — | Org-scoped tables with cascade deletes is the right model. |

**Do not port** — these serve the GitOps/self-host workflow we are leaving:
`githubapp` (748), `bootstrap` (630), `sync` (476), `yamledit` (282), `cloud` (284),
`skillrepo` (221), the Bicep/Fly/AKS deploy tree, and `webapi` (3,781) which must be
rewritten around real tenancy regardless.

**Do not port, superseded:** `internal/memory` and `internal/writeback` — actor state
replaces them.

## Corrections to the UX spec

[`2026-08-28-nightshift-design.md`](./2026-08-28-nightshift-design.md) needs three edits:

1. Its "Substrate" section maps ten governance primitives to Managed Agents. Replace with
   a pointer here; we build eight of them.
2. "Deliberately deferred → whose machines" is answered: hosted, by us.
3. Two CMA-specific UX constraints no longer apply: the 15%/9-minute scheduling jitter,
   and "a budgeted session pauses rather than fails". Our own scheduler and metering
   define those behaviours now, and the UX should state whatever we actually implement.

## Open questions

- **Does per-actor egress policy land before we need it?** If Substrate ships it, the proxy
  becomes defence in depth rather than the primary control. Worth tracking upstream.
- **GCS dependency for snapshots** — a cloud coupling inherited from Substrate. Acceptable?
- **Verified state reset.** The threat model flags suspend/resume state cleanup as needing
  testing. Hosting strangers' agents means verifying it ourselves, not taking it on faith.
- **Fixed cost before customer one.** A k8s fleet with microVM-capable nodes is a standing
  bill. What is the runway assumption?
- **Connector catalog** — still the top product risk from the UX spec, now with per-tenant
  OAuth on top. Needs its own spec.
- **Grader model and cost.** Grading every run doubles per-run inference at minimum.

## Explicitly out of scope

Self-hosting, billing implementation, the connector catalog, and the real web frontend.
The UX prototype in `research/` stays research until sessions are done.

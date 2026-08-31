# Nightshift Cluster Compute — Design (Plan 5)

**Status:** Spec awaiting review
**Date:** 2026-08-31
**Author:** gambtho
**Parent:** [`2026-08-30-nightshift-platform-design.md`](./2026-08-30-nightshift-platform-design.md) — this designs Plan 5 of the
[roadmap](../plans/2026-08-30-nightshift-platform-roadmap.md): the two
cluster-facing implementations of the `Compute` seam, the egress topology that
turns the Plan 2 proxy from advisory into the guarantee, harness
containerization, and the deployment shape.
**Primary input:** [`../../research/2026-08-30-substrate-verification.md`](../../research/2026-08-30-substrate-verification.md) —
where the spike refuted or changed a platform-spec claim, this design follows
the spike. The corrections the spike flagged are **listed, not applied,
here** — a separate docs-only PR owns editing the platform spec; see
[Platform-spec corrections](#platform-spec-corrections-needed).

## What this delivers

Three things the seam was built to receive, plus the network that makes the
permit real:

1. **`compute.Substrate`** — actors on Agent Substrate (pinned commit),
   invoke-over-HTTP resume, suspension as the steady state.
2. **`compute.KubeJobs`** — plain Kubernetes Jobs, stateless, the honest
   fallback for a pre-1.0 dependency on the critical execution path.
3. **The egress topology** — default-deny NetworkPolicies we author (the spike
   proved upstream ships none for egress), with the Plan 2 proxy as the sole
   egress and a conformance test that proves it from inside a sandbox.

Plus the two prerequisites both implementations share: the harness as a
container image, and the standalone-proxy deployment split the proxy spec
deferred here.

## The seam's contract, restated after Plan 3

`local.go` predates Plan 3 and its per-actor mutex reads like the
serialization contract. It no longer is. Since migration 00008, **one active
run per workflow is a DB admission rule** (`run_one_active_per_workflow`): a
second concurrent fire is rejected with 409 before `Invoke` is ever called.
What a `Compute` implementation must actually provide:

- **`EnsureActor` is idempotent** — same `WorkflowRef`, same actor, same state.
- **`Invoke` is asynchronous and at-least-once** — the engine retries and
  `Redispatch` re-invokes; the implementation (with the harness) must make
  duplicate delivery of the same `RunID` harmless. This closes Plan 3's
  deferred "invoke-idempotency by RunID (Plan 5)".
- **Actor state persists across invokes** where the backend can provide it —
  a capability, not an invariant (see the Jobs section for the honest
  degradation).
- **Serialization within an actor is defence-in-depth**, not the admission
  mechanism. The harness still refuses to run two episodes at once (a shared
  state directory makes overlap a data race), but it will never be asked to
  under a healthy control plane.

Two small changes to the existing engine ship with Plan 5 (everything else
this spec adds is new code beside it): the hardcoded
`TemplateRef{Name: "harness-v1"}` becomes configuration
(`NIGHTSHIFT_HARNESS_TEMPLATE`), because the template names a deployed
harness release, not a constant — see the template mapping below — and
`dispatch` stamps the workflow's `actor_invoked_at` (two new timestamp
columns; see the suspender loop).

## Harness containerization

A new `cmd/harness` binary wraps the existing `internal/harness` library —
the library is untouched; what's new is transport. One image, two entrypoints:

```
harness serve   Substrate mode: HTTP server, long-lived, suspendable
harness once    Jobs mode: execute one run from env, exit
```

**`harness serve`** listens on `:8081`:

- `POST /invoke` — body `{"run_id": "...", "run_token": "..."}` (the wire form
  of `compute.InvokeRequest`). Replies **202 after accepting** the episode
  into its queue, then executes it asynchronously — the response is the
  acknowledgment the invoker retries against, which is what makes the
  cold-boot lost-request window (spike C4) closable by retry.
- **Serial execution:** one episode at a time, matching `local.go`'s guard.
- `GET /readyz` — 200 once the server accepts invokes. Declaring `readyz` in
  the template lets Substrate skip its 20 s golden-snapshot warmup wait
  (spike C2).
- **Dedupe by RunID needs no journal — the run-token lifecycle already
  provides it.** Every episode begins with
  `GET /internal/runs/{id}/context`, which the internal API authorizes
  against the run's stored token hash and active status, and finalization
  atomically clears that hash. So a duplicate `POST /invoke` — engine retry,
  `Redispatch`, router-level replay — queues behind the serial executor, and
  by the time it executes the original episode has finalized: its context
  fetch gets 401/403 and the episode is discarded without side effects. The
  three cases fall out correctly: a duplicate of a **completed** run is
  rejected by the cleared hash; a duplicate of a run whose episode **crashed
  before finalizing** re-executes — the at-least-once behavior the engine
  wants (a persisted journal, considered and rejected, would wrongly
  suppress exactly this case and permanently strand the run until the
  reaper); an episode that hits a **transient context-fetch failure** simply
  ends without journaling anything, so the invoker's retry re-runs it.
- **A forged invoke is inert.** The harness holds no verification key; it
  simply uses the presented run token, and a forged or stale token fails the
  same first call — the internal API checks signature, stored hash, and
  run/path binding. Nothing is recorded, nothing is poisoned; the harness
  discards the episode and stays idle.

**`harness once`** reads `NIGHTSHIFT_RUN_ID` / `NIGHTSHIFT_RUN_TOKEN` from the
environment, executes the episode, exits 0 if the run record was delivered
(regardless of run status — a `failed` run that finalized is a successful
delivery), non-zero otherwise.

**All harness egress goes to one base URL.** `NIGHTSHIFT_PROXY_BASE` is the
proxy's address; LLM calls use `{base}/proxy/llm/{provider}` and the internal
client's base becomes `{base}/proxy/internal` (the pass-through the proxy spec
added exactly for this flip). The harness image carries **no credentials and
no keys** — its only secret is the per-run token delivered at invoke time.

**Image:** static Go binary on a distroless base plus CA roots; built by CI
with the digest recorded — Substrate's ActorTemplate CEL rejects non-digest
image references (spike C2), so the build pipeline emits digests, not tags.

## The Substrate implementation

### Identity mapping

| Nightshift      | Substrate                             | Notes                                                                                                                                                                                                                                                                                                                                                                                                                               |
| --------------- | ------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Tenant          | **atespace** `t-<tenant-uuid>`        | The spike corrected the platform spec: atespace is upstream's intended per-tenant isolation unit (identity, SPIFFE scope, snapshot grant model). One per tenant aligns us with where enforcement is converging. Created lazily by `EnsureActor` (get-then-create, idempotent) so tenant creation doesn't couple to Substrate availability. Deleting a tenant empties then deletes the atespace — offboarding maps onto it directly. |
| Workflow        | **actor** `wf-<workflow-uuid>`        | Long-lived; a run is an episode. Both names are valid DNS labels (≤ 63 chars).                                                                                                                                                                                                                                                                                                                                                      |
| Harness release | **ActorTemplate** `harness-<version>` | See below — templates map to harness releases, **not** workflow versions.                                                                                                                                                                                                                                                                                                                                                           |
| Run             | an invoke + the pushed run record     | Nothing run-shaped exists on the Substrate side.                                                                                                                                                                                                                                                                                                                                                                                    |

**Templates map to harness releases, not workflow versions.** The platform
spec (and the seam's own comment) coupled workflow versioning to ActorTemplate
immutability: "a new version is a new template and a new golden snapshot".
That coupling dissolves under the shipped architecture: the harness fetches
its steps at run time (`GET /internal/runs/{id}/context`), so the actor image
and its golden snapshot contain **no workflow-version data at all**. A
per-version template would cost a golden-actor create/warm/suspend cycle on
every edit and buy nothing. Versioning's governance (immutable versions,
re-approval) lives entirely in our DB, where Plans 1–3 already put it. The
template names the deployed harness release — `NIGHTSHIFT_HARNESS_TEMPLATE` —
and changes only when the harness image does. Templates are created by
deployment tooling at release time, not by the server; the server only
references the name.

**Consequence, stated honestly — harness upgrades reset actor state.** An
actor is a process (RAM + writable layer) built from its template's image;
Substrate has no state-export API, so moving a workflow's actor to a new
harness release means deleting the actor and creating it from the new
template. With today's tool-less harness nothing durable is lost (the harness
keeps no cross-run state at all), but once actor memory is a product feature this
becomes a real migration cost. The upgrade procedure — for each workflow with
no active run: `Destroy` then `EnsureActor` against the new template — is an
operational job, deliberately not automatic. A durable-memory layer that
survives image changes is future work and out of scope here.

### The four methods

```
EnsureActor: GetActor(wf-<id>) in t-<tenant>; if absent, ensure atespace
             then CreateActor(template). Idempotent under concurrency:
             AlreadyExists from CreateAtespace or CreateActor is not an
             error — re-read and return the existing resource (two
             replicas racing get-then-create must both succeed). Found-
             with-older-template is left running (upgrades are the
             explicit op above).
Invoke:      POST http://<router-svc>/invoke with Host: wf-<id>.t-<tenant>
             .actors.resources.substrate.ate.dev — the router resumes the
             actor (idempotent, singleflight upstream) and tunnels us to
             the harness. Retry on 5xx/timeout/connection-reset with
             backoff until 202 or a dispatch deadline (~2 min); the
             token-lifecycle dedupe makes retries safe, and router 503s
             are parking-lot backpressure, retryable by upstream design.
Suspend:     Control.SuspendActor. Durable (snapshot to object storage,
             node-free). PauseActor (node-local, resume pinned) is a cheap
             path the spike surfaced; deferred as an optimization — one
             suspension semantics first.
Destroy:     Control.DeleteActor; NotFound is success (idempotent, matching
             Local). Wired to workflow deletion when that endpoint exists.
```

Two details the spike dictates:

- **Addressing:** the actor DNS suffix is a compile-time upstream constant
  served by Substrate's own CoreDNS. Our scheduler does not adopt that DNS —
  `Invoke` dials the router Service directly and sets the `Host` header (the
  spike's live-test pattern). The name never leaks past the `Substrate`
  struct, let alone into our API.
- **Client:** gRPC against `ate-api-server` with the proto **vendored at the
  pinned commit** (`69828945` at spike time; re-pin deliberately, roughly
  monthly — upstream averages one API-affecting change per day and disclaims
  compatibility).

### Suspension is driven by us, polling

The economic premise is that a weekly workflow is suspended ~99.9% of the
time, and nothing upstream was verified to suspend idle actors for us; the
control plane owns it. A **suspender loop** joins the scheduler and reaper in
the engine's family of ticking loops:

- **"Needs suspending" is DB state, not loop memory — two timestamps,
  compared, never cleared.** The workflow row gains `actor_invoked_at` and
  `actor_suspended_at` (both `timestamptz`; migration numbered whenever the
  implementation plan reaches it): the engine's `dispatch` sets
  `actor_invoked_at = now()` on every invoke, the suspender sets
  `actor_suspended_at = now()` after a successful `Suspend`. Each tick
  (default 60 s) the eligibility query is: workflows with
  `actor_suspended_at IS NULL OR actor_suspended_at < actor_invoked_at`
  (the actor has been woken since it was last suspended), **no active run**
  (the admission index makes this a cheap check), and latest run finalized
  more than an idle threshold ago (default 60 s) — call `Compute.Suspend`,
  then stamp. Because the state lives in the DB and both writes are
  monotonic set-to-now (a clear-the-flag design was rejected: a dispatch
  clearing the flag while a slow suspender stamps after it would leave the
  actor awake until the next fire), every interleaving self-heals: a fire
  that lands mid-suspend advances `actor_invoked_at` past the stamp the
  suspender is about to write, so the workflow is simply eligible again
  next tick. A crash between suspend and stamp costs one redundant Suspend
  call; nothing is ever missed and nothing spams the upstream API per tick.
- Suspending an already-suspended actor is a no-op upstream; racing a fresh
  fire is benign — the router's resume-on-request undoes a suspend that lands
  just before an invoke, at the cost of one resume, and the timestamp
  comparison above re-queues the workflow for suspension afterward.
- On `Local` and `KubeJobs`, `Suspend` is a no-op, so the loop is inert
  off-Substrate.

Polling is not a shortcut: the spike confirmed there is no Watch/streaming
API of any kind — upstream's own CLI polls. Any actor-state view we ever
surface ("is this workflow's actor resuming?") is a poll with backoff.

### Snapshots and object storage

The platform spec's "GCS dependency — acceptable?" question dissolved:
storage is pluggable (GCS default, S3 supported), and upstream's own dev
setup runs an in-cluster S3 store. **v1 deploys an in-cluster S3-compatible
object store** (MinIO or rustfs) in the Substrate namespace. This is not just
operational simplicity — it is what makes the egress topology sound: worker
pods must reach object storage to suspend/resume, NetworkPolicy cannot
distinguish the worker's own traffic from masqueraded actor traffic leaving
the same pod, and an in-cluster store keeps that flow entirely inside the
default-deny perimeter. Allowing workers egress to real GCS would hand every
actor a masquerade path to a world-reachable storage API — an exfiltration
channel. Managed object storage can return later via a per-cloud egress
design; it is not a v1 constraint.

**Snapshot garbage collection is ours.** Nothing upstream deletes snapshot
objects, ever. v1 posture: bucket lifecycle rules (age-based expiry, with an
exception horizon comfortably beyond the longest suspend we support) plus a
metric on bucket size; a real GC keyed to actor deletion is follow-up work,
recorded as an open item, not silently ignored.

### Failure behavior

- **Dispatch failures are already handled**: `Invoke` errors flow into
  `engine.failDispatch` (run finalized `dispatch_failed`), and the reaper
  sweeps runs whose harness died mid-episode. Neither changes.
- **After the 202, the reaper is the backstop — by design, not omission.**
  Once the harness acks, the run is marked dispatched and no delivery
  failure can surface to the engine; an episode lost after that point (a
  transient context-fetch failure that exhausts the harness's brief local
  retries, a worker crash that rolls the actor back to its last snapshot)
  leaves the run active until the reaper finalizes it as orphaned at the
  run deadline. This is deliberately the same contract Local compute ships
  today: nothing in the control plane can distinguish a lost episode from a
  slow one without per-run heartbeat state, and a control-plane retry of
  accepted-but-unfinalized runs would duplicate LLM spend against
  legitimately slow episodes. If reap-latency on lost episodes ever hurts,
  the fix is run heartbeats (harness pings, reaper tightens its deadline
  for silent runs) — recorded as future work, not smuggled in here.
- **First fire after actor creation** cold-boots (seconds, not the 257 ms
  resume); the 202-ack-with-retry protocol absorbs it. Golden-memory resume
  is microvm-only; on the v1 gVisor pool a fresh actor cold-boots — accepted.
- **Lost invokes never touch the indexes.** A request lost to a cold boot or
  shed by the router's parking lot (a 503 under pool saturation) is retried
  as the **same RunID against the same run row** — retries create no rows,
  so `run_one_active_per_workflow` and `run_workflow_firetime_unique` see
  exactly one run per occurrence however many delivery attempts it takes,
  and the token-lifecycle dedupe makes an attempt that secretly succeeded
  harmless. Recovery from a lost invoke is always **retry**, never re-fire;
  only exhausting the retry window converts the loss into the terminal
  `dispatch_failed` above.
- **Substrate control-plane outage — occurrences fail fast, honestly.** When
  `EnsureActor`/`Invoke` exhaust the invoke retry window (~2 min), the engine
  finalizes the run `dispatch_failed` — and that is terminal: finalization
  clears the token hash, `dispatchPending` selects only pending undispatched
  rows, and the `run_workflow_firetime_unique` index means that _occurrence_
  can never be re-fired. So an outage produces one visible `dispatch_failed`
  run record per missed occurrence, and the next occurrence tries afresh.
  This is deliberate: it matches the engine's shipped semantics
  (`failDispatch` finalizes immediately), keeps failures visible instead of
  silently deferred, and is exactly the signal Plan 4's alerting consumes.
  Only transient blips inside the retry window are absorbed. Sustained
  outage is the moment the Jobs backend exists for — a deployment-config
  flip, not a data migration, because no Nightshift state lives in Substrate.

## The Kubernetes Jobs fallback

**One Job per run, and honesty about state.** The actor-state story does not
hold here and this spec does not pretend otherwise: a Job pod's filesystem is
an `emptyDir` that dies with the run. "Memory becomes state, not a hack"
(platform spec) is a **Substrate-backed capability**, not a seam guarantee.
Today that costs nothing — the tool-less harness keeps no cross-run state,
and the `<memory>` mechanism was deliberately not ported — but the product
must not promise cross-run actor memory to tenants on a Jobs deployment. The
seam already shapes this correctly (`Suspend` a no-op is a precedent Local
set); what was missing is this paragraph saying it out loud. A per-workflow
PersistentVolume was considered and rejected: it reintroduces
state-without-snapshots (partial-write corruption on pod kill), fights
scheduling, and builds a worse Substrate inside Kubernetes.

Mechanics:

- `EnsureActor` returns a synthetic `ActorID` (`<tenant>/<workflow>`, as
  Local does) without touching the cluster. Idempotent trivially.
- `Invoke` creates a Job named **`run-<run-uuid>`** in the runner namespace:
  the harness image, `args: [once]`, RunID/RunToken in env, proxy base URL,
  labels `nightshift.io/tenant|workflow|run` for log correlation.
  **Job-name idempotency, by state, not blindly:** a duplicate invoke hits
  `AlreadyExists`, and the existing Job's status decides — **active or
  succeeded** is success (the Kubernetes API is enforcing the RunID dedupe
  the token lifecycle provides on Substrate); **terminally failed** means
  delete-then-recreate with the current token, so a `Redispatch` genuinely
  re-attempts a run whose first pod died. Treating every `AlreadyExists` as
  success would let a failed Job absorb the retry and strand the run until
  the reaper. The delete-then-recreate path also shrinks the engine's
  documented residual: a `Redispatch` after a double `MarkRunDispatched`
  failure now delivers its fresh token whenever the first Job has already
  failed — only a still-active Job holding the invalidated token still
  strands until the reaper.
- Job spec: `restartPolicy: Never`, `backoffLimit: 0` (a retried pod is
  duplicate LLM spend; at-least-onceness lives in the control plane, and the
  reaper finalizes orphans), `activeDeadlineSeconds` = run deadline,
  `ttlSecondsAfterFinished` for GC (default 1 h — pods stay long enough to
  debug, run records don't live in pods), resource requests/limits from
  deployment config, and `runtimeClassName: gvisor` where the cluster
  provides it (required posture for production multi-tenancy; plain runc
  acceptable only in dev/CI).
- `Suspend` is a no-op. `Destroy` deletes any Jobs labeled with the workflow
  (best-effort cleanup).

## Egress topology: making the proxy the guarantee

The proxy spec's posture: on cluster compute, "default-deny egress
NetworkPolicy with the proxy as the sole permitted destination makes this
same proxy the guarantee. Nothing in the proxy changes — only the network
around it." The spike then established that **upstream ships no egress
NetworkPolicy at any scope** (ingress-only, pinned by their e2e), that actor
traffic exits via atunnel to a default-deployed `atenet-egress` gateway that
authenticates actors but authorizes every destination, and that a
compatibility masquerade leaks the rest. So every egress policy below is
**ours to author and own** in the deployment manifests.

Default-deny egress (`policyTypes: [Egress]`, empty allow beyond the listed)
applied per component:

| Pods                                        | Allowed egress                                                                                                                                   | Why                                                                                                                                                                                                                                                                              |
| ------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Substrate **workers** (per-pool)            | kube-dns; atunnel peers (worker↔worker mTLS); `atenet-egress` gateway; **only what atelet demonstrably needs, if atelet runs in the worker pod** | The tightest set the layout allows — see the masquerade caveat below. Everything a worker needs is in-cluster by construction (that is what the in-cluster object store buys).                                                                                                   |
| **atelet / node agents** (if separate pods) | kube-dns; Substrate control plane; in-cluster object store                                                                                       | The spike: only atelet touches object storage. Allowances follow the component that needs them — every flow moved off the worker allowlist is a flow a masquerading actor cannot ride. Whether substrate's pod layout permits this split is a **verify-at-implementation** item. |
| **`atenet-egress` gateway**                 | kube-dns; **the Nightshift proxy Service only**                                                                                                  | The choke point. All tunneled actor traffic converges here; its only onward hop is our proxy. No internet.                                                                                                                                                                       |
| **Jobs runner namespace**                   | kube-dns; **the Nightshift proxy Service only**                                                                                                  | The same guarantee with one fewer hop.                                                                                                                                                                                                                                           |
| **Nightshift proxy**                        | kube-dns; Postgres; **control-plane Service**; **internet `:443`**                                                                               | The only pod in the system allowed outbound internet. The control-plane allowance is load-bearing: `/proxy/internal/...` is a reverse proxy to `Config.InternalBase` — without it, every harness context fetch, event, and finalize would fail.                                  |
| **Control plane**                           | kube-dns; Postgres; Substrate control-plane gRPC; router Service                                                                                 | Fires runs and drives suspend; no internet.                                                                                                                                                                                                                                      |

**The masquerade caveat — what this topology does and does not guarantee.**
The compatibility masquerade means non-tunneled actor traffic leaves the
worker pod as worker traffic, and NetworkPolicy cannot tell them apart. Two
consequences, stated plainly:

- **Guaranteed against a fully hostile actor:** no internet egress, no
  direct path to an LLM provider, no credential exposure — because nothing
  on any actor-reachable allowlist has internet access except the proxy,
  which authenticates and permit-checks every request.
- **Not guaranteed by policy alone:** an actor can masquerade to whatever
  remains on its own worker's allowlist. That is why the worker row above is
  minimized and the atelet split matters: each in-cluster service left
  actor-reachable (worst case: the Substrate control plane's gRPC, the
  object store's S3 API) is defended by that service's own authentication,
  not by the network — and the spike notes upstream's authn is early. The
  residual shrinks to zero when upstream lands its masquerade restriction
  (their own TODO) or `EgressPolicy` datapath enforcement; until then the
  conformance test pins the _actual_ reachable set so it can only shrink
  deliberately, never grow silently.

The harness cooperates but is never trusted: its base URLs point at the
proxy, and run records travel `{proxy}/proxy/internal/...` — but the
guarantee above is the policy set's, and it holds against a fully hostile
actor.

**In-cluster hop security, stated plainly:** actor→gateway is mTLS
(upstream's atunnel); gateway→proxy and Job→proxy are plaintext in-cluster
HTTP carrying the run token. The threat model this defends is hostile
_actors_, not hostile nodes or a compromised CNI. Terminating TLS on the
proxy, and authenticating actors by their Substrate-minted SPIFFE certs
(`ActorIdentity.MintCert` — the spike's suggested answer to the proxy
trust-boundary question), are recorded as defence-in-depth follow-ups, not
v1.

### The conformance test — proving "no direct egress"

The proxy spec promised it; this is it. A **probe image** (same base as the
harness, plus a probe binary) runs inside the real sandbox on each backend —
as an actor from a `conformance-probe` ActorTemplate on Substrate, as a Job
in the runner namespace on Jobs — attempts each connection class, and pushes
a verdict through the one channel it is permitted. The CI e2e (kind, per the
spike's verified ~6-minute bring-up) asserts:

1. Direct internet (`https://example.com`) — **fails**.
2. Direct provider (`https://api.anthropic.com`) — **fails** (a permit
   holder still can't skip the proxy).
3. Direct in-cluster targets off the worker allowlist — Postgres, the
   Nightshift control-plane Service — **fail**. Targets that remain on the
   worker allowlist (masquerade caveat: worst case the Substrate control
   plane and the object store, best case neither if the atelet split holds)
   are **pinned**: the test records exactly which are reachable and fails
   on any growth, so the residual set shrinks deliberately and never widens
   silently.
4. `{proxy}/proxy/llm/{provider}` with a valid run token and permit —
   **succeeds** (fake upstream); with a non-permitted provider — **403**.
5. `{proxy}/proxy/internal/runs/{id}/events` with the run token —
   **succeeds**; the same path with a forged token — **401**.

Item 3 is the one a unit test can never give us: it proves the _policies_,
not the proxy. The suite runs on both backends in CI; a policy regression
fails the build, not a customer.

The spike also assigned Plan 5 a **cross-actor state-leak probe**
(suspend/resume state reset is upstream's own untested item): two actors of
different tenants on a shared worker, one writes markers (memory + fs), the
other proves it can't read them across a suspend/resume cycle. Same e2e
suite, Substrate backend only.

## The standalone proxy split

The proxy spec deferred the split's auth decision — shared runner key vs
introspection endpoint — to this plan. **Decision: shared runner key, shared
database, own vault key — not introspection.** The deciding constraint is not
token verification; it is that `AuthSource` is one of four `Deps` the proxy
resolves per request, and the other three (`PermitSource`,
`CredentialSource`, `EventSink`) need the DB regardless. An introspection
endpoint would add a per-request RTT and a liveness coupling while removing
neither the DB connection nor — decisively — the vault: serving decrypted
credentials from the control plane would violate the vault contract
("decryption happens only inside the vault package, only on the proxy's
request path") and the platform spec's "the proxy is the only component that
ever sees a customer credential". Introspection buys nothing and costs the
architecture's best isolation property.

Shape:

- **Same binary, new subcommand.** `nightshift proxy` mounts only
  `proxy.RegisterRoutes` with the same `proxyadapter` wiring; `nightshift
serve` keeps everything else and, in cluster deployments, stops mounting
  the proxy routes and stops reading the platform keys. The grow-in-place
  design made this a wiring change, as intended.
- **Key placement follows function.** Proxy: `NIGHTSHIFT_RUNNER_KEY`
  (verify), `NIGHTSHIFT_VAULT_KEY` (decrypt), platform provider keys,
  `DATABASE_URL`. Control plane: `NIGHTSHIFT_RUNNER_KEY` (sign),
  `NIGHTSHIFT_VAULT_KEY` (KEK mint and connection writes), session key,
  `DATABASE_URL` — and **no provider keys anywhere near it**.
- `Config.InternalBase` points at the control-plane Service (it already
  restricts the forwarded remainder to `internal/`); revocation stays
  cache-free and immediate because both processes read the same rows.
- Local dev keeps the single-process mount — the split is a deployment
  shape, not a code fork.

## Deployment shape

Namespaces and components (manifests live under `deploy/`, kustomize
overlays per environment; written in the implementation plan, not here):

```
nightshift-system    control plane Deployment (serve) · proxy Deployment
                     (proxy) · Postgres (dev: in-cluster; prod: managed)
substrate-system     upstream install at the pinned commit · in-cluster S3
                     object store · WorkerPool (gvisor, fixed replicas)
nightshift-runners   Jobs-backend namespace (empty on Substrate deployments)
```

- **Backend selection:** `NIGHTSHIFT_COMPUTE=local|kubejobs|substrate`, one
  backend per deployment; no mixed routing in v1. Flipping a deployment from
  `substrate` to `kubejobs` loses actor state (accepted by design — that is
  the fallback's contract) and nothing else: every durable record is in
  Postgres.
- **Capacity:** WorkerPool `replicas` is warm capacity and is manually
  scaled (spike C7 — autoscaling is upstream roadmap); the fixed-fleet cost
  question stays open in the platform spec where it lives.
- **Dev/CI:** kind, both backends, using upstream's install scripts at the
  pinned commit; the spike measured the full bring-up at ~6 minutes without
  KVM (gVisor pulls its own `runsc`; microvm auto-disables). The conformance
  suite is the gate.
- **RBAC:** the control plane gets a Role in `nightshift-runners` for Jobs
  CRUD only; nothing in the product path needs cluster-scoped rights.
  Substrate template/atespace administration uses upstream's gRPC, not the
  Kubernetes API.

## Testing

- **Unit (`compute` package):** `Substrate` against a fake gRPC
  control-plane + httptest router — EnsureActor idempotency (present/absent
  atespace and actor, **and two concurrent callers racing get-then-create:
  both succeed on `AlreadyExists`**), invoke retry-until-202 with a
  flapping router, suspend/destroy idempotency on NotFound. `KubeJobs`
  against the fake clientset — Job shape; `AlreadyExists` against an
  **active** and a **succeeded** Job is success; against a **failed** Job
  it deletes and recreates with the current token; Destroy label selection.
- **Harness server:** a duplicate `POST /invoke` of a finalized run queues,
  gets 401/403 on its context fetch, and produces no second episode (the
  token-lifecycle dedupe, tested against a fake internal API); a duplicate
  of a crashed-before-finalize run **re-executes**; serial execution under
  concurrent invokes; a forged-token invoke is discarded with no recorded
  state; a transient context-fetch failure leaves the run re-invokable;
  **process loss immediately after the 202** leaves the run active and
  reap-eligible, never wedged in a state the reaper won't sweep; `readyz`.
- **Suspender loop:** fake Compute records suspend calls — idle workflow
  suspended once and `actor_suspended_at` stamped, active-run workflow
  skipped, stamped-and-not-reinvoked workflow skipped, **a dispatch that
  lands between selection and stamping advances `actor_invoked_at` so the
  workflow is re-eligible next tick (the stale-stamp race)**, a crash
  between suspend and stamp yields one redundant (harmless) suspend, no-op
  backends inert.
- **e2e (kind, CI):** on each backend — fire a workflow through the real
  sandbox path with a fake LLM upstream behind the proxy; assert the run
  record; then the conformance suite above, plus (Substrate) resume-with-
  state across an explicit suspend, and the cross-actor state-leak probe.

## Platform-spec corrections (needed)

Listed for cross-checking; **not applied here**. A separate docs-only PR owns
folding the spike's corrections into the platform spec. Items 1–6 restate the
spike's flagged list; item 7 is new from this design and is **not** in the
spike's list — the docs PR should pick it up (or the platform spec keeps a
pointer here):

1. Control-plane surface: four RPCs → full CRUD (32 RPCs + `ActorIdentity`);
   keep "no exec/attach", add "no watch/streaming — observation is
   poll-based".
2. Snapshots: "stored in GCS" → pluggable object storage (GCS default, S3
   supported); the GCS open question retired; snapshot GC recorded as our
   operational duty.
3. Atespace: "not a security boundary" → the intended per-tenant isolation
   boundary, not yet authz-enforced; isolation still ours today; Nightshift
   adopts atespace-per-tenant.
4. Egress finding rewritten per spike F1: no Kubernetes-layer egress control
   at any scope upstream; default-deployed gateway authenticates but does
   not authorize; per-actor `EgressPolicy` + credential injection landed
   2026-08-28 unenforced. Open question replaced by a tracking posture: keep
   the permit compilable to upstream's `EgressPolicy` (hostname-level rules,
   no per-path semantics theirs can't express).
5. "WorkerPool NetworkPolicy stays as defence in depth" → marked ours to
   build; this spec builds it.
6. Threat-model quotes attributed as design goals ("little to no security
   hardening at this time"); egress default-deny and verified state reset
   noted as unshipped upstream.
7. **New, from this design:** governance primitive #8's "a new version is a
   new template and a new golden snapshot" corrected — templates map to
   harness releases; workflow versions are run-time context (see
   [template mapping](#identity-mapping)). The `compute.go` comment saying
   otherwise is updated during implementation.

## Explicitly out of scope

`server/` and `deploy/` implementation (the plan that follows this spec);
connector egress rules (connector-catalog spec); durable actor memory that
survives harness upgrades; Pause-based cheap suspension; per-actor SPIFFE
authentication to the proxy and proxy TLS (recorded follow-ups); WorkerPool
autoscaling; managed object storage; billing for compute.

## Open questions

- **Does Substrate's pod layout permit the atelet split?** The worker-row
  minimization (masquerade caveat) assumes control-plane and object-store
  egress can move to atelet's pods. If atelet runs inside the worker pod,
  those flows stay actor-reachable and the pinned conformance set is the
  fallback control. Verify at implementation, against the pinned commit.
- **Snapshot GC beyond lifecycle rules** — keyed to actor deletion; needs a
  small design when actor memory becomes a product surface.
- **Re-pin cadence in practice** — monthly is the spike's estimate; the
  first two re-pins will tell us the real cost of upstream's proto churn.
- **When upstream enforces `EgressPolicy`** — resolved in shape, open only in
  timing. Upstream's rules are **network-layer** (hostname/CIDR first-match
  plus header injection); the connector catalog's permit entries are
  **application-layer** (per-operation, per-path, effect-scoped). So upstream
  enforcement, when it lands, replaces only the floor beneath our proxy — the
  default-deny NetworkPolicies this spec authors, and eventually the
  masquerade residual — never the proxy itself. **The proxy has no expiry
  date; it has a shrinking lower half.** The permit's _host-level floor_
  stays compilable to upstream `EgressPolicy` (correction 4) so we can adopt
  that floor when it ships; op-level enforcement and credential injection
  remain ours permanently.
- **Actor state across permit narrowing.** When a new approved version
  narrows the permit, the actor keeps state accumulated under the wider one —
  narrowing bounds future _reach_, not past _knowledge_, and the still-alive
  state can only leave through destinations the narrowed permit allows.
  That is the v1 default, chosen over purge-on-narrow (which would destroy
  the memory feature on every edit). Whether narrowing should _offer_ a
  state reset — the `Destroy`-then-`EnsureActor` op the harness-upgrade
  procedure already defines — is a product decision that belongs with the
  graduated-permits work, where permit transitions are first-class.
- **Suspend timing vs. billing** — if compute cost ever becomes
  tenant-visible, the 60 s idle threshold is a pricing knob, not just an
  efficiency one.

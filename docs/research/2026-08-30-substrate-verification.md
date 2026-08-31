> Historical (2026-08-31): the Substrate thread was closed by the pivot to
> customer-deployed Tomte; the spike's value was extracted into the platform
> spec corrections and [the board](../superpowers/plans/2026-08-31-parallel-sessions.md).

# Agent Substrate — Verification Spike

**Date:** 2026-08-30
**Author:** gambtho
**Verifies:** the "Constraints from Substrate" section of
[`../superpowers/specs/2026-08-30-nightshift-platform-design.md`](../superpowers/specs/2026-08-30-nightshift-platform-design.md),
whose facts came from fetched doc summaries, and the assumptions Plan 5 of
[`../superpowers/plans/2026-08-30-nightshift-platform-roadmap.md`](../superpowers/plans/2026-08-30-nightshift-platform-roadmap.md)
builds on.
**Method:** source inspection of a fresh clone of
`github.com/agent-substrate/substrate` at commit `69828945` (2026-08-28, "Bump Go
to 1.27"), plus a full local bring-up on kind with a live actor exercised. All
file paths below are relative to the substrate repo at that commit.

## Headline results

1. The spec's two load-bearing findings both **changed under verification** — in
   Substrate's favor, but not enough to change what we build _today_:
   - Egress: Substrate is **building exactly the control we built in Plan 2** —
     a per-actor `EgressPolicy` API with hostname allowlists **and credential
     header injection** landed upstream **two days before this spike**
     (2026-08-28), but nothing in the datapath enforces it yet. Our proxy
     remains necessary; it now also has an expiry date question.
   - Logs: verified — there is no retrieval API on the control plane, push-based
     run records stand. Nuance: actor stdout is harvestable from worker pod logs
     via the Kubernetes API, so we do get an ops-debugging path for free.
2. The gRPC surface is a **superset** of the spec's four RPCs: 32 methods across
   full CRUD for actors, templates, atespaces, workers, snapshots, and egress
   policies. No exec/attach anywhere (confirmed). No Watch/streaming either —
   anything we want to observe, we poll or push.
3. The quickstart **works**: kind cluster → control plane → demo actor in ~6
   minutes on this machine (WSL2), and the core economic claim held live —
   suspend → HTTP request → auto-resume in **257 ms** with RAM and file state
   preserved, restored onto a _different_ worker pod than the one it suspended
   from.
4. Churn is real: history restarted 2026-05-13, 721 commits since, accelerating
   (88/mo → 288/mo), ~94 commits touching the control-plane proto in 90 days,
   only tag `v0.0.0`, and the threat model says "little to no security hardening
   at this time" (`docs/threat-model.md:10`). Plan 5's decision to sequence
   Substrate last, behind a seam with a Jobs fallback, is the right call —
   verified, not just asserted.

---

## Claim-by-claim verification

### C1. "Control plane is a gRPC API: CreateActor, ResumeActor, SuspendActor, DeleteActor. No exec or attach."

**Verdict: CHANGED (superset; no-exec confirmed).**

**Evidence.** `pkg/proto/ateapipb/ateapi.proto` defines three services,
registered in `cmd/ateapi/main.go:235-237`:

- `Control` — 32 unary RPCs (`ateapi.proto:25-126`): the four the spec lists,
  plus `GetActor`/`UpdateActor`/`ListActors`, **`PauseActor`**,
  `Get/Create/Update/DeleteActorEgressPolicy`, snapshot reads and tag CRUD
  (`GetActorSnapshot`, `ListActorSnapshots`, `*ActorSnapshotTag`), worker CRUD +
  `DrainWorker`, atespace CRUD, and ActorTemplate create/get/list/delete.
- `ActorIdentity` — `MintJWT`, `MintCert` (`ateapi.proto:1397-1416`): per-actor
  cryptographic identity (SPIFFE
  `spiffe://substrate-actor.local/atespace/${atespace}/actor/${name}`,
  `docs/api-guide.md:549`).
- `Debug` — `DebugClear` (drops the database; dev-only).

No `Exec`/`Attach`/`Shell`/tty RPC exists in `pkg/proto/` or `internal/proto/`;
the only exec-capable proto is Kata's vendored guest-agent API inside
`cmd/ateom-microvm/internal/third_party/`, not exposed by any server.
`kubectl-ate` has no exec/attach/port-forward subcommand. The only path into an
actor is HTTP(S) via the router.

**Impact.**

- Spec correction: replace the four-RPC list with "full CRUD control plane (32
  RPCs); no exec/attach; no watch/streaming".
- Plan 5's `Compute` seam holds, but two upstream distinctions are worth
  surfacing in its design: **Pause vs Suspend** (pause keeps the snapshot
  node-local and resume is pinned to that node; suspend uploads to object
  storage — `ateapi.proto` `ActorState`, `cmd/atelet/main.go:807-861`), and
  `ResumeActorRequest.boot` (skip snapshot, cold-boot). Neither must leak
  through the seam, but the Substrate implementation should use pause/resume
  where node affinity is acceptable — it is the cheap path.
- `ActorIdentity.MintCert` matters beyond Plan 5: see finding F1 — it is a
  candidate answer to the egress-proxy trust-boundary question the roadmap
  lists as owed spec work.

### C2. "ActorTemplate is an immutable CRD referencing image/config; generates a golden snapshot."

**Verdict: VERIFIED (with useful mechanics the spec lacks).**

**Evidence.** `pkg/api/v1alpha1/actortemplate_types.go:656-659` — immutability
is CEL-enforced on the CRD, not convention:

```go
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Spec is immutable"
Spec ActorTemplateSpec `json:"spec"`
```

Golden snapshot generation is automatic: `cmd/atecontroller/internal/controllers/actortemplate_controller.go`
runs a phase machine that creates a golden actor in the reserved `ate-golden`
atespace, resumes it, waits a 20 s warmup (skipped when every container declares
`readyz`), suspends it, and records `Status.GoldenSnapshot`. Container images
**must be digest-pinned** (CEL `self.contains('@')`,
`actortemplate_types.go:301`); max 10 containers; resource _limits_ only;
`ResumeSource: Golden` (resume from golden memory+fs) requires
`sandboxClass: microvm`.

**Impact.** The spec's versioning primitive (#8 — "a new version is a new
template and a new golden snapshot") is confirmed and cheap: template creation
alone produces the golden snapshot. Plan 5 must budget for digest-pinning in
the template pipeline (our image builds must emit digests, not tags) and know
that golden-memory resume is microvm-only — on gVisor pools a new version
cold-boots.

### C3. "Snapshots capture RAM state plus the writable layer, stored in Google Cloud Storage."

**Verdict: CHANGED — capture verified; storage is pluggable (GCS _or_ S3), GCS default.**

**Evidence.** Capture: gVisor uses `runsc checkpoint` — "checkpoint.img …
contains the memory, sentry state, and filesystem deltas"
(`internal/proto/ateompb/ateom.pb.go:1097-1099`, `cmd/ateom-gvisor/main.go:721`,
`runsc.go:164`). MicroVM uses Cloud Hypervisor's snapshot API plus a
`rootfs-upper.tar` of the host-side overlay upper
(`cmd/ateom-microvm/checkpoint.go:40-61`, `rootfsupper.go:19-45`). No CRIU, no
Firecracker.

Storage: a two-method `ObjectStorage` interface
(`cmd/atelet/internal/ategcs/objects.go:38-41`) with **two implementations** —
GCS (`gcs.go:43`) and S3 via aws-sdk-go-v2 (`s3.go:50`) — selected by the
`ATE_STORAGE_BACKEND` env var (`cmd/atelet/main.go:216-244`; GCS is the
default). The kind quickstart runs entirely on **rustfs**, an in-cluster
S3-compatible store (`manifests/ate-install/kind/rustfs.yaml`), which we
observed running in the live bring-up. Only `atelet` touches object storage.
There is no filesystem backend, and nothing ever deletes snapshot objects.

**Impact.** The spec's open question "GCS dependency — acceptable?" **dissolves**:
we can run on any S3-compatible store today (the project itself does, in dev).
Two follow-ups for Plan 5: snapshot garbage collection is ours to operate
(nothing prunes the bucket), and `location` URIs are scheme-agnostic (`gs://`
naming survives even on S3 backends — cosmetic, but confusing in configs).

### C4. "Actors are HTTP-addressable at `<name>.<atespace>.actors.resources.substrate.ate.dev`; atenet-router (Envoy) intercepts, resumes, tunnels."

**Verdict: VERIFIED (from source and live).**

**Evidence.** The DNS suffix is a compile-time constant
(`internal/resources/actor.go:21-23`) served by a custom CoreDNS that answers
with the router Service IP (`cmd/atenet/internal/dns/corefile.go:44-55`). The
router is Envoy + an `ext_proc` processor
(`manifests/ate-install/atenet-router.yaml:269-270`): it parses the actor from
`:authority`, admits through a parking lot, calls `Control.ResumeActor`
(singleflight-deduped, `cmd/atenet/internal/router/ingress/resumer.go:163-210`),
then tunnels to the worker's mTLS `atunnel` listener on :443
(`ingress/ingress.go:86-192`). Resume is idempotent and fast-paths
already-running actors "because the router resumes per routed request"
(`cmd/ateapi/internal/controlapi/workflow_resume.go:74-88`).

Live: `curl -H "Host: my-counter-1.demo...."` against a **suspended** actor
returned in **0.257 s** with `preserved memory count: 3 | preserved file
counter: 3` — state intact across suspend, restored onto a different worker pod
than the previous episode. One caveat: the very first request (cold boot from
template, no snapshot) took the full 10 s and returned an **empty body** — the
request was not replayed into the freshly booted workload.

**Impact.** The invocation model in the spec ("fire a workflow = authenticated
HTTP request; resume sub-second") is confirmed. Plan 5 should treat **first
fire after version creation** specially: cold boot latency is seconds, and a
request racing the boot can be lost — fire-and-poll or retry-on-empty semantics
belong in the Substrate `Compute` implementation. The hardcoded DNS suffix also
means our scheduler addresses actors by a name we don't control; fine, but it
must not leak into our public API.

### C5. "`atespace` is not a security boundary — DNS/logical grouping only. Tenant isolation is ours to enforce."

**Verdict: REFUTED in wording, CONFIRMED in consequence.**

**Evidence.** Upstream now defines the atespace as precisely the opposite of
"not a security boundary": "Atespace is the **isolation boundary** an Actor is
created into" (`pkg/proto/ateapipb/ateapi.proto:595-603`, `docs/glossary.md:26-38`).
It is a global-scoped control-plane record (not a Kubernetes namespace, not a
CRD), forms half of every actor's identity, scopes SPIFFE identities, and the
snapshot layout "exists so that access can be granted per tenant"
(`docs/api-guide.md:379`). **However**, no per-atespace authorization
enforcement exists in the API server today (nothing in `internal/ateapiauth`
keys on atespace) — the boundary is identity/naming convention plus sandbox and
NetworkPolicy layers.

**Impact.** The spec's conclusion stands — tenant isolation is still ours to
enforce — but the spec's _reasoning_ should be corrected: atespace is the
intended per-tenant isolation unit and is converging toward real enforcement.
Design decision for Plan 5: **one atespace per Nightshift tenant** (not per
workflow) aligns us with upstream's grant model, DNS scheme, and snapshot
access-control direction. Also operational: an atespace must exist before any
actor (`CreateAtespace`), and cannot be deleted until empty — tenant
provisioning/offboarding maps onto it directly.

### C6. "Threat model: mutually-untrusting multi-tenant, gVisor/microVM mandated, default-deny ingress/egress, full state reset, credentials not exposed in sandboxes."

**Verdict: VERIFIED as stated goals; PARTLY REFUTED as shipped behavior.**

**Evidence.** The threat model says all of it (`docs/threat-model.md:65,94,96,106,108`),
and sandboxing is real: `sandboxClass` is a closed enum `gvisor|microvm`
(`pkg/api/v1alpha1/sandboxconfig_types.go:26-30`) — there is **no plain-runc
class at all**. Ingress default-deny is real at two layers (per-pool
NetworkPolicy admitting only the router; atunnel rejecting mis-addressed
requests with 421). But **egress default-deny is not implemented**: the worker's
nftables `forward` chain policy is _accept_, non-tunneled actor traffic is
masqueraded out, and the code carries the TODO "Restrict the compatibility
masquerade to DNS … and drop all other non-tunneled actor egress"
(`internal/ateomnet/net.go:224-295`). State reset between actors is a stated
requirement (T-27) with partial implementation (nftables/netns/microVM cleanup)
and the docs themselves flag it as needing testing. And the threat model opens
with: "Substrate is an early, fast moving product… It also has little to no
security hardening at this time" (`docs/threat-model.md:10`).

**Impact.** The spec quotes the threat model as if it described the product;
it describes the _target_. Two concrete consequences: (a) the platform spec's
"actors get no direct egress at all" must be **our** cluster's NetworkPolicy,
because Substrate does not deliver it today — this belongs in Plan 5's
deployment work and in the egress-proxy spec's "how 'no direct egress' is
proven" item; (b) the spec's "verified state reset" open question is upstream's
own open item too (their docs propose honeypot testing) — our Plan 5 acceptance
tests should include a state-leak probe across suspend/resume on a shared
worker.

### C7. "WorkerPool CRD defines warm capacity and `spec.sandboxClass`."

**Verdict: VERIFIED.**

**Evidence.** `pkg/api/v1alpha1/workerpool_types.go:82-118`: `replicas`
(required — warm capacity _is_ the replica count; no separate warm/standby
field), `sandboxClass` enum `gvisor|microvm` defaulting to gvisor, pod template
knobs, `sandboxConfigName`. Warm-pool autoscaling is roadmap-only
(`docs/roadmap.md:25`). GPU resources are gvisor-pools-only (CEL,
`workerpool_types.go:80-81`).

**Impact.** None on the seam. For capacity planning (the spec's "fixed cost
before customer one" question): warm capacity is manually scaled today; our
scheduler will need to drive `replicas` itself or accept fixed fleets.

---

## The two load-bearing findings

### F1. "Egress policy is per-WorkerPool NetworkPolicy only; no per-actor egress control has landed."

**Verdict: CHANGED — materially, and two days before this spike.**

**Evidence, in layers:**

1. The per-pool NetworkPolicy exists but is **ingress-only**. The controller
   comment is explicit: "Egress is left unmanaged by Kubernetes NetworkPolicy
   for now while waiting for further progress on Egress API designs"
   (`cmd/atecontroller/internal/controllers/networkpolicy_controller.go:119-121`),
   and an e2e test _pins_ egress-unmanaged as the contract
   (`internal/e2e/suites/networkpolicy/networkpolicy_test.go:113-116`). So the
   spec's premise ("NetworkPolicy at the WorkerPool boundary" controls egress)
   was already stale: at the Kubernetes layer there is **no egress control at
   any scope**.
2. There is a real egress datapath the spec didn't know about: actor TCP is
   transparently redirected (nftables REDIRECT) into `atunnel`, which CONNECTs
   to a central **`atenet-egress` gateway** (Envoy + ext_proc) over mTLS using
   the actor's minted identity certificate. Deployed by default
   (`manifests/ate-install/ate-api-server.yaml:102`); we observed it running in
   the bring-up. Today it **authenticates the actor and authorizes every
   destination** (`cmd/atenet/internal/router/egress/egress.go:88-193`).
3. A per-Actor **`EgressPolicy` API landed 2026-08-28** (commits `01e92cca`,
   `ac609340`, `4e5a5a28`, `3799bff8`): ordered first-match rules over hostname
   wildcards / CIDRs / all, **default-deny when no rule matches**, and
   `EgressRuleEffects.inject_static_headers` → `CredentialHeaderInjection`
   resolving `substrate-secret://<provider-class>/<provider-name>/…` URIs into
   request headers (`pkg/proto/ateapipb/ateapi.proto:310-438`). That is,
   rule-for-rule, the permit-plus-credential-injection design of our egress
   proxy.
4. **Nothing enforces it yet.** `EgressPolicy` has zero references outside the
   API server and its store — the gateway never fetches it. Stored and
   validated, not enforced. A TLS-MITM credential-injection stack (sdsmint,
   `egress-mitm.ate.dev` trust bundle) exists as a non-default overlay with
   only its trust half e2e-tested.

**Impact on the spec and plans.**

- The spec's threat assessment ("directly threatens the product's core
  promise") was right, and the mitigation we shipped (Plan 2's proxy, merged)
  remains **necessary today** — nothing upstream enforces a permit yet.
- But the spec's open question "does per-actor egress policy land before we
  need it?" has half-landed: the **API** is upstream now, enforcement is
  clearly imminent (the gateway, the identity certs, and the MITM stack are all
  in place waiting to consume it). The realistic 6-month outcome is that
  Substrate enforces hostname-level permits with credential injection natively.
  Our proxy then becomes defence-in-depth _or_ migrates to programming
  upstream's `EgressPolicy` — and because their rule model (hostname/CIDR
  first-match + header injection) is nearly isomorphic to our permit, we should
  **keep our permit compilable to their `EgressPolicy`** and avoid semantics
  theirs can't express (e.g. per-path rules) unless the product demands them.
- New input for the owed egress-proxy trust-boundary spec: Substrate already
  mints per-actor mTLS identity certificates (`ActorIdentity.MintCert`,
  SPIFFE-shaped) and its gateway authenticates actors with them. That is a
  ready-made answer to "how does an actor authenticate to the proxy" when we
  run on Substrate — stronger than the bearer run-token we use today.
- Defence-in-depth correction (see C6): the WorkerPool NetworkPolicy the spec
  planned to keep "as defence in depth — default-deny, with the proxy the only
  permitted destination" **does not exist upstream for egress**. We must author
  that NetworkPolicy ourselves in Plan 5's deployment manifests.

### F2. "There is no log or event retrieval API — records must be pushed."

**Verdict: VERIFIED.**

**Evidence.** No log/event/output RPC exists on `Control`; no streaming or
Watch RPC exists at all (checked against every `Control_*_FullMethodName` in
`pkg/proto/ateapipb/ateapi_grpc.pb.go`). What exists instead: actor
stdout/stderr is forwarded into the **worker pod's** stdout as structured JSON
tagged with `ate.*` identity labels (`internal/actorlog/logger.go:15-23`), and
`kubectl ate logs` reads it via the plain Kubernetes pod-logs API, polling
`GetActor` every 2 s to chase worker migrations
(`cmd/kubectl-ate/internal/cmd/logs_actors.go:80,214`). Docs: "To view
historical logs across past worker pods and suspension cycles, use a
centralized logging backend" (`docs/observability.md:37`). Telemetry is
likewise push (OTLP relay through atelet).

**Impact.** The spec's architecture consequence — harness **pushes** run
records to our control plane — is confirmed and stays. Two refinements: (a)
since we operate the cluster, worker-pod logs give us an _operator_ debugging
channel (multiplexed, label-filtered) at zero cost — worth wiring into our own
centralized logging, but never into the product's run records; (b) no Watch API
means our control plane's actor-state views (e.g. "is this workflow's actor
still resuming?") are poll-only — Plan 5 should budget a poller with backoff,
as upstream's own CLI does.

---

## Additional findings not in the spec

- **Project health.** Public history begins 2026-05-13 (squashed); 721 commits
  by 2026-08-28, accelerating monthly (88 → 157 → 188 → 288); 30+ contributors,
  led by well-known Kubernetes maintainers (the top four are kind's creator and
  other sig-network/sig-node veterans); single tag `v0.0.0`; README disclaims
  backward compatibility outright. ~94 commits touched control-plane protos in
  the last 90 days — roughly one API-affecting change per day, including
  renames/drops (`e1adb331` dropped a status field at HEAD). **Plan 5 should
  vendor the proto at a pinned commit and expect to re-pin monthly.**
- **Bring-up (attempted and succeeded).** On WSL2 with Docker 29.7.2 and 20
  cores: `hack/create-kind-cluster.sh` (~3 min) → `hack/install-ate-kind.sh
--deploy-ate-system` (~2.5 min, builds all images with ko; Go 1.27
  auto-toolchain) → `--deploy-demo-counter` (~1 min) → atespace + actor via
  `kubectl-ate` → live traffic. No cluster fights; the only wart is the
  deprecation warning for `ClusterTrustBundle v1beta1` on every CLI call and
  the empty-body first response on cold boot (C4). MicroVM class was
  auto-disabled (no `/dev/kvm` exposed) — gVisor needs no host install (atelet
  pulls a pinned nightly `runsc` at runtime). A dev/test loop for Plan 5 on
  plain kind is therefore **feasible and cheap**, including in CI where KVM is
  absent.
- **Postgres, not Redis**, backs the control plane (`atepg` replaced `ateredis`
  in June, commit `60073ecd`) — one fewer moving part than older docs imply.
- **Parking/backpressure** is built into the router (`docs/request-parking.md`,
  `ingress/parking.go`): saturated pools shed requests. Our scheduler must
  treat fire-time 503s as retryable, not workflow failures.

## Spec corrections needed (flagged, not applied)

In `docs/superpowers/specs/2026-08-30-nightshift-platform-design.md`:

1. "Confirmed" list, item 1: replace the four-RPC surface with the real 32-RPC
   CRUD surface (+ `ActorIdentity` service); keep "no exec or attach", add "no
   watch/streaming".
2. Item on snapshots: "stored in Google Cloud Storage" → "stored in pluggable
   object storage (GCS default, S3 supported; dev uses an in-cluster S3
   store)". Retire the GCS-coupling open question; add snapshot GC as our
   operational duty.
3. Item on atespace: replace "not a security boundary" with "the intended
   per-tenant isolation boundary, not yet enforced by authz — isolation is
   still ours to enforce today"; adopt atespace-per-tenant.
4. Finding 1 (egress): rewrite per F1 above — Kubernetes-layer egress control
   does not exist at any scope; a default-deployed egress gateway
   authenticates-but-does-not-authorize; the per-actor `EgressPolicy` +
   credential-injection API landed 2026-08-28 unenforced. Update the "does
   per-actor egress land" open question to a tracking task with a concrete
   convergence plan (permit → `EgressPolicy` compilability).
5. "WorkerPool NetworkPolicy stays as defence in depth" (egress proxy section):
   mark as **ours to build** — upstream's pool NetworkPolicy is ingress-only by
   pinned contract.
6. Threat-model quotes: attribute as design goals, noting "little to no
   security hardening at this time", and that egress default-deny and verified
   state reset are unshipped upstream.

For Plan 5, when written: pin a substrate commit; one atespace per tenant;
digest-pinned template images; pause-vs-suspend awareness; cold-boot/first-fire
retry semantics; poll-based actor-state observation; author the worker egress
NetworkPolicy ourselves; include a cross-actor state-leak test; budget snapshot
GC.

## What was not verified

- GKE/production deployment path, microVM class (no KVM in this environment),
  the MITM credential-injection overlay, and multi-node scheduling behavior.
- Performance beyond a single actor (their benchmarking suite exists but was
  not run).
- State-reset guarantees between hostile actors — upstream flags this as
  untested; we observed correct state carry-over for one actor, not isolation
  between two.

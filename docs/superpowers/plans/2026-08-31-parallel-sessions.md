# Nightshift — Parallel Session Coordination

**Date:** 2026-08-31
**Purpose:** the live picture of which work runs in parallel, which work is
serialized on `server/`, and ready-to-paste prompts for the next sessions.
Update this file when a plan merges or a session finishes.

## State of the world

| Thread                          | State                                                          |
| ------------------------------- | -------------------------------------------------------------- |
| Plan 1 — Foundation             | **Merged** (PR #1)                                             |
| Plan 2 — Egress proxy + vault   | **Merged** (PR #5)                                             |
| Plan 3 — Scheduling + metering  | **Executing** (branch `sched-spec`; owns `server/`)            |
| Identity spec                   | Written (PR #6) — implementation queued                        |
| Connector-catalog spec          | Written — plan+implementation queued                           |
| Substrate verification spike    | Findings written — feeds the Plan 5 spec                       |
| User research / facilitator kit | Separate session (branch `demo/dev-persona`) — always parallel |

## The rule

**One session owns `server/` at a time.** Everything else — specs, research,
`src/`, future `web/` — parallelizes freely. Doc PRs merge to `main` whenever
ready.

### Serialized `server/` queue

1. **Plan 3** (running now) →
2. **Identity implementation** (small, invasive: replaces the session
   mechanism across httpapi and every test helper; retires
   `NIGHTSHIFT_SESSION_KEY`) →
3. **Connectors** (plan + implementation — collides with `permit.Parse`,
   the proxy, and the harness, and wants identity's session changes
   settled) →
4. **Plan 4 implementation** → 5. **Plan 5 implementation**

### Parallel-safe lanes, open now

- Merge all outstanding spec PRs to `main`.
- **Plan 4 spec** (grading + alerting) — prompt below.
- **Plan 5 spec** (Substrate/K8s Compute) — prompt below.
- **Identity plan-writing** — allowed against the delta sheet below;
  execution still gated on Plan 3's merge.
- Anything in `src/`, user research, prototype work.

---

## Delta sheet: what Plan 3 changes (for the identity session's plan)

Write the identity implementation plan against merged `main` PLUS these
deltas; re-verify each against the real tree before executing.

- **Migrations 00007 and 00008 are taken** (`00007_schedule.sql`,
  `00008_scheduling_runs.sql`); identity's migrations start at **00009**.
- `httpapi.Deps` is now
  `{Store *store.Store; SessionKey []byte; Engine *engine.Engine; Vault *vault.Master}`
  — `Signer` and `Compute` moved inside
  `internal/engine.Engine{Store, Signer, Compute, TokenTTL, Now}`. The
  session surface (`RequireSession`, `SessionCookie`, `SessionClaims`,
  `ClaimsFrom`) is otherwise untouched — that is identity's surface to
  replace.
- `store.Tenant` gains `MonthlyCapCents *int`; `CreateTenant`'s
  SELECT/RETURNING lists include `monthly_cap_cents`.
- `store.CreateRun` is now
  `(ctx, tenantID, workflowID, id, version int, tokenHash, fireReason string, fireTime *time.Time)`;
  `store.FinalizeRun` gained a trailing `perRunCapCents int`. New sentinels
  `store.ErrActiveRun` / `ErrAlreadyFired`; new run columns `fire_time`,
  `dispatched_at`; a one-active-run-per-workflow unique index (a second
  concurrent fire is a 409).
- `store.VersionDoc` gains `Schedule json.RawMessage`; `workflow_version`
  has a `schedule jsonb` column; the permit gained an optional
  `spend.per_run_cents`.
- New env vars: `NIGHTSHIFT_RUN_TOKEN_TTL` (default 1h),
  `NIGHTSHIFT_RUN_DEADLINE` (default 2h, must exceed the TTL),
  `NIGHTSHIFT_DEFAULT_MONTHLY_CAP_CENTS` (default 0). `serve()` starts
  scheduler and reaper goroutines and wires the meter as the proxy's
  `Hook` — identity's `serve()` edits must not displace those.
- Test helpers: `httpapi_test.newEnv` builds an `engine.Engine` over a
  `fakeCompute`; the e2e file has a shared `newDoHelper`. Identity's
  session replacement touches `newEnv`, the `SessionCookie` call sites in
  e2e, and `dev-session` in `main.go`.
- **Execution gate: do not touch `server/` until the Plan 3 PR merges and
  the coordinating session gives the green light.**

---

## Session prompt — Plan 4 spec (rubric grading + alerting)

> Design Nightshift's rubric grading + alerting (Plan 4 of the roadmap), in
> `/home/tng/workspace/nightshift` (branch `main`). Use
> `superpowers:brainstorming` (architectural) and run `my:blindspot-pass`
> before locking the design. Read first:
> `docs/superpowers/specs/2026-08-28-nightshift-design.md` (the alert
> surface — four blocks, auto-pause after 3 consecutive failures, "silence
> is good"), `docs/superpowers/specs/2026-08-30-nightshift-platform-design.md`
> (primitive 2: grading is what lets an alert name a specific broken
> promise; grader cost doubles per-run inference — an open question to
> answer), the roadmap (Plan 4 row: grader + auto-pause + alert delivery +
> the `internal/publish`/`internal/template` port from
> `/home/tng/workspace/cronfoundry`, read-only), and
> `docs/superpowers/specs/2026-08-31-nightshift-scheduling-metering-design.md`
> (the `spend.exceeded` run-event shape yours consumes; the pause/resume
> switch was explicitly deferred TO you — you own its semantics, including
> the scheduler honoring it). Design at minimum: the rubric artifact's
> gradeable-criteria schema (today it's opaque JSON), the grader (which
> model, how invoked — note the egress proxy governs all LLM traffic, so
> the grader either goes through it or gets a deliberate platform-internal
> exemption you must justify), per-criterion run scoring storage, the
> 3-consecutive-failures auto-pause trigger and the mutable pause switch,
> alert content/delivery (email+push per the UX spec; the publish port),
> and cost controls for grading. Deliverable: a spec in
> `docs/superpowers/specs/`, own branch in a linked worktree under
> `~/workspace/nightshift-worktrees/<name>` (a hook blocks the primary
> checkout; commit as `gambtho <thomasgamble2@gmail.com>`; prettier
> markdown), PR to main. **Spec only — no implementation plan, no `server/`
> changes** (Plan 3 and the identity implementation are queued ahead of
> you).

## Session prompt — Plan 5 spec (Substrate + K8s-Jobs Compute)

> Design the cluster-facing `Compute` implementations (Plan 5), in
> `/home/tng/workspace/nightshift` (branch `main`). Use
> `superpowers:brainstorming` (architectural). Read first: `docs/research/`
> for the Substrate verification spike findings (your primary input —
> where the spike refuted or changed a platform-spec claim, your design
> follows the spike, and you list the platform-spec corrections needed),
> `docs/superpowers/specs/2026-08-30-nightshift-platform-design.md` (the
> Compute seam and both constraints),
> `docs/superpowers/specs/2026-08-30-nightshift-egress-proxy-design.md`
> (enforcement posture: your NetworkPolicy makes the proxy the guarantee;
> the `/proxy/internal/` pass-through exists so the proxy can be the sole
> egress; the standalone-proxy split decision — shared runner key vs
> introspection endpoint — was explicitly deferred to you), and
> `server/internal/compute/compute.go` + `local.go` on main (the interface
> you implement and the semantics to preserve: per-actor serialization,
> persistent state, idempotent EnsureActor). Design: the Substrate
> implementation (ActorTemplate mapping to workflow versions,
> invoke-over-HTTP resume flow, snapshot/GCS posture), the Kubernetes-Jobs
> fallback (stateless — reconcile that honestly with the actor-state
> story), the default-deny NetworkPolicy + proxy-as-sole-egress topology
> with the conformance test the proxy spec promises, harness
> containerization, and the deployment shape. Deliverable: spec in
> `docs/superpowers/specs/`, own branch in a linked worktree under
> `~/workspace/nightshift-worktrees/<name>` (hook blocks primary checkout;
> gambtho identity; prettier), PR to main. **Spec only; no `server/` or
> `deploy/` changes yet.**

## Also owed eventually

- Connector implementation plan (after identity lands; written against
  the then-current tree).
- Platform-spec corrections from the Substrate spike (folded in by the
  Plan 5 spec session or a small doc PR).
- The user-research read-out feeding UX changes back into the prototype.

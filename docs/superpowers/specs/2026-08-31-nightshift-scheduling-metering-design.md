# Nightshift Scheduling + Spend Metering — Design

**Status:** Design approved in conversation; spec awaiting review
**Date:** 2026-08-31
**Author:** gambtho
**Parent:** [`2026-08-30-nightshift-platform-design.md`](./2026-08-30-nightshift-platform-design.md) — this designs governance
primitives 3 (spend metering and caps) and 4 (scheduling), Plan 3 of the
[roadmap](../plans/2026-08-30-nightshift-platform-roadmap.md). It closes roadmap
decision 10 (the orphaned-run reaper and the run-token TTL vs queueing revisit).

## Scope decisions

| Decision                                 | Why                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **The schedule is a versioned artifact** | A fourth artifact alongside steps/permit/rubric. "Every Monday at 9" is part of the job the user approved; changing the cadence is an edit requiring re-approval, consistent with approve-once. Pause/resume is deliberately NOT here — that mutable switch arrives with Plan 4's auto-pause, which owns its semantics.                                                                                                                                      |
| **Tenant cap is monthly, calendar UTC**  | A cents-per-calendar-month cap on the tenant row with a platform default. Matches how people think about bills; resets on the 1st, UTC. The rolling-window alternative is smoother but harder to explain to a non-technical user and costlier per check.                                                                                                                                                                                                     |
| **Per-run cap lives in the permit**      | The UX spec draws the spend cap inside the approved blast radius; the permit is the approved document, so the cap goes there (`spend.per_run_cents`, optional).                                                                                                                                                                                                                                                                                              |
| **In-process engine, index-coordinated** | A ticker inside `serve()`, not a job queue or an external cron. The `fire_time` partial unique index makes duplicate fire attempts harmless, which is all the multi-instance safety one process needs; a `FOR UPDATE SKIP LOCKED` claim queue is the documented scale-up path, not the v1. An external scheduler would push core semantics (fairness, overlap, catch-up) into deployment config — the wrong side of the boundary for a governance primitive. |
| **One firing path**                      | `httpapi.fireRun`'s core moves into an `engine.Fire` service; the HTTP handler and the scheduler both call it. Two firing paths would drift.                                                                                                                                                                                                                                                                                                                 |

## The schedule artifact

New nullable `schedule jsonb` column on `workflow_version`; `Schedule` field on
`store.VersionDoc`; same create/add-version/read endpoints. A workflow without a
schedule is manual-only — exactly today's behavior.

```json
{ "cron": "0 9 * * MON", "tz": "America/New_York" }
```

Validation at the workflow API, fail closed, permit-style:

- `cron` parses under `robfig/cron/v3`'s standard 5-field parser (no seconds
  field, no `@`-descriptors in v1 — fewer surprises to explain).
- `tz` loads via `time.LoadLocation`; IANA names only.
- Both fields or neither; unknown fields rejected; sub-minute cadence is
  impossible by construction (5-field cron floors at 1 minute), and the parser
  choice is part of the contract.
- Because the artifact is versioned, a run always fires from the schedule its
  approved version declares.

## Firing

### Idempotency (migration)

`run` gains `fire_time timestamptz` (NULL for manual fires), plus:

```sql
CREATE UNIQUE INDEX run_workflow_firetime_unique
    ON run (workflow_id, fire_time) WHERE fire_time IS NOT NULL;
ALTER TABLE run ADD CONSTRAINT run_fire_reason_time_consistent
    CHECK ((fire_reason = 'schedule') = (fire_time IS NOT NULL));
```

This is cronfoundry's proven idempotent-tick shape, harvested deliberately.
A duplicate fire attempt — second process, restart mid-tick, clock skew —
loses on the index and is treated as success (a specific `ErrAlreadyFired`
maps to a no-op). **The index is the coordination**; there is no leader
election at this scale.

### The tick

A 60-second ticker in `serve()`:

1. Load workflows with an approved version carrying a schedule.
2. For each, compute the most recent due occurrence in the schedule's own
   zone (`cron.Next` walked from the last observed minute boundary).
3. For anything due in the window, run the pre-fire checks (below), then call
   `engine.Fire(ctx, tenantID, workflowID, version, "schedule", fireTime)`.

`engine.Fire` is the extracted core of today's `fireRun`: sign run token →
`CreateRun` (now with `fireTime`) → `EnsureActor` → `Invoke` → `failDispatch`
on dispatch errors. The engine takes an injectable `Now func() time.Time` so
tests drive the clock.

### Catch-up policy: skip

Missed occurrences while the server was down do not replay — only the current
due occurrence fires. A Monday digest arriving twelve times after an outage is
the worse failure; skipped occurrences are visible as gaps in run history.

### Overlap policy: skip-while-active

If the workflow already has a run in `pending`/`running`, the tick skips this
occurrence (the parent spec's "default to serialize" applied at the scheduling
layer — the per-actor lock already serializes execution; this prevents a
backlog forming behind a stuck run, which also protects the run-token TTL).
Skips log through the redacting handler with workflow/tenant IDs; a durable
skip record is deliberately deferred until Plan 4's alerting needs one.

### Pre-fire checks

The scheduler consults the meter before firing: a tenant at its monthly cap
gets a skip-and-log, not a run that is doomed to a 429. Manual fires are
allowed through to fail loudly — a person is watching those.

## Spend metering

### Per-run cap — permit extension

Permit v1 gains an optional object (a deliberate schema change in
`permit.Parse`; strictness retained):

```json
{ "v": 1, "llm": { ... }, "spend": { "per_run_cents": 50 }, "connections": {} }
```

**Enforcement honesty.** Runs are single-model-call today, so `Hook.Before`
sees zero accumulated spend for the current run — the per-run cap cannot block
its own run's only request. In v1 it is enforced post-hoc: finalization
compares `cost_cents` to the cap and appends a `spend.exceeded` run event,
which is precisely the input Plan 4's alerting consumes ("which rule it's
missing"). Pre-request enforcement becomes real when multi-turn runs arrive
(connector work) — through this same hook, with no proxy changes.

### Tenant cap — monthly, UTC

- `tenant.monthly_cap_cents int` (nullable). NULL → platform default from
  `NIGHTSHIFT_DEFAULT_MONTHLY_CAP_CENTS`; `0` = unlimited (dev).
- `meter.Hook` (new `server/internal/meter` package implementing
  `proxy.Hook`) sums `cost_cents` of the tenant's **finalized** runs in the
  current UTC calendar month; at/over cap →
  `HookError{Status: 429, Msg: "monthly spend cap reached"}`. The proxy
  already maps that to 429 + a `proxy.denied` (reason `"hook"`) audit event;
  the harness surfaces it as `llm_error` and the run finalizes failed.
- **Stated overshoot bound**: in-flight runs are not counted, so a tenant can
  exceed the cap by (concurrent runs × per-run cost). At current scale that
  bound is small; making it exact requires reservation accounting, deferred
  until real billing exists.
- Meter query failure **fails closed**: no spend visibility, no spend.

## The orphaned-run reaper

A 5-minute ticker sweeps
`status IN ('pending','running') AND created_at < now() - deadline` and
finalizes each through the normal `FinalizeRun` path as `failed` /
`error_kind: "orphaned"` — which also clears the token hash, so a zombie
harness waking later is locked out of both the proxy and the internal API.
This covers all three orphaning modes from the Plan 2 branch review: server
restart killing in-flight Local goroutines, a failed context fetch that never
finalizes, and queue waits outliving the token.

**TTL vs queueing, resolved:** both configurable —
`NIGHTSHIFT_RUN_TOKEN_TTL` (default 1h) and `NIGHTSHIFT_RUN_DEADLINE`
(default 2h) — with a startup invariant `deadline > TTL`: a run whose token
has expired can never finalize itself, so reaping after expiry is
guaranteed-safe and reaping before it would be premature.

## Failure handling and observability

Scheduler and reaper ticks are crash-isolated: each tick runs in a recovered
closure — a panic or DB error logs and skips that tick, never kills `serve()`.
Duplicate fires are absorbed by the unique index. The audit story rides
existing surfaces: `fire_reason`/`fire_time` distinguish scheduled runs,
`error_kind: "orphaned"` marks reaped ones, `spend.exceeded` run events flag
per-run overruns, and cap denials appear as the proxy's existing
`proxy.denied` (reason `"hook"`) events. No new public API surface; the v1
endpoints already expose every column involved (the version endpoints gain the
`schedule` field, still under the API's unstable stamp).

## Testing

- **Schedule validation**: accept/reject matrix (bad cron, `@daily`
  descriptors, seconds field, non-IANA tz, one-of-two fields, unknown keys).
- **Engine**: fire-parity (manual vs scheduled runs identical modulo
  reason/fire_time); idempotency (concurrent same-`fire_time` fires → one
  run); catch-up skip (stale last-check fires exactly once); overlap skip;
  a DST-boundary test pinning `robfig/cron`'s behavior across a
  spring-forward in `America/New_York` so a dependency upgrade cannot
  silently change semantics.
- **Meter**: under/at/over cap against real Postgres with runs straddling a
  UTC month boundary; `spend.exceeded` event on finalize; meter-DB-failure
  fails closed; scheduler skip-when-capped.
- **Reaper**: stuck run past deadline → `failed/orphaned` with hash cleared
  (proxy rejects the token afterward); younger runs untouched; the
  `deadline > TTL` startup invariant.
- **e2e**: a 1-minute-scheduled workflow fires through the real engine →
  proxy → fake upstream with no HTTP fire call, driven by the injectable
  clock.

## Explicitly out of scope

Pause/resume and auto-pause (Plan 4); durable skip records (Plan 4, if
alerting needs them); reservation-exact cap accounting and billing; per-tenant
fairness beyond skip-when-capped (a real quota system waits for real load);
leader election / multi-instance serve (the unique index already makes it
safe, just wasteful); catch-up replay of missed occurrences; pre-request
per-run cap enforcement (arrives with multi-turn runs).

## Open questions

- **Fairness under load** — the parent spec names "per-tenant fairness and
  quotas"; v1's tick fires everything due. With few tenants this is moot;
  revisit when a tick can contain more work than a tick interval.
- **`spend.exceeded` and auto-pause** — whether three consecutive exceed
  events should count toward Plan 4's auto-pause trigger is Plan 4's call;
  the event shape here is designed to make that possible.
- **Billing linkage** — monthly caps are guardrails, not invoices; real
  billing will want its own ledger table rather than `SUM(cost_cents)`.

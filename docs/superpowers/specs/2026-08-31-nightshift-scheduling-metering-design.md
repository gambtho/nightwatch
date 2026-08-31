# Nightshift Scheduling + Spend Metering — Design

**Status:** Design approved in conversation; spec awaiting review
**Date:** 2026-08-31
**Author:** gambtho
**Parent:** [`2026-08-30-nightshift-platform-design.md`](./2026-08-30-nightshift-platform-design.md) — this designs the
**v1 slice** of governance primitives 3 (spend metering and caps) and 4
(scheduling), Plan 3 of the
[roadmap](../plans/2026-08-30-nightshift-platform-roadmap.md). It closes roadmap
decision 10 (the orphaned-run reaper and the run-token TTL vs queueing revisit).

## Delivery boundary — what of primitives 3 and 4 lands now

Stated up front so the milestone claims exactly what it delivers:

- **Delivered**: the tenant monthly cap enforced before every model request
  (fail closed); the per-run cap as approved, priced-model-validated data with
  transactional overrun detection; cron+IANA scheduling with at-least-once
  dispatch recovery; DB-enforced single-active-run admission; the reaper.
- **Deliberately narrowed, with the gate named**: pre-request enforcement of
  the _per-run_ cap waits for multi-turn runs (today a run is one model call,
  so there is no "before the next request" moment inside a run — the hook
  seam is ready and unchanged when that arrives); scheduler fairness/quotas
  beyond cap-skip wait for a load level where one tick can exceed a tick
  interval (see Open questions). These are narrowings of the parent's
  primitives, not silent omissions.

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

### Idempotency and admission (migration)

`run` gains `fire_time timestamptz` (NULL for manual fires) and
`dispatched_at timestamptz` (set when `Invoke` succeeds), plus:

```sql
CREATE UNIQUE INDEX run_workflow_firetime_unique
    ON run (workflow_id, fire_time) WHERE fire_time IS NOT NULL;
-- One active run per workflow, DB-enforced: the parent spec's "default to
-- serialize" as an admission rule, not a check-then-fire race. Applies to
-- manual and scheduled fires alike.
CREATE UNIQUE INDEX run_one_active_per_workflow
    ON run (workflow_id) WHERE status IN ('pending', 'running');
ALTER TABLE run ADD CONSTRAINT run_fire_reason_time_consistent
    CHECK ((fire_reason = 'schedule') = (fire_time IS NOT NULL));
```

The `fire_time` index is cronfoundry's proven idempotent-tick shape; the
active-run index is new and load-bearing: a manual fire while a run is active
gets **409 "a run is already active"** (a person is watching), and a
scheduled fire maps the violation to a skip. **The indexes are the
coordination** — no leader election, and no window between checking and
firing.

### The tick — create, then dispatch (at-least-once)

Row creation and dispatch are **separate steps**, because a crash between
them must not lose the occurrence (the unique index alone gives idempotent
_row creation_, not reliable _dispatch_ — cronfoundry's scheduler learned the
same lesson and redispatches pending rows). A 60-second ticker in `serve()`:

1. **Create**: load workflows with an approved version carrying a schedule;
   for each, compute the most recent due occurrence in the schedule's own
   zone; if it is due and within the staleness window (below), and the
   pre-fire checks pass, insert the run row (token signed, `fire_time` set,
   `dispatched_at` NULL). Unique-index violations — either index — are
   skips, not errors.
2. **Dispatch**: select scheduled runs with `status = 'pending' AND
dispatched_at IS NULL` (including rows created by a previous, crashed
   process), call `EnsureActor` + `Invoke`, and set `dispatched_at` on
   success. A row with `dispatched_at IS NULL` provably never reached an
   actor — no model call happened — so redispatch is safe. A crash _after_
   `Invoke` (dispatched but the process died with the work queued) is the
   reaper's case: that run becomes a **visible** `failed/orphaned`, not a
   silent skip.

`engine.Fire` is the extracted core of today's `fireRun` (sign token →
`CreateRun` → dispatch), shared by the HTTP handler and the tick, with an
injectable `Now func() time.Time`. The manual path keeps create+dispatch in
one call; only the scheduler exploits the split.

### Catch-up policy: skip, deterministically

Only the **most recent** due occurrence is ever considered, and only while it
is younger than the staleness window `W = max(2 × tick interval, 5 minutes)`.
Older occurrences never fire, regardless of restarts — no persistent cursor,
no "last observed boundary" state: the rule is a pure function of (schedule,
now). A Monday digest arriving twelve times after an outage is the worse
failure; skipped occurrences are visible as gaps in run history.

### Overlap: admission, not a check

Single-active-run is enforced by the `run_one_active_per_workflow` index at
insert time (see above) — there is no separate skip-while-active check to
race. A consequence worth stating: a run can no longer _queue_ behind an
active run at the run level at all, which is what makes the token-TTL
question tractable (below). Scheduled skips log through the redacting handler
with workflow/tenant IDs; a durable skip record is deliberately deferred
until Plan 4's alerting needs one.

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

**Priced models are a precondition — without this, both caps are vacuous.**
Today a workflow's model is an unrestricted string and `llm.CostCents`
deliberately returns 0 for unknown (provider, model) pairs, so an unpriced
model would spend real money that no cap ever sees. Therefore: `llm` gains
`Priced(provider, model) bool`, and workflow validation **fails closed** — a
steps document naming an unpriced (provider, model) is a 400 at
create/add-version, same style as permit validation. The price table becomes
governance data, not best-effort metadata (its maintenance story is an open
question below).

**Per-run enforcement honesty.** Runs are single-model-call today, so
`Hook.Before` sees zero accumulated spend for the current run — the per-run
cap cannot block its own run's only request. In v1 it is detected at
finalization, **transactionally**: the internal API's finalize handler
resolves the run's permit cap and passes it to `FinalizeRun`, which inserts
the `spend.exceeded` run event and the terminal status update in ONE
transaction (the event insert happens while the run is still non-terminal, so
the terminal-immutability guard is satisfied, and a reaper race cannot
produce a false event — whichever finalization wins the status transition
writes the event). This event is precisely the input Plan 4's alerting
consumes. Pre-request per-run enforcement becomes real when multi-turn runs
arrive — through this same hook, with no proxy changes.

**Run visibility correction**: `fire_reason` exists in the schema but is not
currently scanned or exposed — `store.Run`, `runCols`, and the public run
JSON gain `fire_reason` and `fire_time` as part of this work, so scheduled
runs are distinguishable through the API (the claim below depends on it).

### Tenant cap — monthly, UTC

- `tenant.monthly_cap_cents int` (nullable). NULL → platform default from
  `NIGHTSHIFT_DEFAULT_MONTHLY_CAP_CENTS`; `0` = unlimited (dev).
- `meter.Hook` (new `server/internal/meter` package implementing
  `proxy.Hook`) computes the tenant's month-to-date spend as: `SUM(cost_cents)
OVER runs WHERE tenant_id = $1 AND finished_at >= <UTC month start> AND
cost_cents IS NOT NULL` — month membership by **`finished_at`** (cost exists
  only at finalization), and **all** finalized runs with a recorded cost count,
  failed ones included (they spent real money). Backed by a new partial index
  `ON run (tenant_id, finished_at) WHERE cost_cents IS NOT NULL` so the
  per-request check is an index range scan, never a tenant-wide table scan.
  At/over cap → `HookError{Status: 429, Msg: "monthly spend cap reached"}`.
  The proxy already maps that to 429 + a `proxy.denied` (reason `"hook"`)
  audit event; the harness surfaces it as `llm_error` and the run finalizes
  failed.
- **Stated overshoot bound**: in-flight runs are not counted, so a tenant can
  exceed the cap by (concurrent runs × per-run cost). At current scale that
  bound is small; making it exact requires reservation accounting, deferred
  until real billing exists.
- Meter query failure **fails closed**: no spend visibility, no spend.

## The orphaned-run reaper

A 5-minute ticker sweeps
`status IN ('pending','running') AND COALESCE(dispatched_at, created_at) < now() - deadline`
and finalizes each through the normal `FinalizeRun` path as `failed` /
`error_kind: "orphaned"`. Keying the deadline off the latest **dispatch
episode** (falling back to creation for never-dispatched rows) is deliberate:
a redispatched run carries a freshly signed token, so its reap window must
track the redispatch, and the escalation design's future `awaiting_input`
status (which resumes runs days after creation) depends on exactly this
keying — building it now avoids a retrofit (escalation-spec amendment 2) — which also clears the token hash, so a zombie
harness waking later is locked out of both the proxy and the internal API.
This covers all three orphaning modes from the Plan 2 branch review: server
restart killing in-flight Local goroutines, a failed context fetch that never
finalizes, and queue waits outliving the token.

**TTL vs queueing, resolved structurally:** the single-active-run admission
index means a run can no longer queue behind another run at all — the
expiring-while-queued scenario from roadmap decision 10 is dissolved, not
mitigated. What remains is a run's own lifetime: both knobs are configurable —
`NIGHTSHIFT_RUN_TOKEN_TTL` (default 1h, replacing the hard-coded
`runTokenTTL` const) and `NIGHTSHIFT_RUN_DEADLINE` (default 2h) — with a
startup invariant `deadline > TTL`: a run whose token has expired can never
finalize itself, so reaping after expiry is guaranteed-safe and reaping
before it would be premature.

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
  reason/fire_time/dispatched_at); idempotency (concurrent same-`fire_time`
  fires → one run); **dispatch recovery** (a pending scheduled row with
  `dispatched_at IS NULL` — simulating a crash between create and invoke —
  is redispatched by the next tick, exactly once); **admission** (a second
  fire — manual or scheduled — while a run is active loses on the index:
  manual → 409, scheduled → skip; concurrent attempts race the index, not a
  check); staleness-window skip (an occurrence older than W never fires,
  after any restart); a DST-boundary test pinning `robfig/cron`'s behavior
  across a spring-forward in `America/New_York` so a dependency upgrade
  cannot silently change semantics.
- **Meter**: under/at/over cap against real Postgres with runs straddling a
  UTC month boundary **by `finished_at`**, failed-but-costed runs counted;
  transactional `spend.exceeded` (event and terminal status land atomically;
  a lost finalize race writes no false event); **unpriced model rejected
  with 400 at the workflow API**; meter-DB-failure fails closed; scheduler
  skip-when-capped.
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
- **Price-table governance** — requiring priced models makes the table
  load-bearing: who updates it, and what happens to approved workflows when a
  model is delisted (existing approved versions keep running priced-as-was,
  or fail closed?). Needs an answer before the table drifts.
- **BYOK cap semantics** — when a tenant's own key pays the provider bill,
  the platform monthly cap protects the platform's margin, not the tenant's
  wallet; whether BYOK spend should count against the same cap (and whether
  BYOK-only models can bypass the priced-model rule, since openrouter's
  catalog is unbounded) is deferred to the billing/connector work.

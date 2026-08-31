# Nightshift Escalation — Design

**Status:** Design approved in conversation; spec awaiting review
**Date:** 2026-08-31
**Author:** gambtho
**Parent:** [`2026-08-30-nightshift-platform-design.md`](./2026-08-30-nightshift-platform-design.md)
**Amends:** [`2026-08-28-nightshift-design.md`](./2026-08-28-nightshift-design.md) — specifically
[Why there is no runtime approval gate](./2026-08-28-nightshift-design.md#why-there-is-no-runtime-approval-gate).
**Depends on:** the connector catalog spec (branch `spec/connector-catalog`, unmerged) for
the operation vocabulary an amendment is drawn from.
**Companions:** [`2026-08-31-nightshift-graduated-permits-design.md`](./2026-08-31-nightshift-graduated-permits-design.md)
and [`2026-08-31-nightshift-objectives-design.md`](./2026-08-31-nightshift-objectives-design.md),
both of which emit the escalation object this spec defines.

## Why this amends an explicit decision

The UX spec rejected runtime approval gates, and its reasoning was right:

> A runtime approval gate — pausing a run until a human confirms a tool call — is a
> **stall, not a safeguard** on a scheduled 3AM run: the workflow goes idle and waits for
> someone who is asleep.

That holds for a weekly digest. It does not hold for a delegated outcome that crosses a
consequential boundary — a bill dispute where the agent must ask before accepting a
contractual change, or a follow-up booking where only the patient can choose the slot.
Those are the cases where "meaningful control at every consequential boundary" is the
entire product, and approve-once cannot express them.

This spec keeps the original objection intact by making escalation **asynchronous**. A
waiting run holds no thread, no credential, and no worker. It is not a stall; it is a
suspended actor, which is the normal state of every Nightshift workflow anyway. The
question the original decision actually settled — _may a run block a human at 3AM?_ — is
still answered no.

**Approve-once remains the default.** Escalation is opt-in per workflow.

## Scope decisions

| Decision                                                             | Why                                                                                                                                                                                                                                                    |
| -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Three kinds, one mechanism**                                       | `decision` (choose within the permit), `amendment` (widen the permit), `completion` (declare the objective met) share one table, one API, one surface, one audit trail. Splitting them would duplicate the riskiest screen in the product three times. |
| **An approved amendment _is_ a workflow version approval**           | Reuses immutable versions, the `workflow_version_one_approved` index, and the real `approved_by` FK. No parallel authorization path, and no second definition of "the approved blast radius".                                                          |
| **Escalate-on-deny is opt-in per workflow; default stays hard deny** | Turning every permit denial into a user prompt is exactly the approval fatigue the UX spec warns about, and it trains users to click through. A permit that asks instead of refusing is not a permit.                                                  |
| **Suspension revokes the run token**                                 | Reuses finalize-as-revocation (`00005_run_token_revocation.sql`). A run waiting three days holds no live credential — strictly better than today's one-hour window.                                                                                    |
| **For amendments, the out-of-band message carries no authority**     | The escalation channel is this product's phishing surface. The notification says a decision is waiting; the user authenticates into the app to act.                                                                                                    |
| **Deltas are structural, drawn from the connector catalog**          | The model names `{connector, op}` pairs and resource entries. It cannot author prose scope. The user approves a diagram diff the control plane computed.                                                                                               |
| **Timeout fails closed**                                             | Consistent with permit parsing and the proxy's deny path.                                                                                                                                                                                              |
| **One open escalation per run**                                      | Enforced by a partial unique index. Prevents flooding and keeps the resume path unambiguous.                                                                                                                                                           |

## Run lifecycle

```
pending ──> running ──> succeeded | failed
                │
                ├──> awaiting_input ──> running    (answered)
                └──                 └──> failed    (deadline passed)
```

`awaiting_input` is a new value for `run.status`, whose CHECK constraint in
`00004_run.sql` currently admits exactly four. A migration extends it, and the status
literals spread across the store, proxy adapter, and HTTP API need a sweep.

**Suspending** (`running → awaiting_input`): create the escalation row, clear
`runner_token_hash`, call `Compute.Suspend`.

**Resuming** (`awaiting_input → running`): mint a fresh run token, `Compute.Invoke` with
the answer in the payload.

**Expiring** (`awaiting_input → failed`): `error_kind: "escalation_expired"`.

### Existing guards that must change

Three places assume a run is short-lived and in one of two states. Each was found by
reading the code, not the specs:

- **`AppendRunEvent`** guards on `status IN ('pending','running')`
  (`server/internal/store/run.go:131`). Add `'awaiting_input'`, or a suspended run cannot
  record that it is suspended.
- **`FinalizeRun`** guards on the same pair (`server/internal/store/run.go:117`). Add
  `'awaiting_input'` for the expiry path.
- **`VerifyRunToken`** rejects any run not `pending`/`running`
  (`server/internal/proxyadapter/adapter.go`). **This needs no change.** Clearing the token
  hash on suspend means the harness's old token fails the bind check on its own. The status
  check is redundant belt-and-braces here, and leaving it strict is the safer default.

`InvokeRequest` (`server/internal/compute/compute.go`) gains an optional answer payload;
the interface is otherwise unchanged.

## Reconciliation with Plan 3 (scheduling and metering)

Plan 3 is in flight on branch `sched-spec` and lands three mechanisms that a fourth run
status interacts with directly. Written against
`2026-08-31-nightshift-scheduling-metering-design.md` (branch `sched-spec`, unmerged);
re-verify against the merged tree before implementing.

**1. `awaiting_input` must join the single-active-run index.** Plan 3 adds:

```sql
CREATE UNIQUE INDEX run_one_active_per_workflow
    ON run (workflow_id) WHERE status IN ('pending', 'running');
```

If `awaiting_input` stays outside that predicate, the scheduler fires a second run of a
workflow whose first run is still waiting on a human — two concurrent runs, defeating the
parent spec's "default to serialize", and a second escalation on the same workflow. The
one-open-escalation index is per _run_ and would not catch it.

So the predicate becomes `status IN ('pending', 'running', 'awaiting_input')`.

**Consequence, stated plainly:** a workflow with an open escalation does not fire again
until it is answered or expires. That is the correct behavior — the workflow is blocked on
a person — but it means the escalation deadline is also a cap on how long a workflow can
stall. A 72-hour deadline costs a weekly workflow nothing and costs a daily workflow three
runs. **The deadline default should be chosen against the workflow's cadence**, not set
globally, and skipped occurrences must be visible the way Plan 3 makes scheduled skips
visible.

**2. The reaper must key off `dispatched_at`, not `created_at`.** Plan 3's reaper sweeps:

```sql
status IN ('pending','running') AND created_at < now() - deadline
```

`awaiting_input` is already outside that predicate, so a waiting run is safe — no change
needed there, and this spec's earlier concern is satisfied by the implementation as
written. **Keep it that way.**

The real problem is on the far side. A run that waits 72 hours and then resumes returns to
`running` with a `created_at` three days old, and the next five-minute tick reaps it
immediately. This design would introduce that bug into a correct mechanism.

The fix is already available: Plan 3 adds `dispatched_at`, set when `Invoke` succeeds, and
resuming an escalation calls `Invoke` again. So:

- The reaper's predicate becomes `COALESCE(dispatched_at, created_at) < now() - deadline`,
  preserving today's semantics for a run that never dispatched.
- Resume re-stamps `dispatched_at`.

This also keeps Plan 3's `deadline > TTL` startup invariant meaningful: it now bounds a
single dispatch episode rather than the run's whole wall-clock life, which is what it was
always actually protecting.

**3. The permit is no longer LLM-only.** Plan 3 adds `spend.per_run_cents` to the permit
document. An amendment delta can therefore widen a spend cap as well as add operations —
which is a scope widening like any other and must appear in the diagram diff, not slip
through as a numeric field. Amendment validation covers the `spend` section too.

## Where an escalation comes from

**Proxy-originated (`amendment`).** The harness calls a connector operation outside the
permit. Today the proxy denies and records a deny event. With escalate-on-deny enabled it
also creates an escalation and returns a distinguishable response; the harness treats it as
`suspended` rather than failed.

This needs no new _decision-making_ in the harness — it does not have to formulate a
question, only recognize one response code. But it does need connector operations to exist
for there to be anything worth escalating, and the connector spec defines an operation's
`args_schema` as the LLM tool definition. **So this path arrives with connector work, not
before it.**

**Harness-originated (`decision`).** An `ask_human` tool with a closed argument schema:
question, options, recommended option. This is what a "you choose the appointment slot"
journey needs, and it does require the agent to author a question, so it is genuinely
downstream of the tool loop.

**Control-plane-originated (`amendment` with a pre-authored delta, and `completion`).**
Emitted by policy rather than by the agent — see the graduated-permits and objectives
specs.

## The escalation is an attack surface

This is the part of the design that most needs to be right, and neither existing spec
considers it.

An escalation is a second approval screen. It has less context than the first, it arrives
over a channel we do not control, and its content is influenced by whatever untrusted
material the agent just read — tickets, emails, a counterparty's chat replies. The
straight-line attack: attacker-controlled text induces the agent to request scope, the
request reaches the user as a plausible notification, the user approves. The product's
credibility rests entirely on the approval screen being honest; this is a way to put a
dishonest one in front of the user.

Controls, in the order they matter:

1. **Structural deltas only.** An amendment's scope is a set of `{connector, op}` pairs and
   resource-list entries drawn from the catalog and validated by the catalog validator. The
   model cannot express scope in prose, cannot name an operation that does not exist, and
   the catalog's append-only rule means an operation's reach cannot have silently widened
   since it was authored.
2. **The user approves a picture, not a sentence.** The control plane computes the diff to
   the blast-radius diagram from the two permit documents — the same visual the user
   approved originally, with the addition highlighted.
3. **Attributed rendering.** Any model-authored text — the "why" — renders as quoted,
   attributed, untrusted content, visually distinct from product copy, and never as a link.
   "Show me why" links to the run's event trail so the user can see what the agent actually
   read.
4. **No authority in the channel.** Amendment notifications contain no action links and no
   tokens. The user comes into the app. `decision`-kind escalations may have one-click
   answers, because nothing widens.
5. **Rate limits.** Per-run and per-window caps; exceeding them auto-pauses the workflow.
   Escalation flooding is both an attack and a plain UX failure.
6. **Denial is free.** The answer UI makes deny at least as easy as approve. A denied
   amendment is a normal outcome, not an error state.

**Accepted cost:** control 4 fights the UX spec's "they will not return to a dashboard".
Requiring a return visit to widen scope is the right trade — but it is real friction, and
it belongs in user testing rather than in an assumption.

## Data model

```sql
CREATE TABLE escalation (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    run_id uuid NOT NULL,
    workflow_id uuid NOT NULL,
    kind text NOT NULL
        CHECK (kind IN ('decision', 'amendment', 'completion')),
    status text NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'answered', 'denied', 'expired', 'cancelled')),
    question text NOT NULL,
    options jsonb NOT NULL DEFAULT '[]',
    delta jsonb,               -- amendment only; catalog-validated
    evidence jsonb,            -- completion and pre-authored amendments
    answer jsonb,
    answered_by uuid,
    resulting_version int,     -- the version an approved amendment created
    deadline_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    answered_at timestamptz,
    FOREIGN KEY (tenant_id, workflow_id)
        REFERENCES workflow (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (run_id) REFERENCES run (id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, answered_by)
        REFERENCES app_user (tenant_id, id),
    FOREIGN KEY (workflow_id, resulting_version)
        REFERENCES workflow_version (workflow_id, version)
);

-- At most one open escalation per run, enforced by the database.
CREATE UNIQUE INDEX escalation_one_open_per_run
    ON escalation (run_id) WHERE status = 'open';
```

Composite tenant FKs throughout, matching the convention set in `00003_workflow.sql`.

## Amendment approval, precisely

1. The escalation is answered `approve`.
2. The control plane computes the new permit: the currently approved version's permit plus
   the delta, validated against the catalog.
3. It creates workflow version N+1 as a draft, carrying the same steps, rubric, and
   objective, with the widened permit.
4. It approves N+1. `approved_by` is the answering user; version N becomes `superseded`.
   The `workflow_version_one_approved` index holds throughout.
5. It re-pins `run.version` to N+1 and appends a run event recording the transition.
6. It mints a fresh run token and re-invokes the actor with the answer.

Because `PermitForRun` resolves the permit live from `run.version` on every proxy request
(`server/internal/proxyadapter/adapter.go`), the next egress call is governed by the
widened permit. **The proxy needs no changes at all.**

**On re-pinning.** `run.version` becomes "the version currently governing this run" rather
than "the version this run started under". The run's event trail records what governed it
when. An `effective_version` column preserving the original was considered and rejected:
extra state whose only consumer is audit, which the event trail already serves.

## API

New under `/v1`, covered by the existing alpha stability notice in `docs/api/v1.md`:

| Method & path                        | Body                                     | Response                                        |
| ------------------------------------ | ---------------------------------------- | ----------------------------------------------- |
| `GET /v1/escalations?status=open`    | —                                        | `200 {escalations: [...]}` for the tenant       |
| `GET /v1/workflows/{id}/escalations` | —                                        | `200 {escalations: [...]}`                      |
| `GET /v1/escalations/{id}`           | —                                        | `200 {escalation, diagram_diff?}`               |
| `POST /v1/escalations/{id}/answer`   | `{decision: "approve"\|"deny", choice?}` | `200 {escalation, version?}`; `409` if not open |

Escalation creation is not a public endpoint — escalations originate at the proxy, the
harness's internal API, or control-plane policy.

## Failure and edge cases

- **Server restart with runs awaiting input.** Unlike the orphaned-run gap the roadmap
  records as decision 10, `awaiting_input` is durable by construction: the state is in
  Postgres and resumption is driven by a human answer, not by an in-memory handle. This path
  is more robust than the current `running` path. An `awaiting_input` run is governed by the
  escalation deadline rather than the run deadline — see
  [Reconciliation with Plan 3](#reconciliation-with-plan-3-scheduling-and-metering).
- **Answer after expiry.** Rejected, `409`.
- **A different version is approved while a run waits.** The delta was computed against a
  version that is no longer approved. Recommend invalidating the escalation with a clear
  message rather than silently recomputing. Worth an explicit concurrency test.
- **The Kubernetes-Jobs `Compute` backend has no suspend/resume**, so it cannot support
  escalation. A run there would have to terminate and re-execute from the objective with
  prior run events as context. Stated as a known limitation of the fallback; Substrate is
  the supported path for escalating workflows.
- **Denied amendment.** Recommend returning the denial to the agent so it can finish within
  the existing permit, failing the run only if it cannot proceed. A denial is information,
  not an error.

## UX

A fifth surface, sibling to the alert, with the same four-block shape:

- **What I want to do** — the operation, in catalog copy, not model prose.
- **Why** — attributed, quoted, with a link to the run's event trail.
- **What changes if you say yes** — the blast-radius diagram with the addition highlighted.
- **Approve · Deny · (for decisions) the options.**

## Testing

- Answering another tenant's escalation reads as `404`.
- An expired escalation rejects answers.
- An approved amendment produces exactly one new approved version; the prior version is
  `superseded`; the one-approved index never sees two.
- A delta naming an operation outside the catalog is rejected.
- The pre-suspension run token is rejected at the proxy after resume.
- The one-open-escalation index holds under concurrent creation.
- The reaper does not finalize `awaiting_input` runs.
- **A run resumed after waiting longer than the run deadline is not reaped** — the
  `dispatched_at` regression, and the one most likely to be missed.
- **A scheduled fire is refused while the workflow has a run in `awaiting_input`**, and
  resumes firing normally once it is answered.
- A suspended run can still append control-plane events.

## Open questions

- **Step-up auth for amendments inside the app?** Leaning no for v1 — the session is enough
  — and revisiting when multi-user governance lands.
- **Delivery.** Push and email depend on `internal/publish`, which arrives in Plan 4. Until
  then escalations are visible only in-app, which weakens "we'll come find you" for exactly
  the surface that most needs it. Sequencing decision for the plan.
- **Deadline default.** 72 hours is a guess. It should come from user testing, not from this
  document.

## Explicitly out of scope

Multi-approver routing, delegating an escalation to a second person, approval policies
("amounts over $50 need a manager"), reminder cadences beyond a single deadline, and any
synchronous gate.

# Nightshift Graduated Permits — Design

**Status:** Design approved in conversation; spec awaiting review
**Date:** 2026-08-31
**Author:** gambtho
**Parent:** [`2026-08-30-nightshift-platform-design.md`](./2026-08-30-nightshift-platform-design.md)
**Depends on:** [`2026-08-31-nightshift-escalation-design.md`](./2026-08-31-nightshift-escalation-design.md)
(advancement is an escalation) and the connector catalog spec (branch
`spec/connector-catalog`, unmerged) for the `effect: read|write` classification that gives
rungs their shape.

## What this is

A workflow's permit widens over time, but only ever through rungs a human authored and
approved in advance.

The idea comes from a field observation: an engineer with 290 undocumented CNC macro files
did not begin by trusting an agent to write machine code. They began with a bounded,
reviewable task — explain the existing macros — and expanded scope only as the explanations
held up. Trust grew from understanding, to recommendation, to controlled modification, to
new creation. Each step raised both the value and the consequence.

Nightshift currently has no way to express that. A permit is approved once, at full scope,
and never moves. The user is asked to make their maximum trust decision at the moment they
have the least evidence — which is exactly backwards for a target user the UX spec
describes as having trust that is "uncalibrated in both directions."

**This spec is deliberately small.** It adds no enforcement path and no new proxy code. It
is almost entirely a reuse of machinery that already exists.

## Scope decisions

| Decision                                                                       | Why                                                                                                                                                                                                                   |
| ------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **A rung is a workflow version**                                               | Reuses immutability, the `workflow_version_one_approved` index, and `approved_by`. Zero proxy changes, zero permit-schema changes. Every rung was explicitly authored and is reviewable before it is ever reached.    |
| **Advancement requires a human**                                               | `approved_by` is FK-bound to a real `app_user` row — the schema already asserts that approval is a human act. Automatic advancement would have no approver to record, and would widen a security boundary unattended. |
| **Advancement is an escalation of kind `amendment` with a pre-authored delta** | Same table, same surface, same audit trail as an agent-requested amendment. This spec is a policy that emits an escalation.                                                                                           |
| **The whole ladder is shown at first approval**                                | The blast-radius diagram is a teaching device. Showing rung 1 while concealing the ceiling would make the first approval dishonest.                                                                                   |
| **Failure demotes rather than pauses, where a lower rung exists**              | Gentler than today's auto-pause, and mechanically it is just approving a narrower permit. Pausing stays the behavior at the bottom rung.                                                                              |
| **Demotion creates a new version rather than resurrecting a superseded one**   | Keeps versions immutable and approval monotonic in version number. Costs a few rows; saves an invariant.                                                                                                              |

## Model

One nullable column on `workflow_version`:

```sql
ALTER TABLE workflow_version ADD COLUMN ladder_rung int;
```

A non-null `ladder_rung` marks the version as part of a ladder. At build time the user
approves rung 1; rungs 2..n are created as `draft` versions carrying progressively wider
permits. Drafts are inert — the existing schema already guarantees only an approved version
governs a run.

Advancement approves the draft whose `ladder_rung` is the current rung plus one. The
existing `workflow_version_one_approved` partial index supersedes the previous rung
automatically, with no new code.

**`(workflow_id, ladder_rung)` is not unique.** A rung may be instantiated more than once
over a workflow's life — demotion creates a fresh version carrying an earlier rung's
document. Version number remains identity; `ladder_rung` is a label.

## Rung composition

The connector catalog classifies every operation as `effect: read` or `effect: write`, and
allows write operations to carry resource constraints ("only this Slack channel"). That
gives ladders a natural default shape the build conversation can propose:

| Rung | Contains                                                    | The CNC analogue   |
| ---- | ----------------------------------------------------------- | ------------------ |
| 1    | `read` operations only                                      | Explain the macros |
| 2    | \+ `write` operations bound to a narrow resource constraint | Propose an edit    |
| 3    | \+ the remaining approved `write` operations                | Make the change    |

This is a **default, not a rule**. A ladder is an ordered list of permits and nothing
validates their shape. But a non-technical user will not compose one from scratch, so the
default is what makes the feature usable at all.

## Advancement

Policy lives on the workflow:

- `advance_after_runs int` — consecutive qualifying runs at the current rung.
- `require_clean_rubric bool` — whether rubric results gate advancement.

When the policy is satisfied, the control plane opens an `amendment` escalation whose delta
is already written — it is the next rung's permit. The user sees the evidence and confirms
or declines.

**Evidence shown:** the qualifying runs, their rubric results, and their raw outputs.

**Never a model-authored argument for its own promotion.** This matters more than it looks.
The evidence is partly produced by the agent being evaluated, so a summary written by that
agent would close a self-referential trust loop. Raw run records and grader verdicts only.
Requiring a human to confirm is the primary defense; this rule is the secondary one.

### Honest dependency

`require_clean_rubric` needs the grader, which is Plan 4 and not started — the rubric is
opaque `jsonb` today and nothing evaluates it (`00003_workflow.sql`,
`server/internal/httpapi/workflows.go`). Until the grader exists, the only available
evidence is run count plus absence of `failed` status.

**Do not ship `require_clean_rubric` as though it works.** Either sequence this spec after
Plan 4, or ship run-count advancement first and label it as such in the UI.

## Demotion

The UX spec's auto-pause after three consecutive rubric failures becomes:

- If a lower rung exists, create a new version copying rung N−1's document (same
  `ladder_rung` value), approve it, and notify. The workflow keeps running with less reach.
- At the bottom rung, pause as today.

Demotion notification reuses the alert surface, which already names the broken rule and what
it did about it. "It's now doing less" is a better message than "it stopped."

## Interaction with the other specs

- **Escalation.** An agent-requested amendment can jump ahead of, or outside, the ladder —
  it is the same mechanism producing the same versions. The ladder is the _scheduled_ path
  to more reach; an amendment is the _ad hoc_ one. Both leave identical audit trails.
- **Objectives.** A goal workflow may complete before climbing its ladder. No special
  handling; the ladder simply stops mattering.

## UX

- **Approve screen** shows the ladder: current rung highlighted, later rungs visible but
  clearly not yet granted, the ceiling explicit.
- **Home** shows position and the next gate — "rung 2 of 3 · advances after 2 more clean
  runs".
- **Advancement prompt** is the escalation surface, with the diagram diff between rungs.

## Testing

- Advancement produces exactly one approved version; the prior rung is `superseded`.
- Demotion copies rather than resurrecting; the copied version carries the earlier
  `ladder_rung`.
- Advancing past the top rung is rejected.
- A workflow with no ladder (`ladder_rung` null everywhere) behaves exactly as today —
  the regression case that matters most.
- Cross-tenant: advancing another tenant's workflow reads as `404`.
- The one-approved-version index holds across advancement and demotion.

## Open questions

- **Does the first approval get harder?** Showing the full ladder is more honest but puts
  more on the screen the UX spec says must be readable in about three seconds. This is a
  user-testing question, and it is the main risk this spec carries.
- **Default `advance_after_runs`.** Unknown. Likely differs per cadence — three runs is
  three weeks for a weekly workflow and three days for a daily one. Perhaps the gate should
  be elapsed time, not run count.

## Explicitly out of scope

Automatic advancement, per-capability independent ladders, trust shared across workflows or
tenants, rung-specific rubrics, and any notion of a global trust score.

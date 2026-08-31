# Nightshift Objectives — Design

**Status:** Design approved in conversation; spec awaiting review
**Date:** 2026-08-31
**Author:** gambtho
**Parent:** [`2026-08-30-nightshift-platform-design.md`](./2026-08-30-nightshift-platform-design.md)
**Depends on:** [`2026-08-31-nightshift-escalation-design.md`](./2026-08-31-nightshift-escalation-design.md)
— completion is an escalation kind.
**Amends:** [`2026-08-28-nightshift-design.md`](./2026-08-28-nightshift-design.md) — the
core model grows from three artifacts to four.

## The gap

Every Nightshift workflow today is perpetual. A workflow owns versions, a version is fired
on a cadence, a run is an episode, and nothing ever ends. The data model has no concept of
an objective and no completion predicate anywhere in it.

That fits a weekly digest, which genuinely never finishes. It does not fit a delegated
outcome:

> "Find out why my bill increased, resolve any incorrect charges, and make sure I do not
> have to do this again next month." … The agent monitors the next bill and **closes the
> task only after the credit and restored roaming control are visible**.

There is a beginning, a persistent objective, periodic episodes working toward it, and an
end condition verified against the world. Nightshift can express the middle and neither
end.

## Scope decisions

| Decision                                                                            | Why                                                                                                                                                                                                                                                                                                           |
| ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **The objective is a fourth artifact, not a rubric rule**                           | Rubric rules are invariants — always true of every run — and three consecutive failures auto-pause the workflow. A completion predicate is a terminal condition: eventually true, once. Encoded as a rubric rule, a goal not yet reached would auto-pause the workflow trying to reach it. Exactly backwards. |
| **`workflow.mode` is `standing` or `goal`, defaulting to `standing`**               | Everything that exists today keeps behaving identically. Goal workflows are additive.                                                                                                                                                                                                                         |
| **Completion is an escalation kind; a human confirms with evidence**                | Self-certified completion is an injection target — a counterparty replying "your case is resolved" would close a case where nothing was fixed. It is also the user's payoff moment, which they should get to see.                                                                                             |
| **`done_when` is natural language, not a predicate DSL**                            | The target user cannot write a predicate language, and a DSL here would be false precision dressed up as rigor. A human evaluates it at confirmation; the grader can evaluate it later.                                                                                                                       |
| **Every objective carries a horizon; unmet at the horizon means alert and abandon** | Fail closed. A goal workflow must not quietly run forever having failed.                                                                                                                                                                                                                                      |
| **Completion suspends, then destroys, the actor**                                   | `Compute.Destroy` already exists. Goal workflows get a bounded lifetime cost — a partial answer to the standing-fleet-bill risk both the platform spec and the leadership brief flag.                                                                                                                         |
| **A goal and a standing watch are two workflows**                                   | The billing ask contains both in one sentence. Intake must split them rather than produce one muddled object.                                                                                                                                                                                                 |

## The fourth artifact

The core model in the UX spec becomes **Steps · Permit · Rubric · Objective**. The
objective is a versioned artifact stored alongside the others on `workflow_version`, so
changing the goal requires re-approval exactly as changing the permit does.

```json
{
  "v": 1,
  "goal": "Get the incorrect roaming charges reversed on my mobile account.",
  "done_when": "A credit for the disputed roaming charges appears on a statement, and the roaming block shows as active.",
  "horizon": { "kind": "duration", "value": "P60D" },
  "check_cadence": "0 9 * * MON"
}
```

In the user's words, per the three-artifact table's pattern:

| Artifact      | In the user's words                  | What it becomes                              |
| ------------- | ------------------------------------ | -------------------------------------------- |
| **Objective** | "until the credit actually shows up" | A completion predicate and a give-up horizon |

**A goal workflow is not one long run.** It is periodic episodes against a persistent
objective — which is precisely what the long-lived actor gives us, and a good argument for
the actor model that the platform spec does not currently make.

## Lifecycle

`workflow.status`: `active` → `paused` | `completed` | `abandoned`.

- **Active.** Fires on `check_cadence` like any workflow.
- **Believes it is done.** The run emits a `completion` escalation carrying its evidence.
  The run finishes normally; the workflow stays `active` until a human answers.
- **Confirmed.** Workflow → `completed`, `completed_at` set, scheduling stops, the actor is
  suspended and later destroyed (see retention below).
- **Declined.** The escalation is denied, the workflow stays `active`, and the denial is
  returned to the agent as context on its next run — "not done, and here is why" is the most
  useful thing the agent can be told.
- **Horizon reached without confirmation.** Alert, workflow → `abandoned`, actor destroyed.
  The alert says what it tried and what it never managed to verify.

### Schema

```sql
ALTER TABLE workflow_version ADD COLUMN objective jsonb;
ALTER TABLE workflow ADD COLUMN mode text NOT NULL DEFAULT 'standing'
    CHECK (mode IN ('standing', 'goal'));
ALTER TABLE workflow ADD COLUMN status text NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'paused', 'completed', 'abandoned'));
ALTER TABLE workflow ADD COLUMN completed_at timestamptz;
```

`objective` is nullable: standing workflows have none. A `goal`-mode workflow whose
approved version carries no objective is rejected at approval, not at run time — consistent
with how permits fail closed at version creation.

## Interaction with scheduling (Plan 3)

The scheduler must read `workflow.status` and fire only `active` workflows. A `completed` or
`abandoned` workflow is never scheduled again. This is a constraint on a plan that has not
been written yet, and it belongs in the roadmap's Plan 3 notes.

## Actor retention after completion

Destroying the actor immediately is the cheap answer and the wrong one. If the carrier
reverses the credit three weeks later, the user's natural move is to reopen — and the
workflow's memory of the whole dispute, which is the actor's RAM and working volume, is
gone.

Recommend a **retention window**: on completion, suspend the actor and destroy it after 30
days. A suspended actor is roughly free, which is the entire economic premise, so the window
costs storage and nothing else. Reopening inside the window resumes with full memory;
outside it, a new workflow starts cold.

## UX

- **Home** distinguishes the two modes. A standing workflow shows "next run Monday". A goal
  workflow shows "working on it since Aug 12 · closes when the credit appears".
- **Completed workflows move to a done list, visible, with the outcome.** They must not
  simply vanish — the outcome is what the user delegated for, and it is the product's best
  evidence that the thing works.
- **The completion prompt is the payoff screen**: here is the credit, here is the reference
  number, here is the restored setting — close this?

## The intake requirement

"Resolve this and make sure I don't have to do this again next month" is **two workflows**:
a goal (fix this bill) and a standing watch (check future bills for the same fault). One
ends; the other never does. They need different permits and different cadences.

The build conversation must notice the conjunction and split it, proposing both. If it
instead produces one object it will be wrong in one of two ways: a goal that never closes,
or a standing workflow that never had a definition of done. This is a real elicitation
requirement on intake, and the UX spec's open risk that "the build conversation must
actively push toward gradeable rules, and we have not designed that elicitation yet" now
covers one more thing it must elicit.

## Testing

- A standing workflow with `mode = 'standing'` behaves exactly as today — the regression
  case that matters most.
- Approving a `goal` version with no objective is rejected.
- A confirmed completion stops scheduling; a declined one does not.
- The horizon fires and abandons when no completion is confirmed.
- A completed workflow rejects new runs (`409`, matching the no-approved-version case).
- Cross-tenant: completing another tenant's workflow reads as `404`.

## Open questions

- **Who evaluates `done_when` before the grader exists?** Only the agent can propose, and
  only a human can confirm. That is workable but means every goal workflow requires one
  human interaction to close. Acceptable — it is one interaction per objective, not per run —
  but it should be tested rather than assumed.
- **Grader-based auto-close.** Once Plan 4 lands, low-stakes goals could close without a
  human. Deliberately not designed here; it needs its own risk argument.
- **Retention window length.** 30 days is a guess informed by billing cycles.
- **Reopening.** Currently a new workflow seeded from the old one. Whether that should be a
  first-class "reopen" action is undecided.

## Explicitly out of scope

A predicate DSL, automatic reopening, dependencies or ordering between goals, and
multi-objective workflows.

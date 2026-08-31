# Nightshift Platform — Plan Roadmap

**Date:** 2026-08-30
**Spec:** [`../specs/2026-08-30-nightshift-platform-design.md`](../specs/2026-08-30-nightshift-platform-design.md)

The platform spec is too large for one implementation plan. This roadmap records the
decomposition into plans that each produce working, testable software, plus the scoping
decisions made while cutting it. Each plan is written when we reach it; Plans 1-3
are written and shipped.

## The sequence

The five numbered plans keep their numbers — nine specs cross-reference them
by number, and renumbering would invalidate every one. Work added after the
original decomposition is tracked in the table below this one.

| #   | Plan                                      | Status                  |
| --- | ----------------------------------------- | ----------------------- |
| 1   | **Foundation**                            | Shipped (PR #1)         |
| 2   | **Egress proxy + credential vaulting**    | Shipped (PR #5)         |
| 3   | **Scheduling + spend metering**           | Shipped (PR #10)        |
| 4   | **Rubric grading + alerting**             | Spec in review (PR #13) |
| 5   | **Substrate + Kubernetes-Jobs `Compute`** | Spec in review (PR #12) |

## Work outside the numbered plans

Added after the original decomposition, as specs merged and the goal state
sharpened. Every row touches `server/` unless noted, so every row competes
for the same single-session lock.

| Work                        | Spec           | Plan | Notes                                                              |
| --------------------------- | -------------- | ---- | ------------------------------------------------------------------ |
| **Identity + onboarding**   | Merged (PR #6) | none | In progress; owns `server/`                                        |
| **Connector catalog**       | Merged (PR #8) | owed | Top product risk in both specs; four downstream specs depend on it |
| **Build conversation**      | In flight      | owed | The previously unowned surface; resolves scoping decision 9        |
| **Frontend (`web/`) + CLI** | owed           | owed | `web/` does not take the `server/` lock; the CLI does              |
| **Escalation**              | Merged (PR #9) | owed | Carries Plan 3 amendment 1                                         |
| **Objectives**              | Merged (PR #9) | owed | Widens `workflow.status`                                           |
| **Graduated permits**       | Merged (PR #9) | owed | Hard dependency on Plan 4's grader                                 |

## Execution order

**Serialized on `server/`** — one session at a time:

1. Identity → 2. Connectors → 3. Plan 4 (grading + alerting) → 4. Escalation → 5. Objectives → 6. Graduated permits → 7. Plan 5 (Compute)

Escalation precedes objectives because the objectives spec declares the
dependency; graduated permits follows Plan 4 because `require_clean_rubric`
needs the grader; Plan 5 stays last, and the verification spike confirmed
that was right rather than merely asserted.

**Permanent parallel lanes** — no `server/` lock:

- The frontend at `web/`. Opens when identity merges and the build-conversation
  spec lands. This is the product's entry point, not a later phase: without it
  the platform has no user.
- Specs, research, docs, `src/`.

**Known ceilings, recorded not scheduled.** The agent-first scenarios reach
past the current model in two places: a user-supplied document corpus is
neither a connector nor a host allowlist (scenario 2's 290 CNC macro files),
and acting on behalf of someone with a third party's consent has no model —
identity is one owner per tenant, with multi-user governance deferred
(scenario 3's patient, care team, and insurer).

## Delegation specs (written 2026-08-31, no plans yet)

Three specs written together after validating the product direction against the
"agent-first" scenario document. They address the three structural gaps that review
found: no way to ask a human mid-run, no way for trust to grow with evidence, and no
way for a workflow to have a goal and finish. Each depends on the connector catalog for
its operation vocabulary; that spec has merged (PR #8), so all three are now plannable,
sequenced in the execution order above.

- **[Escalation](../specs/2026-08-31-nightshift-escalation-design.md)** — async
  runtime escalation. Amends the UX spec's "no runtime approval gate" decision by making
  the wait asynchronous: a suspended run holds no thread, no credential, and no worker.
  Adds run status `awaiting_input` and an `escalation` table. An approved amendment _is_
  a workflow version approval, so the proxy needs no changes. Prerequisite for the other
  two.
- **[Graduated permits](../specs/2026-08-31-nightshift-graduated-permits-design.md)** —
  a permit that widens through rungs a human approved in advance. A rung is a workflow
  version, so this adds one nullable column and no enforcement path. `require_clean_rubric`
  depends on the Plan 4 grader and must not ship before it.
- **[Objectives](../specs/2026-08-31-nightshift-objectives-design.md)** — a fourth
  artifact beside steps/permit/rubric, plus `workflow.mode` and `workflow.status`.
  Cadence stays in Plan 3's `schedule` artifact rather than the objective.

**Three amendments these specs owed Plan 3** — status as of 2026-08-31; the escalation
spec carries the detail:

1. `awaiting_input` joins the `run_one_active_per_workflow` index predicate, or the
   scheduler fires a second run while the first waits on a human. **Still owed; lands
   with the escalation implementation.**
2. The reaper keys off `COALESCE(dispatched_at, created_at)`, not `created_at`, or a
   resumed run is reaped immediately for having been created days ago. **Closed by
   Plan 3 itself.**
3. `engine.Fire` fires only `active` workflows. **Moved to Plan 4 (PR #13), which
   creates `workflow.status` and has `engine.Fire` re-check it — objectives inherits
   this delivered rather than owing it.**

## Scoping decisions made during decomposition

Recorded here so later plans don't rediscover them. Evidence: the 2026-08-30 survey of
cronfoundry (see the foundation plan's task notes).

1. **Plan 1's harness is tool-less** — one model exchange per run, no MCP. CronFoundry's
   `internal/mcp` is stdio-subprocess-only, which a hosted multi-tenant platform cannot
   safely run; the transport must be redesigned, and that redesign belongs with the
   connector-catalog spec, not the foundation. The tracer bullet proves tenancy,
   versioning/approval, the Compute seam, and pushed run records without tools.
2. **LLM providers ported: `anthropic`, `openai`, `openrouter`.** Dropped:
   `copilot-enterprise` (GitHub-coupled, undocumented token API — the survey's
   do-not-port) and `azure-foundry` (no `ChatTurn`, and it alone drags a second major
   version of the OpenAI SDK). Provider neutrality is retained with three.
3. **Not ported in Plan 1:** `internal/publish` and `internal/template` (both serve
   output delivery; no destinations exist until alerting — Plan 4), `internal/secrets`
   and `internal/redact` (no customer credentials exist until the proxy — Plan 2),
   `internal/memory` / `internal/writeback` (superseded by actor state, per the spec).
4. **Composite foreign keys from day one.** CronFoundry's own migration comment admits
   its single-column FKs don't enforce same-org child relationships. Every child table
   here carries `FOREIGN KEY (tenant_id, workflow_id) REFERENCES workflow (tenant_id, id)`.
5. **Separate keys per concern.** CronFoundry signs sessions with the same master key
   that encrypts secrets. Nightshift uses `NIGHTSHIFT_SESSION_KEY` and
   `NIGHTSHIFT_RUNNER_KEY` from the start; the secrets KEK arrives in Plan 2 as a third.
6. **Tenant in every claim.** `SessionClaims` carries `TenantID` and `UserID`;
   `RunClaims` carries `TenantID` — closing cronfoundry's largest multi-tenancy gap
   (its session claims had no tenant at all).
7. **Hand-written pgx queries, not sqlc.** Six tables don't justify a codegen step; the
   store's method signatures make the tenant parameter explicit. Revisit sqlc if the
   schema grows past what hand-written queries keep honest.
8. **The prototype stays at `src/`** — the quarantine move from the session handoff was
   explicitly declined (2026-08-30). The Go platform lives at `server/` alongside it.
9. **The v1 API ships stamped "unstable (alpha)".** Plan 1's `steps` document is the
   compiled execution form (system prompt, provider, model), not the user-facing
   `{id, text}` steps the UX prototype defines. Freezing that as a public contract
   would leak execution internals (2026-08-30 Codex review); before the contract
   freezes, the user-facing artifact joins the version document and the execution form
   becomes server-derived.
10. **No orphaned-run recovery in the foundation.** A server restart kills in-flight
    `Local` goroutines, a failed context fetch never finalizes, and per-actor queueing
    can outlive the 1h run-token TTL — in each case a run is stuck `pending`/`running`
    forever with nothing noticing. Plan 3 (scheduling) must add an orphaned-run reaper
    that finalizes runs stuck past a deadline as `failed`/`error_kind: "orphaned"`, and
    revisit the run-token TTL against actor queueing depth. (2026-08-30 final branch
    review.) Closed by Plan 3 (2026-08-31): reaper + deadline>TTL invariant shipped;
    queueing dissolved by the one-active-run admission index.

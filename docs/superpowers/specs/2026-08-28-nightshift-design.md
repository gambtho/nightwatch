# Nightshift — Design

**Status:** Design approved; UX prototype not yet built
**Date:** 2026-08-28
**Author:** gambtho
**Supersedes:** nothing. CronFoundry (`~/workspace/cronfoundry`) is explicitly *not* a
starting point — see [Relationship to CronFoundry](#relationship-to-cronfoundry).

## One-line pitch

Nightshift takes over the recurring thinking work you keep meaning to do and never
get to — safely enough that you can let it run while you sleep.

## Problem

There is a category of work that never gets done:

- It **recurs** on a cadence.
- It **needs judgment**, so it can't be scripted.

Zapier and n8n can't do it — they can move data but not decide what matters. A person
doesn't do it — it's tedious, low-status, and due again next week. Claude Desktop
*could* do it, but you have to be there, remember to ask, and paste in the context by
hand. So the work sits undone: the support themes nobody summarizes, the renewals
nobody tracks, the follow-ups nobody chases.

The blocker on automating it isn't capability — an agent session can already do this
work. The blocker is that **nobody will let an ungoverned agent run unattended against
their real systems**, and the tools that could govern it are REST APIs aimed at
developers.

## Target user

**A non-technical person who does not regularly use AI**, and who arrives with a
specific problem they're already annoyed by.

This definition drives more of the design than any other decision:

- **They have an intent, but phrase it in job language, not automation language.**
  They will not answer "what workflow do you want to build?" They will answer "every
  Monday I spend an hour digging through tickets."
- **They don't know what's possible.** They don't know a computer can now read their
  tickets and tell them what keeps coming up.
- **Their trust is uncalibrated in both directions.** With no experience, they either
  assume it's magic and approve anything, or assume it's a toy and approve nothing.
- **They will not return to a dashboard.** Any design that depends on routine
  check-ins will fail with this user.

They live in **inbox, calendar, and one chat tool**, near-universally. Their system of
record varies (tickets, CRM, spreadsheets, a shared drive). Design for the universal
three; treat the fourth as pluggable. This keeps the product scenario-agnostic without
making it abstract, and means we do **not** need to pick a vertical to start.

### Non-users

- Developers who would rather write the YAML. They're served by the substrate directly.
- Anyone needing sub-minute latency, event-driven triggers, or human-in-the-loop
  approval *during* a run (see [Runtime gates](#why-there-is-no-runtime-approval-gate)).

## What makes something a Nightshift job

Five tests. A candidate job should pass all five; the fifth is the value proposition.

1. **It recurs** — otherwise you'd just ask Claude when you need it.
2. **It needs judgment** — otherwise a script does it cheaper and more reliably.
3. **It reads a lot and writes a little** — which is also what makes a small blast
   radius achievable.
4. **A human reads the output** — so bad work is caught by someone who cares.
5. **It isn't getting done today** — the gap between "can't be scripted" and "won't be
   done by hand."

## Core model: three artifacts

A build conversation produces three artifacts, not one. Users already speak all three
fluently, just not in that order — the interview's job is to notice which one it's
hearing and sort it.

| Artifact | In the user's words | What it becomes |
|---|---|---|
| **Steps** | "summarize last week's tickets" | Agent system prompt + kickoff message |
| **Permit** | "only our support channel, don't let it email anyone" | Toolset + networking + vault + budget config |
| **Rubric** | "never miss a security issue, keep it under a page" | Outcome definition enforced by a separate grader |

The **rubric is what makes the alerting honest** (see [The four
surfaces](#4-the-alert)). Without it, "something looks off" is guesswork. With it, the
product can name a specific broken promise and how long it's been broken.

## The four surfaces

### 1. First run — intake and verdict

**Decision: no interview, no gallery.** The user arrives with a problem; take it. One
box: *"What do you want taken care of?"* — "describe it how you'd describe it to a
coworker." Starter phrasings are offered for anyone who freezes, all in job language.
Nothing is connected and nothing runs at this stage.

The important half is the response: an **honest feasibility verdict** in three blocks.

- **I can do this** — concrete, in their terms.
- **I'd get this wrong** — at least one real limitation, named up front.
- **I'd need access to** — read/write scopes, in plain language.

**Rationale for admitting a limitation before demonstrating any value:** a user with no
calibration who is told "yes" to everything either discovers the limits weeks later and
writes off the category, or never notices and trusts output they shouldn't. Naming one
real limitation in the first thirty seconds is what makes the yeses credible. Nothing
else in this space says no.

**Known risk (accepted):** this may read as weakness before the user has seen the thing
work. The fallback, if prototype feedback shows this, is to surface limitations after
the first successful run instead.

### 2. Build — chat with a live permit

**Decision: chat on the left, the permit diagram on the right, updating as you talk.**

Rejected alternatives: a collapsed "reach" bar (calmest, but hides the thing that
carries the product), and inline per-capability consent prompts (highest comprehension,
but turns the approval screen into a formality and adds clicks everywhere else).

The diagram updates live and highlights newly added capability, so scope is something
the user *feels while describing*, not something they audit at the end.

### 3. Approve — the blast radius

**Decision: a diagram with a hard boundary,** not a document and not a dry-run receipt.
Scope is judged in about three seconds without reading a list: what it can read (green),
what it can write (amber), the agent and its spend cap in the middle, a dashed limit
line, and struck-through items outside it (email, DMs, deleting, payments, the rest of
the internet).

This screen carries an unusual amount of weight for two reasons:

1. It is the **only** gate (see below).
2. The product name doesn't signal governance, so **the diagram has to.**

It also has a second job beyond safety: it is a **teaching device**. It shows a skeptic
that the thing is bounded, and shows an over-truster that it has limits at all.

Rejected alternatives: "The Permit" (a plain-language document with equal weight given
to what it can never do — good content, slower to read) and "The Receipt" (approve a
real dry run's actual output — most convincing, but costs a test run and was rejected as
the primary model).

#### Why there is no runtime approval gate

The substrate offers `always_ask`, which pauses a session until a human confirms a tool
call. On a scheduled 3AM run that is a **stall, not a safeguard** — the session goes idle
and waits. Approve-once is therefore correct for unattended work, and the design commits
to it. Everything the user needs to judge must be legible on the approval screen.

Consequence: **declared blast radius is content of the approval screen**, not a separate
runtime mechanism.

### 4. The alert

**Decision: silence is good; alert on trouble; reach the user outside the app.**

The quiet home lists workflows with last run, cost, rule-compliance, and next run — and
tells the user explicitly: *"You don't need to check this page. If something goes wrong,
we'll come find you."* That line is load-bearing; it's what stops an anxious non-AI user
from checking, which is the behavior the whole model depends on.

The alert reaches them by email and push. Four blocks:

- **Which rule it's missing**, and for how many runs.
- **Why it thinks that's happening** (e.g. "since Aug 4 the `category` field has been
  empty on almost every ticket; I've been guessing, and guessing badly").
- **What it already did about it** — paused.
- One-click actions: show me the runs / let's fix it / it's fine, resume.

**Auto-pause after 3 consecutive quality failures.** Accepted risk: a workflow that only
needed a nudge is now stopped until the user acts, and this user may not act. Judged the
better failure mode than continuing to send output the user trusts and shouldn't.

## Substrate

Nightshift is a UX layer over the **Managed Agents** API. It does not implement
scheduling, sandboxing, credential storage, or spend enforcement itself.

> **Verification status:** the mapping below was read from the bundled `claude-api`
> skill documentation (`shared/managed-agents-*.md`) on 2026-08-28, not from a live API.
> Re-verify against current docs before implementing against the real substrate.

| Nightshift concept | Substrate primitive |
|---|---|
| Which tools it may use | Toolset `default_config.enabled: false`, opt in per tool |
| What it may reach on the network | Environment `networking: limited` + `allowed_hosts` |
| Which sites it may search/fetch | Per-tool `allowed_domains` / `blocked_domains` |
| Spend cap | `budget.max_list_cost` — platform-enforced; **pauses, not kills** |
| Credentials | Vaults — substituted at egress, never visible in the sandbox |
| Isolation | Fresh per-session container |
| Schedule | Scheduled deployments — cron + IANA timezone |
| Proof it ran | `deployment_runs` — one record per firing, including failures |
| Change control | Agents are versioned; sessions pin a version |
| Rule grading | Outcomes — rubric scored by an independent grader per iteration |

Two substrate behaviors the UX must not paper over:

- **Scheduled runs are jittered** up to 15% of the interval (capped at 9 minutes). Never
  promise an exact minute.
- **A budgeted session pauses at its cap rather than failing.** "Paused — hit its spend
  limit" is a distinct user-visible state from "failed."

## Prototype scope

This spec's implementation is a **UX-only prototype** (the "C" path). Its output is a
validated design, not code we keep.

**Real:**

- All four surfaces, clickable, at interaction fidelity.
- One worked scenario end to end, with realistic content: the weekly support digest.
- The permit diagram building live during a scripted build conversation.

**Faked:**

- No Managed Agents calls, no execution, no scheduling, no persistence.
- The build conversation is scripted, not model-driven.
- Run history, costs, and grader results are fixtures.

**Explicitly out of scope:** connectors, auth, multi-user/roles, billing, deployment.

**Autonomous choice (low-risk, reversible):** React + Vite + TypeScript, matching the
likeliest production path. No project formatter is configured yet; add one before the
prototype grows past a handful of files.

### What the prototype is testing

1. Does a non-AI user understand the blast-radius diagram without being taught it?
2. Does the "I'd get this wrong" verdict build trust or deter?
3. Can they describe a real job in their own words and recognize the result as theirs?
4. Does the quiet home read as reassuring or abandoned?

## Relationship to CronFoundry

CronFoundry is **not** the starting point, by explicit decision. Its PRD lists as
non-users exactly the two things Nightshift makes central: people with no GitHub account
or cloud infra, and multi-step agentic workflows. Its GitOps/YAML/GitHub-App spine is
baked into its data model, API, and onboarding, so extending it would mean fighting the
codebase's assumptions in every screen. Nightshift is greenfield.

## Deliberately deferred

- **Whose machines.** Hosted-by-us, deploy-into-their-org, and BYO-cloud are all
  plausible; the substrate abstracts compute, so the decision does not block UX work.
- **The connector catalog.** This is the real ceiling on the product — the build agent
  can only propose steps for tools that exist — and the real cost. It needs its own
  spec, not a paragraph here.
- **Multi-user governance.** Who may create, who must approve, what an admin constrains.
  The current design assumes one person who both builds and approves.
- **Pricing and cost pass-through**, including how a per-run spend cap is explained to
  someone who has never bought inference.
- **Credential capture UX** — what happens when a workflow needs a key the user has to
  go find.

## Open risks

1. **The connector ceiling may bind before the UX does.** A perfect builder is worthless
   if it can only reach two systems. Validate desired connectors during prototype
   testing.
2. **"Silence is good" is least safe for exactly this user.** They have no instinct for
   how agents fail, so they can't fill gaps the system doesn't report. Rubric grading
   mitigates this but only covers rules the user thought to state.
3. **Rubric quality is user-dependent.** Vague criteria produce noisy grading. The build
   conversation must actively push toward gradeable rules, and we have not designed that
   elicitation yet.
4. **Approve-once means a stale permit.** If a connected system changes shape, the
   permit stays as approved while the world moves. Re-approval triggers are undesigned.

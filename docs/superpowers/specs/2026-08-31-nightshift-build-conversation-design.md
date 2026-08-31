# Nightshift Build Conversation — Design

**Status:** Design proposed; spec awaiting review
**Date:** 2026-08-31
**Author:** gambtho
**Parent:** [`2026-08-28-nightshift-design.md`](./2026-08-28-nightshift-design.md) —
this is the successor that spec's surfaces 1 and 2 have been waiting for: the
connectors spec scopes out "the build conversation itself (UX spec's successor
owns the screens)", and no other document owns it.
**Depends on:** [`2026-08-30-nightshift-connectors-design.md`](./2026-08-30-nightshift-connectors-design.md)
(`GET /v1/catalog`, the OAuth connect flow, MCP registration, and the
"constraint elicitation" open item this spec closes) and
[`2026-08-31-nightshift-objectives-design.md`](./2026-08-31-nightshift-objectives-design.md)
(the mode split this spec's intake must perform).
**Resolves:** roadmap scoping decision 9 (the user-facing steps artifact joins
the version document; the execution form becomes server-derived) and the UX
spec's deferred "credential capture UX" and "pricing … explained to someone who
has never bought inference" items, plus its open risk 3 (rubric elicitation).
**Written against:** the rubric v1 schema in
[`2026-08-31-nightshift-grading-alerting-design.md`](./2026-08-31-nightshift-grading-alerting-design.md)
(branch `spec/grading-alerting`, unmerged) — re-verify on merge — and the
agent-first scenarios document (external, in-progress, provided 2026-08-31),
whose closing paragraph — the builder's experience of creating these
workflows — is precisely this spec's subject; see
[The builder's four questions](#the-builders-four-questions).

## What this is

The build conversation is intake, the honest feasibility verdict, and the
guided conversation that turns a job described in job language into the
artifacts a workflow version carries. The UX prototype scripts it
(`src/fixtures/conversation.ts`); this spec designs the real, model-driven
version and states exactly what the frontend and `server/` each owe it.

What the conversation must produce has grown since the UX spec's three
artifacts. A version now carries **steps, permit, rubric, schedule, and
(goal mode) objective**, and the delegation specs added per-workflow policy
the build is the only surface that can set: escalation opt-in, a permit
ladder, and an advancement policy. The build conversation is where every one
of these is elicited or defaulted — there is no second chance, because the
target user will not return to a dashboard to tune anything.

## Scope decisions

| Decision                                                                                           | Why                                                                                                                                                                                                                                                                                                                                                   |
| -------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **A build is a server-side resource, not frontend chat state**                                     | Credential capture means OAuth redirects and "go find your Zendesk key" — a build must survive a page unload and a three-day pause. It is also spend (see below) and an audit subject, so it lives in Postgres like everything else that matters.                                                                                                     |
| **The build agent runs in the control plane, not in an actor**                                     | It needs no sandbox: its tools are control-plane internals (catalog reads, draft mutation, option listing), it holds no tenant credential, and its output is validated structurally before anything renders as scope. An actor would add resume latency to every turn and put the artifact validator on the wrong side of a trust boundary.           |
| **The agent proposes structurally; the server validates; scope surfaces never render model prose** | The escalation spec's control set applies before approval, not just after: the permit draft is `{connector, op}` references and resource entries validated against the catalog, the diagram and the "I'd need access to" block render from catalog copy keyed by those references, and constraint values enter by structural pick, not model rewrite. |
| **The verdict admits a limitation by contract, not by disposition**                                | "I'd get this wrong" is a required, non-empty block in the verdict's response schema, instantiated from a fixed limitation taxonomy. A model asked to be humble drifts; a schema that rejects a verdict with no limitation does not.                                                                                                                  |
| **Compilation is deterministic and happens at approval**                                           | The user-approved steps and rubric text land verbatim inside the execution form. An LLM compile step would insert unreviewed model-authored instructions between approval and execution — the same class of hole as self-certified completion. Approval-time (not fire-time) compilation means approve-once survives compiler changes.                |
| **Submission requires every named connection to be `ok`**                                          | The approve screen must be true and the first run must work. A permit naming a connection that does not exist yet would make both false. The build waits — resumable, with a visible "waiting on you" state — rather than letting a broken workflow through.                                                                                          |
| **Build spend is metered under the tenant monthly cap**                                            | The build agent is the platform's only pre-approval inference. It gets the grader's treatment (Plan 4): pre-call `OverCap` consult, cost counted against the monthly cap, never against any per-run permit cap (there is no run). A per-build turn cap bounds a single conversation.                                                                  |

## The build resource

```sql
CREATE TABLE build (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    created_by uuid NOT NULL,
    status text NOT NULL DEFAULT 'intake'
        CHECK (status IN ('intake', 'shaping', 'ready', 'submitted', 'abandoned')),
    intake_text text NOT NULL,
    verdict jsonb,
    transcript jsonb NOT NULL DEFAULT '[]',
    drafts jsonb NOT NULL DEFAULT '[]',
    turn_count int NOT NULL DEFAULT 0,
    cost_cents int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, created_by) REFERENCES app_user (tenant_id, id)
);
```

- `drafts` is a list of one or two draft workflows (see
  [the mode split](#the-mode-split)); each draft holds a name, a mode, and
  the artifact set as far as it has been elicited, every mutation validated
  by the same parsers the version API uses (`permit.Parse`,
  `rubric.Parse`, schedule validation, objective validation) plus
  `ValidateConnections`. A draft that would be rejected at version creation
  cannot exist inside a build — invalid state is unrepresentable, and submit
  cannot fail on shape.
- `status`: `intake` (text received, verdict pending) → `shaping`
  (conversation in progress) → `ready` (artifacts complete, connections
  `ok`) → `submitted` (workflows created). `abandoned` after 30 idle days,
  by the same kind of sweep as the reaper. Any pre-`submitted` state is
  resumable.
- **Submit** creates each draft's workflow and version 1 through the same
  store paths as `POST /v1/workflows`, then sends the user to the existing
  approve surface. **The build never approves anything.** Approval stays the
  one gate, on the existing endpoint, rendered from the stored version — not
  from the conversation.

### API

| Method & path                   | Body                                         | Response                                                            |
| ------------------------------- | -------------------------------------------- | ------------------------------------------------------------------- |
| `POST /v1/builds`               | `{text}`                                     | `201 {build}` — verdict generation begins                           |
| `GET /v1/builds`                | —                                            | `200 {builds: [...]}` (drives the "waiting on you" list)            |
| `GET /v1/builds/{id}`           | —                                            | `200 {build}` — transcript, verdict, drafts, connection checklist   |
| `POST /v1/builds/{id}/messages` | `{text}`                                     | `200 {reply, drafts}` — one agent turn; drafts reflect any mutation |
| `POST /v1/builds/{id}/options`  | `{draft, connector, op, constraint}`         | `200 {options: [...]}` — see constraint elicitation                 |
| `POST /v1/builds/{id}/select`   | `{draft, connector, op, constraint, values}` | `200 {drafts}` — structural pick, no model in the loop              |
| `POST /v1/builds/{id}/submit`   | —                                            | `201 {workflows: [...]}`; `409` unless status is `ready`            |

Session-authed, tenant-scoped, cross-tenant reads as `404` — house pattern
throughout. Whether `messages` streams token-by-token or returns whole turns
is a frontend latency call the API should not preclude (SSE on the same
route); it changes nothing structural.

## The build agent

A control-plane conversation loop over the existing `llm` package with a
platform key — the grader's posture (Plan 4), not the harness's: fixed
platform provider/model (`NIGHTSHIFT_BUILD_PROVIDER` / `NIGHTSHIFT_BUILD_MODEL`,
defaulting to `anthropic` / `claude-sonnet-5` — the build needs more judgment
than grading does), a deliberate egress-proxy exemption on the same grounds
the grader states, pre-call metering, and structural output parsing.

Its tool surface is closed and control-plane internal:

- **`catalog`** — the same content as `GET /v1/catalog`: curated connectors,
  ops with plain-language copy and `effect`, the tenant's connection status,
  registered MCP servers with discovered-tools snapshots.
- **`propose`** — mutate a draft: set steps, grant/revoke `{connector, op}`,
  set resource constraints (values only via `select`, see below), set rubric
  criteria, schedule, objective, spend cap, policy fields. Every call passes
  the artifact validators; a validation failure returns to the model as a
  tool error, never lands in the draft.
- **`ask_options`** — request that the frontend show a structural picker for
  a constraint slot (the model asks the question; it does not fetch or relay
  the values).

**What the model can never author:** the "I'd need access to" block, the
permit diagram, resource-constraint values, connection status, and spend
figures. All of these render server-side from validated draft state and
catalog copy. Model prose appears only as conversation text, where the user
is present and typed the other half — this is the ordinary chat surface, not
the escalation channel, so attributed-quoting rules do not apply here; they
apply the moment build content reaches an approval or notification surface.

**Injection posture.** Intake text is the user's own. Third-party text
enters the conversation in exactly one way: option labels from build-time
read ops (channel names, ticket field values) — chosen by human click,
carried structurally, and echoed to the model as selected context. A
hostile channel name can therefore try to steer the conversation, but it
cannot place anything on a scope surface (structural rendering), cannot
widen a draft silently (the user watches the diagram move), and cannot
approve (the build cannot approve). Residual risk is conversation-quality
corruption, bounded by the same human presence the surface is built around.

**Limits.** Turn cap per build (default 60; hitting it ends the
conversation with a save-and-resume message, not an error), `OverCap`
consult before every call, per-build `cost_cents` recorded and visible.

## Intake and the honest verdict

Intake stays exactly the UX spec's one box; nothing is connected and
nothing runs. The verdict is the build agent's first turn, produced against
two inputs only: the intake text and the catalog. It is a structured
object, not prose:

```json
{
  "v": 1,
  "feasibility": "yes | partial | no | not_a_fit",
  "can": [
    {
      "text": "Read last week's Zendesk tickets and pull out repeated themes",
      "ops": [{ "connector": "zendesk", "op": "search_tickets" }]
    }
  ],
  "wrong": [
    {
      "kind": "judgment_ambiguity",
      "text": "Deciding which complaints are the same underlying issue is a judgment call — I'll sometimes lump or split differently than you would."
    }
  ],
  "proposed": [{ "mode": "standing", "name": "Weekly support digest" }]
}
```

- **"I can do this"** — each item carries the catalog ops it would use.
  The server rejects a verdict citing an op that does not exist; a claim
  with no op behind it may only appear under `feasibility: partial` with a
  matching `wrong` entry. This is what "grounded in the catalog rather than
  improvised" means mechanically: the yes-list is checkable, item by item.
- **"I'd need access to"** — not model output at all. The server computes
  it as the union of connectors/ops referenced by `can`, grouped per
  connector, rendered from the catalog's plain-language descriptions (the
  copy the connectors spec says is "written once" for exactly this surface),
  split read/write. Ungranted, unconnected — a preview of the permit the
  conversation will build.
- **"I'd get this wrong"** — at least one entry, enforced by schema. `kind`
  comes from a fixed taxonomy the platform prompt defines:
  `judgment_ambiguity` (the call could go either way),
  `data_dependency` (quality rides on fields someone else maintains),
  `unreachable_system` (named system has no connector and no registered MCP
  server), `cannot_verify_world` (it can read claims, not truth — a
  counterparty saying "resolved" is not resolution), and
  `beyond_charter` (irreversible or out-of-bounds actions it will refuse).
  The model instantiates the kind concretely against this job. A generic
  disclaimer ("AI can make mistakes") fails review; the taxonomy exists to
  force specificity.

`feasibility: no` (the job hinges on an unreachable system) says so
plainly, names the gap, and offers the pluggable path — register the
vendor's MCP server, with the custom-URL option for self-hosted — rather
than a shrunken counter-offer the user didn't ask for. Every
`unreachable_system` entry is also recorded (system name, verbatim user
phrasing) as connector demand signal: open risk 1 says the catalog ceiling
binds first, and intake is where the evidence of what to build next shows
up for free. `not_a_fit` covers requests failing the five tests (one-shot
asks, sub-minute triggers, pure data-moving) and points at the right tool,
including plain Claude — the honest verdict includes "you don't need this
product for that."

### What would falsify the weakness-first risk

The UX spec accepts the risk that admitting a limitation before showing
value reads as weakness. That is a testable claim, and user test 1 (the
facilitator kit already exists) is the instrument. Two measures:

1. **Continuation** — of users who receive a verdict, how many proceed
   into shaping? Run the deferred variant (limitations surface after the
   first successful run) as a control arm.
2. **Calibration** — after approval, ask: "name one thing this workflow
   will get wrong." Users who can answer were taught by the verdict; users
   who answer "nothing" were not.

The risk is **falsified** — weakness-first vindicated — if continuation is
not materially lower than the control arm while calibration is better. The
risk is **confirmed** if weakness-first loses users _and_ the control arm
calibrates just as well after their first run. Confirmed means taking the
UX spec's stated fallback (surface limitations after the first successful
run), not softening the limitation copy — the block's honesty is
load-bearing for everything else the product claims.

## The mode split

The verdict's `proposed` list declares mode per workflow, and the
objectives spec's requirement lands here: a conjunction like scenario 1's
"resolve any incorrect charges, **and make sure I do not have to do this
again next month**" must yield two proposed workflows — a goal ("Get the
roaming charges reversed", with an objective and horizon) and a standing
watch ("Check each new bill for the same fault", with neither).

Mechanically the split is cheap to keep honest, because the objective
artifact is structurally mandatory in one mode and forbidden in the other
(objectives spec: a goal version with no objective is rejected). The build
enforces the same rule at draft level, so a muddled single object cannot
reach submit. The elicitation rule for the agent: **an end condition heard
in intake ("until", "once it's fixed", "and then stop") signals goal mode;
a recurrence with no end signals standing; both in one sentence means two
drafts.** The two drafts share the conversation — access granted while
shaping one is offered, not assumed, for the other (they need different
permits: the goal writes to the carrier; the watch only reads bills).

The user sees the split as the verdict's proposal ("that's two jobs — one
that ends, one that keeps watch"), shapes both in one conversation, and
approves each separately on its own diagram.

One bias to guard against: the product's founding example (the weekly
digest) is standing, but all three scenarios in the agent-first document —
the bill dispute, the macro estate, the follow-up booking — are goal
workflows. `workflow.mode` defaults to `standing` at the schema level for
compatibility; **intake must not inherit that default as a prior.** A
delegated outcome phrased with no explicit end ("arrange the follow-up my
care team requested") is still a goal — the end condition is implicit in
the outcome — and the agent's restatement should surface it ("done when
the appointment is booked and you have the instructions") rather than
quietly minting a workflow that never finishes.

## Artifact elicitation

The conversation is not a wizard. The UX spec's observation stands: users
volunteer all artifacts in any order, and the agent's job is to notice
which one it is hearing and sort it — the prototype's script (t3 grants
scope and t5 states a rubric rule inside answers about steps) is the
fidelity target. What follows is the per-artifact discipline, not a screen
order.

### Steps

Restated in the user's vocabulary as the user-facing form (see
[decision 9](#scoping-decision-9-two-forms-of-steps-resolved)): short,
imperative, one judgment per step, no tool names or provider language. The
draft updates as understanding does; the user confirms the restatement, not
a paraphrase at the end.

### Permit

Derived from steps, minimally: every step maps to the catalog ops it needs,
and nothing is granted that no step uses. Write ops trigger the constraint
conversation:

- The agent asks in job language ("where should the summary go?").
- For a constraint slot whose catalog binding declares an option source
  (see below), the frontend calls `POST /v1/builds/{id}/options`. The
  server invokes the listing read op through a **control-plane connector
  client** — session-authed, the `refresh-tools` precedent ("build-time
  discovery is not a run and must not be reachable with a run token"),
  same compile-and-inject path and hardening as the proxy, restricted to
  `effect: read` ops that appear as an `options_from` binding. It requires
  the connector's connection to be `ok`, so connect precedes constraint
  refinement.
- The user picks from a picker; `POST /v1/builds/{id}/select` writes the
  exact resource value into the draft structurally. **The model never
  transcribes a channel ID.**

This closes the connectors spec's "constraint elicitation" open item:
build-time op invocation is session-authed through a dedicated
control-plane path, not deferred to first run, and the proxy still only
ever accepts run tokens.

**Catalog addition required:** a constraint binding gains an optional
`options_from: {op, items_path, value_field, label_field}` naming the read
op that lists candidate values (e.g. `post_message.channel` ←
`list_channels`). Without it, "only our support channel" is elicited as
free text and the flagship constraint is a typo away from wrong.

**MCP snapshot freshness (resolving the connectors spec's staleness
question):** when the conversation first grants a tool from a registered
MCP server, the build triggers `refresh-tools` and the draft pins the fresh
snapshot revision. The permit then carries a snapshot at most one
conversation old, and the approval screen never shows tools a vendor has
since removed.

**Hesitation is a feature.** When the user balks at a write grant — the
graduated-permits spec's founding observation — the agent offers the ladder
default: rung 1 read-only, rung 2 constrained writes, rung 3 the rest,
`advance_after_runs` defaulting to 3, `require_clean_rubric` only once the
Plan 4 grader ships (that spec's honest-dependency rule). Escalation
(escalate-on-deny) is offered only for goal-mode workflows whose steps
cross consequential boundaries — accepting a contractual change, choosing
among real-world options — and defaults off everywhere else; a permit that
asks instead of refusing is not a permit. Neither knob is ever asked about
in the abstract; both are offered as answers to hesitation or to a detected
boundary, with a plain default taken silently otherwise.

### Rubric — the elicitation open risk 3 leaves undesigned

The target is Plan 4's rubric v1: 1–10 criteria `{id, rule}`, pass/fail,
graded by a model that sees **only the run's output** and whose contract
says "the rule text must stand alone." That gives elicitation a crisp
definition of gradeable, and the conversation applies it as three tests to
every candidate rule:

1. **Output-decidable** — could a stranger reading only the finished
   output decide pass/fail? "Never miss a security issue" is not decidable
   from the output (the misses are the absences); "security-related items
   appear in their own section at the top, never buried" is. The agent's
   core move is converting _outcome anxieties_ into _output shapes_: ask
   what the user is afraid of, then propose the visible form vigilance
   should take.
2. **Invariant, not terminal** — true of every run, or true once at the
   end? "Until the credit shows up" is an objective wearing a rubric's
   clothes; the agent moves it (the objectives spec's backwards-auto-pause
   argument, applied at elicitation time).
3. **Self-contained** — no pronouns into the conversation, no "as
   discussed": the rule text is what the grader reads and what the alert
   quotes, verbatim and alone.

For a vague rule ("make it good", "keep it professional") the agent offers
two or three concrete rewrites drawn from this job's material and lets the
user pick or edit; it never silently substitutes its own rule. Rules come
from two sources: promises the user states, and promises the agent heard
inside the steps ("flag it separately at the top" in the prototype script
is both a step and the rubric rule "security items lead the digest") — the
second source is offered back explicitly ("want me to hold every run to
that?"). Criterion ids are slugs minted once and preserved across edits,
because Plan 4 makes the id the streak identity.

An anxiety that fails test 1 in both directions — genuinely not decidable
from output — is not laundered into a bad rule. The agent says what the
grader can and cannot check, keeps the instruction in the steps, and moves
on with the user's understanding intact. An empty rubric remains legal
(`{}` = ungraded); the conversation states the consequence in alert terms:
no rules means no broken-promise alerts, only crash alerts.

### Objective (goal mode)

`goal`, `done_when`, `horizon` — natural language per the objectives spec,
with the same stand-alone discipline as rubric rules, plus one elicitation
rule of its own: **`done_when` must name evidence the agent can read, not a
counterparty's assurance.** "The carrier says it's resolved" fails (the
spec's injection argument); "a credit for the disputed amount appears on a
statement, and the roaming block shows active" passes. The horizon is
proposed from the job's own rhythm (two billing cycles, not a magic
number) and stated in the completion terms the user will see again at the
payoff screen.

### Schedule

Cadence in the user's words, compiled to `{cron, tz}` (Plan 3 validation),
tz defaulting from the browser, always confirmed back in words plus the
next three fire times — "every Monday at 9am your time; next: Mon Sep 7"
is how a non-technical user catches a cron mistake. Goal workflows get the
same treatment ("how often should I check?"), per the objectives spec's
cadence decision.

### Spend

The deferred "explained to someone who has never bought inference" item.
The server proposes the per-run cap from the compiled form's model pricing
and a token estimate; the conversation renders **money at the job's rhythm,
never tokens**: "each run costs a few cents; I'll never spend more than
50¢ on one run or about $2 a month on this job. If a run would go over,
I stop it and tell you." The monthly figure is `cap × occurrences` from
the schedule — derived, not a second cap. The user can raise or lower it;
the number lands in `spend.per_run_cents` and is inside the approval
diagram where the UX spec drew it.

## Scoping decision 9: two forms of steps, resolved

**The user-facing artifact** joins the version document as steps v1, and
is what `POST /v1/workflows` and `POST /v1/workflows/{id}/versions` accept:

```json
{
  "v": 1,
  "steps": [
    {
      "id": "gather",
      "text": "Look at last week's support tickets from Zendesk and #support."
    },
    { "id": "themes", "text": "Work out what keeps coming up and what got worse." },
    { "id": "post", "text": "Post a short digest in #team-digest, security items first." }
  ]
}
```

Validated by a new `steps.Parse` in the house idiom (strict decode, fail
closed): `v: 1`, 1–20 steps, `id` a unique slug (same charset as rubric
criterion ids, same reason — stable identity across edits), `text`
non-empty ≤ 500 chars. Execution fields (`system_prompt`, `provider`,
`model`, `max_tokens`) in a steps document are **rejected** — the alpha
notice in `docs/api/v1.md` reserved exactly this break, and this spec
spends it.

**The compiled execution form** becomes a server-derived column on
`workflow_version` (`compiled jsonb`), written **at approval time** by a
deterministic compiler — template assembly, zero model calls:

- `system_prompt` = fixed harness preamble (role, honesty rules, escalation
  affordance if enabled) + the numbered user steps **verbatim** + the
  rubric rules **verbatim** (the agent should know the promises it is
  graded on) + the objective verbatim in goal mode.
- `kickoff` = fixed text naming the fire occasion (scheduled occurrence or
  manual).
- `provider`, `model` = platform policy (`NIGHTSHIFT_RUN_PROVIDER` /
  `NIGHTSHIFT_RUN_MODEL` defaults; per-tenant override is a designed-for
  seam, not built) — priced by construction, which is where the current
  unpriced-model `400` moves.
- `max_tokens` = derived from `spend.per_run_cents` against the model's
  pricing.

Why deterministic, and why approval-time: the text the user approved is
byte-identical inside what runs — auditable by diff, no model-authored
instructions inserted after the approval gate — and an approved version's
behavior cannot drift when the compiler's preamble improves. A compiler
change affects only future approvals; `compiled` records a
`compiler_v` for audit. `GET /internal/runs/{id}/context` serves the
compiled form; the harness contract does not change shape. The public
version document returns the user-facing steps and never the compiled form
(the leak decision 9 exists to prevent).

**Migration** (pre-release, alpha notice): existing rows copy their old
steps document into `compiled` (stamped `compiler_v: 0`) and synthesize
`{"v":1,"steps":[{"id":"job","text":<kickoff>}]}` as the user-facing
artifact — dev data stays runnable, nothing is silently reinterpreted.

## Credential capture

Deferred by the UX spec; the connectors spec built the machinery (OAuth
start/callback, incremental re-consent, MCP registration with probe) and
left the screens here.

- The draft permit carries a **connection checklist**: each named
  connection is `ok` / `needs_reauth` / `missing`, rendered as cards in the
  conversation at the moment the grant happens — connecting is part of the
  conversation's flow, not a settings page.
- **OAuth (curated):** the card starts
  `POST /v1/connections/oauth/{connector}/start` with the scope union the
  draft's granted ops require — this is the connectors spec's re-consent
  trigger, landing on its named surface. The frontend passes a return path;
  the callback lands the user back in the build with the card now `ok`.
  One consent covers Gmail and Calendar (shared `google` namespace), and
  the card says so rather than asking twice.
- **API key (remote MCP):** the card runs MCP registration
  (`POST /v1/mcp-servers` with the secret; probe verifies before anything
  persists). "Go find the key" is the moment the resumable build exists
  for: the card shows the registry entry's `key_hint` — **a new registry
  field**, per-entry copy for where the key lives ("Zendesk: Admin Center →
  Apps and Integrations → APIs") — and the build waits in `shaping`,
  surfaced on the home screen's "waiting on you" list, resumable from
  where it stopped. No timeout pressure; the 30-day abandon sweep is the
  only clock.
- **Submission blocks** until the checklist is all-`ok` (scope decision
  above). The block is a checklist, not an error.

## "Will this expose my secrets?"

The scenarios document asks how a builder gains confidence their solution
will not leak. The true answers exist — credentials never enter the
sandbox, the proxy substitutes them at the boundary, per-tenant DEKs at
rest — but the target user cannot evaluate any of that, and copy that
argues architecture reads as the fine print on a promise. The design
principle: **make each claim at the moment it is concrete, and pair every
claim with something the user can do to check it.**

Three claims, three moments, three handles:

1. **"The agent never holds your sign-in."** Said on the connection card,
   at the moment of handing over a credential. Consequence phrasing, not
   mechanism: "The agent asks Nightshift to act; Nightshift signs the
   request itself. Your password and keys never enter the agent's
   workspace." Checkable handle: "Disconnect any time — from here or from
   your Google account — and it stops working immediately." (True by
   construction: per-request permit resolution, nothing outlives the
   database check.)
2. **"It cannot reach anything outside this picture."** Said by the
   approval diagram, whose dashed boundary is also drawn as the credential
   line: keys sit outside the boundary with Nightshift, and requests cross
   it signed. One caption line, because the diagram carries it: "Your
   sign-ins stay on this side of the line." Checkable handle: the
   struck-through items — the user can see email, DMs, payments outside
   the line before ever trusting it.
3. **"Everything it does is written down — including what it was refused."**
   Said on the approve screen and again in the first-run summary. The run
   trail records every request and every denial (`proxy.denied` is audit
   content by design). Checkable handle: "see everything it did" links to
   the run's event trail; a denied request appearing there is the fence
   _visibly holding_, which teaches more than any promise.

What the copy must **never** claim: content privacy ("we can't see your
data" — false: the proxy sees request contents), or absolute safety. The
verdict's honesty posture extends here — one true, specific, checkable
claim outweighs three broad ones, and the first overclaim discovered
retroactively poisons every other promise the product made.

## The builder's four questions

The scenarios document closes with the question this spec answers, split
four ways. Where each answer lives:

- **Do they need to understand engineering?** No, by construction rather
  than by tone: intake and steps stay in job language, cron and timezones
  hide behind words-plus-next-three-fires, spend renders as money at the
  job's rhythm, constraint values arrive by picker, and provider/model
  choices left the user's hands entirely (decision 9). The only technical
  act remaining is pasting an API key, and the `key_hint` copy walks it.
- **How do they feel empowered and successful?** The verdict answers "can
  I even have this?" in the first thirty seconds; the restatement makes
  the steps recognizably theirs (prototype test 3); the diagram makes
  scope something they authored rather than accepted; and the goal-mode
  payoff screen (objectives spec) is the delegation visibly finishing.
- **How do they tinker and explore?** A build is cheap and disposable:
  nothing connects and nothing runs before they choose it, so starting a
  build to see the verdict _is_ the exploration path, and abandoning one
  costs nothing. The deeper form — scenario 2's trust journey — is the
  ladder: rung 1 read-only is "tinkering" institutionalized, with the
  ceiling visible. What is missing is re-entering a live workflow's build
  conversation to adjust it; that is scoped out below, and it is the
  gap most worth closing next.
- **How are they confident it won't expose their secrets?** The
  [three checkable claims](#will-this-expose-my-secrets), each placed at
  its concrete moment.

## What this needs from the frontend

The frontend lane's checklist. Surfaces 1 and 2 of the prototype rebuilt
over the real API:

1. **Intake** — the one box, starter phrasings, `POST /v1/builds`.
2. **Verdict render** — the three blocks from the structured verdict:
   `can`/`wrong` as conversation content, "I'd need access to" from the
   server-computed block; `no` / `not_a_fit` layouts; the two-workflow
   proposal card for splits.
3. **Build chat** — transcript over `messages` (SSE-ready), with the live
   permit diagram driven by validated draft state (never by chat text),
   highlighting new grants per the UX spec.
4. **Structural pickers** — the `options`/`select` round trip for
   constraint slots; multi-select where the resource list allows several.
5. **Connection cards** — OAuth launch/return (return-path handling),
   MCP registration with `key_hint`, the three connection states, the
   secret-safety copy from claim 1.
6. **Connection checklist / waiting-on-you** — blocked-submit rendering,
   builds list with resumable state on the home screen.
7. **Rubric editing** — criteria as editable cards (id preserved on edit),
   the rewrite-offer interaction, the empty-rubric consequence line.
8. **Schedule confirmation** — words plus next-three-fires preview.
9. **Spend line** — per-run and derived monthly figure, editable, in the
   diagram.
10. **Ladder and escalation affordances** — the hesitation path: ladder
    proposal card (full ladder visible per graduated-permits), escalation
    opt-in card for goal workflows.
11. **Handoff to approve** — submit, then the existing approve surface per
    created workflow; two-workflow builds approve sequentially.

## What this needs from server/

The server queue slot's checklist, in dependency order:

1. **Decision 9 migration** — `steps.Parse`; `compiled` column +
   deterministic compiler invoked at approval; internal run context serves
   `compiled`; public API accepts/returns user-facing steps only; pricing
   validation moves to the platform-selected model; dev-data migration as
   above. Independent of everything else and unblocks freezing the v1
   contract.
2. **The build resource** — table, statuses, abandon sweep, endpoints,
   submit path reusing the version-creation stack.
3. **The build agent loop** — control-plane LLM path with platform key,
   metering integration (`OverCap` pre-call, cost to monthly cap), turn
   cap, the closed tool surface, verdict schema validation including the
   catalog-reference check and the required-`wrong` rule.
4. **Control-plane connector client for options** — session-authed
   read-op invocation for `options_from` bindings, sharing the proxy's
   compile-and-inject code path; never accepts run tokens; rejects
   `effect: write`.
5. **Catalog additions** — `options_from` on constraint bindings;
   `key_hint` on MCP registry entries; verify per-op plain-language copy
   reads correctly in the verdict's computed access block (it was written
   for this surface; this is its first consumer).
6. **Verdict demand signal** — persist `unreachable_system` entries
   (system name, user phrasing) for catalog prioritization.
7. **OAuth return-path support** — the connect flow's `state`/redirect
   carrying a front-end return location back to the build.
8. **MCP snapshot refresh hook** — build-initiated `refresh-tools` on
   first grant from a server (endpoint exists; the build calls it).

Items 2–8 depend on the connectors implementation (the catalog and connect
flows must exist); item 1 does not and should land first.

## Testing

- A verdict citing an op absent from the catalog is rejected; a verdict
  with an empty `wrong` block is rejected.
- The computed access block contains only catalog copy for ops referenced
  by `can` — never model text.
- A draft permit granting an op no step references fails the minimality
  lint (warn), and one naming an unknown op is impossible (validator).
- `select` writes exact values; a constraint value that never appeared in
  the options response is rejected (the model cannot smuggle one through).
- The options endpoint rejects write ops, run tokens, and other tenants'
  builds (`404`).
- Submit: blocked while any connection is not `ok`; a goal draft without
  an objective (or a standing draft with one) cannot reach `ready`; a
  two-draft build creates two workflows with distinct permits; created
  versions are `draft` and unapproved.
- Compiler: deterministic (same inputs, byte-identical output); approved
  steps and rubric text appear verbatim in `compiled.system_prompt`;
  compiling at approval means a later compiler change does not alter an
  approved version's `compiled`.
- Steps v1: execution fields rejected; migration synthesizes user-facing
  steps and preserves old documents as `compiled`.
- Build metering: model calls blocked once the tenant monthly cap is hit;
  turn cap ends the conversation resumably.
- Resumability: a build survives process restart and an OAuth round trip
  with transcript and drafts intact.
- Cross-tenant: every build route reads as `404`, house pattern.

## Open questions

- **A user-supplied document estate has no artifact shape.** Scenario 2's
  290 macro files are neither a connector nor an egress destination, so
  today's permit cannot express "these files I handed you and nothing
  else". Recorded as a known ceiling, not scope here — but the verdict
  must not misclassify it: hand-me-the-files jobs are `partial`, named as
  such, not `unreachable_system` (there is no system to reach). A future
  "materials" input would slot beside `connections` in the permit without
  disturbing this spec's elicitation model.
- **Multi-party journeys exceed one-owner tenancy.** Scenario 3 has a
  patient, a care team, and an insurer; identity is one owner per tenant
  and multi-user governance is deferred everywhere. The build resource
  carries `created_by` from day one and nothing in this design assumes
  builder = approver structurally (submit and approve are separate
  endpoints), so the deferral stays cheap — but the clinical scenario's
  escalation routing ("returns exceptions to the right person") is not
  buildable until governance lands.
- **Sensitive domains.** Scenario 3 is PHI-adjacent. Nothing here designs
  domain-specific handling (retention, copy, escalation defaults for
  clinical content); if healthcare becomes a real vertical it needs its
  own pass, not a footnote.
- **Verdict latency.** A grounded verdict is one large model call; if it
  takes eight seconds, the first thing a new user experiences is a
  spinner. Streaming the `can` block as it generates is the likely answer;
  needs prototyping, not deciding here.
- **Build model cost per conversation.** Sonnet-class turns × 60-turn cap
  is real money per abandoned build. Measure in dogfooding before choosing
  rate limits beyond the monthly cap.
- **`done_when` and the grader.** The objectives spec leaves completion
  human-confirmed; once Plan 4's grader exists, whether the same
  stand-alone-text discipline makes `done_when` machine-checkable enough
  for low-stakes auto-close needs its own risk argument (that spec's
  deferral stands).
- **Nudging stalled builds.** "Waiting on you" is in-app only until
  Plan 4's email delivery lands; whether a stalled build warrants an email
  nudge, and after how long, is a product call for then.

## Explicitly out of scope

The approve surface itself (owned by the UX spec; this spec only hands off
to it), multi-user governance (one person builds and approves), editing an
approved workflow through conversation (re-entry into a build from an
existing workflow is a later feature; today edits are new versions via the
API), voice or non-chat intake, build-time invocation of write ops or MCP
tool calls, per-tenant model overrides for the build agent or compiler,
and any implementation plan (this spec precedes one deliberately).

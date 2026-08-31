# steps v1 — implementation notes

Scope: item 1 of "What this needs from server/" in the build-conversation spec
(decision 9). Items 2–10 untouched.

## Confirmed facts (tree)

- Migrations end at `00009_identity.sql` → this change is `00010`.
- `httpapi.Deps` = {Store, Engine, Vault, PublicBaseURL, Mailer}; mutating
  routes wrapped `mut(auth(...))`; tests mint session rows.
- Rubric criterion id charset (grading spec line 69): slug `[a-z0-9]` with
  interior hyphens, ≤ 64 chars.
- `store.VersionDoc` keeps permit/rubric as `json.RawMessage`; steps was a
  typed execution struct — becomes RawMessage (user-facing v1 doc).
- Harness `Steps` shape unchanged: {system_prompt, kickoff, provider, model,
  max_tokens} — compiled doc is a superset (adds `compiler_v`), harness's
  lenient decode ignores it. Internal-context contract does not change shape.
- No `NIGHTSHIFT_RUN_PROVIDER`/`NIGHTSHIFT_RUN_MODEL` existed before; added.

## Decisions (autonomous, conservative)

- New package `internal/steps`: `Parse` (house idiom, mirrors `permit.Parse`:
  DisallowUnknownFields + trailing-data check → execution fields rejected by
  construction), `Compile` (deterministic template assembly, `compiler_v: 1`).
- `compiled` written inside the ApproveVersion transaction (store gains a
  `compiled` argument) so an approved row always carries its compiled form.
- Pricing 400 moves entirely to approval time (the only moment a model is
  selected); create/add no longer touch pricing at all. Platform model from
  `NIGHTSHIFT_RUN_PROVIDER`/`NIGHTSHIFT_RUN_MODEL`, defaults
  `anthropic`/`claude-haiku-4-5` (priced, cheapest catalogued).
- `max_tokens`: no spend cap → 4096 (harness default); with cap →
  floor(per_run_cents × 1M / out_price), clamped to ≤ 8192 so a large budget
  cannot compile a max_tokens beyond what every catalogued model accepts.
  Helper `llm.MaxTokensForBudget`.
- Kickoff: compiled kickoff is fixed text; the internal context handler
  appends the fire occasion (manual / scheduled occurrence at fire_time) from
  the run row — deterministic string assembly, no model calls.
- Rubric is still opaque JSON; the compiler embeds it verbatim (compact)
  when it is a non-empty object, per "the agent should know the promises it
  is graded on". Escalation affordance and goal-mode objective: not built
  yet, deliberately absent from compiler v1.
- Migration copies old steps → `compiled` + `{"compiler_v":0}` for ALL rows
  (spec: "existing rows"), synthesizes
  `{"v":1,"steps":[{"id":"job","text":<old kickoff>}]}` as user-facing steps.
  Down migration drops `compiled` only — the steps transform is one-way
  (pre-release dev data, per spec's alpha-notice framing).

## Polish round (adversarial review, 4 agents)

Fixed: approval now fails closed on a corrupt permit instead of silently
compiling the default max_tokens (was loosening the owner's spend cap on
corrupt data); DB CHECK `approved → compiled` added to 00010 (matching the
table's DB-enforced-invariant pattern); the harness-shape test decodes into
the real `harness.Steps` type; several comment/doc inaccuracies corrected.

Reported, deliberately not implemented:

- Approval does not cross-check the platform run provider against the
  permit's `llm.providers` allowlist — a mismatch surfaces as a proxy
  denial mid-run rather than a 400 at approval. Whether approval should
  gate on it is a permit-semantics question for the connectors work
  (the permit shape changes there), so recorded, not built.
- `steps.Compile` accepts a zero `Platform{}` without error; its one
  caller guards (Priced check first). An error return would be safer if
  a second caller appears.

## Risks / follow-ups

- Migrated user-facing steps text is the old kickoff verbatim: may be empty
  or > 500 chars. Parse guards the API boundary only; stored dev rows are
  readable regardless. Acceptable per spec ("dev data stays runnable").
- versionJSON now returns `steps` as the raw stored doc; compiled is never
  serialized in httpapi.

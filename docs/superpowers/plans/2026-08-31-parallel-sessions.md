# Nightshift — Parallel Session Coordination

**Date:** 2026-08-31
**Purpose:** the live picture of which work runs in parallel, which work is
serialized on `server/`, and ready-to-paste prompts for the next sessions.
**Single writer:** the coordinating session owns this file. Route board
findings to it rather than editing here; if its socket is stale, ask the user
which session currently holds the board.
Owned by the coordinating session; updated when a plan merges, a session
finishes, or a cross-cutting decision is taken.

## State of the world

| Thread                           | State                                                               |
| -------------------------------- | ------------------------------------------------------------------- |
| Plan 1 — Foundation              | **Merged** (PR #1)                                                  |
| Plan 2 — Egress proxy + vault    | **Merged** (PR #5)                                                  |
| Plan 3 — Scheduling + metering   | **Merged** (PR #10)                                                 |
| Identity spec                    | **Merged** (PR #6)                                                  |
| Identity implementation          | **Merged** (PR #17)                                                 |
| Steps v1 (decision 9)            | **Merged** (PR #19) — **`server/` is FREE and unassigned**          |
| Connector-catalog spec           | **Merged** (PR #8) — plan owed                                      |
| Delegation specs                 | **Merged** (PR #9) — escalation, permits, objectives; plans owed    |
| Substrate verification spike     | **Merged** (PR #7) — corrections owned by the docs lane             |
| Plan 4 spec — grading + alerting | **Merged** (PR #13) — implementation owed                           |
| Plan 5 spec — Compute            | **Merged** (PR #12) — implementation owed                           |
| Build-conversation spec          | **Merged** (PR #14) — implementation owed                           |
| Docs corrections + roadmap       | **Merged** (PR #15)                                                 |
| Upstream Substrate egress PR     | **Suspended** — see the outward-facing-actions rule below           |
| Frontend (`web/`) + CLI          | **Lane OPEN, nothing started** — no `web/` directory exists         |
| User research / demo re-skin     | Branch `demo/dev-persona` — a permanent demo variant, not for merge |

## The rule

**One session owns `server/` at a time.** Everything else — specs, research,
`src/`, and the frontend at `web/` — parallelizes freely. Doc PRs merge to
`main` whenever ready.

The frontend does **not** take the `server/` lock: it is a new top-level
directory and a permanent parallel lane. A real CLI does take the lock, since
it extends the existing `server/cmd/nightshift` binary
(`migrate` / `serve` / `dev-session`).

### Serialized `server/` queue

1. ~~**Plan 3**~~ (merged, PR #10) →
2. ~~**Identity implementation**~~ (merged, PR #17) — replaced the session
   mechanism across httpapi and every test helper; retired
   `NIGHTSHIFT_SESSION_KEY`. **`server/` is now free and unassigned. The next
   owner is a decision, not a default** — see the note under item 3. →
3. **Connectors** (plan + implementation — collides with `permit.Parse`, the
   proxy, and the harness, and wants identity's session changes settled; four
   downstream specs depend on its operation vocabulary). **Contested:** the
   build-conversation spec's first server item — user-facing steps v1 plus the
   approval-time compile, resolving scoping decision 9 — is deliberately
   independent of connectors and is a much smaller slot. Taking it first
   unblocks the frontend sooner, because the frontend cannot consume a `steps`
   contract that is still the compiled execution form. Connectors is the bigger
   unlock but the longer occupancy. →
4. **Plan 4** — grading + alerting. Creates `workflow.status`
   (`active`|`paused`) and delivers **Plan 3 amendment 3** (`engine.Fire`
   re-checks status). Objectives widens the CHECK later. →
5. **Escalation** (carries Plan 3 amendment 1) →
6. **Objectives** (widens `workflow.status`; no longer carries amendment 3) →
7. **Graduated permits** (hard dependency on Plan 4's grader) →
8. **Plan 5** — Substrate + K8s-Jobs Compute

Escalation precedes objectives because the objectives spec declares the
dependency. Plan 5 stays last, and the verification spike confirmed that was
right rather than merely asserted.

**Unslotted, needs a decision:** the build-conversation spec resolves roadmap
scoping decision 9 (the user-facing `steps` artifact joins the version
document; the execution form becomes server-derived). That is a `server/`
change with no queue position yet. It most likely rides with connectors.

### Parallel-safe lanes, open now

- **Frontend at `web/`** — the product's entry point, not a later phase.
  **Both blockers are cleared** (identity, PR #17; build-conversation spec,
  PR #14). Nothing stands in front of it, no `web/` directory exists yet, and
  no session is scoped for it.
- **Docs, specs, research** — always open.
- **External contributions** (e.g. the upstream Substrate egress work).
- Anything in `src/`, user research, prototype work.

## Rule: outward-facing actions

Added 2026-08-31 after an issue was opened on a third-party repository
(`agent-substrate/substrate#1332`) that the user had not meant to authorize.
It was closed eleven minutes later; the footprint was one issue and nothing
else.

**For any lane touching a repository we do not own**, an approval must name
the exact artifact being published. A paraphrase, a pronoun, or a near-miss —
"open the pr" when only a drafted issue exists — is not approval. It is a
question to ask.

The failure mode to avoid is reasoning that one interpretation is "safe under
every reading." When an instruction is ambiguous **and** the action is
irreversible **and** it is public **and** it runs under the user's identity,
the ambiguity is itself the stop signal. Quote the words back and wait; it
costs one message.

This project tracks its own work in docs, not issues. An external issue
tracker is only ever the required entry protocol for contributing upstream —
never our own bookkeeping.

## Cross-cutting decisions

Recorded so the next session doesn't rediscover them. Each was settled by one
session and affects others.

- **Postmark is the single transactional email provider** (Plan 4 spec,
  PR #13). This closes the identity spec's "email provider (shared with
  Plan 4)" open question; identity builds magic-link delivery against it.
- **Pause is `workflow.status`, not a parallel boolean** (Plan 4). One
  lifecycle model: `active|paused` now, objectives widens to add
  `completed|abandoned`. `streak_anchor_at` is stamped on resume and on
  version approval so a resumed workflow doesn't immediately re-pause on its
  old failure streak.
- **`spend.exceeded` ×3 feeds auto-pause** (Plan 4), answering an open
  question Plan 3 left. Budget failures are hard failures, not quality
  failures.
- **The grader gets a justified egress-proxy exemption** (Plan 4). All other
  LLM traffic still goes through the proxy.
- **`require_clean_rubric` means succeeded ∧ graded ∧ passed** (Plan 4), the
  contract the graduated-permits implementation builds against.
- **Egress default-deny is entirely ours** (Plan 5 spec, PR #12). Upstream
  Substrate does not ship it — the worker's nftables `forward` policy is
  `accept`. The Plan 5 design assumes upstream `EgressPolicy` datapath
  enforcement does not land in our timeframe. Upstream is actively designing
  delivery under `agent-substrate/substrate#1325` (opened 2026-08-30);
  gateway-side enforcement is maintainer territory.

---

## Delta sheet: what Plan 3 changes (for the identity session's plan)

**Plan 3 is now merged — these deltas are live on `main`.** Verify each
against the real tree; late additions beyond the original sheet: the
scheduler/reaper/meter wiring in `serve()` (do not displace it),
`httpapi.approveVersion` re-checks `llm.Priced` on the stored draft, and
`internal/engine`, `internal/schedule`, `internal/meter` are new packages.

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
- **Execution gate: OPEN (2026-08-31).** Plan 3 (PR #10) is merged and the
  coordinating session has green-lit the identity implementation — it owns
  `server/` until its PR merges.

## Delta sheet: what steps v1 changes (merged)

**Merged as `3d48839` (PR #19); this is live on `main`.** Verified against the
tree, not against session summaries. The v1 steps contract is now what the
build-conversation spec defines, so the frontend lane can rely on it.

- **Migrations on `main` now run through `00010_steps_v1.sql`**
  (`00009_identity.sql` landed with identity); next free is **00011**.
- **`workflow_version` gains `compiled jsonb`**, with a DB CHECK enforcing
  **approved ⇒ compiled** — and tighter than that phrase suggests:
  `CHECK (status <> 'approved' OR (compiled IS NOT NULL AND jsonb_typeof(compiled) = 'object'))`,
  which rejects SQL `NULL`, JSONB `null`, **and** non-object values. The dev-data migration copies each old
  execution-form steps document into `compiled` stamped `compiler_v 0` and
  synthesizes a v1 user-facing artifact from the old kickoff.
- **`store.ApproveVersion` now takes a trailing `compiled json.RawMessage`**
  and writes the column inside the approval transaction. Its doc comment names
  the escalation amendment flow as the documented second caller that must
  **recompile rather than copy**.
- **The internal run context refuses a compiled document lacking
  provider/model** before marking the run running (a pre-merge review fix).
- **`store.StepsDoc` is gone**; `VersionDoc.Steps` is `json.RawMessage`.
  `harness.Steps` is unchanged.
- **`httpapi.Deps` gains `RunProvider` / `RunModel`**
  (`NIGHTSHIFT_RUN_PROVIDER` / `NIGHTSHIFT_RUN_MODEL`, defaulting to
  `anthropic` / `claude-haiku-4-5`). They must be a **priced pair** or
  approvals 400 — decision 9 took provider and model out of the user's hands.
- **The public API accepts only `{v: 1, steps: [{id, text}]}`**; an
  execution-form steps document is a 400. The reserved alpha break in
  `docs/api/v1.md` is marked spent.
- **For the connectors lane:** approval does **not** cross-check the platform
  run provider against the permit's `llm.providers` allowlist. A mismatch
  surfaces as a proxy denial at run time rather than a 400 at approval.

## Delta sheet: notes for the escalation implementation

From the delegation-specs session, verified against `main` @ b7af659 after
Plan 3 merged (2026-08-31):

- **Amendment 2 is done** — the reaper keys off
  `COALESCE(dispatched_at, created_at)` (`store/run.go:239`); a suspended
  run that resumes days after creation is no longer reaped on sight.
- **Amendment 1 remains open and belongs to escalation**: the
  `run_one_active_per_workflow` index predicate still reads
  `status IN ('pending','running')` and must gain `awaiting_input`.
- **Amendment 3 moved to Plan 4** (spec PR #13, 2026-08-31). Plan 4 creates
  `workflow.status` and has `engine.Fire` re-check it, so objectives inherits
  a delivered amendment rather than owing one.
- **Two store guards block escalation resume as written** (correct for
  Plan 3, must be widened — not relaxed — by escalation):
  - `MarkRunDispatched` updates `WHERE dispatched_at IS NULL` and returns
    `ErrNotFound` if already stamped; resume must re-stamp `dispatched_at`.
  - `ResetRunToken` requires `status = 'pending' AND dispatched_at IS NULL`;
    a resumed run satisfies neither, but needs a fresh token because
    suspension clears `runner_token_hash`.
  - The right generalization: both guards exist to ensure **no live token
    holder** when the token swap happens. "Never dispatched" implies that
    today; "suspended with the hash cleared" implies it equally. Widen the
    predicates to state the no-token-holder invariant directly rather than
    dropping them.

- **Two consequences of steps v1 for the amendment flow** (raised by the
  delegation-specs session; **live on `main` since PR #19**). The escalation spec's
  amendment flow creates workflow version N+1 and approves it
  **programmatically**, not through the HTTP approve handler — so it inherits
  both approval-time changes without passing through their validation.
  - **Recompile, do not copy.** `approved ⇒ compiled` is a DB CHECK, and
    copying the prior version's `compiled` blob satisfies it while silently
    pinning a stale compilation if the platform provider or model has moved.
    That path must run the compiler — `store.ApproveVersion`'s doc comment
    names it as the caller that must. The CHECK also tests
    `jsonb_typeof(compiled) = 'object'`, so a programmatic approve passing a
    JSONB `null` or a non-object now fails the constraint rather than slipping
    through with an unusable zero-valued context.
  - **Pre-check pricing when the escalation is opened, not when it is
    answered.** The priced-pair gate lands on this path too, and lands badly:
    an unpriced provider/model becomes a 400 at the moment a user clicks
    approve on an escalation, with the run already suspended in
    `awaiting_input`. A bad platform config should surface before a human is
    asked a yes/no question mid-run.

## Owed, with owners

- **Connector implementation plan** — after identity lands, written against
  the then-current tree.
- **Platform-spec corrections from the Substrate spike** — assigned to the
  docs-corrections lane (2026-08-31), which also carries a seventh correction
  found by the Plan 5 session and verified against the code: governance
  primitive #8's "a new version is a new template and a new golden snapshot"
  is wrong. `internalapi.go:92-105` fetches `version.Doc.Steps` from the store
  per run, so ActorTemplates map to **harness releases**, not workflow
  versions; versioning's governance lives entirely in our DB. The same wrong
  claim sits in a comment at `compute.go:22-23` — that one is fixed at Plan 5
  implementation time, not by the docs lane.
- **UX-spec amendment notes** — the objectives and escalation specs each
  declare `Amends: 2026-08-28-nightshift-design.md`, but the UX spec carries
  no pointer. Same docs lane.
- **Frontend and CLI specs** — unwritten. The frontend is the product's entry
  point; nothing user-facing exists without it.
- **The user-research read-out** feeding UX changes back into the prototype.

## Delta sheet: what the identity implementation changes (for connectors, Plan 4, Plan 5)

From the identity session, written with its PR (branch `feat/identity`);
verify against the tree after it merges:

- **Sessions are DB-backed and opaque.** `httpapi.Deps` is now
  `{Store, Engine, Vault, PublicBaseURL *url.URL, Mailer mail.Sender}` —
  `SessionKey` is gone and `NIGHTSHIFT_SESSION_KEY` no longer exists.
  Tests authenticate by minting a session row:
  `httpapi.NewSessionToken()` → `store.CreateSession` →
  `httpapi.SessionCookie(value)` (see `httpapi_test.mintSessionCookie` and
  the e2e `mintSession` helpers). `SessionClaims` keeps its shape; `Role`
  is read from `app_user` at request time via the session join.
- **Every new mutating `/v1` route must be wrapped in the Origin
  middleware** — follow the `mut(auth(...))` pattern in
  `httpapi.RegisterRoutes`. Connectors' op-invocation gateway endpoints
  fall under this.
- **New env for serve:** `NIGHTSHIFT_PUBLIC_BASE_URL` (required; HTTPS
  except localhost) and optional `NIGHTSHIFT_POSTMARK_TOKEN` +
  `NIGHTSHIFT_MAIL_FROM` (unset → magic links go to the log).
- **`internal/mail` exists** with `Sender`, a Postmark implementation, a
  log fallback, and a test `Recorder` — Plan 4's alert delivery should
  reuse it rather than growing a second email path.
- **Migration `00009_identity.sql` is taken**; the next free number is
  `00010`. Emails are globally unique across tenants
  (`lower(email)` index) and stored normalized — any future boundary
  accepting an email must apply `store.NormalizeEmail`.
- **`/build` is the first-login redirect target** (constant
  `httpapi.firstLoginPath`); the SPA has no routing yet and must claim
  that route when it grows one.

## Open design items, recorded not resolved

Raised by a Codex review of the corrected platform spec (docs lane, PR #15).
Relayed to the Plan 5 session 2026-08-31 and all three folded into PR #12
the same day; kept here as the record of what was decided and why.

- **Upstream `EgressPolicy` cannot subsume our proxy.** The connectors design
  enforces per-operation and per-path rules — application layer. Upstream's
  `EgressPolicy` is network layer. Convergence can therefore only ever replace
  the network-layer floor beneath the proxy, never the proxy itself. This
  narrows the roadmap's long-standing "does the proxy have an expiry date"
  question: it does not, it has a shrinking lower half. Any convergence note
  in the platform spec should say so rather than implying full convergence.
  **Resolved in PR #12** (commit `4dc4a4b`): the permit's host-level floor
  stays compilable to upstream `EgressPolicy`; op-level enforcement and
  credential injection are ours permanently. Follow-on consistency fix routed
  to the docs lane — PR #15's tracking task still said to "avoid semantics
  theirs cannot express (e.g. per-path rules)", which the merged connectors
  spec already requires, and still implied the proxy could migrate away.
- **State retention across permit narrowing** — **resolved in PR #12** with a
  stated v1 default: state persists across narrowing (narrowing bounds future
  reach, not past knowledge, and retained state can only leave through the
  narrowed permit's destinations). Purge-on-narrow was rejected as a default
  because it would destroy the memory feature on every edit. Whether narrowing
  should _offer_ a reset is delegated to graduated permits, where permit
  transitions are first-class.
- **Retry and idempotency for cold-boot request loss** — what the control
  plane does when an invoke is lost to a cold boot or a request the router
  sheds under pool saturation, and how that stays idempotent against Plan 3's
  one-active-run admission index and `run_workflow_firetime_unique`.
  **Resolved in PR #12**: a cold-boot loss or parking-shed 503 is retried as
  the same RunID against the same run row, so retries create no rows and both
  indexes see exactly one run per occurrence. Recovery is always retry, never
  re-fire; only window exhaustion converts to a terminal `dispatch_failed`.

## Follow-ups

- **Linkify the Plan 5 compute spec references** in the platform spec once
  PR #12 merges. The docs lane cited it by filename plus "PR #12, unmerged"
  because the file is not yet in-tree (the escalation-spec precedent).

## Known ceilings, recorded not scheduled

Surfaced 2026-08-31 by reading the agent-first scenarios document against the
merged specs. Neither is a reason to change course; both should be known
before a demo rather than discovered during one.

- **A user-supplied document corpus has no model.** Scenario 2's 290 CNC macro
  files are neither a connector nor a host allowlist. The permit has no shape
  for "this estate of files I brought".
- **Acting on behalf of someone, with a third party's consent, has no model.**
  Scenario 3 involves a patient, a care team, and an insurer. Identity is one
  owner per tenant, `CHECK (role IN ('owner'))`, with multi-user governance
  explicitly deferred.

Also note: everything shipped in Plans 1-3 serves _standing_ workflows on a
cadence, while all three scenarios are _goal_ workflows that end.
`workflow.mode` exists only in the objectives spec, at queue position 6.

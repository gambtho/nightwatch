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

| Thread                           | State                                                                                                      |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| Plan 1 — Foundation              | **Merged** (PR #1)                                                                                         |
| Plan 2 — Egress proxy + vault    | **Merged** (PR #5)                                                                                         |
| Plan 3 — Scheduling + metering   | **Merged** (PR #10)                                                                                        |
| Identity spec                    | **Merged** (PR #6)                                                                                         |
| Identity implementation          | **Merged** (PR #17)                                                                                        |
| Steps v1 (decision 9)            | **Merged** (PR #19) — **`server/` is FREE and unassigned**                                                 |
| Connectors                       | **Phases 1, 2, 4 merged.** Paused by design — phase 3 on call for the build lane; 5→6 deferred, 5 before 6 |
| Connector-catalog spec           | **Merged** (PR #8) — plan owed                                                                             |
| Delegation specs                 | **Merged** (PR #9) — escalation, permits, objectives; plans owed                                           |
| Substrate verification spike     | **Merged** (PR #7) — value extracted; thread closed by the direction change                                |
| Plan 4 spec — grading + alerting | **Merged** (PR #13) — implementation owed                                                                  |
| Plan 5 spec — Compute            | **Shelved by the direction change** — spec stays merged as a record; no implementation                     |
| Build-conversation spec          | **Merged** (PR #14) — implementation owed                                                                  |
| Docs corrections + roadmap       | **Merged** (PR #15)                                                                                        |
| Upstream Substrate egress PR     | **Closed** — the direction change ends the substrate thread                                                |
| Frontend (`web/`) + CLI          | **`/setup` shipped** (PR #32) — the loop closes. CLI still not started                                     |
| User research / demo re-skin     | Branch `demo/dev-persona` — a permanent demo variant, not for merge                                        |

## Direction change (2026-08-31): customer-deployed, UX-first, endpoint-agnostic

Leadership decision, relayed by the user and confirmed in two answers to the
coordinating session. This **reverses two recorded decisions** — "hosted,
multi-tenant, operated by us" and "self-hosting is explicitly not a supported
path" (platform design, 2026-08-30) — and both reversals are deliberate.
The old rationale stands as history; do not relitigate it.

The new shape:

- **Nightshift is customer-deployed.** We ship the stack; we do not operate a
  fleet. Plan 5 (Substrate + K8s Compute) is shelved, the substrate research
  thread is closed, and the kagent question is moot.
- **The egress proxy remains the enforcement point** — it ships _inside_ the
  deployment. The blast-radius permit stays enforced, not advisory. This was
  the load-bearing question and the user answered it explicitly.
- **LLM-endpoint agnostic.** The provider layer already speaks Anthropic,
  OpenAI, and OpenRouter; the pivot adds "any OpenAI-compatible base URL" as
  deploy-time configuration.
- **Identity, vault, and metering simplify dramatically** — the user's words.
  Direction, not yet design: one deployment ≈ one tenant; per-tenant KEK
  envelope collapses toward the single master key that already exists;
  magic-link/Postmark yields to something a self-hosted operator can run
  (the log-fallback path already exists); per-run and per-workflow spend caps
  stay (they are the product's spend story), monthly tenant billing caps go.

**Verified starting point:** `serve()` already runs the entire stack — API,
proxy, scheduler, reaper, local compute — in one process against one
Postgres, and the connector e2e passes in that shape. The pivot is mostly
subtraction and packaging.

Two clarifications from the user, after the coordinating session over-read
"customer-deployed" as "technical user" — twice, and was corrected:

- **Deployer == user, and the user is still the non-technical person.** The
  bar is "click install" — a desktop-app-grade experience (no terminal, no
  docker, no database administration), not an operator persona. The UX spec's
  target user, the build conversation as front door, and all four surfaces
  survive unchanged. The dev-persona research stays the companion study.
- **OAuth is dropped entirely.** Consequences, verified: the `google-calendar`
  curated connector dies (Google user data requires OAuth), so **curated v1 is
  Slack alone** via pasted bot token; calendar/inbox arrive through remote MCP
  servers that do their own auth — the deferred phases 5→6 become the main
  connector road. Connector phase 2's OAuth machinery (~2,000 lines, landed
  the same day) is sunk. And the deferred credential-capture UX problem is now
  every connector's problem: token-paste for a non-technical user is exactly
  the friction OAuth removed, so guided capture copy becomes load-bearing.

Two hard problems this hands the pivot spec: **packaging** (the store is pgx
throughout — bundled-invisible Postgres vs a SQLite rewrite is a real
decision), and **the sleeping machine**.

**Correction (2026-08-31, found by the pivot-spec session, verified against
the code): fire-on-wake does NOT work today.** This section previously called
it half-solved; that was wrong. `mostRecentDue` has the right shape (latest
occurrence ≤ now, skip older), but it walks from `now.Add(-window)` and
`Scheduler.window()` defaults to max(2×interval, 5min) with no override from
`serve()` — an occurrence more than ~5 minutes old at wake never fires. The
pivot spec designs the fix (wake-aware lookback + a persisted
`scheduler_heartbeat` row) in its roadmap item P1. The board lesson repeats:
verify the parameters, not just the mechanism.

**The serialized queue is FROZEN pending the pivot spec.** No session takes
`server/` until the pivot spec merges and the queue is re-derived from it.

## The rule

**One session owns `server/` at a time.** Everything else — specs, research,
`src/`, and the frontend at `web/` — parallelizes freely. Doc PRs merge to
`main` whenever ready.

The frontend does **not** take the `server/` lock: it is a new top-level
directory and a permanent parallel lane. A real CLI does take the lock, since
it extends the existing `server/cmd/nightshift` binary
(`migrate` / `serve` / `dev-session`).

### Serialized `server/` queue

**`server/` is FREE and unassigned.** The connectors lane has paused by design,
not stalled. Highest-value next occupant per the dependency map below: the
**build-conversation lane** (its `server/` checklist items 2, 3, 6 — the build
resource, the build agent loop, and the verdict demand signal), because the
frontend's remaining ten checklist items all wait on the build resource.

1. ~~**Plan 3**~~ (merged, PR #10) →
2. ~~**Identity implementation**~~ (merged, PR #17) — replaced the session
   mechanism across httpapi and every test helper; retired
   `NIGHTSHIFT_SESSION_KEY`. →
3. ~~**Steps v1 (decision 9)**~~ (merged, PR #19) — the user-facing steps
   artifact plus the approval-time compiler. Taken ahead of connectors because
   it was small, independent of everything else, and the frontend cannot
   consume a `steps` contract that is still the compiled execution form.
   **`server/` has been free and unassigned since it merged.** →
4. **Connectors** — paused by design: phases 1, 2, and 4 are merged; phase 3
   remains on call after the build-conversation lane. Phases 5 then 6 remain
   deferred. →
5. **Plan 4** — grading + alerting. Creates `workflow.status`
   (`active`|`paused`) and delivers **Plan 3 amendment 3** (`engine.Fire`
   re-checks status). Objectives widens the CHECK later. →
6. **Escalation** (carries Plan 3 amendment 1) →
7. **Objectives** (widens `workflow.status`; no longer carries amendment 3) →
8. **Graduated permits** (hard dependency on Plan 4's grader) →
9. **Plan 5** — Substrate + K8s-Jobs Compute

Escalation precedes objectives because the objectives spec declares the
dependency. Plan 5 stays last, and the verification spike confirmed that was
right rather than merely asserted.

### Parallel-safe lanes, open now

- **Frontend at `web/`** — `/setup` shipped (PR #32), alongside login over
  magic-link, the approve blast-radius diagram driven only by the permit
  document, the quiet home with run history, and `/build` as an honest
  placeholder rather than a faked build conversation. **The lane is now
  largely blocked**: 10 of the 11 items on the build-conversation spec's
  frontend checklist need the build resource, which needs connectors. What it
  built — the permit diagram, steps rendering, schedule wording — are the
  components those surfaces will drive live.
- **Docs, specs, research** — always open.
- **External contributions** (e.g. the upstream Substrate egress work).
- Anything in `src/`, user research, prototype work.

## Pivot spec delivered (PR #37); NAME UNDER REVIEW

`docs/superpowers/specs/2026-08-31-tomte-pivot-design.md`, branch
`spec/pivot-click-install`. Carries the Tomte naming, the reusable
connections manager (charter item 4 upgrade), and the I/O palette positioned
in the build-conversation triage.

**The Tomte name is under leadership review (2026-08-31).** Both naming lanes
hold: the mechanical rename is COMPLETE and open as **PR #38** with an
on-hold comment (verified: server suite green, web tsc + 110 tests + build
green, real `tomte serve` boot); PR #37 keeps Tomte until the final name
lands. If the name changes, both are a one-commit `tomte→<newname>`
find-replace — the rename session confirmed nothing else on its branch
collides with the literal string — except the module-path portion, which
waits on the user's second repo-rename click (the repo is ALREADY
`gambtho/tomte`). Two rename facts worth memory: the derived-key labels
(`tomte:run-jwt` HKDF info, `tomte-oauth-state` salt) are cryptographic
inputs — renaming them invalidates outstanding run tokens, fine pre-release,
not fine later; and `src/` (retired prototype) deliberately still greps as
nightshift.

Name screening: Duende is DEAD (Duende Software is IdentityServer's company —
an identity-security vendor, our worst possible neighbor); Momoy is legally
clean but loaded (sacred Chumash Datura figure — appropriation risk;
"ugly/nasty" in Hiligaynon; one letter from the Momo creepypasta). Nisse and
Bymorning remain viable fallbacks.

Board-relevant spec contents:

- **Proposed queue re-derivation** (adopt on merge unless the user objects):
  P1 subtraction + floor (OAuth/login/mail removal, local-session mint, the
  wake-window fix, endpoint record + custom base URLs, pricing-gate rework,
  budget rename) → P2 connectors main road (Slack token capture, then old
  phases 5→6; phase 3 on demand) → P3 build conversation → P4 grading/alerting
  (OS notifications replace Postmark delivery; grader-consent copy) →
  P5–P7 escalation / objectives / graduated permits unchanged.
- **`serve()` becomes a library entry point in P1** so the packaging lane
  (desktop shell) never needs the `server/` lock.
- **CI / catalog-gate ownership is now pivot-critical, not hygiene:**
  auto-update ships catalog changes to users' machines, so the narrow-only
  rule is what keeps a silent update compatible with approve-once. Still
  unowned.
- Sunk cost named plainly: connector phase 2's OAuth (~2,000 lines incl.
  `internal/oauth` and migration 00011's oauth/epoch surface), identity's
  magic-link/signup half, Postmark, and the CASA decision retired.

## UX feedback intake (2026-08-31)

From the user, routed to the pivot-spec session the same day:

- **A standalone, reusable connections manager.** Add a connector once
  (paste a token, register an MCP server); every later build conversation
  finds it connected. The data layer already supports it — connections are
  tenant-scoped and the catalog reports `connected` — so this is surface
  design only. Folded into the pivot spec's charter item 4 (guided capture
  without OAuth) as a scope upgrade: the capture surface is durable, and
  build conversations link into it rather than owning capture.
- **A post-verdict graphical inputs/outputs palette.** The verdict's
  "I'd need access to" block made visual: possible inputs (read) and
  outputs (write) from the catalog, connected vs available-but-unconnected.
  Positioned in the pivot spec's estate triage as an amendment the
  build-conversation spec's successor gains — not designed yet. The
  build-conversation frontend items 5–6 become entry points into the
  connections manager; item 5's OAuth framing is dead regardless.

## Rule: no pre-stacked PR bases

Added 2026-08-31 after a stacked PR merged into the wrong target and a full
phase of work was silently stranded off `main`.

**What happened.** The connectors lane stacked phase 2 on phase 1 and phase 4
on phase 2, each PR based on its predecessor's branch. Phase 1 was
squash-merged to `main`; phase 2 was merged **into `feat/connectors-phase1`**
rather than being retargeted first. GitHub reported it MERGED, so it looked
done — but 24 files and roughly 2,000 lines of vault-OAuth work existed only on
that branch. It was caught by noticing `main`'s migrations stopped at `00010`
when phase 2 should have added `00011`.

**The rule for lanes shipping a phased plan: each phase PR targets `main`, and
waits for its predecessor to merge.** Review latency is cheaper than a routing
accident. Do not pre-stack bases.

If a stack already exists, the only safe merge order is: merge the base to
`main`, **retarget the child to `main`**, then merge the child. A child merged
while still pointing at its base lands in the base, not in `main`.

**A merge is not confirmation that work is on `main`.** Verify against the
tree — a migration number, a package directory, a diff against the branch —
before recording a phase as landed. This board recorded phase 2 as merged on
the strength of a MERGED status, and it was wrong.

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

## The harness path is verified (2026-08-31)

`TestEndToEndConnectorToolRun` verifies the harness/test path: a constrained
write succeeds on an approved channel, an unlisted channel is denied and
audited, and no credential ever reaches the harness. The harness projects
permit-granted operations as tools, calls them through the enforcing proxy with
credentials injected at the boundary, and returns tool-level failures as
`IsError`.

This does not establish an available end-to-end workflow on `main`: the
`nightshift serve` startup deadlock remains blocked there until PR #24 merges
and `main` is reverified.

What is still missing is the _product_, not the loop: the build conversation,
so that a non-technical person can describe a job instead of filling a form.

## Blocking defects

- **`nightshift serve` deadlocks at startup on `main`.** Found by the frontend
  lane, which could not fix it (no `server/` writes), and verified by the
  coordinating session at `server/cmd/nightshift/main.go:188`:

  ```go
  slog.SetDefault(slog.New(redact.Handler{Inner: slog.Default().Handler(), ...}))
  ```

  `slog.SetDefault` also rewires the standard `log` package to write through
  the slog default, and the handler captured as `Inner` is the original
  default handler, which itself writes via `log`. The first `slog.Info`
  re-enters and self-deadlocks on the log mutex, **before the socket binds**.
  Go's own docs warn against using the default handler as an inner handler.
  Fix: build the redact handler over an explicit `slog.NewTextHandler` rather
  than over `slog.Default().Handler()`.

  Consequence: the binary does not start. The frontend lane had to smoke-test
  against a hand-rolled `httpapi.RegisterRoutes` harness instead.
  **Fixed in PR #24** (hang reproduced on `main`, fix verified serving, suite
  green) — awaiting merge, and it should not wait behind feature work.

- **No CI exists, and a merged security control depends on it.** The repo has
  no `.github/` and no workflow files anywhere — verified. The connectors spec
  states the catalog's narrow-only rule "is enforced mechanically, not by
  convention: a CI [gate]" (line 104), and the merged **escalation** design's
  first anti-injection control rests on it: "the catalog's append-only rule
  means an operation's reach cannot have silently widened" (line 188). With no
  CI, that is convention plus a tool nobody runs.

  **Interim, in the connectors plan phase 1:** a committed catalog baseline
  plus `cmd/catalog-gate`, with the server refusing to boot on a widening
  diff. That is tamper-**evident** — a widening becomes a deliberate two-file
  edit visible in review — not tamper-proof. Only a PR-time CI diff against
  the merge base closes it.

  **CI setup is unowned and repo-global.** It is a security follow-up, not
  tooling hygiene, and it needs an owner who is not the connectors lane.

## Cross-cutting decisions

- **The product is renamed: Nightshift → Tomte** (user decision, 2026-08-31,
  provisional pending a real trademark search — "let's try for Tomte").
  Why: "Night Shift" is Apple's display feature on every Mac and iPhone — a
  direct collision for a click-install desktop app — and the repo name
  `nightwatch` collides with Nightwatch.js. Knockout screening killed
  Smallhours, Midwatch, Nightjar, Domovoi, Lutin, Duende, and Brownie;
  Tomte surfaced no software collision (a defunct German band holds the name
  in music only). The tomte — the Scandinavian household spirit that quietly
  makes its rounds while the household sleeps, and works under a contract of
  respect and due payment — is nearly a spec for the product.
  - New docs and specs use **Tomte**; dated historical specs are not
    rewritten. The pivot spec is being written under the new name.
  - The mechanical rename (binary, `NIGHTSHIFT_` → `TOMTE_`, module path,
    cookie, repo) is **the one exception to the frozen `server/` queue** —
    it rides now, before the pivot spec merges, so the spec lands unstale.
  - The GitHub repo rename (`gambtho/nightwatch` → `gambtho/tomte`) is the
    user's own click — outward-facing, not a session's to take. Old remotes
    redirect.
  - Still owed: trademark counsel check and domain/handle availability.

- **Gmail is out of v1; curated connectors are Calendar + Slack** (decided
  2026-08-31; the user delegated the call to the coordinating session).
  Google CASA is months of lead time plus real money and annual renewal, and
  the evidence did not justify it for a product with zero users: the design's
  worked scenario (tickets in, digest to Slack) does not need Gmail, none of
  the three agent-first scenarios require Gmail read, and alerting already
  chose Postmark so outbound email needs no Google scopes.
  - **The "metadata-first" option never existed.** Verified against Google's
    current Gmail scope documentation: `gmail.metadata` is itself a
    **restricted** scope, as are `readonly`, `compose`, `insert`, `modify`,
    and `settings.sharing`. Only `gmail.labels`, `gmail.send`, and the add-on
    scopes fall outside. A metadata-first connector would have needed CASA
    too, so the choice was always Gmail-or-not.
  - **Gmail is removed from the catalog definitions and the baseline**, not
    shipped unconsentable — an entry nobody can consent to still appears in
    `GET /v1/catalog` and the build conversation would offer it. A connector
    removal is a narrowing, so the catalog gate accepts it.
  - **Reversible:** re-adding Gmail is a catalog addition plus the CASA
    process, not a rework. Revisit if user testing shows inbox is the binding
    constraint rather than one of three.
- **Connector phases resequenced to 1 → 2 → 4**, deferring 3 (options client)
  and 5/6 (remote MCP), to reach a workflow that actually does something
  sooner. Dependency-checked and endorsed by the connectors lane: phase 4
  depends only on 1 and 2, and phase 3 serves the build conversation, which
  does not exist yet. **5 must still precede 6** whenever MCP returns.
- **`GET /v1/catalog` shape, for the frontend lane** (connectors phase 1,
  PR #27): session-authed, no query params,
  `{connectors: [{id, name, description, auth_provider, connected, ops: [{name, description, effect, scopes, args_schema, constraints}]}]}`.
  Two rules a permit-builder UI must know: a granted op must carry a resources
  list for **every** field named in that op's `constraints`, or the version
  write 400s; and resources on fields **not** in `constraints` also 400,
  because unenforceable narrowing is rejected as false security. `connected`
  is a plain boolean now; a richer status field joins it in phase 2.

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

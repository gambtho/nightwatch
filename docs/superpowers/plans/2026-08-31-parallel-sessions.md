# Tomte — Parallel Session Coordination

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
| Pivot spec (click-install)       | **Merged** (PR #37) — queue re-derived from it below                                                       |
| Rename Nightshift → Tomte        | **Merged** (PR #38) — name FINAL (user, 2026-08-31); trademark counsel still owed                          |
| `serve` startup deadlock         | **Fixed and merged** (PR #24) — `main` boots; the rename lane verified a real `tomte serve` boot           |
| P1 — Subtraction and floor       | **Merged** (#48, plan #45) — the floor is on `main`; `server/` free until the hygiene pass takes it        |
| CI / catalog gate                | **Merged** (#41) — no-CI defect closed; `tomtectl/` workflow still owed (routed to estate-cleanup prompt)  |
| Packaging shell (`app/`)         | **Merged as record** (#42, Wails v3 spike) — lane PAUSED by direction change 2; `app/` gets a paused banner |
| Frontend pivot surfaces          | **Merged** (#43, #46, #47) — lane idle; login retirement now URGENT (P1 deleted its endpoints), in cleanup |
| Root README + MIT license        | **Merged** (#44)                                                                                           |
| K8s agent track (THE FOCUS)      | **K1 MERGED** (#49); **K2 GO** (user, 2026-08-31) — K1 session exited, fresh-session prompt below           |
| Lean-in cleanup                  | **COMPLETE AND FULLY MERGED** (#51, #52, #54, #55, #56) — the repo is onboarding-ready                     |
| P2 — Connectors main road        | **OWNS `server/`. PR A MERGED (#57)**; phase B (key-verify + spend_entry ledger, migration 00013) is GO    |
| Pivot demo (`demo/tomte-pivot`)  | **Delivered** (2052be6, verified from fresh checkout; five presets in). Permanent demo branch, never merged |

## Direction change 2 (2026-08-31, evening): K8s-first agent track, CLI before UI

Leadership decision, relayed by the user. The verbatim ask: **"can we have a
template for an agent that creates a hello world agent running on a k8s
cluster, then expand to leverage llm to enhance the agent, allow connectors,
etc -- use a simple cli to get the agent running on k8s"** — with the stated
frame: focus specifically on an agent running on Kubernetes (simplest
possible solution, no Substrate), CLI rather than UI, and from there
**transition to the full Tomte experience** (governance, defined
connectors, etc.).

A second verbatim leadership quote (relayed 2026-08-31, same change):
**"having an artifact that shows my agent topology -- almost agent as code
(ideally yaml template or something like that)"** — the template is not an
internal config detail; it is a first-class, human-readable **agent-as-code
YAML artifact** that shows the agent topology. The YAML is the deliverable
leadership wants to be able to look at.

Three shaping answers from the user, deliberate — do not relitigate:

- **Phase-1 shape: CLI-local, agents on K8s.** No hosted control plane at
  first: a simple CLI (kubeconfig-driven) gets the agent running on a
  cluster. The coordinator's recommendation (whole verified stack
  in-cluster) was considered and not chosen.
- **Bare agent first.** Phase 1 ships WITHOUT the governance spine — no
  egress proxy, no permits, key via K8s Secret. Governance arrives in the
  transition, not on day 1. (Recorded with the coordinator's named risk:
  retrofitting enforcement is the expensive path; the transition phases
  must treat the existing proxy/permit stack as the destination, not
  reinvent it.)
- **P1 continues as planned.** The server/ lane keeps building the Tomte
  floor — the "full Tomte experience" the K8s track transitions into is
  the P1+ stack. The pivot-spec estate is NOT re-triaged by this change.

Lane dispositions: **K8s agent track is the focus** (new lane, prompt
below). **Packaging (`app/`) is paused** — the desktop shell is not the
near-term path; PR #42 stays as the record; the pending product calls
(platform order, update feed) are moot while paused. **Frontend goes idle**
after its three open PRs (#43/#46/#47) merge; login retirement still
follows P1's local-session mint whenever the user wants it. CI, README,
demo lanes are unaffected. Click-install's ultimate fate (shelved vs
deferred) is deliberately NOT decided — the user answered the desktop
question with the quote above; nothing forecloses either path.

## K8s track K1 delivered (PR #49) — decisions of record

From the K1 session (2026-08-31), coordinator-verified checks green:

- **Naming/structure**: new top-level `tomtectl/`, own Go module
  (`github.com/gambtho/tomte/tomtectl`), binary `tomtectl` (kubectl-echo;
  cannot collide with a future server-extending `tomte` CLI). Commands:
  `init` / `up` / `status` / `logs [--follow]` / `down`; agent resolved
  from `-f` (default `./agent.yaml`).
- **Schema**: `apiVersion: tomte.dev/v1alpha1, kind: Agent` per the board's
  format decision; spec nouns are Tomte's own — `steps: [{id, text}]` +
  `schedule.every` (restricted `<n><s|m|h>`), commented empty `llm: {}` /
  `connectors: []` slots, **rejected if non-empty at K1** rather than
  ignored. `llm` vocabulary aligned with `web/src/local/endpoints.ts`.
- **Mechanism**: client-go (not kubectl shell-out) — self-contained binary,
  real status/logs, K2/K3 need the typed API. Plain namespaced ConfigMap +
  Deployment, label `tomte.dev/agent`; no CRD/operator/Helm. Runtime is an
  explicitly-labeled placeholder (busybox + script re-reading the mounted
  agent.yaml each wake — behavior never in the image); K2's real runtime
  image replaces it on the same mount contract.
- **Owed**: no CI workflow covers `tomtectl/` — routed to the
  estate-cleanup prompt below (`tomtectl.yml` mirroring `server.yml`).

## Lean-in cleanup (2026-08-31): onboarding-ready repo

User decision: fully lean into direction change 2 and clean the repo for
incoming team members — remove no-longer-relevant code, general hygiene.
Settled calls: **the retired prototype is removed from `main`** (history
and the two demo branches keep it); **web/ stays in-tree, idle** (its three
PRs merged first); **hygiene and P2 parallelize** — hygiene takes the
`server/` lock now, P2 writes its plan lock-free and takes the lock when
the hygiene PR merges. All pre-cleanup PRs (#40–#48) are merged; cleanup
runs on the post-P1 tree.

Three cleanup lanes + P2, prompts below. Estate and docs lanes are
lock-free; hygiene holds `server/`.

### Prompt — Estate removal (repo-global, no `server/` lock)

> You own the estate-removal lane: delete what the pivots left behind, so
> a new team member never trips over dead code. Read the board
> `docs/superpowers/plans/2026-08-31-parallel-sessions.md` (direction
> change 2, the P1 delta sheet, and the K1-delivered section) first. Work
> in a linked worktree; PRs to `main`; you may split into independent PRs
> (no stacking). Do not touch `server/` internals (the hygiene lane holds
> that lock) — the only `server/`-adjacent thing you own is nothing; web,
> root, and `.github` are yours.
>
> 1. **Remove the retired prototype from `main`** (user-approved): `src/`,
>    root `index.html`, root `package.json` + `package-lock.json`, root
>    `tsconfig*` files, `vite.config.ts`, `dist/`, and any root vitest
>    config — the pre-pivot demo app, deliberately still
>    nightshift-branded. Git history and the demo branches
>    (`demo/dev-persona`, `demo/tomte-pivot`) keep their own copies —
>    verify both branches exist on origin before deleting, and do not
>    touch them. Check for anything that references the root app: the
>    pre-commit hook's prettier step, `.gitignore` entries, README repo
>    layout, CI path assumptions.
> 2. **Retire the web login screen** (its endpoints died in P1 — see the
>    P1 delta sheet): remove the magic-link login UI and its API calls;
>    the session now arrives via the shell/dev mint and
>    `GET /local/handoff?token=&next=`. Keep this PR minimal — dead-route
>    removal plus whatever redirect keeps the dev flow working
>    (`tomte dev-session` still mints). 161+ web tests green after.
> 3. **Add `.github/workflows/tomtectl.yml`** mirroring `server.yml`'s
>    gofmt/vet/test shape for the `tomtectl/` module — owed by K1 (PR
>    #49). Verify it runs green on your own PR once #49 is merged (base
>    it on post-#49 main).
> 4. **Mark `app/` paused**: a short banner at the top of `app/README.md`
>    — paused by direction change 2, board is the authority, code stays
>    as the Wails v3 record.
> 5. **Update the root README's repo-layout section** to match the
>    post-removal tree (prototype gone, `tomtectl/` added and named as
>    the current focus, `app/` marked paused, `web/` marked idle).
>
> Verification: server suite, web tests + build, tomtectl build/test all
> green; CI green on each PR; grep for dangling references to the deleted
> paths before calling it done.

### Prompt — Server hygiene pass (takes the `server/` lock NOW)

> You own the `server/` hygiene pass — you hold the `server/` lock from
> launch until your PR merges; announce the merge to the coordinating
> session so P2 can take the lock. Read the board (P1 delta sheet, frozen
> labels) first. Linked worktree; one focused PR to `main` (two only if
> mechanical-deletion vs comment-polish split genuinely helps review).
>
> Scope — **hygiene only, zero behavior change**:
> - Dead code left at P1's subtraction edges: orphaned helpers, unused
>   exports, unreferenced test fixtures, unused deps (`go mod tidy`).
> - Stale comments and doc comments: references to magic links, Postmark,
>   OAuth, monthly tenant billing, hosted/multi-tenant framing, "Plan N"
>   numbering that no longer guides anyone — rewrite to present truth or
>   delete; comments should state constraints, not history.
> - TODO triage: fix the trivial ones, route the real ones to the board,
>   delete the stale ones.
> - `gofmt`, `go vet`, and staticcheck (if installable) clean.
> - Use your polish/improve skills (e.g. `my:improve`, `my:polish-core
>   --fix`) and apply only safe, high-confidence fixes.
>
> Hard constraints: no API or wire-format changes, no migrations, no
> public-symbol renames beyond deleting dead ones, the frozen label
> `tomte:run-jwt` untouched, full suite + connector e2e green, real
> `tomte serve` boot before the PR. If you find a real bug, report it to
> the coordinating session rather than widening this PR.

### Prompt — Docs re-orientation for onboarding (lock-free, docs only)

> You own the docs-onboarding lane: make the repo legible to new team
> members joining after two same-day direction changes. Read the board
> and the root README first. Linked worktree; one PR to `main`; you
> rewrite no dated historical spec (project convention) — banners and
> indexes only.
>
> 1. **`docs/README.md` index**: every doc under `docs/`, split into
>    LIVING (the board, the pivot spec, the P1 plan, K1/tomtectl docs,
>    api/v1.md) vs HISTORICAL (superseded specs and plans — one line each
>    on what superseded it). Newcomers read top to bottom and know what
>    is current in five minutes.
> 2. **Banners on superseded docs**: a 2–3 line header note on each
>    historical spec pointing to its successor (precedent: the two design
>    anchors already carry banners). Content below the banner untouched.
> 3. **Root README refresh**: lead with the current direction — the K8s
>    agent track, the agent-as-code YAML, a `tomtectl` hello-world
>    pointer — then the Tomte destination (governance/connectors) as the
>    arc, then repo layout (coordinate with the estate lane: write the
>    layout post-prototype-removal). Keep the no-trademark-claims and
>    built-vs-designed constraints from the README prompt.
> 4. **A short "working here" section** (root README or CONTRIBUTING.md):
>    the lane model, the one-session-owns-`server/` rule, no pre-stacked
>    PR bases, where the board lives, and the 15-minute path: clone →
>    read → hello world on kind.
>
> Verify every command you document by running it. Accuracy over polish.

### Prompt — P2: connectors main road (plan now; lock after hygiene)

> You own P2 — the connectors main road, the next `server/` occupant.
> Sequencing is explicit: **write and PR your implementation plan first
> (docs-only, no lock needed); take the `server/` lock only when the
> hygiene pass's PR has merged** (ask the coordinating session if
> unclear). Read the board (P1 delta sheet, five-preset decision, direction
> change 2), the pivot spec's "Credentials without OAuth" and capture-guide
> sections, and the merged connectors spec. Plan against the merged
> post-P1, post-hygiene tree.
>
> Scope, in order:
> 1. **Slack token capture + verify**: the structured capture guide in the
>    catalog `auth` block (`start_url`, steps, `secret_prefix: xoxb-`,
>    `verify_op: auth_test`), control-plane verify at paste time via the
>    session-authed path (never a run token), scope warning from Slack's
>    `x-oauth-scopes` response header. The Slack `manifest_url` still has
>    no hosted home (board open question) — ship the plain-create flow;
>    do not claim pre-fill.
> 2. **First-run LLM key verification** (assigned to P2 by the board after
>    a Codex coverage-gap finding): the spec's disclosed, metered "one
>    live, minimal call" through the proxy path at paste time, cost
>    recorded so it counts against the budget once set.
> 3. **Old connector phases 5 → 6** (remote MCP registration/discovery
>    with the full SSRF defense set, then MCP proxy enforcement — 5
>    strictly before 6).
> 4. **Phase 3 (options client)** only when the build conversation lane
>    needs it — confirm with the coordinating session before starting it.
>
> The frontend's merged connections manager (#47) coded its expectations
> into fake seams at `web/src/local/connections.ts`
> (ok/needs_reauth/missing states, capture guides, verify-then-store, MCP
> registry) — implement to those contracts or flag the divergence to the
> board before diverging. Each phase PRs to `main`, waits for its
> predecessor; no pre-stacked bases. Suite + e2e green per PR.

## P2 plan decisions, coordinator-ruled (PR #53, 2026-08-31)

Four calls led in the P2 plan, ruled by the coordinating session:

1. **Verify-then-store inside `PUT /v1/connections` — accepted.** One call,
   matching the merged #47 frontend seam; a separate verify endpoint would
   add a round trip and a partially-verified state for no gain. Re-paste of
   the same connection must stay idempotent and re-verify.
2. **Slack verify requires body-level `ok:true` — accepted, load-bearing.**
   Slack returns HTTP 200 with `{ok:false, error:"invalid_auth"}` for bad
   tokens; a status-only check would store garbage. This is the kind of
   vendor quirk the capture-guide design exists for.
3. **"Through the proxy path" read as shared machinery, not a literal
   proxy hop — accepted as spec interpretation.** The literal hop cannot
   work pre-storage (run-token-only; the connector route strips
   `x-oauth-scopes`). The spec's own capture-guide section already blesses
   control-plane verify for connectors ("session-authed options path —
   never a run token"), so the same posture for the LLM key is consistent.
   Two conditions attach: the shared code must be the proxy's actual
   validation + injection machinery (base-URL validation, header
   injection), not a reimplementation; and the call stays disclosed +
   metered (PR B's `spend_entry` ledger records it — the plan's finding
   that no non-run spend path exists today is verified correct, and the
   ledger is the right fix, migration 00013).
4. **MCP registry ships schema-only, no vendor list — accepted.** Vendor
   entries are content; they follow when real vendors are chosen.

Frontend divergences flagged in the plan (additive `missing_scopes` on
VerifyResult; `registerMcpServer` response gains the created row) are
accepted as additive; the frontend wires them when it wakes.

**Phase A review saves (PR #57), for the board's memory:**

- **The catalog gate's baseline fingerprint silently excluded the new
  `capture` block** — a repointed `verify_op` would not have demanded a
  baseline update, a real hole in the narrow-only control's coverage as
  the catalog grows fields. Now covered, with a drift test so the next
  new catalog field cannot silently escape the fingerprint either.
- **Verify initially failed OPEN on an unreadable 2xx** — a WAF or
  TLS-proxy HTML interstitial would have "verified" a token. Now fails
  closed, tested. Pattern worth repeating: every verify path fails
  closed on anything but a well-formed positive.

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

~~**The serialized queue is FROZEN pending the pivot spec.**~~ **Unfrozen
2026-08-31:** the pivot spec merged (PR #37) and the queue below is re-derived
from its "Proposed new roadmap" section, adopted as proposed.

## The rule

**One session owns `server/` at a time.** Everything else — specs, research,
`src/`, and the frontend at `web/` — parallelizes freely. Doc PRs merge to
`main` whenever ready.

The frontend does **not** take the `server/` lock: it is a new top-level
directory and a permanent parallel lane. A real CLI does take the lock, since
it extends the existing `server/cmd/nightshift` binary
(`migrate` / `serve` / `dev-session`).

### Serialized `server/` queue — re-derived from the pivot spec (2026-08-31)

The pre-pivot queue (positions 4–9: connectors → Plan 4 → escalation →
objectives → graduated permits → Plan 5) is superseded. History: Plans 1–3,
identity, and steps v1 are merged; connector phases 1, 2, 4 are merged.

**`server/` is assigned to P1 on launch.** The queue, from the pivot spec's
"Proposed new roadmap":

1. **P1 — Subtraction and floor** (prompt below): remove OAuth
   (packages, routes, store paths, migration dropping the `oauth` kind +
   `epoch`, catalog defs) and login/mail; local-session mint + handoff token;
   wake-aware scheduler window + `scheduler_heartbeat`; endpoint record +
   custom base URLs + local endpoints; pricing-gate rework (user-entered
   prices keyed by (endpoint, model), local-$0 by classification); budget
   rename; `serve()` as a library entry point. Mostly deletion; every later
   item builds on this floor. →
2. **P2 — Connectors main road**: Slack token capture + verify (capture guide
   in catalog, control-plane verify), **plus first-run LLM key verification**
   — the spec's disclosed, metered "one live, minimal call" at paste time,
   assigned here 2026-08-31 after a Codex review of the P1 plan flagged it
   unowned (it wants the same session-authed verify path capture-verify
   builds) — then old phases 5 → 6 (remote MCP — 5 before 6, unchanged),
   phase 3 (options client) when the build needs it. →
3. **P3 — Build conversation**: the build resource, agent loop, and its
   checklist — unchanged as the highest product value, now against the P1
   endpoint model. →
4. **P4 — Grading + alerting**: as specced, with OS-notification delivery
   replacing the Postmark path and the grader-consent copy. Still creates
   `workflow.status` and delivers Plan 3 amendment 3. →
5. **P5 — Escalation** (carries Plan 3 amendment 1) →
6. **P6 — Objectives** (widens `workflow.status`) →
7. **P7 — Graduated permits** (hard dependency on P4's grader)

Escalation still precedes objectives (the objectives spec declares the
dependency). Plan 5 (Substrate + K8s Compute) is dead, not queued — the spec
stays merged as a record.

### Parallel-safe lanes, open now

- **Packaging shell at `app/`** (new top-level dir, prompt below) — tray
  shell, embedded Postgres, first-run flow, keychain, `go:embed` SPA serving,
  auto-update skeleton, notifier. Its one `server/` need — `serve()` as a
  library entry — rides P1; the lane never takes the lock.
- **CI, repo-global** (prompt below) — the catalog gate as a PR-time check,
  now pivot-critical: auto-update ships catalog changes to users' machines,
  so the narrow-only rule is what keeps a silent update compatible with
  approve-once.
- **Frontend at `web/`** (prompt below) — un-blocked by the pivot: first-run
  and settings surfaces, capture cards, the connections manager, and
  sleeping-machine copy are all P1-or-earlier work, prototyped on fake data
  where the API lands later. Login-screen *removal* sequences behind P1's
  local-session merge. The build-conversation checklist items still wait on
  P3. What it built — the permit diagram, steps rendering, schedule
  wording — are the components those surfaces will drive live.
- **Docs, specs, research** — always open.
- Anything in `src/`, user research, prototype work.

## Ready-to-paste prompts (2026-08-31, post-pivot-merge)

Six sessions can launch in parallel. P1 holds the `server/` lock; the other
five never take it. Each prompt is self-contained.

### Prompt — P1: subtraction and floor (owns `server/`)

> You own the `server/` lock for P1 of the Tomte pivot. Read, in full, the
> merged pivot spec `docs/superpowers/specs/2026-08-31-tomte-pivot-design.md`
> and the coordination board
> `docs/superpowers/plans/2026-08-31-parallel-sessions.md` before writing
> anything. Work in a linked worktree on a branch off current `main`.
>
> Write a concise implementation plan first (against the current tree, in
> `docs/superpowers/plans/`), PR the plan to `main`, then implement. P1's
> scope, from the spec's roadmap item 1 (section names in parentheses):
>
> 1. Remove OAuth end-to-end: `server/internal/oauth`, the httpapi oauth
>    routes, the refresh/epoch/advisory-lock machinery in
>    `store/connection.go`, a migration dropping the `oauth` credential kind
>    and `epoch`, and the `google-calendar` catalog definition + baseline
>    entry — a removal is a narrowing, so the catalog gate accepts it
>    ("Estate triage" connectors row). The `api_key` half survives untouched.
> 2. Remove login and mail: magic-link request/verify endpoints and
>    interstitial, `login_token` + its sweep (migration drop), enumeration
>    defenses and rate budgets, `internal/mail`, `TOMTE_POSTMARK_TOKEN` /
>    `TOMTE_MAIL_FROM`. Keep the session core: `session` table,
>    `RequireSession`, `SessionClaims`, cookie attributes, Origin middleware
>    ("Identity at its floor").
> 3. Local-session mint: generalize `dev-session` into a `local-session`
>    path inside the server library (not a CLI), plus the single-use,
>    short-TTL `/local/handoff` token exchange for open-in-browser
>    ("Identity at its floor").
> 4. Wake-aware scheduler window: persisted single-row `scheduler_heartbeat`,
>    per-tick lookback `max(window, now − lastTick + interval)`;
>    `mostRecentDue` unchanged. Test the case that never fires today: an
>    occurrence hours old at wake ("The sleeping machine").
> 5. Endpoint record: `{kind: anthropic|openai_compatible, base_url,
>    connection}`; presets are fixed base URLs; custom base URLs validated
>    (HTTPS except loopback, no userinfo, path allowlist unchanged); a
>    `local` endpoint carries no connection and the proxy skips credential
>    resolution/injection — the spec names that contract as worth its own
>    test. Approval records the endpoint identity; switching endpoints is a
>    recorded governance event that re-runs the pricing gate
>    ("Endpoint agnosticism").
>    **Preset amendment (user decision, 2026-08-31, supersedes the spec's
>    three-preset list): GitHub Models and Azure AI Foundry are first-class
>    presets**, five in all plus "another service" and "on this computer".
>    Two wrinkles your plan must resolve against current vendor docs, not
>    assumption: (a) GitHub Models is a fixed base URL
>    (`https://models.github.ai/inference`, OpenAI-compatible, auth = GitHub
>    PAT with `models:read`), but its quota is subscription-included rather
>    than per-token — decide how it meets the pricing gate (user-entered
>    price vs a stated-quota treatment) and record the decision; (b) Azure
>    AI Foundry has no fixed base URL (per-resource endpoints) and its auth
>    is nonstandard — `api-key` header and `api-version` query on the
>    classic Azure OpenAI path vs Bearer on the newer Foundry Models API —
>    so the preset contributes validation + capture guidance, and the
>    proxy's header-injection and path-allowlist contracts need explicit
>    Azure handling, verified against Azure's current docs.
> 6. Pricing-gate rework: bundled table as today; user-entered prices stored
>    per (endpoint canonical base URL, model); local-$0 by explicit preset
>    classification, never loopback inference; `max_tokens` derivation falls
>    back to a fixed default on local ("The priced-pair gate, reworked").
> 7. Budget rename: `tenant.monthly_cap_cents` becomes the user's local
>    budget — same enforcement, new copy ("Vault and metering").
> 8. `serve()` exposed as a library entry point (the packaging lane consumes
>    it); `TOMTE_PUBLIC_BASE_URL` becomes the auto-configured loopback origin
>    rather than a required value; add the `Host` allowlist hardening
>    ("The shell", "Loopback security posture").
>
> Binding rules from the board: PRs target `main`; if you split P1 into
> several PRs, no pre-stacked bases — each waits for its predecessor to
> merge. Never rename the derived-key labels `tomte:run-jwt` /
> `tomte-oauth-state`. Verify the next free migration number in
> `server/internal/db/migrations/` at branch time (00012 as of this writing).
> Before each PR: full server suite green including the connector e2e, and a
> real `tomte serve` boot. When done, report a delta sheet for the consuming
> lanes: the `serve()` library signature for packaging, and the
> endpoint/pricing/budget API shapes for the frontend.

### Prompt — CI and the catalog gate (repo-global, parallel)

> You own the CI lane for the Tomte repo — repo-global, pivot-critical, and
> explicitly not a `server/` occupant. Read the coordination board
> `docs/superpowers/plans/2026-08-31-parallel-sessions.md` ("Blocking
> defects" and the pivot-spec section) and the pivot spec's "Auto-update and
> migration-on-update" section first.
>
> Context: the repo has no `.github/` at all (verify), yet two merged
> security designs assume a CI-enforced catalog narrow-only rule, and the
> pivot makes it load-bearing — auto-update ships catalog changes to users'
> machines, so the gate is what keeps a silent update compatible with
> approve-once. `server/cmd/catalog-gate` exists; inspect its interface
> rather than assuming it.
>
> Deliver GitHub Actions workflows, in a linked worktree, PR to `main`:
> 1. Server: `go test ./...` (inspect `server/internal/testpg` to see how
>    tests obtain Postgres and provide it as a service), `go vet`, gofmt
>    check.
> 2. Web: install, `tsc`, vitest, build.
> 3. Catalog gate: run the gate so a PR fails on any catalog **widening**
>    against its merge base; narrowings pass.
>
> You do not hold the `server/` lock: do not modify server code. If the gate
> tool needs changes to run in CI, report that to the coordinating session
> instead of editing. Done means the workflows ran green on your own PR —
> link the runs.

### Prompt — Packaging shell spike + plan (`app/`, parallel)

> You own the packaging lane for Tomte — the click-install desktop shell.
> Read, in full, the merged pivot spec
> `docs/superpowers/specs/2026-08-31-tomte-pivot-design.md` — especially
> "Packaging — the central problem", "First run", "Auto-update", "Where
> state lives", and the open questions — plus the board
> `docs/superpowers/plans/2026-08-31-parallel-sessions.md`.
>
> Your lane is the new top-level `app/` directory; you never touch
> `server/`. Your one server need — `serve()` as a library entry point —
> arrives with P1; until then a subprocess of today's `tomte serve` is fine
> for spiking.
>
> First deliverable is a plan plus a spike, not production code:
> 1. Evaluate shell frameworks (Wails-class Go-native vs Tauri + Go sidecar)
>    against the spec's shell contract: tray-resident, window optional,
>    single instance, embedded webview, autostart, OS notifications, and
>    supervising a bundled Postgres child. Build the smallest spike that
>    proves the risky parts on your platform: tray app → init/start embedded
>    Postgres → run migrations → boot the server → webview at loopback.
> 2. Recommend a platform ship order given macOS notarization / Windows
>    signing cost and lead time, and propose update-feed hosting. These are
>    product calls: put recommendations to the user via the coordinating
>    session; do not decide unilaterally.
>
> PR the plan (with the framework decision and spike findings) to `main`
> before deepening the implementation.

### Prompt — Frontend pivot surfaces (`web/`, parallel)

> You own the frontend lane for the Tomte pivot. Read the merged pivot spec
> `docs/superpowers/specs/2026-08-31-tomte-pivot-design.md` — especially
> "First run", "Credentials without OAuth" (the connections manager), "The
> sleeping machine", and the build-conversation row of the estate triage —
> plus the board `docs/superpowers/plans/2026-08-31-parallel-sessions.md`
> (the `GET /v1/catalog` shape is recorded there). Work in a linked
> worktree; `web/` never takes the `server/` lock.
>
> Build, prototyping on fake data behind a thin interface wherever the API
> lands later (P1: endpoint record, price form, budget; P2: capture guide,
> connection states):
> 1. First-run flow: the endpoint chooser — five presets (Anthropic /
>    OpenAI / OpenRouter / GitHub Models / Azure AI Foundry — the last two
>    added by user decision 2026-08-31), plus "another service" and "on
>    this computer" — the guided key-capture card with the disclosed
>    metered verify call, the budget screen. GitHub Models' card guides a
>    PAT with `models:read`; Azure AI Foundry's card also collects the
>    per-resource endpoint URL.
> 2. Settings: endpoint switch as an explicit confirmation naming affected
>    workflows ("your 3 workflows will now run against …"), budget edit,
>    autostart toggle.
> 3. The standalone connections manager: one screen, every catalog connector
>    and registered MCP server with state (`connected` boolean today;
>    `ok`/`needs_reauth`/`missing` come with P2), owning capture cards and
>    disconnect; build conversations link into it.
> 4. Sleeping-machine copy: home renders `fire_time` as
>    "scheduled 3:00 AM · ran 7:42 AM, when your computer woke"; the schedule
>    confirmation carries the always-on guidance in the spec's order.
>
> Do NOT remove the login screen yet — its replacement (the shell-minted
> local session) merges with P1; sequence login retirement behind that merge
> and coordinate through the board. PRs to `main` per surface; keep them
> independent.

### Prompt — Pivot demo (`demo/tomte-pivot`, parallel, not for merge)

> You own the demo lane for Tomte: a runnable `npm run dev` demo that tells
> the click-install pivot story end to end, on fake data, for leadership and
> user testing. Read the merged pivot spec
> `docs/superpowers/specs/2026-08-31-tomte-pivot-design.md` (especially
> "First run", "The sleeping machine", "Credentials without OAuth", and
> "Enforcement posture") and the board
> `docs/superpowers/plans/2026-08-31-parallel-sessions.md` first.
>
> Follow the established demo pattern (`demo/dev-persona` is the precedent):
> a branch off `main` named `demo/tomte-pivot`, re-skinning the root
> prototype (`src/`, root `npm run dev`) — **a permanent demo variant, never
> merged to `main`**. On this branch, rebrand the prototype to Tomte freely
> (on `main` the prototype deliberately stays nightshift-branded). You touch
> no `server/` code and take no lock; everything is faked in the frontend.
>
> The demo walks the pivot's happy path as one continuous story:
> 1. First run: choose where your AI runs (Anthropic / OpenAI / OpenRouter /
>    GitHub Models / Azure AI Foundry / "another service" / "on this
>    computer"), paste a key via the guided
>    capture card, see the disclosed test-call verify succeed, set the
>    monthly budget ("how much Tomte may spend from your key per month").
> 2. Build conversation: describe a job in plain words (reuse the
>    existing demo scenario), connect Slack once through the connections
>    manager (paste an xoxb- token, watch it verify), and get the verdict.
> 3. Approve: the blast-radius picture with the softened enforcement copy
>    ("it can only act through Tomte's checkpoint, and every request is
>    checked against this picture") and the spend line.
> 4. The quiet home: a run history where one row reads
>    "scheduled 3:00 AM · ran 7:42 AM, when your computer woke", plus a
>    budget meter and an alert example.
>
> Keep every claim on screen consistent with the spec — the demo is a
> promise leadership will repeat. Where the spec softens or renames copy
> (enforcement posture, budget wording), use the spec's words verbatim.
> Verify `npm run dev` boots clean from a fresh checkout of the branch and
> the walkthrough needs no narration to follow.

### Prompt — README + MIT license (docs lane, parallel)

> Add a root `README.md` and an MIT `LICENSE` to the Tomte repo
> (`gambtho/tomte`). Neither exists today — verify (only `server/README.md`
> and `web/README.md` exist). Read the merged pivot spec
> `docs/superpowers/specs/2026-08-31-tomte-pivot-design.md` and the board
> `docs/superpowers/plans/2026-08-31-parallel-sessions.md` first; the README
> must describe the product as the pivot defines it. Work in a linked
> worktree; one PR to `main`; no `server/` or `web/` code changes.
>
> LICENSE: the standard MIT text, `Copyright (c) 2026 Tom Gamble`. Also set
> `"license": "MIT"` in the root and `web/` package.json and add the
> license comment/field Go tooling expects if the repo convention has one
> (check; if none, skip — do not invent one).
>
> README contents, in order: what Tomte is (one short paragraph — a
> click-install desktop app that lets a non-technical person safely delegate
> recurring work to an AI agent, on any LLM endpoint they bring); how the
> enforcement works, using the spec's exact posture wording — every
> credential and call passes through Tomte's checkpoint; a software
> boundary, not a sandbox — never overclaim; project status (early
> development, pre-release, direction set by the pivot spec — link it);
> repo layout (`server/` the Go control plane + proxy + scheduler, `web/`
> the SPA, `src/` + root vite files the retired early prototype kept for
> reference, `docs/` specs and coordination); a minimal dev quickstart
> (verify each command actually works before writing it: Go/Postgres
> versions, `tomte serve`, `tomte dev-session`, `web/` npm run dev); and
> the license line.
>
> Constraints: the name Tomte is final but trademark counsel is pending —
> no ™ marks or trademark claims. Do not describe hosted/multi-tenant
> behavior (that direction is reversed) or features that don't exist yet as
> if they exist; write in terms of what is built vs designed. Keep it under
> ~120 lines — a README, not a spec.

### Prompt — K8s agent track, phase K1 (THE FOCUS, new lane, parallel)

> You own the K8s agent track — the project's current focus (direction
> change 2). Read the board
> `docs/superpowers/plans/2026-08-31-parallel-sessions.md` (the
> "Direction change 2" section carries the verbatim leadership ask and the
> three shaping decisions) before writing anything. Work in a linked
> worktree; you take no `server/` lock and touch no existing top-level dir.
>
> The ask, in two verbatim leadership quotes: "a template for an agent
> that creates a hello world agent running on a k8s cluster, then expand
> to leverage llm to enhance the agent, allow connectors, etc — use a
> simple cli to get the agent running on k8s" and "having an artifact that
> shows my agent topology — almost agent as code (ideally yaml template or
> something like that)". Simplest possible solution; no Substrate; CLI,
> not UI.
>
> **Phase K1 (this session): hello world on a cluster via a simple CLI,
> defined by an agent-as-code YAML.** Brainstorm briefly, write a short
> plan (PR it to `main` or lead your implementation PR with it — your call
> at this size), then build:
>
> 1. **The agent-as-code YAML — the centerpiece.** One human-readable,
>    versioned YAML file (e.g. `agent.yaml`) IS the agent: its identity,
>    what it does (for K1, print/serve hello world on a loop), and its
>    topology — written so that reading the file shows the topology, and
>    leadership can be handed the file itself as the artifact. Schema
>    tiny but shaped for growth: a `version`, the agent's task/behavior,
>    and empty-but-named slots where K2's `llm:` (endpoint kind/base_url +
>    secret ref, vocabulary aligned with the board's five-preset enum) and
>    K3's `connectors:` will land — so the file grows downward into
>    Tomte's steps/permit shape without a rewrite. The running agent
>    consumes this file mounted via ConfigMap — behavior comes from the
>    YAML, never hardcoded in the image.
>    **Format decision (coordinator-surveyed, 2026-08-31): net-new schema
>    in the Kubernetes resource envelope** — `apiVersion: tomte.dev/v1alpha1`,
>    `kind: Agent`, `metadata:`, `spec:` — NOT a CRD at K1 (the CLI reads
>    it; nothing is registered with the API server), but envelope-shaped so
>    CRD-hood later is a registration step, not a schema rewrite. Existing
>    formats were surveyed and not adopted: kagent's CRDs are the closest
>    prior art (borrow its noun-shape where natural) but bring a
>    controller + their Python engine as the runtime, which would route
>    the Tomte transition through someone else's loop; K8s SIG-apps
>    agent-sandbox is a runtime-isolation CRD to watch, not a topology
>    format; Docker Compose's `models:` vocabulary is worth echoing but
>    targets the Docker runtime. Our `spec:` keeps our own nouns — the
>    governance slots are the differentiator no existing format carries.
> 2. **A simple CLI** (new top-level dir; propose the name — Go, matching
>    the repo): `init` scaffolds an `agent.yaml` from the template;
>    deploy/run applies it to the cluster the user's kubeconfig points at
>    (YAML → ConfigMap + Deployment/Job); `status` and `logs` round it
>    out. The YAML is the single source of truth — the CLI derives all
>    K8s objects from it; nobody hand-edits manifests. No CRDs, no
>    operator, no controller; plain namespaced objects via client-go or
>    shelling out to kubectl — pick the simplest that is honest, and say
>    why. Helm only if it genuinely earns it (at K1 it probably doesn't).
> 3. **Verification is a real cluster**: stand up kind (or k3d), run your
>    own CLI end to end, and show `logs` returning hello world. A README
>    in the lane dir walks a newcomer through the same five minutes and
>    shows the `agent.yaml` up front — it is the pitch.
>
> **Known destination, not K1 scope**: K2 adds an LLM call (OpenAI-
> compatible base URL + key from a K8s Secret; align the endpoint config
> vocabulary with the board's five-preset enum so convergence stays
> cheap). K3 adds connectors and begins the transition to the full Tomte
> experience — the EXISTING proxy/permit/scheduler stack is that
> destination; do not design K1 in a way that forecloses mounting it
> later (this is why the config-file seam matters). Phase 1 is
> deliberately bare — no proxy, no permits (user decision, recorded on
> the board); resist the urge to add governance early.
>
> PR to `main`; CI (PR #41's workflows) must pass once #41 is merged.
> Report your naming and structure decisions to the coordinating session
> for the board.

### Prompt — K8s agent track, phase K2 (LLM-enhanced agent)

> You own K2 of the K8s agent track — the project's current focus. K1 is
> merged (`tomtectl/` on `main`: agent-as-code YAML, CLI, hello-world
> runtime verified on kind). Read the board
> `docs/superpowers/plans/2026-08-31-parallel-sessions.md` (direction
> change 2, the K1-delivered decisions, the five-preset decision, and the
> phase-A fail-closed guidance) and `tomtectl/`'s code and README before
> writing anything. Linked worktree; PR to `main` (the tomtectl CI
> workflow is live); no pre-stacking; you take no `server/` lock (P2
> holds it) and touch only `tomtectl/`.
>
> Scope — the leadership arc's "expand to leverage llm to enhance the
> agent":
> 1. **The `llm:` block comes alive** in agent.yaml:
>    `{kind, base_url, secretRef}` using the vocabulary K1 aligned
>    (anthropic | openai_compatible; explicit local stays keyless). The
>    key arrives as a **Kubernetes Secret reference** — tomtectl gains
>    the minimal command or flag to create/point at that Secret; the key
>    never appears in the YAML, a ConfigMap, or logs.
> 2. **A real runtime image replaces the busybox placeholder on the SAME
>    mount contract**: minimal Go runtime that reads the mounted
>    agent.yaml, sends its steps/task to the configured endpoint on the
>    schedule, and writes results to logs (`tomtectl logs` shows them).
>    Still deliberately bare — no proxy, no permits; governance mounts at
>    K3 (standing decision; flag anything pushing you toward governance
>    early to the coordinating session instead of adding it).
> 3. Non-empty `llm:` stops being rejected; non-empty `connectors:` still
>    rejects.
> 4. **Verification on a real kind cluster, end to end**: an in-cluster
>    stub OpenAI-compatible server for the automated proof, plus the
>    documented (ideally hand-verified) real-endpoint path with a pasted
>    key. Fail CLOSED on unreadable or error responses — well-formed
>    positive or fail is board-recorded standing guidance.
>
> Report decisions to the coordinating session for the board.

## Pivot spec MERGED (PR #37); name FINAL

`docs/superpowers/specs/2026-08-31-tomte-pivot-design.md` — merged to `main`
2026-08-31 with all review fixes. Carries the Tomte naming, the reusable
connections manager (charter item 4 upgrade), and the I/O palette positioned
in the build-conversation triage.

**The name is FINAL: Tomte** (user, 2026-08-31, after leadership review;
trademark counsel still owed — the one remaining debt on the name). The
mechanical rename **merged as PR #38** (verified: server suite green, web
tsc + 110 tests + build green, real `tomte serve` boot).
Screening history for the record: Duende died (Duende
Software is IdentityServer's company), Momoy was legally clean but loaded
(sacred Chumash Datura figure; "ugly/nasty" in Hiligaynon; Momo-adjacent).
Two rename facts worth memory: the derived-key labels
(`tomte:run-jwt` HKDF info, `tomte-oauth-state` salt) are cryptographic
inputs — renaming them invalidates outstanding run tokens, fine pre-release,
not fine later; and `src/` (retired prototype) deliberately still greps as
nightshift.

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

## Cross-cutting decision: five endpoint presets (2026-08-31)

**GitHub Models and Azure AI Foundry join Anthropic, OpenAI, and OpenRouter
as first-class endpoint presets** (user decision, 2026-08-31). This amends
the pivot spec's "Endpoint agnosticism" preset list; the spec file is not
rewritten — this board entry is the record. Verified basis: GitHub Models
(`https://models.github.ai/inference`) is an official OpenAI-compatible
chat/completions endpoint authed by a GitHub PAT (`models:read`), with
entitlements tied to Copilot plans. Azure AI Foundry is per-resource
endpoints with nonstandard auth (`api-key` header + `api-version` query on
the classic path). The P1 prompt carries the two implementation wrinkles
(pricing-gate fit for subscription quota; Azure auth/path handling in the
proxy). Leadership copy names them "GitHub Models (included with GitHub
Copilot plans)" — do not claim `api.githubcopilot.com` support; it is
undocumented for third parties and needs token exchange.

**P1's resolutions (plan PR #45, 2026-08-31), coordinator-reviewed:**

- **Azure — accepted as planned.** P1 pins the **v1 GA API** (per-resource
  base `https://<resource>.openai.azure.com/openai/v1` or
  `<resource>.services.ai.azure.com/openai/v1`; plain OpenAI-compatible, no
  `api-version` param). Keys inject as an `api-key` header (Entra/Bearer out
  of scope v1). Validation: HTTPS + Azure host-suffix allowlist + exact
  `/openai/v1` path. No bundled prices — Azure rides the user-entered
  (base_url, model) price path, so the gate applies via the price form.
- **GitHub Models — amended, blanket $0 rejected.** The plan proposed
  classifying the preset zero-cost with the gate skipped. Verified against
  GitHub's current docs: GitHub Models has **opt-in pay-as-you-go billing**
  beyond the free quota (token units × per-model multipliers; default
  GitHub-side spending limit $0 until raised), so a hard $0 would record a
  paying user's real spend as zero — the silent unmetered spend the gate
  exists to prevent. Direction to P1: explicit free-vs-paid choice on the
  github preset (free → $0 classification with honest copy; paid → the
  user-entered price path), switchable in settings since an org can enable
  paid usage after first run.
  **Resolved (plan PR #45, commit 0023855), coordinator-accepted:**
  `zero_cost` is an explicit per-endpoint boolean on the `llm_endpoint`
  record, never preset-inferred — `local` forces true, `github` takes the
  user's free-vs-paid answer from the capture card, every other preset
  forces false, all DB CHECK-enforced. The settings toggle exists by
  construction: flipping it goes through `PUT /v1/settings/endpoint`, the
  recorded governance switch that re-runs the pricing gate and recompiles
  approved versions. Compiled docs always carry resolved prices, so no
  preset special-casing survives at runtime.
- Preset enum for the record:
  `anthropic|openai|openrouter|github|azure|custom|local`. Next migration
  confirmed 00012.

## Delta sheet: what P1 changes (PR #48, delivered pending review)

From the P1 session (2026-08-31); full detail in the PR body. Verify against
the tree after merge. `server/` passes to **P2 (connectors main road)** when
#48 merges.

- **For packaging** (paused, but this is its contract when it resumes —
  and for the K8s track's eventual convergence):
  `server.Start(ctx, server.Options) (*server.Server, error)` at the module
  root. `Options{DatabaseURL, ListenAddr (":0" ok), PublicBaseURL (optional
  override), RunnerKey, VaultKey, StateDir, RunProvider/RunModel (legacy env
  mode), RunTokenTTL, RunDeadline, DefaultMonthlyCapCents, PlatformKeys,
  LogHandler}`; `Server.{Addr, BaseURL, MintLocalSession, HandoffURL,
  Shutdown, Err}`. Host allowlist is automatic (whole mux, logged
  rejections). **OWED to whoever ships a webview**: browser-level
  verification that the http-loopback cookie (plain `tomte_session`, not
  Secure — the Safari constraint) is accepted.
- **For frontend** (idle; wire-up work when it resumes): settings API
  documented in `docs/api/v1.md` — `GET/PUT /v1/settings/endpoint`
  (presets `anthropic|openai|openrouter|github|azure|custom|local`; switch
  409s: `connection_missing` / `unpriced_models` / `provider_not_permitted`
  / `invalid_permit`), `GET/PUT /v1/settings/prices` (explicit base_url),
  `GET/PUT /v1/settings/budget`. Approve 400
  `{error:"unpriced_model", model, base_url}` is the inline price-form
  trigger. Login/magic-link routes are gone;
  `GET /local/handoff?token=&next=` sets the session cookie. Catalog lists
  Slack only; connection JSON lost `metadata`, kept `status`.
- **Post-plan deltas, coordinator-noted**: endpoint-provider mismatch at
  the proxy fails CLOSED (no static-table fallback once an endpoint record
  is configured); a `Caps.OverCap` infrastructure error keeps the scheduler
  heartbeat, so wake catch-up survives transient DB errors.
- Frozen labels honored: `tomte:run-jwt` untouched; `tomte-oauth-state`
  deleted with its consumer, never renamed.

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

The startup deadlock that blocked this on `main` is resolved: PR #24 merged,
and the rename lane verified a real `tomte serve` boot (fresh Postgres,
`/v1/me` served) on what is now merged code.

What is still missing is the _product_, not the loop: the build conversation,
so that a non-technical person can describe a job instead of filling a form.

## Blocking defects

- ~~**`nightshift serve` deadlocks at startup on `main`.**~~ **Resolved:
  PR #24 merged; `main` boots** (real `tomte serve` verified by the rename
  lane). Historical detail kept below for the record. Found by the frontend
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

  **Delivered: PR #41** (2026-08-31, CI lane) —
  `.github/workflows/{server,web,catalog-gate}.yml`, all green on the PR
  (coordinating session verified: gate/test/build all SUCCESS). The gate
  runs the boot-level embedded defs-vs-baseline check on every run plus a
  PR-only diff of `defs/` against the merge base via `cmd/catalog-gate`'s
  existing two-dir mode — no server code changes were needed, so nothing
  entered the `server/` queue. Proven, not asserted: a defs-only widening
  failed the embedded check, and the load-bearing case (a two-file
  defs+baseline widening the boot gate cannot stop) was caught by the
  merge-base diff ("WIDENING: slack.list_channels: scope admin added");
  both demo commits reverted, net diff is the 3 workflow files. Note:
  `testpg` self-provisions Postgres via testcontainers — no service
  container, no DSN override. Workflows run unfiltered on all PRs + push
  to `main` so they stay simple if later made required. **This defect and
  the "CI unowned" flag close when #41 merges.** Escalation history: made
  pivot-critical by the merged pivot spec — auto-update ships catalog
  changes to users' machines, so the narrow-only rule is what keeps a
  silent update compatible with approve-once.

## Cross-cutting decisions

- **The product is renamed: Nightshift → Tomte** (user decision, 2026-08-31;
  confirmed FINAL the same day after leadership review — "lets stick with
  Tomte". Trademark counsel is still owed but the name is settled; do not
  relitigate). Post-release freeze note: the derived-key labels
  `tomte:run-jwt` and `tomte-oauth-state` are cryptographic inputs and must
  never be renamed after release.
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

- ~~**Postmark is the single transactional email provider**~~ (Plan 4 spec,
  PR #13). **Retired by the pivot** (2026-08-31): nothing transactional
  remains to send. `internal/mail` is removed in P1; alert delivery moves to
  OS notifications in P4.
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

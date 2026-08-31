# Tomte Click-Install Pivot — Design

**Status:** Design proposed; spec awaiting review
**Date:** 2026-08-31
**Author:** gambtho
**Amends:** [`2026-08-30-nightshift-platform-design.md`](./2026-08-30-nightshift-platform-design.md) —
this spec **reverses** two of its recorded decisions: "Hosted, multi-tenant,
operated by us" and (via the UX spec's deferred-items answer) "self-hosting is
explicitly not a supported path". Both reversals are deliberate, decided by
leadership and recorded in the coordination doc's "Direction change" section
(2026-08-31). The old rationale stands as history and is not relitigated here.
**Preserves:** [`2026-08-28-nightshift-design.md`](./2026-08-28-nightshift-design.md) —
the target user, the four surfaces, and the build conversation as front door
survive unchanged.
**Retires half of:** [`2026-08-30-nightshift-connectors-design.md`](./2026-08-30-nightshift-connectors-design.md) —
its OAuth machinery dies with this pivot; its static-`api_key` and remote-MCP
halves become the entire credential story.

## Naming

The product is renamed **Tomte** (decided 2026-08-31, relayed by the
coordinating session): "Nightshift" collides with Apple's Night Shift — a
real problem for a click-install desktop app on Macs — and the repo name
collides with Nightwatch.js. The tomte of Scandinavian folklore is the
household spirit that quietly makes its rounds while the household sleeps,
under a contract of respect and due payment — nearly a spec for this
product. The name is provisional pending a real trademark search. This
document uses Tomte for the product and cites historical documents by their
existing Nightshift filenames; a mechanical rename (binary, module path,
`NIGHTSHIFT_*` → `TOMTE_*` env prefix, repo) runs in parallel and env names
below are written in their `TOMTE_*` form — on `main` today they still read
`NIGHTSHIFT_*`.

## What this is

Tomte becomes a **click-install, self-contained app** that its user — still
the UX spec's non-technical person — installs on their own machine. The bar is
a desktop-app-grade install: no terminal, no docker, no database
administration. The app talks to **any LLM endpoint** (Anthropic, OpenAI,
OpenRouter, any OpenAI-compatible base URL, including local models) configured
at first run with a pasted key. **OAuth is dropped entirely.** The egress
proxy ships inside the install and remains the enforcement point — the
blast-radius permit stays enforced, never advisory.

## Decisions already taken (the charter — designed within, not relitigated)

1. **Installed by its user; the user is the non-technical person.** Click
   install, no operator persona. The dev-persona research stays a companion
   study, not the target.
2. **The egress proxy remains the enforcement point** and ships inside the
   install.
3. **OAuth is dropped entirely.**
4. **Any LLM endpoint is first-run configuration.**
5. **The build conversation remains the front door; all four surfaces survive
   unchanged.**

## Verified starting points — with one correction

- `serve()` already runs the entire stack — API, proxy, scheduler, reaper,
  local compute — in one process against one Postgres
  (`server/cmd/nightshift/main.go:154`), and the connector e2e is green in that
  shape. The pivot is mostly subtraction and packaging.
- `scheduler.mostRecentDue` fires only the latest occurrence ≤ now and skips
  older ones (`server/internal/engine/scheduler.go:85`) — the right mechanism
  for fire-on-wake. **Correction, found by reading the code:** the coordination
  doc records fire-on-wake as already working, but `Scheduler.window()`
  defaults to `max(2 × interval, 5 min)` and `serve()` passes no override
  (`scheduler.go:36-44`, `main.go:277`). `mostRecentDue` walks forward from
  `now − window`, so a 3 AM occurrence on a machine that wakes at 7:42 is
  outside the lookback and **never fires**. Fire-on-wake needs the wake-aware
  window designed below — a small, contained scheduler change, not a new
  mechanism.

## Scope decisions

| Decision                                                                     | Why                                                                                                                                                                                                                                                                                                           |
| ---------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Bundled-invisible Postgres, not a SQLite rewrite**                         | The store is Postgres to the bone, and the only green end-to-end shape runs on it. A rewrite is the opposite of "mostly subtraction and packaging". Weighed honestly below — SQLite is the better end-state for a desktop app, and the revisit trigger is named.                                              |
| **Tray-resident desktop shell with an embedded webview**                     | The scheduler must run when no window is open; alerts need OS notifications; autostart needs an installed-app identity. Browser-at-localhost gives none of those. The shell framework (Wails-class Go-native vs Tauri + sidecar) is an implementation-plan choice; this spec pins the contract, not the tool. |
| **Sessions kept at their floor; login removed**                              | The OS user session is the trust boundary. DB-backed opaque sessions, `RequireSession`, and the Origin middleware survive because a loopback web app still faces hostile-website and DNS-rebinding traffic; magic links, `login_token`, and Postmark die.                                                     |
| **Guided token capture is a first-class surface**                            | With OAuth gone, "go here, click this, paste this" is every connector's front step. The catalog grows a structured capture guide per connector, verified at paste time — not free-text instructions.                                                                                                          |
| **Pricing is layered: bundled table → user-entered price → local-model \$0** | The priced-pair 400 protected the platform's money; the user now spends their own. The gate survives (no silent unmetered spend) but the dead end doesn't: an unpriced model on a remote endpoint gets an inline two-number price form, not a bare 400. Localhost endpoints are \$0 by construction.          |
| **Master key in the OS keychain**                                            | `TOMTE_VAULT_KEY` as an env var is operator ergonomics. On a user's machine the shell generates the key at first run and stores it in the platform keychain (Keychain / Credential Manager / Secret Service), with a 0600 file fallback where no keychain exists. The env path survives for development.      |
| **One deployment = one tenant; the schema keeps its tenant plumbing**        | Ripping `tenant_id` out of every table and query is churn with no payoff and forecloses nothing. The single tenant is minted at first run. The per-tenant KEK envelope stays as built; its multi-tenant rationale is simply gone.                                                                             |

## Packaging — the central problem

### The store: bundled Postgres vs SQLite, weighed honestly

**What a SQLite rewrite would actually touch.** The dependency is deeper than
"pgx as a driver": `pg_advisory_xact_lock` guards connection rotation and
deletion (`store/connection.go:125,195,259`), `AddVersion` serializes on
`FOR UPDATE` (`store/workflow.go:102`), migration 00010 uses a
`jsonb_typeof` CHECK, and the schema leans on partial unique indexes
(`run_one_active_per_workflow`, `run_workflow_firetime_unique`,
`workflow_version_one_approved`), composite FKs, `gen_random_uuid()`, and
`timestamptz` across 11 migrations. Every store file, the `testpg` harness,
and all store-touching tests would change, and the connector e2e — the one
proof the whole shape works — would need re-verification from scratch.

**What SQLite would buy.** It is genuinely the better desktop end-state: no
child process to supervise, no port or socket, backup is copying one file,
and auto-update never has to think about a database engine's own major
versions. Partial indexes and CHECKs port; advisory locks collapse into
in-process mutexes (a single process is the only writer); `FOR UPDATE` is
moot under SQLite's single-writer model.

**Decision: bundle Postgres for v1.** The pivot's premise is subtraction and
packaging; a storage rewrite is a re-platforming that invalidates the one
verified end-to-end shape at exactly the moment the queue is frozen on this
spec. The costs are accepted and named:

- **Payload**: ~30–50 MB of Postgres binaries per platform (the
  embedded-postgres pattern: pinned binaries unpacked into the app's state
  dir, initdb'd on first run).
- **A child process to supervise**: the shell starts Postgres before the
  server and stops it after; crash-restart with backoff; on Windows this can
  trip antivirus heuristics and needs testing on real consumer machines.
- **Reachability**: Unix domain socket on macOS/Linux; on Windows, loopback
  TCP on a per-install random port with a per-install random password. Never
  a well-known port; nothing listens beyond loopback.
- **Engine upgrades are ours**: the Postgres major version is **pinned for
  v1**. When a bump is eventually needed, the data is one user's rows —
  small enough that `pg_dump` → new engine → restore at update time is the
  plan, not `pg_upgrade`. Auto-update refuses to cross a Postgres major
  without that dump/restore step succeeding.

**Revisit trigger for SQLite**: if field support shows the bundled engine
itself generating a meaningful share of failures (init, locking, AV
quarantine, corruption), a SQLite port becomes a scheduled project with the
then-current test suite as its safety net. Recorded as rejected-for-v1, not
rejected.

### The shell

A tray/menu-bar-resident app. Contract, independent of framework choice:

- **One process tree the user sees as one app.** The shell owns lifecycle:
  start Postgres → run migrations → start the server (`serve()` exposed as a
  library entry point, not a subprocess with env-var plumbing, where the
  framework allows) → open the window on first run.
- **The window is optional; the tray is not.** Closing the window leaves
  Tomte running — the scheduler is the product. Tray menu: Open Tomte ·
  Pause everything · Quit. Quit warns that scheduled work stops.
- **Autostart at login, on by default,** consented to on the first-run screen
  in plain words ("Tomte starts with your computer so your scheduled work
  happens without you"), with a settings toggle.
- **OS notifications** are the shell's job: the server keeps writing durable
  alert rows; a thin notifier delivers them natively (see the Plan 4 triage
  entry).
- **The SPA is embedded** (`go:embed` of the built `web/` bundle) and served
  by the server on the loopback listener; the Vite-as-origin topology stays
  dev-only.
- **Single instance**: a second launch focuses the existing app.

**Loopback security posture.** The server binds loopback only. A hostile web
page can still make requests at `127.0.0.1`: cross-origin browser requests
carry a foreign `Origin` and are 403'd by the existing middleware;
DNS-rebinding requests arrive with the attacker's `Host` and no session
cookie (cookies are scoped to the rebound hostname). Two small hardenings
join them: a `Host` allowlist (the configured loopback origin only), and
`TOMTE_PUBLIC_BASE_URL` becomes the auto-configured loopback origin rather
than a required deploy-time value.

### First run — paste a key and go

1. Window opens on a single screen: **choose where your AI runs** — Anthropic
   / OpenAI / OpenRouter presets, "another service" (OpenAI-compatible base
   URL), or "on this computer" (localhost base URL — Ollama/LM Studio class,
   no key, \$0). Each preset carries a guided capture card for its key ("go
   here, click Create Key, paste it here") — the same capture pattern
   connectors use.
2. The key is **verified with one live, minimal call** through the proxy
   path before anything else is asked. A bad paste fails here, not at the
   first 3 AM run.
3. **Set a monthly budget** — one number, suggested default, explained as
   "how much Tomte may spend from your key per month" (see Metering).
4. Land in the **build conversation**. No email, no account, no login —
   under the hood the app generated the master key into the keychain,
   initialized Postgres, ran migrations, minted the tenant + owner user, and
   minted the session.

### Auto-update and migration-on-update

- **Updates are staged and applied on restart**: check → download → verify
  signature → swap on next launch (platform-standard mechanisms; the update
  feed is an operational choice for the implementation plan).
- **Migrations run at app start, before serve** — `migrate` stops being a
  separate operator command on this path. Before applying migrations, the
  shell takes a **backup** (`pg_dump` to the state dir, rotated, last 3
  kept) — the data is one user's rows, so this costs nothing and turns a
  failed migration into a restore instead of a support case.
- **The catalog gate matters more, not less**: the narrow-only rule
  (connectors spec) is what makes silent auto-update compatible with
  approve-once — an update may never widen what an approved permit means.
  CI enforcement of the gate is still unowned; this spec re-flags it to the
  coordinating session as a pivot-critical follow-up rather than tooling
  hygiene.

### Where state lives

One per-user application-data directory
(`~/Library/Application Support/Tomte`, `%APPDATA%\Tomte`,
`~/.local/share/tomte`):

- `pgdata/` — the bundled Postgres data directory.
- `actors/` — actor working state (today's `TOMTE_STATE_DIR`).
- `config` — endpoint choice, budget, autostart, listen port. Non-secret.
- `backups/` — rotated pre-migration dumps.
- Secrets never live here in plaintext: the master key is in the keychain;
  everything the vault holds is encrypted under it exactly as today.

## The sleeping machine

### The default story: ready when you sit down

Tomte runs on the user's computer, and computers sleep. The honest product
copy, written here because it is load-bearing:

> **Tomte works while your computer is on.** If your computer is asleep when
> a job is scheduled, the job runs as soon as it wakes — once, not twelve
> times. You'll see it on the home page: _"scheduled 3:00 AM · ran 7:42 AM,
> when your computer woke."_

The home surface renders exactly that: `fire_time` (the scheduled occurrence)
already exists on the run and is exposed; the copy is a rendering of data the
schema already carries. The quiet home's "you don't need to check this page"
line survives unchanged — an on-wake run still alerts on trouble and stays
silent on success.

### Making it true: the wake-aware window

The mechanism is right and the window is wrong (see the correction above).
Design:

- The scheduler keeps a **last-completed-tick timestamp** — in memory, and
  persisted once per tick to a single-row `scheduler_heartbeat` table so the
  catch-up also covers quit-and-relaunch, not just sleep.
- Each tick's lookback becomes
  `max(window, now − lastTick + interval)` — the sleep or downtime gap plus
  margin.
- `mostRecentDue` is unchanged: it already returns only the **latest**
  occurrence ≤ now, so a week of downtime fires each workflow at most once,
  and the `run_workflow_firetime_unique` index keeps even that idempotent.

**One accepted edge, named**: a run that is mid-flight when the machine
sleeps longer than `TOMTE_RUN_DEADLINE` (default 2 h) is reaped as
`failed/orphaned` on wake even though its goroutine may still complete into
a revoked token. Wall-clock deadlines cannot distinguish "slow" from
"frozen". Rare (requires sleeping mid-run for hours), visible (the run is
marked, the failure trigger sees it), and strictly better than the
alternative of never reaping.

### The always-on option

For the user who wants true 3 AM, the app says so plainly in schedule
settings and in the schedule confirmation of the build conversation, in
this order:

1. **Keep this computer awake** — one line of OS-appropriate guidance
   (plugged in, sleep set to never / lid rules), linking the OS settings
   pane. Tomte does not fight the power manager itself in v1 (no
   caffeinate-style wake locks — a scheduler holding sleep hostage is the
   wrong default for a laptop; revisit only on demand).
2. **Install Tomte on a machine that stays on** — a desktop, a mini PC. One
   install per machine; moving is export/import (out of scope v1, named
   below).
3. **A hosted always-on tier** — named as a possible future product,
   explicitly **out of scope** for this spec and not designed here.

## Identity at its floor

One install, one user. What survives, what dies:

- **Survives — the session core.** DB-backed opaque sessions, the `session`
  table, `RequireSession`'s session→`app_user` join, `SessionClaims`, cookie
  attributes, and the Origin middleware all stand. They are built, tested,
  and still earning their keep: they are the loopback surface's defense
  (above), and every handler already speaks this contract.
- **Survives — the tenant and owner row.** Minted once at first run
  (`CreateTenant` + KEK + `UpsertUser` in the existing transaction shape,
  with a placeholder local identity instead of a verified email). Approval
  stays a distinct recorded act with a real `approved_by`.
- **The shell mints the session.** At launch, the shell inserts a session
  row (the `dev-session` mechanism generalized into a `local-session` path
  inside the server library — not a CLI) and injects the cookie into the
  webview. "Open in browser" from the tray mints a fresh one-time session
  URL on loopback. Sessions keep their idle/absolute expiry; re-mint at
  launch makes expiry invisible to the user.
- **Dies — login as a surface.** The email form, magic-link request/verify
  endpoints, the interstitial, `login_token` and its sweep, enumeration
  defenses, rate budgets: removed, with a migration dropping `login_token`.
- **Dies — Postmark and the mailer.** `internal/mail` and
  `TOMTE_POSTMARK_TOKEN`/`TOMTE_MAIL_FROM` are removed; nothing
  transactional remains to send (alert delivery moves to OS notifications —
  Plan 4 triage below). The "Postmark is the single email provider"
  cross-cutting decision is retired with it.
- **Not added — an app lock.** The OS user account is the boundary; other OS
  users cannot read the keychain-held master key. A per-app passcode is a
  possible later feature, not v1.

## Credentials without OAuth — guided capture as a surface

The deferred credential-capture UX is now every connector's front step, and
it is designed as structure, not as prose instructions.

### The connections manager — a standalone, reusable surface

Capture does not live inside any one build conversation. **Connections are
added once, in a standalone connections manager, and every subsequent build
finds them already connected** (user feedback, 2026-08-31, relayed by the
coordinating session). This is surface design, not backend design — the data
layer already supports it: connections are tenant-scoped
(`PUT /v1/connections/{name}`, `GET /v1/connections`) and `GET /v1/catalog`
reports `connected` per connector.

The manager is one screen listing every catalog connector and registered MCP
server with its state (`ok` / `needs_reauth` / `missing`), and it **owns**
the capture cards below, MCP registration, re-paste on revocation, and
disconnect. The build conversation **links into it** at its connect moments
and returns to the conversation when the card lands `ok` — it does not own
capture. The manager also exposes, per connector, the connected/available
distinction and each op's `effect: read|write` classification: exactly the
state the post-verdict inputs/outputs palette (see the build-conversation
triage entry) will need, so nothing here forecloses that surface.

### The capture guide, in the catalog

Curated connector `auth` becomes token-shaped and gains a structured guide:

```json
{
  "auth": {
    "provider": "slack",
    "kind": "api_key",
    "capture": {
      "start_url": "https://api.slack.com/apps?new_app=1&manifest_url=…",
      "steps": [
        "Click **Create App** — we've pre-filled what Tomte needs.",
        "Click **Install to your workspace** and approve.",
        "Copy the token that starts with `xoxb-` and paste it below."
      ],
      "secret_prefix": "xoxb-",
      "verify_op": "auth_test"
    }
  }
}
```

- **Shape check at paste** (`secret_prefix`) catches the wrong-string paste
  instantly.
- **Live verify at paste**: the control-plane connector client (the build
  spec's session-authed options path — never a run token) invokes the
  connector's `verify_op` with the pasted token before storing it. Slack's
  `auth.test` also returns granted scopes via the `x-oauth-scopes` response
  header; the card warns immediately if the installed app is missing scopes
  an op needs, instead of failing at first run.
- Storage is the connectors spec's existing `api_key` kind under the
  connector's provider namespace — machinery that is already merged. The
  proxy's credential injection attaches the stored token per the connector's
  header spec; the OAuth bearer path is deleted, not bypassed.

### Curated v1: Slack alone

Slack ships as the single curated connector, via **bot token with an
app-manifest link**: Slack's manifest flow lets the capture card pre-author
the app (name, scopes matching the catalog ops) so the user clicks Create →
Install → copy token. This is honestly the hardest paste in the product —
three clicks inside a foreign admin UI — and the card design above exists
because of it. `google-calendar` leaves the catalog and the baseline
(Google user data requires OAuth; a removal is a narrowing, so the catalog
gate accepts it).

### Calendar and inbox: the MCP main road

Calendar and inbox arrive as **remote MCP servers that do their own auth** —
the user registers a vendor's MCP endpoint (one-click from the registry;
custom URL as the advanced path) and pastes whatever key that vendor issues,
captured by the same card pattern (`key_hint` from the build spec
generalizes into `capture`). The vendor's OAuth, if any, happens on the
vendor's site under the vendor's app — Tomte only ever holds the resulting
MCP credential.

**What this reorders:** the connectors plan's deferred phases 5 → 6 (remote
MCP registration/discovery/SSRF hardening, then MCP proxy enforcement)
**become the main connector road**, promoted ahead of any further curated
work; "5 before 6" is preserved. Phase 3 (the options client) still lands
when the build conversation needs it. The full SSRF defense set the
connectors spec designed for user-controlled destinations applies unchanged
— it was designed for exactly this surface and none of it was OAuth-shaped.

## Vault and metering, simplified

- **Key hierarchy**: unchanged in schema (master → tenant KEK → per-secret
  DEK). The master key moves from a deploy-time env var to a
  first-run-generated secret in the **OS keychain**, handed to the server by
  the shell at boot; `TOMTE_VAULT_KEY` survives as the dev/headless path;
  Linux without a Secret Service falls back to a 0600 file in the state
  dir. The KEK layer's multi-tenant rationale is gone; it is kept because
  removing it is churn, not because it earns anything.
- **Per-run and per-workflow caps stay exactly as approved** — they are the
  diagram's spend line and the product's spend story
  (`spend.per_run_cents`, the `spend.exceeded` event, the Plan 4 spend
  trigger: all unchanged).
- **The monthly cap becomes a local budget.** `tenant.monthly_cap_cents` is
  reinterpreted: set by the user at first run, editable in settings,
  enforced by the existing meter exactly as today (pre-call check, fail
  closed, scheduler skip-when-capped). The copy changes from a platform
  bill to _"how much Tomte may spend from your key this month"_ — with the
  honest caveat stated in settings: Tomte meters what goes **through
  Tomte**; it cannot see other uses of the same key. Plan 4's `monthly_cap`
  alert survives with re-worded copy ("your budget is used up until the
  1st — raise it in settings or wait").
- **Grader and build-agent spend now come from the user's own key.** Both
  already meter against the monthly cap; what changes is consent and copy —
  see the Plan 4 and build-conversation triage entries.

## Endpoint agnosticism

### Where the endpoint lives

First-run configuration produces one **endpoint record** in local config
plus one vault connection:

- `{kind: "anthropic" | "openai_compatible", base_url, connection}` — the
  three presets are fixed base URLs; "another service" and "on this
  computer" are `openai_compatible` with a user-entered base URL. The key
  is a vault `llm_api_key` connection, as today.
- The proxy's per-provider fixed upstream host becomes the **endpoint
  record's host** — still resolved proxy-side, still never model-authored
  (the harness names a provider, never a URL). The base URL is validated at
  entry: HTTPS required except for loopback hosts (the local-model case);
  no userinfo; the path allowlist (`POST /chat/completions`,
  `POST /v1/messages`) is unchanged.
- Multiple endpoints (e.g. Anthropic for runs, local model for drafts) are
  **out of scope v1** — one endpoint, switchable in settings. The permit's
  `llm.providers` allowlist and `llm.connection` survive unchanged and keep
  the door open.

### The priced-pair gate, reworked

Today approval 400s on an unpriced (provider, model) pair — correct when the
platform fronted the money, a dead end when the user spends their own. The
gate's purpose (no silent unmetered spend) survives; the dead end does not:

1. **Bundled price table** — `llm/pricing.go` ships in the app and updates
   with the app. Known pairs behave exactly as today: cents caps, cents
   spend lines.
2. **User-entered price** — for an unpriced model on a remote endpoint, the
   approval surface offers an inline two-number form (\$ per million tokens
   in / out, with a link to the provider's pricing page) instead of
   refusing. The entered price is stored per (provider, model) in local
   config and feeds `CostCents` exactly as a bundled row would. Approval
   still does not proceed without a price for remote endpoints — the gate
   holds; the fix moved from "impossible" to "one form".
3. **Local endpoints are \$0 by construction** — a loopback base URL marks
   the endpoint `local`; runs record zero cost, the spend line renders
   "runs on your computer — free", and the pricing gate does not apply.
   `max_tokens` derivation (steps v1 compiler) falls back to a fixed
   default instead of a price-derived one.

## Enforcement posture on one machine — stated plainly

The permit's guarantee changes shape and the product copy must match it:

- **What is enforced**: every credential lives on the proxy side of the
  process; the harness holds a run token and nothing else; every tool call
  and LLM call is permit-checked, credential-injected, and audited at the
  proxy exactly as designed. The threat the blast radius exists for — the
  model steering the harness beyond what the user approved — is governed by
  a real boundary: the harness has no credentials to leak and no tool
  surface except what the control plane projects from the approved permit.
- **What is not claimed**: the harness shares a process and an OS user with
  the proxy in v1, so this is a **software boundary, not a sandbox**. The
  build spec's claim 2 ("it cannot reach anything outside this picture")
  had a Plan 5 NetworkPolicy launch gate; that gate is replaced by this
  posture statement, and the approval-screen copy is softened one notch to
  match: _"it can only act through Tomte's checkpoint, and every request is
  checked against this picture"_ — true as built.
- **Hardening path, named not scheduled**: split the harness into a
  credential-free child process, then OS-level egress restriction on that
  process. Neither is v1; both strengthen an existing boundary rather than
  create the first one.

## Estate triage — every merged spec under click-install

| Spec                                  | Verdict             | What changes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| ------------------------------------- | ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Egress proxy + vault (Plan 2)**     | **Survives**        | Enforcement posture restated (above). Fixed provider hosts become the endpoint record's host. Vault master key moves to the keychain. The per-tenant KEK envelope stays as built.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| **Scheduling + metering (Plan 3)**    | **Simplifies**      | Wake-aware window + heartbeat (the one addition). Monthly cap → local budget, same enforcement. Priced-pair gate reworked per above. Everything else — admission indexes, reaper, `engine.Fire`, schedule artifact — unchanged.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| **Identity**                          | **Simplifies hard** | Session core, tenant/owner row, Origin middleware, approval-as-recorded-act survive. Magic links, `login_token`, enumeration defenses, Postmark/`internal/mail`, public-base-URL-as-deploy-config all die. The shell mints sessions.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| **Steps v1 / decision 9**             | **Survives**        | Untouched, except `max_tokens` derivation gains the local-endpoint fallback. `RunProvider`/`RunModel` platform policy becomes "the configured endpoint".                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| **Connectors**                        | **Splits**          | The OAuth half dies: `internal/oauth`, `httpapi/oauth.go`, the refresh/epoch/advisory-lock machinery in `store/connection.go`, migration 00011's `oauth` kind and `epoch`, `google-calendar` defs. The `api_key` kind, capture-at-registration, `needs_reauth` (a pasted token can still be revoked upstream), the op-invocation gateway, constraints, SSRF suite, and MCP design all survive and **become the whole story**. Phases 5→6 promote from deferred to the main road. Curated v1 is Slack alone, via bot token.                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| **Grading + alerting (Plan 4)**       | **Simplifies**      | Grader, `run_grade`, pause switch, triggers, alert rows, in-app alert center: all survive. **Delivery changes**: the Postmark/`internal/publish` email path is replaced by OS notifications through the shell (the alert row stays the durable fact; the notifier is a new thin destination; the `alert_delivery` extension point the spec already named). **Consent changes**: grader spend is the user's own bill — the build conversation's spend line names it ("checking my work adds ~1–2¢ per run") and an empty rubric remains the off switch; grader model defaults to a cheap model on the configured endpoint, falling back to the run model where the endpoint offers no known cheap model.                                                                                                                                                                                                                                                                                             |
| **Escalation**                        | **Survives**        | Local compute's `Suspend` is already a no-op over durable state, so `awaiting_input` works today. Notification of a waiting decision arrives as an OS notification (no-authority-in-channel rule unchanged — the notification opens the app). A machine asleep past the escalation deadline expires the escalation on wake — same fail-closed semantics, worth one line of copy on the deadline picker.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| **Graduated permits**                 | **Survives**        | Pure control-plane reuse of versions + escalations; nothing hosted about it.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| **Objectives**                        | **Survives**        | Actor retention after completion is a local directory — the 30-day window costs nothing. The horizon/abandon alert rides the new notification path.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| **Build conversation**                | **Simplifies**      | The build resource, agent loop, verdict schema, mode split, elicitation discipline, options/select, submit atomicity: all survive. "Platform key" becomes the user's endpoint throughout (build agent and grader alike — the named proxy exemptions stand). OAuth connect cards are replaced by links into the standalone connections manager (frontend checklist items 5–6 become entry points into it; item 5's OAuth launch/return framing is dead). **Amendment its successor gains** (user feedback, 2026-08-31): after the verdict, the user sees possible inputs and outputs **graphically** — the "I'd need access to" block as an interactive palette driven by the catalog, read ops as inputs, write ops as outputs, connected distinguished from available-but-unconnected. Not designed here; the connections manager exposes the state it needs. The secrets copy's claim 2 launch gate is replaced per the enforcement-posture section. Spend copy speaks about the user's own bill. |
| **Compute (Plan 5, Substrate + K8s)** | **Dead**            | Already shelved by the direction change; the spec stays merged as a record. Local compute is the compute. The `Compute` interface stays — it is the seam a future hosted tier would re-enter through.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| **Platform design**                   | **Amended**         | By this spec: hosted/multi-tenant/operated-by-us reversed; self-hosting (as click-install) is the product. Tenancy-posture, Substrate-constraint, and harvest sections stand as history.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |

## Sunk cost, named plainly

Built correctly, to specs that this pivot retires — not mistakes, spent
work:

- **Connector phase 2's OAuth machinery** (~2,000 lines, merged): the
  `internal/oauth` package, the start/callback flow, singleflight +
  advisory-lock refresh, the credential-epoch CAS design, incremental
  re-consent, and migration 00011's oauth surface. The `api_key` half of the
  same phase survives and carries the pivot.
- **Most of identity's signup flow** (merged): magic-link request/verify,
  the scanner-proof interstitial, enumeration resistance, rate budgets,
  `login_token`, Postmark integration. The session core it also built is
  kept — roughly half that implementation survives.
- **Adjacent decisions retired with them**: Postmark as the single email
  provider; the Google CASA analysis (moot — no Google OAuth app at all
  now); the `google-calendar` catalog definition; per-tenant KEK envelope's
  multi-tenant rationale (schema kept).

## Proposed new roadmap

For the coordinating session to fold into the frozen queue. Serialized
`server/` work first, in order; parallel lanes after.

1. **P1 — Subtraction and floor** (`server/`, first): remove OAuth
   (packages, routes, store paths, migration dropping `oauth` kind +
   `epoch`, catalog defs) and login/mail; add the local-session mint;
   wake-aware scheduler window + heartbeat; endpoint record + custom base
   URL + local endpoints; pricing-gate rework (user-entered prices,
   local-\$0); budget rename and copy. Mostly deletion; one migration; every
   later item builds on this floor.
2. **P2 — Connectors main road** (`server/`): Slack token capture + verify
   (capture guide in catalog, control-plane verify), then old phases 5 → 6
   (remote MCP), phase 3 (options client) when the build needs it.
3. **P3 — Build conversation** (`server/`): the build resource, agent loop,
   and its checklist — unchanged as the highest product value, now against
   the P1 endpoint model.
4. **P4 — Grading + alerting**: as specced, with the notification
   destination replacing email delivery and the consent copy.
5. **P5–P7 — Escalation → objectives → graduated permits**: unchanged
   relative order and rationale.

**Parallel lanes, open immediately:**

- **Packaging shell** (new top-level dir, e.g. `app/`): tray shell, embedded
  Postgres, first-run flow, keychain, `go:embed` SPA serving, auto-update
  skeleton, notifier. Needs one coordinated `server/` touch — exposing
  `serve()` as a library entry — which should ride P1.
- **Frontend** (`web/`): retire the login screen; first-run and settings
  surfaces; capture cards; sleeping-machine copy on home and schedule
  confirmation. The build-conversation checklist items still wait on P3.
- **CI** (repo-global, still unowned): the catalog gate as a PR-time check —
  now pivot-critical, since auto-update ships catalog changes to users.

## Open questions

- **Shell framework** — Wails-class (Go-native, server in-process) vs
  Tauri + Go sidecar. Implementation-plan decision; lean Go-native for the
  single-process shape, but webview maturity on all three platforms decides
  it.
- **Platform order and signing** — macOS notarization and Windows signing
  are real money and lead time; which platform ships first is a product
  call the plan must take before packaging work starts.
- **Update feed hosting** — where signed artifacts and the update manifest
  live. Operational, cheap, but someone must own it.
- **Export / move to another machine** — state lives on one machine; a
  guided export/import (dump + vault re-wrap under a new keychain key) is
  owed before "install it on a mini PC" is honest advice. Out of v1, named.
- **Slack manifest hosting** — the capture card's `manifest_url` needs a
  stable public home (likely the docs site). Small, but it is the one
  hosted artifact this otherwise-local product still depends on.
- **Price-table staleness** — bundled prices age between app updates.
  Acceptable v1 (caps err by the drift, budget still enforced in recorded
  cents); revisit if providers reprice faster than we ship.

## Explicitly out of scope

The hosted always-on tier (named as future, not designed); multi-user
governance; multiple concurrent LLM endpoints; SMTP/email alert delivery;
app passcode/lock; machine-to-machine migration (open question above);
harness process isolation and OS sandboxing (hardening path, named);
Postgres major-version upgrades (pinned for v1); mobile companions; and any
implementation plan — the coordinating session re-derives the queue from
this spec first.

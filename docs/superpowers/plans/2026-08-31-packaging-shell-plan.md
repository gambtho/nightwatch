# Tomte packaging shell — plan and spike findings

**Status:** Plan proposed; framework decision recommended, two product calls
routed to the user via the coordinating session
**Date:** 2026-08-31
**Lane:** packaging (`app/`, new top-level directory; never touches `server/`)
**Implements:** the "Packaging shell" parallel lane of
[`2026-08-31-tomte-pivot-design.md`](../specs/2026-08-31-tomte-pivot-design.md)
— tray shell, embedded Postgres, first-run flow, keychain, SPA serving,
auto-update skeleton, notifier.

## Intended outcome

A click-installable desktop app: one artifact per platform that a
non-technical user installs, which lives in the tray, supervises a bundled
Postgres and the Tomte server, opens an embedded webview at the loopback
origin, starts at login, delivers OS notifications, and updates itself with
signature verification. This plan covers the framework decision, spike
evidence, and the phased build; it precedes production code.

## Spike findings (evidence, run 2026-08-31 on linux/amd64, WSL2)

The spike lives at `app/cmd/spike-headless` and `app/cmd/spike-tray`
(shared supervisor in `app/internal/boot`). What it proved:

- **The risky chain works end to end, headless**: state dir → generated
  per-install secrets (0600 file, keychain's spike stand-in) → embedded
  Postgres 16 via `fergusstrange/embedded-postgres` v1.34 (initdb on first
  run, random loopback port, random password) → today's `tomte serve` as a
  subprocess (it runs all 11 migrations itself before binding) → readiness
  probe confirming a 401 from `/v1/catalog` (full API stack up, not just a
  TCP listener) → clean shutdown, server before Postgres.
- **Timings**: cold first run (initdb + 11 migrations + boot) **2.5 s**;
  warm restart **1.5 s** to ready. First-run "paste a key and go" has no
  database-shaped latency problem.
- **Footprint**: 47 MB `pgdata` after initdb; 15 MB compressed engine
  payload — consistent with the spec's 30–50 MB packaging estimate.
- **Idempotence**: second run reuses `pgdata`, goose reports "no migrations
  to run", secrets are stable across runs (the vault key must be — DB
  contents are encrypted under it).
- **Platform gap, named**: the Wails tray/webview binary
  (`go build -tags guispike ./cmd/spike-tray`) does not link on this
  machine — Wails v3 beta.16 on Linux requires **GTK4 + webkitgtk-6.0**
  dev packages (`libgtk-4-dev`, `libwebkitgtk-6.0-dev`, one apt install
  away, needs sudo). The headless supervisor — the genuinely uncertain
  part — is verified; the tray build compiles against the beta.16 API as
  written from the upstream `systray-menu` example but is **not yet
  link-verified**. First GUI verification should happen on the machine of
  whichever platform ships first (see product calls).

## Decision for review: Wails v3, not Tauri + Go sidecar

**Recommendation: Wails v3 (Go-native).** The spec leaned Go-native "for
the single-process shape" with webview maturity as the decider; the
evidence supports taking that lean.

What the shell contract needs, checked against both (verified upstream at
`wailsapp/wails` v3.0.0-beta.16, tagged 2026-08-29, and Tauri v2 docs):

| Contract item | Wails v3 | Tauri v2 |
| --- | --- | --- |
| Tray-resident, window optional | core `SystemTray` API, window-close-to-hide hook | core tray-icon feature |
| Single instance | `single_instance` plugin | plugin |
| Embedded webview at a URL | `WebviewWindowOptions.URL` | core |
| Autostart | `start_at_login` plugin | autostart plugin |
| OS notifications | built-in notifications service | plugin |
| Auto-update | `v3/pkg/updater`: ed25519/ecdsa signature verify (fail-closed), atomic swap, providers for GitHub Releases / appcast / endpoint / keygen | mature built-in updater |
| Supervise Postgres + server | plain Go in-process — the spike's `boot` package is already the supervisor | Rust shell managing a Go sidecar over process IPC |

The decisive asymmetry is the last row plus `serve()`. With Wails, the P1
`serve()` library entry point is an ordinary function call in the same
process: the shell hands the keychain-held master key to the server as a
`[]byte`, supervises one child (Postgres), and lifecycle is one Go
`context`. With Tauri, the entire server plus supervisor becomes a sidecar
binary: the key crosses a process boundary, lifecycle/crash-restart logic
is duplicated on the Rust side, every tray/notification/update interaction
with server state crosses IPC, and the team maintains a second toolchain in
a repo that is otherwise Go + TypeScript.

**The honest cost**: Wails v3 is **beta** (stable API per upstream, but
beta), while Tauri v2 is stable. Weighed and accepted because: the release
cadence is high (three betas in the last week of 2026-08); every contract
item above exists today rather than being roadmap; the beta risk is
concentrated in the thin GUI layer (tray, window, updater UI) while all
product logic stays in our server; and the fallback is real — the `boot`
supervisor package is framework-free, so a forced migration to Tauri
would rewrite only the shell wrapper, not the supervision or first-run
logic. **Revisit trigger**: if v3 betas break us twice in one release
cycle or a required platform's webview proves unstable, re-evaluate
Tauri with the supervisor kept as the sidecar's core.

## Two product calls — routed to the user, not decided here

1. **Platform ship order.** Recommendation: **macOS first, Windows second,
   Linux last.**
   - macOS first: the target non-technical user skews Mac for this product
     shape; notarization (Apple Developer Program, $99/yr, near-instant
     notarization service once enrolled) is cheaper and faster to stand up
     than Windows EV-grade trust; and macOS is where tray-resident apps are
     an established pattern (menu bar).
   - Windows second: code-signing that avoids SmartScreen reputation pain
     effectively means an EV/OV certificate ($300–500/yr, org validation
     lead time measured in weeks) — start the paperwork now even though
     Windows ships second. The spec also flags bundled-Postgres AV
     heuristics on Windows as needing real-machine testing.
   - Linux last: smallest audience for a click-install non-technical
     product; no signing gate, so it can trail without cost.
2. **Update-feed hosting.** Recommendation: **GitHub Releases on the
   existing repo** as the v1 feed. The Wails updater has a GitHub Releases
   provider; artifacts are signed (ed25519, verify fail-closed) so the
   feed host does not need to be trusted, only available; it is free, and
   the repo already lives there. Sensitivity: a public feed on a public
   repo — if the repo stays private, GitHub Releases still works via the
   updater's `endpoint` provider against release assets, or we front it
   with a static page later. The one other hosted artifact (the Slack
   `manifest_url`, per the spec's open questions) can share whatever docs
   site eventually exists; it is not needed for packaging phase 1.

## Architecture (app/ shape)

```
app/
  go.mod                     — own module github.com/gambtho/tomte/app
  internal/boot/             — supervisor: state dir, secrets, embedded PG,
                               server lifecycle (spike version exists; grows
                               crash-restart w/ backoff, pg_dump backups)
  internal/keychain/         — master key: OS keychain via go-keyring;
                               0600-file fallback (Linux w/o Secret Service)
  internal/update/           — feed check + staged apply (Wails updater)
  cmd/spike-headless/        — kept until the real shell subsumes it
  cmd/spike-tray/            — grows into cmd/tomte-app
```

- `app/` is a separate Go module — the server keeps its own dependency
  graph free of GUI/cgo deps; a `go.work` at the repo root joins them for
  local development.
- Until P1 lands, the shell drives `tomte serve` as a subprocess (spike
  shape). When P1 exposes `serve()` as a library entry point, the shell
  imports it; the subprocess path stays behind a flag as the dev/debug
  escape hatch. **Coordination point**: P1's `serve()` signature should
  accept (a) a config struct instead of env vars, or (b) documented env-var
  contract retained — the lane needs the master key passed as bytes, the
  listen addr/port chosen by the shell, and a way to know readiness other
  than log-scraping (return after bind, or a ready callback).
- Postgres reachability per spec: Unix socket on macOS/Linux, loopback TCP
  + random port + random password on Windows. The spike used TCP
  everywhere; sockets are phase 2 work
  (`embedded-postgres` supports extra `postgresql.conf` parameters).

## Ordered implementation steps

1. **Phase 1 — real shell, one platform** (after ship-order call): rename
   `spike-tray` → `cmd/tomte-app`; window-close-to-hide, single-instance
   plugin, start-at-login plugin with first-run consent copy, notifications
   service smoke test; keychain package (go-keyring + file fallback);
   crash-restart with backoff for the Postgres child; bundle pinned PG 16
   binaries in the app payload instead of runtime download (the
   `embedded-postgres` `BinariesPath` hook the spike already exercises via
   cache config).
2. **Phase 2 — first-run flow plumbing**: detect first run (spike logic),
   open window to the server's first-run screen (frontend lane owns the
   screen); Unix-socket Postgres on macOS/Linux; state-dir layout per spec
   (`pgdata/`, `actors/`, `config`, `backups/`).
3. **Phase 3 — update skeleton**: Wails updater wired to the chosen feed;
   ed25519 keypair minted and the public key pinned in the binary;
   pre-migration `pg_dump` backup (rotate, keep 3) before any release that
   carries migrations; the restore-and-relaunch-previous path per spec.
4. **Phase 4 — packaging + signing**: installers via Wails packaging
   (`.app`+DMG / NSIS or MSI), notarization/signing pipelines per the
   ship-order call; real-machine Windows AV testing.
5. **On P1 merge**: swap subprocess → `serve()` library call.

## Testing and verification strategy

- `boot` package: unit tests with a stub server binary (readiness, secret
  stability, teardown order, crash-restart); the headless spike run doubles
  as the integration test and should become a CI job once the CI lane
  exists (it needs only Go + network for the PG payload, no GUI).
- Tray/GUI: manual smoke per platform against a checklist (tray present,
  close-hides, single instance focuses, autostart survives reboot,
  notification delivers); no GUI CI in v1.
- Update path: a fake `endpoint` feed in tests; signature-verification
  failure must abort the swap (fail-closed is upstream-tested, we test our
  wiring).

## Adaptation points

- **Wails v3 beta breakage** — revisit trigger named above.
- **P1's `serve()` shape** — if it keeps env-var config, the shell adapts
  (env is process-local before the call) but readiness signaling must be
  agreed; raise at P1 review.
- **WSL2 cannot verify GUI** — first tray verification moves to the
  ship-order platform's hardware; findings may adjust phase 1.
- **`embedded-postgres` on Windows** — unverified here; the library
  supports Windows but AV behavior is the spec's named risk.

## Explicit exclusions

Per spec: SQLite (rejected-for-v1 with revisit trigger), Postgres major
upgrades (pinned), export/move-to-another-machine, harness process
isolation, hosted tier. This lane also does not touch `server/` (P1 owns
the `serve()` entry point) or `web/` (first-run screen is the frontend
lane's).

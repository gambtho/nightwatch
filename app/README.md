# app/ — Tomte desktop shell (packaging lane)

Spike stage. See the plan:
[`docs/superpowers/plans/2026-08-31-packaging-shell-plan.md`](../docs/superpowers/plans/2026-08-31-packaging-shell-plan.md).

- `internal/boot` — supervisor: state dir, per-install secrets, embedded
  Postgres 16, `tomte serve` child, readiness probe, ordered teardown.
- `cmd/spike-headless` — proves the chain without a GUI:

  ```sh
  (cd ../server && go build -o /tmp/tomte ./cmd/tomte)
  go run ./cmd/spike-headless -server-bin /tmp/tomte
  ```

  State lives in `~/.local/share/tomte-spike` (delete to re-test first run).

- `cmd/spike-tray` — the same chain inside a Wails v3 tray shell with the
  webview at the loopback origin. Build-tagged because it needs cgo GUI
  deps (Linux: `libgtk-4-dev libwebkitgtk-6.0-dev`):

  ```sh
  go build -tags guispike ./cmd/spike-tray
  ```

This module is separate from `server/` so GUI/cgo dependencies never enter
the server's graph. The shell drives `tomte serve` as a subprocess until P1
exposes `serve()` as a library entry point.

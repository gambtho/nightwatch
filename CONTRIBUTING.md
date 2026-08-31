# Working here

How this repo is developed day to day. The product direction and the live
state of every workstream are on the coordination board:
[`docs/superpowers/plans/2026-08-31-parallel-sessions.md`](docs/superpowers/plans/2026-08-31-parallel-sessions.md).

## The lane model

Work runs as parallel **lanes** — often parallel agent sessions — each
owning one area (the K8s track, `server/`, `web/`, docs, CI), coordinated
through the board. The board has a **single writer**, the coordinating
session: route findings and decisions to it rather than editing the board
yourself.

Three standing rules:

- **One session owns `server/` at a time.** Everything else — `tomtectl/`,
  `web/`, docs, CI — parallelizes freely. The board's queue says who holds
  the lock and who is next.
- **No pre-stacked PR bases.** Every PR targets `main`, and a follow-on PR
  waits for its predecessor to merge. (Added after a stacked PR merged into
  its base branch and a full phase of work was silently stranded off
  `main`.)
- **Dated specs and plans under `docs/` are never rewritten.** Supersession
  is recorded with a short banner on the old doc and a line in
  [`docs/README.md`](docs/README.md), which indexes what is living vs
  historical.

## The 15-minute path

1. Clone, then read the [README](README.md) and
   [`docs/README.md`](docs/README.md) — the living docs there (the board's
   "Direction change 2" and "State of the world" sections especially) are
   the current picture.
2. Run hello world on a local cluster (Go 1.26+ and
   [kind](https://kind.sigs.k8s.io/) installed):

   ```sh
   kind create cluster

   cd tomtectl && go build -o tomtectl .
   ./tomtectl init            # writes agent.yaml — read it first
   ./tomtectl up              # ConfigMap + Deployment, from the file
   ./tomtectl status          # 1/1 ready
   ./tomtectl logs --follow   # hello world, on the schedule
   ```

   `./tomtectl down` removes the agent; `kind delete cluster` removes the
   cluster.

3. Pick up work from the board — or ask the coordinating session for a
   lane.

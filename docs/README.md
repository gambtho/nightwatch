# Tomte docs — what is current, what is history

The project changed direction twice on the same day (2026-08-31): first
from a hosted, multi-tenant platform to a click-install desktop app (the
**pivot**), then to a **Kubernetes-first agent track** — CLI before UI,
transitioning into the full Tomte experience. Project convention: dated
specs and plans are never rewritten after the fact. Superseded documents
get a short banner pointing at their successor, and this index says which
documents to trust today.

Read this page top to bottom and you know what is current.

## Living — trust these today

- [The coordination board](superpowers/plans/2026-08-31-parallel-sessions.md)
  — the live picture: both direction changes verbatim, the lane
  assignments, the serialized `server/` queue (P1–P7), and every decision
  of record. Single writer: the coordinating session — route findings to
  it rather than editing the board.
- [The pivot design spec](superpowers/specs/2026-08-31-tomte-pivot-design.md)
  — design source of truth for the full Tomte experience the K8s track
  transitions into: endpoint agnosticism, identity at its floor, the
  pricing gate, credentials without OAuth. Direction change 2 deliberately
  did **not** re-triage this spec's estate.
- [The K1 plan](superpowers/plans/2026-08-31-k8s-agent-k1-plan.md) and
  [`tomtectl/README.md`](../tomtectl/README.md) — the current focus:
  agent-as-code YAML plus the `tomtectl` CLI, hello world on a real
  cluster. K2 (LLM) and K3 (connectors + governance transition) follow.
- [The P1 plan](superpowers/plans/2026-08-31-tomte-p1-subtraction-floor.md)
  — subtraction and floor, implemented and merged (PR #48). The best
  reference for what the server tree looks like after the pivot's
  removals (no OAuth, no login/mail, local session, endpoint record,
  reworked pricing gate, `serve()` as a library).
- [API contract — `api/v1.md`](api/v1.md) — the UI↔server `/v1` contract
  (alpha, unstable).

## Historical — records, kept unrewritten

### Queued designs, still the plan of record

Dated pre-pivot (Nightshift naming), but each is still the design for a
queue item on the board. Read them through the pivot spec's amendments.

- [Build conversation](superpowers/specs/2026-08-31-nightshift-build-conversation-design.md)
  — queue item P3, unchanged as the highest product value.
- [Connector catalog](superpowers/specs/2026-08-30-nightshift-connectors-design.md)
  — its OAuth half was removed in P1; the token-capture road is queue
  item P2 (see the pivot spec's "Credentials without OAuth").
- [Grading + alerting](superpowers/specs/2026-08-31-nightshift-grading-alerting-design.md)
  — queue item P4; OS notifications replace the Postmark delivery path.
- [Escalation](superpowers/specs/2026-08-31-nightshift-escalation-design.md)
  — queue item P5.
- [Objectives](superpowers/specs/2026-08-31-nightshift-objectives-design.md)
  — queue item P6 (depends on escalation).
- [Graduated permits](superpowers/specs/2026-08-31-nightshift-graduated-permits-design.md)
  — queue item P7 (depends on P4's grader).

### Built — records of what exists in `server/`

Executed designs and plans; the code and its tests are now the authority.

- [Egress proxy design](superpowers/specs/2026-08-30-nightshift-egress-proxy-design.md)
  — the proxy survives every direction change as the enforcement point;
  it now ships inside the deployment rather than a hosted fleet.
- [Scheduling + metering design](superpowers/specs/2026-08-31-nightshift-scheduling-metering-design.md)
  — built as Plan 3; P1 later fixed the wake-aware scheduler window and
  renamed the monthly tenant cap to the user's local budget.
- [Platform foundation plan](superpowers/plans/2026-08-30-nightshift-platform-foundation.md)
  (Plan 1, PR #1) ·
  [egress proxy plan](superpowers/plans/2026-08-30-nightshift-egress-proxy.md)
  (Plan 2, PR #5) ·
  [scheduling + metering plan](superpowers/plans/2026-08-31-nightshift-scheduling-metering.md)
  (Plan 3, PR #10).
- [Identity implementation plan](superpowers/plans/2026-08-31-nightshift-identity-implementation.md)
  (PR #17) — its magic-link/mail half was since removed in P1.
- [Connectors implementation plan](superpowers/plans/2026-08-31-nightshift-connectors-implementation.md)
  (phases 1, 2, 4 merged) — phase 2's OAuth machinery was since removed
  in P1; phases 5→6 return as P2's remote-MCP road.

### Superseded or closed

- [UX design (2026-08-28)](superpowers/specs/2026-08-28-nightshift-design.md)
  — the founding UX anchor; the target user and four surfaces stand, but
  its hosted assumptions and OAuth-based capture are superseded by the
  pivot spec. Amended by the escalation and objectives specs.
- [Platform design (2026-08-30)](superpowers/specs/2026-08-30-nightshift-platform-design.md)
  — the hosted, multi-tenant backend; both of its core decisions were
  deliberately reversed by the pivot spec.
- [Identity design](superpowers/specs/2026-08-30-nightshift-identity-design.md)
  — magic-link login, signup, and mail died in P1; the session core
  survives per the pivot spec's "Identity at its floor".
- [Cluster compute design (Plan 5)](superpowers/specs/2026-08-31-nightshift-compute-design.md)
  — shelved by the pivot; no implementation. Despite the topic, unrelated
  to the current K8s agent track, which is CLI-local by decision.
- [Platform roadmap](superpowers/plans/2026-08-30-nightshift-platform-roadmap.md)
  — superseded by the pivot spec's roadmap, tracked on the board as
  queue P1–P7.
- [UX prototype plan](superpowers/plans/2026-08-29-nightshift-ux-prototype.md)
  — the prototype it built is removed from `main` (git history and the
  demo branches keep it); the real frontend is `web/`.
- [Packaging shell plan](superpowers/plans/2026-08-31-packaging-shell-plan.md)
  — the desktop shell, paused by direction change 2; kept as the Wails
  v3 decision record. Click-install's ultimate fate is deliberately
  undecided.
- [Substrate leadership brief](briefs/2026-08-30-substrate-leadership-brief.md)
  and [Substrate verification spike](research/2026-08-30-substrate-verification.md)
  — the substrate thread was closed by the pivot; kept as records of the
  hosted-era analysis.
- [User test 1 facilitator kit](research/2026-08-30-user-test-1-facilitator-kit.md),
  [notes template](research/2026-08-30-user-test-1-notes-template.md), and
  [user test 2 (dev persona) kit](research/2026-08-30-user-test-2-dev-facilitator-kit.md)
  — research artifacts for the UX studies; the dev-persona demo lives on
  the `demo/dev-persona` branch, never merged.

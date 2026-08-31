# Nightshift Platform — Plan Roadmap

**Date:** 2026-08-30
**Spec:** [`../specs/2026-08-30-nightshift-platform-design.md`](../specs/2026-08-30-nightshift-platform-design.md)

The platform spec is too large for one implementation plan. This roadmap records the
decomposition into plans that each produce working, testable software, plus the scoping
decisions made while cutting it. Each plan is written when we reach it; only Plan 1
exists today.

## The sequence

| #   | Plan                                                                                                              | Delivers                                                                                                                                                                                                             | Status      |
| --- | ----------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------- |
| 1   | **Foundation** ([`2026-08-30-nightshift-platform-foundation.md`](./2026-08-30-nightshift-platform-foundation.md)) | `server/` Go module: real tenancy, public v1 API (workflows, versions, approval, runs), the `Compute` seam with a local implementation, the harvested harness (tool-less), run records pushed over the internal API. | Written     |
| 2   | **Egress proxy + credential vaulting**                                                                            | Governance primitive #1. The proxy that enforces the permit, credential injection at the boundary, per-tenant DEKs (rework of cronfoundry `internal/secrets`), `internal/redact` port.                               | Not started |
| 3   | **Scheduling + spend metering**                                                                                   | Cron + IANA timezone scheduler with per-tenant fairness; metering checked before each model request; per-run and per-tenant caps.                                                                                    | Not started |
| 4   | **Rubric grading + alerting**                                                                                     | Independent grader scoring each run per criterion; auto-pause after 3 consecutive failures; alert delivery (port of `internal/publish` lands here).                                                                  | Not started |
| 5   | **Substrate + Kubernetes-Jobs `Compute` implementations**                                                         | The two cluster-facing implementations of the seam Plan 1 defines. Deferred to last deliberately: Substrate is pre-1.0 and needs a fleet; the seam is designed so it slots in.                                       | Not started |

**Prerequisite specs (not plans) still owed:**

- **Connector catalog** — top product risk in both specs; needs its own spec before any
  tool/connector plan. The MCP transport rework (see below) belongs to it.
- **Identity and onboarding** — real signup/login is in no plan yet. Plan 1 ships a
  dev-only session mint; the production auth story (provider, tenant creation flow) is
  an open decision.
- **Egress proxy design detail** — before Plan 2 is written, the proxy's trust
  boundary needs resolving (raised by the 2026-08-30 Codex review): how an actor
  authenticates to the proxy, destination canonicalization, redirect and DNS-rebinding
  defenses, TLS handling, and how "no direct egress" is proven rather than assumed.
  The platform spec asserts the guarantee; Plan 2's spec work must design it.

## Scoping decisions made during decomposition

Recorded here so later plans don't rediscover them. Evidence: the 2026-08-30 survey of
cronfoundry (see the foundation plan's task notes).

1. **Plan 1's harness is tool-less** — one model exchange per run, no MCP. CronFoundry's
   `internal/mcp` is stdio-subprocess-only, which a hosted multi-tenant platform cannot
   safely run; the transport must be redesigned, and that redesign belongs with the
   connector-catalog spec, not the foundation. The tracer bullet proves tenancy,
   versioning/approval, the Compute seam, and pushed run records without tools.
2. **LLM providers ported: `anthropic`, `openai`, `openrouter`.** Dropped:
   `copilot-enterprise` (GitHub-coupled, undocumented token API — the survey's
   do-not-port) and `azure-foundry` (no `ChatTurn`, and it alone drags a second major
   version of the OpenAI SDK). Provider neutrality is retained with three.
3. **Not ported in Plan 1:** `internal/publish` and `internal/template` (both serve
   output delivery; no destinations exist until alerting — Plan 4), `internal/secrets`
   and `internal/redact` (no customer credentials exist until the proxy — Plan 2),
   `internal/memory` / `internal/writeback` (superseded by actor state, per the spec).
4. **Composite foreign keys from day one.** CronFoundry's own migration comment admits
   its single-column FKs don't enforce same-org child relationships. Every child table
   here carries `FOREIGN KEY (tenant_id, workflow_id) REFERENCES workflow (tenant_id, id)`.
5. **Separate keys per concern.** CronFoundry signs sessions with the same master key
   that encrypts secrets. Nightshift uses `NIGHTSHIFT_SESSION_KEY` and
   `NIGHTSHIFT_RUNNER_KEY` from the start; the secrets KEK arrives in Plan 2 as a third.
6. **Tenant in every claim.** `SessionClaims` carries `TenantID` and `UserID`;
   `RunClaims` carries `TenantID` — closing cronfoundry's largest multi-tenancy gap
   (its session claims had no tenant at all).
7. **Hand-written pgx queries, not sqlc.** Six tables don't justify a codegen step; the
   store's method signatures make the tenant parameter explicit. Revisit sqlc if the
   schema grows past what hand-written queries keep honest.
8. **The prototype stays at `src/`** — the quarantine move from the session handoff was
   explicitly declined (2026-08-30). The Go platform lives at `server/` alongside it.
9. **The v1 API ships stamped "unstable (alpha)".** Plan 1's `steps` document is the
   compiled execution form (system prompt, provider, model), not the user-facing
   `{id, text}` steps the UX prototype defines. Freezing that as a public contract
   would leak execution internals (2026-08-30 Codex review); before the contract
   freezes, the user-facing artifact joins the version document and the execution form
   becomes server-derived.
10. **No orphaned-run recovery in the foundation.** A server restart kills in-flight
    `Local` goroutines, a failed context fetch never finalizes, and per-actor queueing
    can outlive the 1h run-token TTL — in each case a run is stuck `pending`/`running`
    forever with nothing noticing. Plan 3 (scheduling) must add an orphaned-run reaper
    that finalizes runs stuck past a deadline as `failed`/`error_kind: "orphaned"`, and
    revisit the run-token TTL against actor queueing depth. (2026-08-30 final branch
    review.) Closed by Plan 3 (2026-08-31): reaper + deadline>TTL invariant shipped;
    queueing dissolved by the one-active-run admission index.

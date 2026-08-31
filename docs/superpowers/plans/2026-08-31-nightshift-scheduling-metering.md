# Nightshift Scheduling + Spend Metering Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cron+IANA scheduling with at-least-once dispatch recovery and DB-enforced single-active-run admission, spend metering (monthly tenant cap enforced at the proxy hook, per-run cap with transactional overrun events, priced-model validation), and the orphaned-run reaper.

**Architecture:** Three new packages — `internal/schedule` (cron/tz artifact), `internal/meter` (implements `proxy.Hook`), `internal/engine` (the extracted single firing path, the scheduler tick with create-then-dispatch recovery, and the reaper) — plus migrations 00007/00008 and wiring. Coordination is two partial unique indexes on `run` (idempotent occurrences; one active run per workflow), not locks or leader election.

**Tech Stack:** Go 1.26, Postgres, `robfig/cron/v3` (new dep), existing `internal/{store,permit,llm,proxy,token,compute,httpapi,internalapi}`.

**Spec:** `docs/superpowers/specs/2026-08-31-nightshift-scheduling-metering-design.md` (parent: `docs/superpowers/specs/2026-08-30-nightshift-platform-design.md`)

## Global Constraints

- Module `github.com/gambtho/nightwatch/server`; work in this worktree (branch `sched-spec`); never modify `/home/tng/workspace/cronfoundry` or `src/`.
- Migrations are `00007_schedule.sql` and `00008_scheduling_runs.sql` (00001-00006 exist).
- Tenant-scoping rule stands for all request-path store methods. **Documented exemption**: the scheduler and reaper are _platform_ actors — store methods serving them are cross-tenant by design, carry a `// System query:` doc comment explaining why, return rows that include `TenantID`, and are named `List...` with no `tenantID` parameter. Nothing request-path may call them.
- Fail closed everywhere: invalid schedule/spend documents → 400; unpriced (provider, model) → 400; meter store failure → request denied (`HookError{403}`); scheduler skips are logged, never silently swallowed errors.
- Statuses/reasons are exact strings: `fire_reason` ∈ `manual|schedule`; reaped runs are `failed` with `error_kind: "orphaned"`; overrun events are type `spend.exceeded`.
- Env additions: `NIGHTSHIFT_RUN_TOKEN_TTL` (Go duration, default `1h`), `NIGHTSHIFT_RUN_DEADLINE` (default `2h`, must exceed TTL — startup exits otherwise), `NIGHTSHIFT_DEFAULT_MONTHLY_CAP_CENTS` (int, default `0` = unlimited).
- Verification from `server/`: `gofmt -l .` (prints nothing), `go vet ./...`, `go build ./...`, `go test ./...` (Docker available for testcontainers). Conventional commits. Docs get `npx prettier --write` from the repo root (run it yourself).
- The e2e scheduled-run test drives `Scheduler.Tick` directly with an injected clock — never `time.Sleep` for a ticker.

---

## File structure

```
server/internal/schedule/          schedule.go, schedule_test.go     (cron+tz artifact; robfig wrapper)
server/internal/meter/             meter.go, meter_test.go           (proxy.Hook impl + month spend)
server/internal/engine/            engine.go (Fire/dispatch), scheduler.go, reaper.go + tests
server/internal/db/migrations/     00007_schedule.sql, 00008_scheduling_runs.sql
server/internal/store/             workflow.go, run.go, tenant.go (modified)
server/internal/llm/pricing.go     Priced() + claude-sonnet-5 row
server/internal/permit/permit.go   Spend extension
server/internal/httpapi/           workflows.go (validation), runs.go (engine + 409 + exposure)
server/internal/internalapi/       internalapi.go (finalize resolves the per-run cap)
server/cmd/nightshift/main.go      wiring; docs/api/v1.md, server/README.md updated
```

---

### Task 1: Priced models — `llm.Priced` and fail-closed validation

**Files:**

- Modify: `server/internal/llm/pricing.go`, `server/internal/httpapi/workflows.go` (decodeDoc)
- Test: `server/internal/llm/pricing_test.go` (extend), `server/internal/httpapi/workflows_test.go` (one added test)

**Interfaces:**

- Consumes: existing `priceTable`, `decodeDoc`.
- Produces: `llm.Priced(provider, model string) bool`; the anthropic table gains `"claude-sonnet-5": {in: 300, out: 1500}` (Sonnet-class $3/$15 per MTok, consistent with the `claude-sonnet-4-5` row — every existing fixture uses this model, closing the Plan-1 deferred minor); `decodeDoc` rejects unpriced steps with 400.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/llm/pricing_test.go`:

```go
func TestPriced(t *testing.T) {
	if !Priced("anthropic", "claude-sonnet-5") {
		t.Fatal("claude-sonnet-5 must be priced — every fixture uses it")
	}
	if Priced("anthropic", "no-such-model") {
		t.Fatal("unknown model must not be priced")
	}
	if Priced("nope", "gpt-4o-mini") {
		t.Fatal("unknown provider must not be priced")
	}
}
```

Append to `server/internal/httpapi/workflows_test.go`:

```go
func TestCreateWorkflowRejectsUnpricedModel(t *testing.T) {
	e := newEnv(t)
	body := workflowBody()
	body["steps"].(map[string]any)["model"] = "claude-imaginary-9"
	resp, out := e.do(t, "POST", "/v1/workflows", body)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, out["error"], "pricing")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/llm/ -run TestPriced && go test ./internal/httpapi/ -run TestCreateWorkflowRejectsUnpricedModel`
Expected: FAIL (compile error — `Priced` undefined; then the API test returns 201).

- [ ] **Step 3: Implement**

In `server/internal/llm/pricing.go`, add to the anthropic map:

```go
		"claude-sonnet-5":   {in: 300, out: 1500},
```

and add after `CostCents`:

```go
// Priced reports whether a (provider, model) pair has a price row. The
// workflow API fails closed on unpriced pairs at create/approve time, which
// is what makes spend caps meaningful — CostCents returning 0 for an
// unknown model must be unreachable for approved workflows, not a loophole.
func Priced(provider, model string) bool {
	_, ok := priceTable[provider][model]
	return ok
}
```

Also update the `priceTable` doc comment's "Unknown ... return 0 rather than erroring" sentence to note that workflow validation makes unknown pairs unreachable for approved workflows.

In `server/internal/httpapi/workflows.go` `decodeDoc`, after the permit validation block:

```go
	if !llm.Priced(body.Steps.Provider, body.Steps.Model) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "no pricing for " + body.Steps.Provider + "/" + body.Steps.Model + " — spend caps require a priced model",
		})
		return body, false
	}
```

(add import `"github.com/gambtho/nightwatch/server/internal/llm"`).

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS — existing fixtures use `claude-sonnet-5` (now priced) and `gpt-4o-mini` (already priced).

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): priced-model validation — spend caps require a price row"
```

---

### Task 2: The schedule artifact

**Files:**

- Create: `server/internal/schedule/schedule.go`, `server/internal/db/migrations/00007_schedule.sql`
- Modify: `server/internal/store/workflow.go` (VersionDoc, versionCols, scanVersion, both INSERTs), `server/internal/httpapi/workflows.go` (versionDocJSON, versionJSON, toVersionJSON, decodeDoc, both VersionDoc constructions)
- Test: `server/internal/schedule/schedule_test.go`, `server/internal/httpapi/workflows_test.go` (one added test), `server/internal/store/workflow_test.go` (extend lifecycle)

**Interfaces:**

- Consumes: nothing internal.
- Produces:

```go
package schedule
type Schedule struct {
	Cron string `json:"cron"`
	TZ   string `json:"tz"`
	// unexported: parsed cron.Schedule and *time.Location
}
func Parse(raw []byte) (*Schedule, error) // strict; both fields; 5-field cron, no descriptors/seconds; IANA tz
func (s *Schedule) Next(after time.Time) time.Time // next occurrence, computed in the schedule's zone
```

and `store.VersionDoc.Schedule json.RawMessage` (nil = manual-only), persisted in a new nullable `schedule jsonb` column, surfaced as `"schedule"` on the version wire shapes.

- [ ] **Step 1: Fetch the dep and write the migration**

```bash
cd server && go get github.com/robfig/cron/v3@latest
```

`server/internal/db/migrations/00007_schedule.sql`:

```sql
-- +goose Up
ALTER TABLE workflow_version ADD COLUMN schedule jsonb;

-- +goose Down
ALTER TABLE workflow_version DROP COLUMN schedule;
```

- [ ] **Step 2: Write the failing tests**

`server/internal/schedule/schedule_test.go`:

```go
package schedule_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/schedule"
)

func TestParseValidAndNext(t *testing.T) {
	s, err := schedule.Parse([]byte(`{"cron":"0 9 * * MON","tz":"America/New_York"}`))
	require.NoError(t, err)

	// Wed Jan 7 2026 12:00 UTC -> next Monday 09:00 America/New_York.
	after := time.Date(2026, 1, 7, 12, 0, 0, 0, time.UTC)
	next := s.Next(after)
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 1, 12, 9, 0, 0, 0, loc).Unix(), next.Unix())
}

func TestParseRejects(t *testing.T) {
	for name, raw := range map[string]string{
		"missing tz":       `{"cron":"* * * * *"}`,
		"missing cron":     `{"tz":"UTC"}`,
		"bad cron":         `{"cron":"not cron","tz":"UTC"}`,
		"descriptor":       `{"cron":"@daily","tz":"UTC"}`,
		"six fields":       `{"cron":"0 0 9 * * MON","tz":"UTC"}`,
		"bad tz":           `{"cron":"* * * * *","tz":"Mars/Olympus"}`,
		"empty tz":         `{"cron":"* * * * *","tz":""}`,
		"local tz":         `{"cron":"* * * * *","tz":"Local"}`,
		"unknown field":    `{"cron":"* * * * *","tz":"UTC","jitter":5}`,
		"trailing garbage": `{"cron":"* * * * *","tz":"UTC"}x`,
		"not json":         `nope`,
	} {
		_, err := schedule.Parse([]byte(raw))
		require.Error(t, err, name)
	}
}

func TestDSTSpringForwardPinned(t *testing.T) {
	// 2026-03-08 02:30 does not exist in America/New_York (clocks jump
	// 02:00 -> 03:00). Pin robfig/cron's behavior for a 02:30 daily
	// schedule across that boundary so a dependency upgrade cannot
	// silently change semantics: the library fires at the next real
	// occurrence after the gap.
	s, err := schedule.Parse([]byte(`{"cron":"30 2 * * *","tz":"America/New_York"}`))
	require.NoError(t, err)
	loc, _ := time.LoadLocation("America/New_York")
	after := time.Date(2026, 3, 7, 12, 0, 0, 0, loc)
	first := s.Next(after)
	second := s.Next(first)
	// The 02:30 slot on Mar 8 is skipped (it does not exist); the pinned
	// expectation is Mar 9 02:30 following Mar 7... after==Mar 7 12:00, so
	// first is Mar 8's occurrence IF the library maps it, else Mar 9.
	// Assert the invariants that matter and log the concrete times:
	require.True(t, first.After(after))
	require.True(t, second.After(first))
	require.Equal(t, 30, second.In(loc).Minute())
	t.Logf("pinned DST behavior: first=%s second=%s", first.In(loc), second.In(loc))
	// Pin the exact first-occurrence date so upgrades are visible:
	require.Equal(t, time.Date(2026, 3, 9, 2, 30, 0, 0, loc).Unix(), second.Unix())
}
```

`server/internal/httpapi/workflows_test.go`:

```go
func TestWorkflowScheduleValidation(t *testing.T) {
	e := newEnv(t)

	body := workflowBody()
	body["schedule"] = map[string]any{"cron": "not cron", "tz": "UTC"}
	resp, _ := e.do(t, "POST", "/v1/workflows", body)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body["schedule"] = map[string]any{"cron": "0 9 * * MON", "tz": "America/New_York"}
	resp, out := e.do(t, "POST", "/v1/workflows", body)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, out["version"].(map[string]any)["schedule"])
}
```

Extend `TestWorkflowVersionLifecycle` in `server/internal/store/workflow_test.go`: change `testDoc()` to include

```go
		Schedule: json.RawMessage(`{"cron":"0 9 * * MON","tz":"UTC"}`),
```

and after the existing `GetApprovedVersion` assertion for v2, add:

```go
	require.JSONEq(t, `{"cron":"0 9 * * MON","tz":"UTC"}`, string(got.Doc.Schedule))
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd server && go test ./internal/schedule/ ./internal/store/ ./internal/httpapi/`
Expected: FAIL (schedule package missing; `VersionDoc` has no `Schedule` field).

- [ ] **Step 4: Implement**

`server/internal/schedule/schedule.go`:

```go
// Package schedule is the workflow's fourth versioned artifact: when the
// job runs. Strictly parsed (fail closed, like the permit), and evaluated
// in the schedule's own IANA zone.
package schedule

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/robfig/cron/v3"
)

// parser accepts exactly the standard 5 fields — no seconds, no
// @-descriptors. The parser choice is part of the API contract.
var parser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

type Schedule struct {
	Cron string `json:"cron"`
	TZ   string `json:"tz"`

	spec cron.Schedule
	loc  *time.Location
}

func Parse(raw []byte) (*Schedule, error) {
	var s Schedule
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("schedule: %w", err)
	}
	if err := dec.Decode(new(struct{})); err != io.EOF {
		return nil, fmt.Errorf("schedule: trailing data after document")
	}
	if s.Cron == "" || s.TZ == "" {
		return nil, fmt.Errorf("schedule: cron and tz are both required")
	}
	if s.TZ == "Local" {
		return nil, fmt.Errorf("schedule: tz must be an IANA zone name, not Local")
	}
	loc, err := time.LoadLocation(s.TZ)
	if err != nil {
		return nil, fmt.Errorf("schedule: tz: %w", err)
	}
	spec, err := parser.Parse(s.Cron)
	if err != nil {
		return nil, fmt.Errorf("schedule: cron: %w", err)
	}
	s.spec = spec
	s.loc = loc
	return &s, nil
}

// Next returns the first occurrence strictly after the given instant,
// evaluated in the schedule's zone.
func (s *Schedule) Next(after time.Time) time.Time {
	return s.spec.Next(after.In(s.loc))
}
```

`server/internal/store/workflow.go`:

- `VersionDoc` gains `Schedule json.RawMessage \`json:"schedule,omitempty"\``(after`Rubric`).
- `versionCols` becomes:

```go
const versionCols = `workflow_id, tenant_id, version, steps, permit, rubric,
	schedule, status, approved_by, approved_at, created_at`
```

- `scanVersion` scans `&v.Doc.Schedule` between `&v.Doc.Rubric` and `&v.Status`.
- Both INSERTs gain the column and a parameter:
  - `CreateWorkflow`: `(workflow_id, tenant_id, version, steps, permit, rubric, schedule) VALUES ($1, $2, 1, $3, $4, $5, $6)` with `doc.Schedule` as the sixth arg.
  - `AddVersion`: `(workflow_id, tenant_id, version, steps, permit, rubric, schedule) VALUES ($1, $2, (…), $3, $4, $5, $6)` with `doc.Schedule` appended.
    (A nil `json.RawMessage` maps to SQL NULL under pgx — manual-only workflows need no special casing.)

`server/internal/httpapi/workflows.go`:

- `versionDocJSON` and `versionJSON` gain `Schedule json.RawMessage \`json:"schedule,omitempty"\``.
- `toVersionJSON` passes `Schedule: v.Doc.Schedule`.
- Both `store.VersionDoc{...}` constructions add `Schedule: body.Schedule`.
- `decodeDoc`, after the priced-model check:

```go
	if body.Schedule != nil {
		if _, err := schedule.Parse(body.Schedule); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return body, false
		}
	}
```

(add import `"github.com/gambtho/nightwatch/server/internal/schedule"`).

- [ ] **Step 5: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server
git commit -m "feat(server): versioned schedule artifact with fail-closed cron/tz validation"
```

---

### Task 3: Run admission, occurrence idempotency, and system queries (store)

**Files:**

- Create: `server/internal/db/migrations/00008_scheduling_runs.sql`
- Modify: `server/internal/store/run.go`, `server/internal/store/store.go` (sentinels), `server/internal/store/tenant.go` (cap column), every `CreateRun` call site (listed in Step 4)
- Test: `server/internal/store/run_test.go` (extend), `server/internal/internalapi/internalapi_test.go` (fixture fix)

**Interfaces:**

- Consumes: Tasks 1-2 merged state.
- Produces:

```go
var ErrAlreadyFired = errors.New("store: occurrence already fired")
var ErrActiveRun = errors.New("store: a run is already active for this workflow")
// Run gains: FireReason string; FireTime, DispatchedAt *time.Time
// Tenant gains: MonthlyCapCents *int
func (s *Store) CreateRun(ctx, tenantID, workflowID, id uuid.UUID, version int, tokenHash, fireReason string, fireTime *time.Time) (Run, error)
func (s *Store) MarkRunDispatched(ctx, tenantID, id uuid.UUID) error            // sets dispatched_at where NULL
func (s *Store) ResetRunToken(ctx, tenantID, id uuid.UUID, newHash string) error // pending + undispatched only
// System query: the scheduler is a platform actor — cross-tenant by design.
func (s *Store) ListUndispatchedScheduledRuns(ctx context.Context) ([]Run, error)
// System query: the scheduler is a platform actor — cross-tenant by design.
func (s *Store) ListSchedulableWorkflows(ctx context.Context) ([]SchedulableWorkflow, error)
// System query: the reaper is a platform actor — cross-tenant by design.
func (s *Store) ListStuckRuns(ctx context.Context, cutoff time.Time) ([]Run, error)
type SchedulableWorkflow struct {
	TenantID   uuid.UUID
	WorkflowID uuid.UUID
	Version    int
	Schedule   json.RawMessage
}
func (s *Store) MonthSpendCents(ctx context.Context, tenantID uuid.UUID, monthStart time.Time) (int, error)
```

`ResetRunToken` exists because redispatch after a crash needs a bearer and only the hash is stored — an undispatched pending run has no token holder, so re-signing is safe by construction.

- [ ] **Step 1: Write the migration**

`server/internal/db/migrations/00008_scheduling_runs.sql`:

```sql
-- +goose Up
ALTER TABLE run ADD COLUMN fire_time timestamptz;
ALTER TABLE run ADD COLUMN dispatched_at timestamptz;
ALTER TABLE tenant ADD COLUMN monthly_cap_cents int;

-- Idempotent occurrences: one run per (workflow, scheduled instant).
CREATE UNIQUE INDEX run_workflow_firetime_unique
    ON run (workflow_id, fire_time) WHERE fire_time IS NOT NULL;

-- One active run per workflow, DB-enforced: the platform spec's "default
-- to serialize" as an admission rule, racing the index instead of a
-- check-then-fire. Applies to manual and scheduled fires alike.
CREATE UNIQUE INDEX run_one_active_per_workflow
    ON run (workflow_id) WHERE status IN ('pending', 'running');

ALTER TABLE run ADD CONSTRAINT run_fire_reason_time_consistent
    CHECK ((fire_reason = 'schedule') = (fire_time IS NOT NULL));

-- Month-to-date spend is a per-request check at the proxy hook: keep it an
-- index range scan, never a tenant-wide table scan.
CREATE INDEX run_tenant_spend_idx
    ON run (tenant_id, finished_at) WHERE cost_cents IS NOT NULL;

-- +goose Down
DROP INDEX run_tenant_spend_idx;
ALTER TABLE run DROP CONSTRAINT run_fire_reason_time_consistent;
DROP INDEX run_one_active_per_workflow;
DROP INDEX run_workflow_firetime_unique;
ALTER TABLE tenant DROP COLUMN monthly_cap_cents;
ALTER TABLE run DROP COLUMN dispatched_at;
ALTER TABLE run DROP COLUMN fire_time;
```

- [ ] **Step 2: Write the failing tests**

Append to `server/internal/store/run_test.go`:

```go
func TestRunAdmissionOneActivePerWorkflow(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, wf := setupApproved(t, s)

	_, err := s.CreateRun(ctx, tn.ID, wf.ID, uuid.New(), 1, "h1", "manual", nil)
	require.NoError(t, err)

	// Second active run on the same workflow loses on the admission index.
	_, err = s.CreateRun(ctx, tn.ID, wf.ID, uuid.New(), 1, "h2", "manual", nil)
	require.ErrorIs(t, err, store.ErrActiveRun)

	// Finalizing the first frees admission.
	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	_, err = s.FinalizeRun(ctx, tn.ID, runs[0].ID, store.RunFinal{Status: "failed"}, 0)
	require.NoError(t, err)
	_, err = s.CreateRun(ctx, tn.ID, wf.ID, uuid.New(), 1, "h3", "manual", nil)
	require.NoError(t, err)
}

func TestRunOccurrenceIdempotency(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, wf := setupApproved(t, s)

	fire := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	run, err := s.CreateRun(ctx, tn.ID, wf.ID, uuid.New(), 1, "h1", "schedule", &fire)
	require.NoError(t, err)
	require.Equal(t, "schedule", run.FireReason)
	require.NotNil(t, run.FireTime)
	require.Nil(t, run.DispatchedAt)

	// Finalize so the active index doesn't mask the occurrence index.
	_, err = s.FinalizeRun(ctx, tn.ID, run.ID, store.RunFinal{Status: "failed"}, 0)
	require.NoError(t, err)
	_, err = s.CreateRun(ctx, tn.ID, wf.ID, uuid.New(), 1, "h2", "schedule", &fire)
	require.ErrorIs(t, err, store.ErrAlreadyFired)
}

func TestDispatchAndTokenReset(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, wf := setupApproved(t, s)

	fire := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	run, err := s.CreateRun(ctx, tn.ID, wf.ID, uuid.New(), 1, "h1", "schedule", &fire)
	require.NoError(t, err)

	undispatched, err := s.ListUndispatchedScheduledRuns(ctx)
	require.NoError(t, err)
	require.Len(t, undispatched, 1)

	require.NoError(t, s.ResetRunToken(ctx, tn.ID, run.ID, "h1-reset"))
	got, err := s.GetRun(ctx, tn.ID, run.ID)
	require.NoError(t, err)
	require.Equal(t, "h1-reset", got.TokenHash)

	require.NoError(t, s.MarkRunDispatched(ctx, tn.ID, run.ID))
	undispatched, err = s.ListUndispatchedScheduledRuns(ctx)
	require.NoError(t, err)
	require.Empty(t, undispatched)

	// Dispatched runs are no longer token-resettable.
	require.ErrorIs(t, s.ResetRunToken(ctx, tn.ID, run.ID, "h1-again"), store.ErrNotFound)
}
```

**Fixture fix (required by the admission index):** in `server/internal/internalapi/internalapi_test.go`, `TestInternalAPIAuth` mints two _active_ runs on the same workflow, which the index now forbids. Change `mintRun` to accept the workflow (`mintRun(t, s, signer, tn, wf)` already does) and have `TestInternalAPIAuth` create a **second workflow** for its second run:

```go
	ctx2 := context.Background()
	user2, err := s.UpsertUser(ctx2, tn.ID, "second@acme.test")
	require.NoError(t, err)
	wf2, _, err := s.CreateWorkflow(ctx2, tn.ID, "second workflow", store.VersionDoc{
		Steps: store.StepsDoc{
			SystemPrompt: "You prepare the weekly support digest.",
			Kickoff:      "Summarize last week's tickets.",
			Provider:     "anthropic",
			Model:        "claude-sonnet-5",
			MaxTokens:    2048,
		},
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx2, tn.ID, wf2.ID, 1, user2.ID)
	require.NoError(t, err)
	otherRunID, otherBearer := mintRun(t, s, signer, tn, wf2)
```

All other `CreateRun` test call sites gain `, "manual", nil` (or `"schedule", &fire` where shown).

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd server && go test ./internal/store/`
Expected: FAIL (compile error — `CreateRun` signature, sentinels undefined).

- [ ] **Step 4: Implement**

`server/internal/store/store.go` — add sentinels below `ErrNotFound`:

```go
// ErrAlreadyFired: a run for this (workflow, fire_time) occurrence exists —
// idempotent-tick collisions map here and are treated as success by callers.
var ErrAlreadyFired = errors.New("store: occurrence already fired")

// ErrActiveRun: the one-active-run-per-workflow admission index rejected the
// insert. Manual fires surface this as 409; scheduled fires skip.
var ErrActiveRun = errors.New("store: a run is already active for this workflow")
```

`server/internal/store/run.go`:

- `Run` gains `FireReason string`, `FireTime *time.Time`, `DispatchedAt *time.Time`.
- `runCols` becomes:

```go
const runCols = `id, tenant_id, workflow_id, version, status, fire_reason,
	fire_time, dispatched_at, started_at, finished_at, tokens_in, tokens_out,
	cost_cents, error_kind, error_msg, output,
	COALESCE(runner_token_hash, ''), created_at`
```

- `scanRun` scans `&r.FireReason, &r.FireTime, &r.DispatchedAt` between `&r.Status` and `&r.StartedAt`.
- `CreateRun`:

```go
func (s *Store) CreateRun(ctx context.Context, tenantID, workflowID, id uuid.UUID, version int, tokenHash, fireReason string, fireTime *time.Time) (Run, error) {
	run, err := scanRun(s.pool.QueryRow(ctx,
		`INSERT INTO run (id, tenant_id, workflow_id, version, runner_token_hash, fire_reason, fire_time)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+runCols,
		id, tenantID, workflowID, version, tokenHash, fireReason, fireTime))
	return run, admissionErr(err)
}

// admissionErr maps the two coordination indexes' unique violations to
// their sentinels; everything else passes through.
func admissionErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "run_one_active_per_workflow":
			return ErrActiveRun
		case "run_workflow_firetime_unique":
			return ErrAlreadyFired
		}
	}
	return err
}
```

(imports gain `"errors"` and `"github.com/jackc/pgx/v5/pgconn"`).

- New methods:

```go
func (s *Store) MarkRunDispatched(ctx context.Context, tenantID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE run SET dispatched_at = now()
		 WHERE id = $1 AND tenant_id = $2 AND dispatched_at IS NULL`,
		id, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetRunToken replaces the stored token hash for a run that was created
// but never dispatched — the redispatch path must mint a fresh bearer
// (only the hash is persisted), and an undispatched pending run has no
// token holder, so the swap is safe by construction.
func (s *Store) ResetRunToken(ctx context.Context, tenantID, id uuid.UUID, newHash string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE run SET runner_token_hash = $3
		 WHERE id = $1 AND tenant_id = $2
		   AND status = 'pending' AND dispatched_at IS NULL`,
		id, tenantID, newHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// System query: the scheduler is a platform actor sweeping all tenants for
// crash-recovery redispatch; rows carry their TenantID.
func (s *Store) ListUndispatchedScheduledRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+runCols+` FROM run
		 WHERE status = 'pending' AND fire_reason = 'schedule'
		   AND dispatched_at IS NULL
		 ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// System query: the reaper is a platform actor; rows carry their TenantID.
func (s *Store) ListStuckRuns(ctx context.Context, cutoff time.Time) ([]Run, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+runCols+` FROM run
		 WHERE status IN ('pending', 'running') AND created_at < $1
		 ORDER BY created_at`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MonthSpendCents(ctx context.Context, tenantID uuid.UUID, monthStart time.Time) (int, error) {
	var cents int
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(cost_cents), 0) FROM run
		 WHERE tenant_id = $1 AND finished_at >= $2 AND cost_cents IS NOT NULL`,
		tenantID, monthStart).Scan(&cents)
	return cents, err
}
```

- `FinalizeRun` gains the cap parameter **in this task as a signature-only change** (`perRunCapCents int`, unused beyond compilation — Task 5 implements the transactional event; keeping the signature change here means every call site is touched exactly once). All callers pass `0` for now: `internalapi.finalize`, `httpapi.failDispatch`, and every test call site (the compiler enumerates them).
- `ListSchedulableWorkflows` goes in `workflow.go`:

```go
type SchedulableWorkflow struct {
	TenantID   uuid.UUID
	WorkflowID uuid.UUID
	Version    int
	Schedule   json.RawMessage
}

// System query: the scheduler is a platform actor scanning every tenant's
// approved, scheduled versions; rows carry their TenantID.
func (s *Store) ListSchedulableWorkflows(ctx context.Context) ([]SchedulableWorkflow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tenant_id, workflow_id, version, schedule FROM workflow_version
		 WHERE status = 'approved' AND schedule IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SchedulableWorkflow
	for rows.Next() {
		var w SchedulableWorkflow
		if err := rows.Scan(&w.TenantID, &w.WorkflowID, &w.Version, &w.Schedule); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
```

- `tenant.go`: `Tenant` gains `MonthlyCapCents *int`; `CreateTenant`'s RETURNING and `GetTenant`'s SELECT gain `monthly_cap_cents` (scan into the new field); add:

```go
func (s *Store) SetTenantMonthlyCap(ctx context.Context, tenantID uuid.UUID, capCents *int) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE tenant SET monthly_cap_cents = $2 WHERE id = $1`,
		tenantID, capCents)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
```

**CreateRun call sites to update** (`, "manual", nil` unless noted): `server/internal/httpapi/runs.go` (fireRun), `server/internal/store/run_test.go` (existing lifecycle/cross-tenant tests), `server/internal/internalapi/internalapi_test.go` (mintRun + the second-workflow fixture fix above), `server/internal/proxyadapter/adapter_test.go` (mintRun). **FinalizeRun call sites** gain `, 0`: internalapi.go, runs.go failDispatch, run_test.go, adapter_test.go (grep confirms the full list; the compiler enforces it).

- [ ] **Step 5: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS, including the internalapi fixture fix and proxyadapter tests.

- [ ] **Step 6: Commit**

```bash
git add server
git commit -m "feat(server): run admission and occurrence indexes, dispatch tracking, system queries, tenant cap column"
```

---

### Task 4: Permit `spend` extension

**Files:**

- Modify: `server/internal/permit/permit.go`
- Test: `server/internal/permit/permit_test.go` (extend)

**Interfaces:**

- Consumes: existing permit v1.
- Produces: `Permit.Spend *Spend` with `type Spend struct { PerRunCents int \`json:"per_run_cents"\` }`; absent = no per-run cap; present requires `PerRunCents > 0`.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/permit/permit_test.go`:

```go
func TestParseSpend(t *testing.T) {
	p, err := permit.Parse([]byte(`{"v":1,"llm":{"providers":["anthropic"]},"spend":{"per_run_cents":50}}`))
	require.NoError(t, err)
	require.NotNil(t, p.Spend)
	require.Equal(t, 50, p.Spend.PerRunCents)

	p, err = permit.Parse(permit.Empty)
	require.NoError(t, err)
	require.Nil(t, p.Spend)

	for name, raw := range map[string]string{
		"zero cap":     `{"v":1,"spend":{"per_run_cents":0}}`,
		"negative cap": `{"v":1,"spend":{"per_run_cents":-5}}`,
		"unknown key":  `{"v":1,"spend":{"per_run_dollars":1}}`,
	} {
		_, err := permit.Parse([]byte(raw))
		require.Error(t, err, name)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/permit/`
Expected: FAIL (`Spend` undefined; unknown-field rejection currently rejects the valid case too).

- [ ] **Step 3: Implement**

In `server/internal/permit/permit.go`: add

```go
// Spend is the approved per-run budget — the number the blast-radius
// diagram shows. Detection is transactional at finalization (see the
// scheduling+metering spec); pre-request enforcement arrives with
// multi-turn runs.
type Spend struct {
	PerRunCents int `json:"per_run_cents"`
}
```

`Permit` gains `Spend *Spend \`json:"spend,omitempty"\``(between`LLM`and`Connections`), and `Parse` gains, after the providers loop:

```go
	if p.Spend != nil && p.Spend.PerRunCents <= 0 {
		return Permit{}, fmt.Errorf("permit: spend.per_run_cents must be positive")
	}
```

(the package doc comment's "v1 governs LLM provider egress only" sentence gets "and the approved spend caps" appended).

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): permit v1 spend extension — approved per-run cap"
```

---

### Task 5: Transactional overrun events and run-provenance exposure

**Files:**

- Modify: `server/internal/store/run.go` (FinalizeRun body), `server/internal/internalapi/internalapi.go` (finalize resolves the cap), `server/internal/httpapi/runs.go` (runJSON exposure)
- Test: `server/internal/store/run_test.go`, `server/internal/internalapi/internalapi_test.go` (extend)

**Interfaces:**

- Consumes: Task 3's `FinalizeRun(..., perRunCapCents int)` signature, Task 4's `permit.Spend`.
- Produces: when `perRunCapCents > 0 && fin.CostCents > perRunCapCents`, `FinalizeRun` inserts a `spend.exceeded` run event **in the same transaction** as the terminal update (event first, while the run is still non-terminal — a lost finalize race writes no false event because the whole tx rolls back when the status UPDATE matches no row). `runJSON` gains `fire_reason` / `fire_time`.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/store/run_test.go`:

```go
func TestFinalizeRunSpendExceededEvent(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, wf := setupApproved(t, s)

	runID := uuid.New()
	_, err := s.CreateRun(ctx, tn.ID, wf.ID, runID, 1, "h", "manual", nil)
	require.NoError(t, err)

	// Over cap: event and terminal status land atomically.
	_, err = s.FinalizeRun(ctx, tn.ID, runID, store.RunFinal{
		Status: "succeeded", CostCents: 75,
	}, 50)
	require.NoError(t, err)
	events, err := s.ListRunEvents(ctx, tn.ID, runID)
	require.NoError(t, err)
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	require.Contains(t, types, "spend.exceeded")

	// A losing second finalize writes nothing — no duplicate event.
	_, err = s.FinalizeRun(ctx, tn.ID, runID, store.RunFinal{Status: "failed", CostCents: 75}, 50)
	require.ErrorIs(t, err, store.ErrNotFound)
	events, err = s.ListRunEvents(ctx, tn.ID, runID)
	require.NoError(t, err)
	require.Len(t, events, 1)

	// Under cap: no event.
	run2 := uuid.New()
	_, err = s.CreateRun(ctx, tn.ID, wf.ID, run2, 1, "h2", "manual", nil)
	require.NoError(t, err)
	_, err = s.FinalizeRun(ctx, tn.ID, run2, store.RunFinal{Status: "succeeded", CostCents: 10}, 50)
	require.NoError(t, err)
	events, err = s.ListRunEvents(ctx, tn.ID, run2)
	require.NoError(t, err)
	require.Empty(t, events)
}
```

Append to `server/internal/internalapi/internalapi_test.go` (the setup's permit must carry a cap for this test — create a dedicated workflow):

```go
func TestFinalizeResolvesPerRunCap(t *testing.T) {
	s, signer, ts, tn, wf := setup(t)
	_ = wf
	ctx := context.Background()
	user, err := s.UpsertUser(ctx, tn.ID, "cap@acme.test")
	require.NoError(t, err)
	capped, _, err := s.CreateWorkflow(ctx, tn.ID, "capped", store.VersionDoc{
		Steps: store.StepsDoc{SystemPrompt: "x", Kickoff: "y", Provider: "anthropic", Model: "claude-sonnet-5", MaxTokens: 100},
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"spend":{"per_run_cents":5},"connections":{}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, capped.ID, 1, user.ID)
	require.NoError(t, err)
	runID, bearer := mintRun(t, s, signer, tn, capped)

	client := harness.NewClient(ts.URL, runID, bearer)
	_, err = client.Context(ctx)
	require.NoError(t, err)
	require.NoError(t, client.Finalize(ctx, harness.Result{
		Status: harness.StatusSucceeded, Output: "big",
		Usage: llm.Usage{InputTokens: 100000, OutputTokens: 50000}, CostCents: 105,
	}))

	events, err := s.ListRunEvents(ctx, tn.ID, runID)
	require.NoError(t, err)
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	require.Contains(t, types, "spend.exceeded")
}
```

(`mintRun` already takes the workflow as a parameter. Add imports `harness` / `llm` if the file lacks them — it already has both for the round-trip test.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/store/ -run TestFinalizeRunSpendExceededEvent && go test ./internal/internalapi/`
Expected: FAIL — FinalizeRun ignores the cap; finalize handler passes 0.

- [ ] **Step 3: Implement**

`server/internal/store/run.go` — `FinalizeRun` becomes transactional:

```go
// FinalizeRun sets the terminal status, clears the token hash (revocation),
// and — when the finalized cost exceeds the approved per-run cap — records
// the spend.exceeded event in the SAME transaction: the event insert happens
// while the run is still non-terminal (satisfying the immutability guard),
// and a finalize that loses the terminal-transition race rolls the event
// back with it.
func (s *Store) FinalizeRun(ctx context.Context, tenantID, id uuid.UUID, fin RunFinal, perRunCapCents int) (Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if perRunCapCents > 0 && fin.CostCents > perRunCapCents {
		if _, err := tx.Exec(ctx,
			`INSERT INTO run_event (run_id, tenant_id, type, payload)
			 SELECT $1, $2, 'spend.exceeded', $3
			 WHERE EXISTS (SELECT 1 FROM run WHERE id = $1 AND tenant_id = $2
			               AND status IN ('pending', 'running'))`,
			id, tenantID,
			[]byte(fmt.Sprintf(`{"cost_cents":%d,"per_run_cap_cents":%d}`, fin.CostCents, perRunCapCents)),
		); err != nil {
			return Run{}, err
		}
	}

	run, err := scanRun(tx.QueryRow(ctx,
		`UPDATE run SET status = $3, finished_at = now(),
		        tokens_in = $4, tokens_out = $5, cost_cents = $6,
		        error_kind = NULLIF($7, ''), error_msg = NULLIF($8, ''),
		        output = $9,
		        runner_token_hash = NULL
		 WHERE id = $1 AND tenant_id = $2
		   AND status IN ('pending', 'running')
		 RETURNING `+runCols,
		id, tenantID, fin.Status, fin.TokensIn, fin.TokensOut, fin.CostCents,
		fin.ErrorKind, fin.ErrorMsg, fin.Output))
	if err != nil {
		return Run{}, err
	}
	return run, tx.Commit(ctx)
}
```

(import `"fmt"`; when the UPDATE matches no row, `scanRun` yields `ErrNotFound` and the deferred rollback discards any event insert — the atomicity the test asserts.)

`server/internal/internalapi/internalapi.go` — in `finalize`, before calling `FinalizeRun`, resolve the cap:

```go
	perRunCap := 0
	if run, err := d.Store.GetRun(r.Context(), claims.TenantID, claims.RunID); err == nil {
		if version, err := d.Store.GetVersion(r.Context(), claims.TenantID, run.WorkflowID, run.Version); err == nil {
			if p, err := permit.Parse(version.Doc.Permit); err == nil && p.Spend != nil {
				perRunCap = p.Spend.PerRunCents
			}
		}
	}
	// Cap resolution is best-effort: permits were validated at creation, so
	// a parse failure here means corrupt data — finalization must still
	// succeed (with no overrun event) rather than strand the run.
```

then pass `perRunCap` as the new argument (import the `permit` package).

`server/internal/httpapi/runs.go` — `runJSON` gains

```go
	FireReason string     `json:"fire_reason"`
	FireTime   *time.Time `json:"fire_time,omitempty"`
```

and `toRunJSON` maps them (`FireReason: r.FireReason, FireTime: r.FireTime`).

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): transactional spend.exceeded events and run provenance in the API"
```

---

### Task 6: The meter

**Files:**

- Create: `server/internal/meter/meter.go`
- Test: `server/internal/meter/meter_test.go`

**Interfaces:**

- Consumes: `store` (MonthSpendCents, GetTenant), `proxy` (Hook, HookRequest, HookError).
- Produces:

```go
package meter
type Meter struct {
	Store      *store.Store
	DefaultCap int // cents per month; 0 = unlimited
	Now        func() time.Time // nil = time.Now
}
func MonthStartUTC(now time.Time) time.Time
func (m *Meter) CapCents(ctx context.Context, tenantID uuid.UUID) (int, error) // tenant override, else DefaultCap
func (m *Meter) OverCap(ctx context.Context, tenantID uuid.UUID) (bool, error)
func (m *Meter) Before(ctx context.Context, req proxy.HookRequest) error // proxy.Hook: over cap -> HookError{429}; store error -> HookError{403} (fail closed)
```

- [ ] **Step 1: Write the failing tests**

`server/internal/meter/meter_test.go`:

```go
package meter_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/meter"
	"github.com/gambtho/nightwatch/server/internal/proxy"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

var testKEK = []byte("test-wrapped-kek")

func TestMeterMonthBoundaryAndCaps(t *testing.T) {
	pool := testpg.New(t)
	s := store.New(pool)
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "wf", store.VersionDoc{
		Steps:  store.StepsDoc{SystemPrompt: "x", Kickoff: "y", Provider: "anthropic", Model: "claude-sonnet-5", MaxTokens: 10},
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID)
	require.NoError(t, err)

	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	// One failed-but-costed run this month (counts), one run last month
	// (does not count). Create+finalize each, then move finished_at.
	mkRun := func(cost int, finishedAt time.Time, status string) {
		id := uuid.New()
		_, err := s.CreateRun(ctx, tn.ID, wf.ID, id, 1, "h", "manual", nil)
		require.NoError(t, err)
		_, err = s.FinalizeRun(ctx, tn.ID, id, store.RunFinal{Status: status, CostCents: cost}, 0)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `UPDATE run SET finished_at = $2 WHERE id = $1`, id, finishedAt)
		require.NoError(t, err)
	}
	mkRun(30, now.Add(-24*time.Hour), "failed")             // this month, failed: counts
	mkRun(100, now.AddDate(0, -1, 0), "succeeded")          // last month: excluded

	m := &meter.Meter{Store: s, DefaultCap: 50, Now: func() time.Time { return now }}

	over, err := m.OverCap(ctx, tn.ID)
	require.NoError(t, err)
	require.False(t, over) // 30 < 50

	mkRun(25, now.Add(-time.Hour), "succeeded") // 55 total this month
	over, err = m.OverCap(ctx, tn.ID)
	require.NoError(t, err)
	require.True(t, over)

	// Hook contract: 429 with the typed error.
	err = m.Before(ctx, proxy.HookRequest{Identity: proxy.RunIdentity{TenantID: tn.ID}, Provider: "anthropic"})
	var he proxy.HookError
	require.ErrorAs(t, err, &he)
	require.Equal(t, http.StatusTooManyRequests, he.Status)

	// Tenant override beats the default.
	bigCap := 1000
	require.NoError(t, s.SetTenantMonthlyCap(ctx, tn.ID, &bigCap))
	require.NoError(t, m.Before(ctx, proxy.HookRequest{Identity: proxy.RunIdentity{TenantID: tn.ID}, Provider: "anthropic"}))

	// Zero cap = unlimited.
	zero := 0
	require.NoError(t, s.SetTenantMonthlyCap(ctx, tn.ID, &zero))
	require.NoError(t, m.Before(ctx, proxy.HookRequest{Identity: proxy.RunIdentity{TenantID: tn.ID}, Provider: "anthropic"}))
}

func TestMeterFailsClosed(t *testing.T) {
	pool := testpg.New(t)
	s := store.New(pool)
	m := &meter.Meter{Store: s, DefaultCap: 50}
	// Unknown tenant: GetTenant errors -> deny with 403-class HookError.
	err := m.Before(context.Background(), proxy.HookRequest{Identity: proxy.RunIdentity{TenantID: uuid.New()}})
	var he proxy.HookError
	require.ErrorAs(t, err, &he)
	require.Equal(t, http.StatusForbidden, he.Status)
}
```

(`mkRun` inside the test is the helper; moving `finished_at` by direct SQL is deliberate — it is the column month membership keys on.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/meter/`
Expected: FAIL (package missing).

- [ ] **Step 3: Implement**

`server/internal/meter/meter.go`:

```go
// Package meter enforces spend caps. It implements proxy.Hook, so the
// monthly tenant cap is checked before every model request at the egress
// proxy — the enforcement point the platform spec mandates. Fail closed:
// if spend cannot be read, the request is denied.
package meter

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/proxy"
	"github.com/gambtho/nightwatch/server/internal/store"
)

type Meter struct {
	Store      *store.Store
	DefaultCap int // cents per calendar month (UTC); 0 = unlimited
	Now        func() time.Time
}

func (m *Meter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func MonthStartUTC(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// CapCents returns the tenant's monthly cap: its own override when set,
// else the platform default. 0 means unlimited.
func (m *Meter) CapCents(ctx context.Context, tenantID uuid.UUID) (int, error) {
	tn, err := m.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	if tn.MonthlyCapCents != nil {
		return *tn.MonthlyCapCents, nil
	}
	return m.DefaultCap, nil
}

func (m *Meter) OverCap(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	cap, err := m.CapCents(ctx, tenantID)
	if err != nil {
		return false, err
	}
	if cap <= 0 {
		return false, nil
	}
	spent, err := m.Store.MonthSpendCents(ctx, tenantID, MonthStartUTC(m.now()))
	if err != nil {
		return false, err
	}
	return spent >= cap, nil
}

// Before implements proxy.Hook.
func (m *Meter) Before(ctx context.Context, req proxy.HookRequest) error {
	over, err := m.OverCap(ctx, req.Identity.TenantID)
	if err != nil {
		// No spend visibility, no spend.
		return proxy.HookError{Status: http.StatusForbidden, Msg: "metering unavailable"}
	}
	if over {
		return proxy.HookError{Status: http.StatusTooManyRequests, Msg: "monthly spend cap reached"}
	}
	return nil
}
```

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): spend meter — monthly UTC tenant cap as the proxy hook, fail closed"
```

---

### Task 7: `engine.Fire` — the single firing path

**Files:**

- Create: `server/internal/engine/engine.go`
- Modify: `server/internal/httpapi/httpapi.go` (Deps: `Signer`/`Compute` replaced by `Engine`), `server/internal/httpapi/runs.go` (fireRun via engine; 409; failDispatch moves to engine), `server/internal/httpapi/workflows_test.go` + `runs_test.go` (env builds an Engine over fakeCompute), `server/e2e_test.go` (Deps literals)
- Test: `server/internal/engine/engine_test.go`, `server/internal/httpapi/runs_test.go` (409 test)

**Interfaces:**

- Consumes: store (CreateRun/MarkRunDispatched/FinalizeRun/sentinels), token, compute.
- Produces:

```go
package engine
type Engine struct {
	Store    *store.Store
	Signer   *token.Signer
	Compute  compute.Compute
	TokenTTL time.Duration // 0 = time.Hour
	Now      func() time.Time
}
// Fire signs a run token, creates the run row (racing the admission and
// occurrence indexes), and dispatches it. Sentinels pass through:
// store.ErrActiveRun / store.ErrAlreadyFired mean "not fired", not failure.
func (e *Engine) Fire(ctx context.Context, tenantID, workflowID uuid.UUID, version int, fireReason string, fireTime *time.Time) (store.Run, error)
// Redispatch re-signs a token for a created-but-never-dispatched run (the
// crash-recovery path) and dispatches it.
func (e *Engine) Redispatch(ctx context.Context, run store.Run) error
```

`httpapi.Deps` becomes `{Store, SessionKey, Engine *engine.Engine, Vault}` — `Signer` and `Compute` were only used by `fireRun`. `internalapi.Deps` keeps its own `Signer` (unchanged).

- [ ] **Step 1: Write the failing tests**

`server/internal/engine/engine_test.go` (fakeCompute mirrors httpapi's — small local copy since they live in different test packages):

```go
package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/compute"
	"github.com/gambtho/nightwatch/server/internal/engine"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
	"github.com/gambtho/nightwatch/server/internal/token"
)

var testKEK = []byte("test-wrapped-kek")

type fakeCompute struct {
	mu        sync.Mutex
	invokes   []compute.InvokeRequest
	invokeErr error
}

func (f *fakeCompute) EnsureActor(ctx context.Context, w compute.WorkflowRef, tmpl compute.TemplateRef) (compute.ActorID, error) {
	return compute.ActorID(w.WorkflowID.String()), nil
}
func (f *fakeCompute) Invoke(ctx context.Context, a compute.ActorID, req compute.InvokeRequest) (compute.Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.invokeErr != nil {
		return compute.Handle{}, f.invokeErr
	}
	f.invokes = append(f.invokes, req)
	return compute.Handle{ActorID: a, RunID: req.RunID}, nil
}
func (f *fakeCompute) Suspend(ctx context.Context, a compute.ActorID) error { return nil }
func (f *fakeCompute) Destroy(ctx context.Context, a compute.ActorID) error { return nil }

func setup(t *testing.T) (*store.Store, *engine.Engine, *fakeCompute, store.Tenant, store.Workflow) {
	t.Helper()
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "wf", store.VersionDoc{
		Steps:  store.StepsDoc{SystemPrompt: "x", Kickoff: "y", Provider: "anthropic", Model: "claude-sonnet-5", MaxTokens: 10},
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID)
	require.NoError(t, err)
	fc := &fakeCompute{}
	eng := &engine.Engine{Store: s, Signer: token.New([]byte("0123456789abcdef0123456789abcdef")), Compute: fc}
	return s, eng, fc, tn, wf
}

func TestFireDispatchesAndMarks(t *testing.T) {
	s, eng, fc, tn, wf := setup(t)
	ctx := context.Background()

	run, err := eng.Fire(ctx, tn.ID, wf.ID, 1, "manual", nil)
	require.NoError(t, err)
	require.Len(t, fc.invokes, 1)
	require.NotEmpty(t, fc.invokes[0].RunToken)

	got, err := s.GetRun(ctx, tn.ID, run.ID)
	require.NoError(t, err)
	require.NotNil(t, got.DispatchedAt)

	// Admission: a second fire while active passes the sentinel through.
	_, err = eng.Fire(ctx, tn.ID, wf.ID, 1, "manual", nil)
	require.ErrorIs(t, err, store.ErrActiveRun)
}

func TestFireDispatchFailureFinalizes(t *testing.T) {
	s, eng, fc, tn, wf := setup(t)
	ctx := context.Background()
	fc.invokeErr = errors.New("no workers")

	_, err := eng.Fire(ctx, tn.ID, wf.ID, 1, "manual", nil)
	require.Error(t, err)
	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "failed", runs[0].Status)
	require.Equal(t, "dispatch_failed", *runs[0].ErrorKind)
}

func TestRedispatchResignsToken(t *testing.T) {
	s, eng, fc, tn, wf := setup(t)
	ctx := context.Background()

	// Simulate crash-before-dispatch: create the row directly, no Invoke.
	fire := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	created, err := s.CreateRun(ctx, tn.ID, wf.ID, uuid.New(), 1, "orphan-hash", "schedule", &fire)
	require.NoError(t, err)

	require.NoError(t, eng.Redispatch(ctx, created))
	require.Len(t, fc.invokes, 1)

	got, err := s.GetRun(ctx, tn.ID, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.DispatchedAt)
	require.NotEqual(t, "orphan-hash", got.TokenHash) // fresh token, fresh hash
	// The dispatched bearer verifies against the new stored hash.
	require.Equal(t, eng.Signer.HashToken(fc.invokes[0].RunToken), got.TokenHash)
}
```

Append to `server/internal/httpapi/runs_test.go`:

```go
func TestFireRunWhileActiveIs409(t *testing.T) {
	e := newEnv(t)
	resp, out := e.do(t, "POST", "/v1/workflows", workflowBody())
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)
	resp, _ = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/versions/1/approve", id), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/runs", id), nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	resp, out = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/runs", id), nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	require.Contains(t, out["error"], "already active")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/engine/ ./internal/httpapi/`
Expected: FAIL (engine package missing).

- [ ] **Step 3: Implement**

`server/internal/engine/engine.go`:

```go
// Package engine owns firing: the ONE path by which a run comes into
// existence and reaches an actor, shared by the HTTP API and the
// scheduler so the two can never drift. It also owns crash recovery
// (Redispatch) — the unique indexes give idempotent row creation; this
// package makes dispatch at-least-once on top of them.
package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/compute"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/token"
)

type Engine struct {
	Store    *store.Store
	Signer   *token.Signer
	Compute  compute.Compute
	TokenTTL time.Duration
	Now      func() time.Time
}

func (e *Engine) ttl() time.Duration {
	if e.TokenTTL > 0 {
		return e.TokenTTL
	}
	return time.Hour
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) Fire(ctx context.Context, tenantID, workflowID uuid.UUID, version int, fireReason string, fireTime *time.Time) (store.Run, error) {
	runID := uuid.New()
	bearer, hash, err := e.Signer.Sign(token.RunClaims{
		RunID: runID, TenantID: tenantID, ExpiresAt: e.now().Add(e.ttl()),
	})
	if err != nil {
		return store.Run{}, err
	}
	run, err := e.Store.CreateRun(ctx, tenantID, workflowID, runID, version, hash, fireReason, fireTime)
	if err != nil {
		return store.Run{}, err // sentinels (ErrActiveRun/ErrAlreadyFired) pass through
	}
	if err := e.dispatch(ctx, run, bearer); err != nil {
		return store.Run{}, err
	}
	return run, nil
}

// Redispatch recovers a run that was created but never dispatched (a crash
// between CreateRun and Invoke). The original bearer is gone — only its
// hash was stored — so a fresh token is signed and the stored hash swapped
// first; an undispatched pending run has no token holder, so the swap is
// safe by construction.
func (e *Engine) Redispatch(ctx context.Context, run store.Run) error {
	bearer, hash, err := e.Signer.Sign(token.RunClaims{
		RunID: run.ID, TenantID: run.TenantID, ExpiresAt: e.now().Add(e.ttl()),
	})
	if err != nil {
		return err
	}
	if err := e.Store.ResetRunToken(ctx, run.TenantID, run.ID, hash); err != nil {
		return err // e.g. someone else dispatched it between list and here
	}
	return e.dispatch(ctx, run, bearer)
}

func (e *Engine) dispatch(ctx context.Context, run store.Run, bearer string) error {
	actor, err := e.Compute.EnsureActor(ctx,
		compute.WorkflowRef{TenantID: run.TenantID, WorkflowID: run.WorkflowID},
		compute.TemplateRef{Name: "harness-v1"})
	if err != nil {
		e.failDispatch(ctx, run, err)
		return err
	}
	if _, err := e.Compute.Invoke(ctx, actor,
		compute.InvokeRequest{RunID: run.ID, RunToken: bearer}); err != nil {
		e.failDispatch(ctx, run, err)
		return err
	}
	if err := e.Store.MarkRunDispatched(ctx, run.TenantID, run.ID); err != nil {
		slog.Error("engine: mark dispatched", "run", run.ID, "err", err)
	}
	return nil
}

// failDispatch records a run that never reached its actor, on a
// cancel-free context so a client disconnect can't abort the write.
func (e *Engine) failDispatch(ctx context.Context, run store.Run, cause error) {
	ctx = context.WithoutCancel(ctx)
	if _, err := e.Store.FinalizeRun(ctx, run.TenantID, run.ID, store.RunFinal{
		Status: "failed", ErrorKind: "dispatch_failed", ErrorMsg: cause.Error(),
	}, 0); err != nil {
		slog.Error("engine: record dispatch failure", "run", run.ID, "err", err)
	}
}
```

`server/internal/httpapi/httpapi.go`: `Deps` drops `Signer`/`Compute`, gains `Engine *engine.Engine` (imports adjusted — `token` and `compute` leave if now unused in the package; `engine` arrives).

`server/internal/httpapi/runs.go`: `fireRun` keeps its approved-version resolution and 409-no-approved-version block, then becomes:

```go
	run, err := d.Engine.Fire(r.Context(), claims.TenantID, wfID, version.Number, "manual", nil)
	if errors.Is(err, store.ErrActiveRun) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a run is already active for this workflow"})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run": toRunJSON(run)})
```

Delete `failDispatch`, `runTokenTTL`, and the now-unused imports (`compute`, `token`, `context`, `slog` — whatever the compiler flags) from runs.go.

Test-env updates: `newEnv` in `workflows_test.go` builds `&engine.Engine{Store: s, Signer: signer, Compute: fc}` and passes `Engine:` in `Deps` (drop `Signer:`/`Compute:`); `runs_test.go`'s dispatch-failure test reaches `invokeErr` through `e.compute` unchanged. `server/e2e_test.go`: both tests' `httpapi.Deps` literals swap `Signer: signer, Compute: local` for `Engine: &engine.Engine{Store: s, Signer: signer, Compute: local}` (import `engine`).

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS (both e2e tests still green — same signer, same compute, one indirection).

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): engine.Fire — one firing path with admission, dispatch recovery hooks, and 409"
```

---

### Task 8: The scheduler

**Files:**

- Create: `server/internal/engine/scheduler.go`
- Test: `server/internal/engine/scheduler_test.go`

**Interfaces:**

- Consumes: `engine.Engine`, `store` system queries, `schedule.Parse/Next`, `meter.Meter` (via a small interface so tests can fake it).
- Produces:

```go
type CapChecker interface {
	OverCap(ctx context.Context, tenantID uuid.UUID) (bool, error)
}
type Scheduler struct {
	Engine   *Engine
	Store    *store.Store
	Caps     CapChecker    // nil = no cap checks
	Interval time.Duration // 0 = time.Minute
	Window   time.Duration // 0 = max(2*Interval, 5*time.Minute)
	Now      func() time.Time
}
func (s *Scheduler) Tick(ctx context.Context)            // one create+dispatch pass; never panics out
func (s *Scheduler) Run(ctx context.Context)             // ticker loop calling Tick until ctx ends
```

- [ ] **Step 1: Write the failing tests**

`server/internal/engine/scheduler_test.go` (reuses `setup`/`fakeCompute` from engine_test.go — same package):

```go
package engine_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/engine"
	"github.com/gambtho/nightwatch/server/internal/store"
)

func scheduledSetup(t *testing.T) (*store.Store, *engine.Engine, *fakeCompute, store.Tenant, store.Workflow) {
	t.Helper()
	s, eng, fc, tn, _ := setup(t)
	ctx := context.Background()
	user, err := s.UpsertUser(ctx, tn.ID, "sched@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "scheduled", store.VersionDoc{
		Steps:    store.StepsDoc{SystemPrompt: "x", Kickoff: "y", Provider: "anthropic", Model: "claude-sonnet-5", MaxTokens: 10},
		Permit:   []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric:   []byte(`{}`),
		Schedule: json.RawMessage(`{"cron":"0 9 * * *","tz":"UTC"}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID)
	require.NoError(t, err)
	return s, eng, fc, tn, wf
}

type fakeCaps struct{ over bool }

func (f fakeCaps) OverCap(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	return f.over, nil
}

func TestTickFiresDueOccurrenceOnce(t *testing.T) {
	s, eng, fc, tn, wf := scheduledSetup(t)
	ctx := context.Background()

	now := time.Date(2026, 9, 7, 9, 0, 30, 0, time.UTC) // 30s after 09:00 due
	sched := &engine.Scheduler{Engine: eng, Store: s, Now: func() time.Time { return now }}

	sched.Tick(ctx)
	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "schedule", runs[0].FireReason)
	require.Equal(t, time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC).Unix(), runs[0].FireTime.Unix())
	require.NotNil(t, runs[0].DispatchedAt)
	require.Len(t, fc.invokes, 1)

	// Same tick window again: occurrence and admission indexes both hold.
	sched.Tick(ctx)
	runs, err = s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
}

func TestTickStalenessWindowSkipsOldOccurrences(t *testing.T) {
	s, eng, _, tn, wf := scheduledSetup(t)
	ctx := context.Background()

	// Hours past the 09:00 occurrence: outside W, never fires.
	now := time.Date(2026, 9, 7, 15, 0, 0, 0, time.UTC)
	sched := &engine.Scheduler{Engine: eng, Store: s, Now: func() time.Time { return now }}
	sched.Tick(ctx)
	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Empty(t, runs)
}

func TestTickSkipsWhenCapped(t *testing.T) {
	s, eng, _, tn, wf := scheduledSetup(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 7, 9, 0, 30, 0, time.UTC)
	sched := &engine.Scheduler{Engine: eng, Store: s, Caps: fakeCaps{over: true}, Now: func() time.Time { return now }}
	sched.Tick(ctx)
	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Empty(t, runs)
}

func TestTickRedispatchesCrashedCreates(t *testing.T) {
	s, eng, fc, tn, wf := scheduledSetup(t)
	ctx := context.Background()

	// Crash between create and dispatch: row exists, never invoked.
	fire := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	_, err := s.CreateRun(ctx, tn.ID, wf.ID, uuid.New(), 1, "lost-hash", "schedule", &fire)
	require.NoError(t, err)

	now := fire.Add(90 * time.Second)
	sched := &engine.Scheduler{Engine: eng, Store: s, Now: func() time.Time { return now }}
	sched.Tick(ctx)

	require.Len(t, fc.invokes, 1) // redispatched, not re-created
	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.NotNil(t, runs[0].DispatchedAt)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/engine/`
Expected: FAIL (`Scheduler` undefined).

- [ ] **Step 3: Implement**

`server/internal/engine/scheduler.go`:

```go
package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/schedule"
	"github.com/gambtho/nightwatch/server/internal/store"
)

// CapChecker is the slice of the meter the scheduler needs: don't create
// runs that are doomed to a 429.
type CapChecker interface {
	OverCap(ctx context.Context, tenantID uuid.UUID) (bool, error)
}

type Scheduler struct {
	Engine   *Engine
	Store    *store.Store
	Caps     CapChecker
	Interval time.Duration
	Window   time.Duration
	Now      func() time.Time
}

func (s *Scheduler) interval() time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return time.Minute
}

func (s *Scheduler) window() time.Duration {
	if s.Window > 0 {
		return s.Window
	}
	if w := 2 * s.interval(); w > 5*time.Minute {
		return w
	}
	return 5 * time.Minute
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Run ticks until the context ends. Each tick is crash-isolated: a panic
// or error skips the tick, never the loop.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick(ctx)
		}
	}
}

// Tick is one create+dispatch pass. Create and dispatch are separate so a
// crash between them loses nothing: the next tick's dispatch step picks up
// created-but-undispatched rows (with a fresh token — see Redispatch).
func (s *Scheduler) Tick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("scheduler: tick panic", "panic", r)
		}
	}()
	s.createDue(ctx)
	s.dispatchPending(ctx)
}

// mostRecentDue returns the latest occurrence <= now, walking Next from
// the window's start — a pure function of (schedule, now): deterministic
// across restarts, no cursor.
func mostRecentDue(sch *schedule.Schedule, now time.Time, window time.Duration) (time.Time, bool) {
	cursor := now.Add(-window)
	var due time.Time
	found := false
	for {
		next := sch.Next(cursor)
		if next.After(now) {
			break
		}
		due, found = next, true
		cursor = next
	}
	return due, found
}

func (s *Scheduler) createDue(ctx context.Context) {
	workflows, err := s.Store.ListSchedulableWorkflows(ctx)
	if err != nil {
		slog.Error("scheduler: list schedulable", "err", err)
		return
	}
	now := s.now()
	for _, w := range workflows {
		sch, err := schedule.Parse(w.Schedule)
		if err != nil {
			// Validated at creation; a parse failure here is corrupt data.
			slog.Error("scheduler: unparseable schedule", "workflow", w.WorkflowID, "err", err)
			continue
		}
		due, ok := mostRecentDue(sch, now, s.window())
		if !ok {
			continue
		}
		if s.Caps != nil {
			over, err := s.Caps.OverCap(ctx, w.TenantID)
			if err != nil || over {
				slog.Info("scheduler: skip (cap)", "tenant", w.TenantID, "workflow", w.WorkflowID, "err", err)
				continue
			}
		}
		_, err = s.Engine.Fire(ctx, w.TenantID, w.WorkflowID, w.Version, "schedule", &due)
		switch {
		case err == nil:
		case errorsIsAny(err, store.ErrAlreadyFired, store.ErrActiveRun):
			// Occurrence already exists, or a run is active: both are skips.
		default:
			slog.Error("scheduler: fire", "workflow", w.WorkflowID, "err", err)
		}
	}
}

func (s *Scheduler) dispatchPending(ctx context.Context) {
	runs, err := s.Store.ListUndispatchedScheduledRuns(ctx)
	if err != nil {
		slog.Error("scheduler: list undispatched", "err", err)
		return
	}
	for _, run := range runs {
		if err := s.Engine.Redispatch(ctx, run); err != nil {
			slog.Error("scheduler: redispatch", "run", run.ID, "err", err)
		}
	}
}

func errorsIsAny(err error, targets ...error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}
```

(add `"errors"` to imports).

**Note on `TestTickFiresDueOccurrenceOnce`'s second Tick:** the freshly fired run is `pending` with `dispatched_at` set, so `dispatchPending` skips it and `createDue` loses on both indexes — the assertion holds without special-casing.
**Note on `TestTickRedispatchesCrashedCreates`:** `createDue` runs first and its `Fire` attempt loses on the admission index (the crashed row is still `pending`) — a skip; `dispatchPending` then recovers the row. Order matters and is deliberate.

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): scheduler tick — deterministic due computation, cap skips, crash-recovery redispatch"
```

---

### Task 9: The reaper

**Files:**

- Create: `server/internal/engine/reaper.go`
- Test: `server/internal/engine/reaper_test.go`

**Interfaces:**

- Consumes: `store.ListStuckRuns`, `store.FinalizeRun`.
- **Also modifies** (escalation-spec amendment 2, folded in pre-merge): `store.ListStuckRuns`'s WHERE becomes `status IN ('pending', 'running') AND COALESCE(dispatched_at, created_at) < $1` — the deadline keys off the latest **dispatch episode**, falling back to creation for never-dispatched rows. Update the method's doc comment accordingly ("the cutoff compares against the latest dispatch episode — a redispatched run's reap window tracks its freshest token; never-dispatched rows key off creation, per the merged escalation design's amendment 2"). Rationale: a redispatched run carries a freshly signed token, and the escalation design's `awaiting_input` resume path depends on exactly this keying — cheaper now than a retrofit.
- Produces:

```go
type Reaper struct {
	Store    *store.Store
	Deadline time.Duration // 0 = 2h
	Interval time.Duration // 0 = 5m
	Now      func() time.Time
}
func (r *Reaper) Sweep(ctx context.Context) int // finalizes stuck runs as failed/orphaned; returns count
func (r *Reaper) Run(ctx context.Context)       // ticker loop
func ValidateRunLifetimes(tokenTTL, deadline time.Duration) error // deadline must exceed TTL
```

- [ ] **Step 1: Write the failing tests**

`server/internal/engine/reaper_test.go` (same package `engine_test`; needs the raw pool to age a run):

```go
func TestReaperSweepsStuckRuns(t *testing.T) {
	pool := testpg.New(t)
	s := store.New(pool)
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "wf", store.VersionDoc{
		Steps:  store.StepsDoc{SystemPrompt: "x", Kickoff: "y", Provider: "anthropic", Model: "claude-sonnet-5", MaxTokens: 10},
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID)
	require.NoError(t, err)

	stuck := uuid.New()
	_, err = s.CreateRun(ctx, tn.ID, wf.ID, stuck, 1, "stuck-hash", "manual", nil)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE run SET created_at = now() - interval '3 hours' WHERE id = $1`, stuck)
	require.NoError(t, err)

	r := &engine.Reaper{Store: s, Deadline: 2 * time.Hour}
	require.Equal(t, 1, r.Sweep(ctx))

	got, err := s.GetRun(ctx, tn.ID, stuck)
	require.NoError(t, err)
	require.Equal(t, "failed", got.Status)
	require.Equal(t, "orphaned", *got.ErrorKind)
	require.Empty(t, got.TokenHash) // finalize cleared it: the token is dead everywhere

	// A younger run is untouched (admission now free after the reap).
	young := uuid.New()
	_, err = s.CreateRun(ctx, tn.ID, wf.ID, young, 1, "young-hash", "manual", nil)
	require.NoError(t, err)
	require.Equal(t, 0, r.Sweep(ctx))
	got, err = s.GetRun(ctx, tn.ID, young)
	require.NoError(t, err)
	require.Equal(t, "pending", got.Status)

	// Escalation-spec amendment 2: an old created_at with a FRESH dispatch
	// episode must NOT be reaped — the deadline tracks the latest dispatch.
	// (Reuse `young`: age its creation, stamp a recent dispatch.)
	_, err = pool.Exec(ctx,
		`UPDATE run SET created_at = now() - interval '3 days',
		        dispatched_at = now() - interval '10 minutes'
		 WHERE id = $1`, young)
	require.NoError(t, err)
	require.Equal(t, 0, r.Sweep(ctx))
	got, err = s.GetRun(ctx, tn.ID, young)
	require.NoError(t, err)
	require.Equal(t, "pending", got.Status)
}

func TestValidateRunLifetimes(t *testing.T) {
	require.NoError(t, engine.ValidateRunLifetimes(time.Hour, 2*time.Hour))
	require.Error(t, engine.ValidateRunLifetimes(time.Hour, time.Hour))
	require.Error(t, engine.ValidateRunLifetimes(2*time.Hour, time.Hour))
}
```

(imports as needed; `testpg`, `uuid`, `time` already used in the package's tests).

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/engine/ -run 'TestReaper|TestValidateRunLifetimes'`
Expected: FAIL (`Reaper` undefined).

- [ ] **Step 3: Implement**

`server/internal/engine/reaper.go`:

```go
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gambtho/nightwatch/server/internal/store"
)

// Reaper finalizes runs stuck in a non-terminal state past the deadline —
// a crashed server, a harness that never finalized, a dispatch that died.
// Finalization clears the token hash, so a zombie harness that wakes later
// is locked out of the proxy and the internal API alike.
type Reaper struct {
	Store    *store.Store
	Deadline time.Duration
	Interval time.Duration
	Now      func() time.Time
}

// ValidateRunLifetimes enforces deadline > tokenTTL at startup: a run whose
// token expired can never finalize itself, so reaping after expiry is
// guaranteed-safe and reaping before it would be premature.
func ValidateRunLifetimes(tokenTTL, deadline time.Duration) error {
	if deadline <= tokenTTL {
		return fmt.Errorf("run deadline (%s) must exceed run token TTL (%s)", deadline, tokenTTL)
	}
	return nil
}

func (r *Reaper) deadline() time.Duration {
	if r.Deadline > 0 {
		return r.Deadline
	}
	return 2 * time.Hour
}

func (r *Reaper) interval() time.Duration {
	if r.Interval > 0 {
		return r.Interval
	}
	return 5 * time.Minute
}

func (r *Reaper) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reaper) Run(ctx context.Context) {
	t := time.NewTicker(r.interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Sweep(ctx)
		}
	}
}

func (r *Reaper) Sweep(ctx context.Context) int {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("reaper: sweep panic", "panic", p)
		}
	}()
	stuck, err := r.Store.ListStuckRuns(ctx, r.now().Add(-r.deadline()))
	if err != nil {
		slog.Error("reaper: list stuck runs", "err", err)
		return 0
	}
	reaped := 0
	for _, run := range stuck {
		if _, err := r.Store.FinalizeRun(ctx, run.TenantID, run.ID, store.RunFinal{
			Status: "failed", ErrorKind: "orphaned",
			ErrorMsg: "run exceeded the platform deadline without finalizing",
		}, 0); err != nil {
			slog.Error("reaper: finalize", "run", run.ID, "err", err)
			continue
		}
		slog.Warn("reaper: orphaned run finalized", "run", run.ID, "tenant", run.TenantID)
		reaped++
	}
	return reaped
}
```

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): orphaned-run reaper with deadline>TTL invariant — roadmap decision 10 closed"
```

---

### Task 10: Wiring, scheduled-run e2e, docs

**Files:**

- Modify: `server/cmd/nightshift/main.go`, `server/e2e_test.go`, `docs/api/v1.md`, `server/README.md`, `docs/superpowers/plans/2026-08-30-nightshift-platform-roadmap.md` (decision-10 closure note)
- Test: `server/e2e_test.go` (new `TestEndToEndScheduledRun`)

- [ ] **Step 1: Wire serve()**

In `server/cmd/nightshift/main.go` `serve()`:

1. Env parsing (add a helper next to `keyFromEnv`):

```go
func durationFromEnv(name string, fallback time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		slog.Error("env var must be a positive Go duration", "name", name, "value", v)
		os.Exit(2)
	}
	return d
}

func intFromEnv(name string, fallback int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		slog.Error("env var must be a non-negative integer", "name", name, "value", v)
		os.Exit(2)
	}
	return n
}
```

2. After the store/signer/vault construction:

```go
	tokenTTL := durationFromEnv("NIGHTSHIFT_RUN_TOKEN_TTL", time.Hour)
	runDeadline := durationFromEnv("NIGHTSHIFT_RUN_DEADLINE", 2*time.Hour)
	if err := engine.ValidateRunLifetimes(tokenTTL, runDeadline); err != nil {
		return err
	}
	defaultCap := intFromEnv("NIGHTSHIFT_DEFAULT_MONTHLY_CAP_CENTS", 0)

	eng := &engine.Engine{Store: s, Signer: signer, Compute: local, TokenTTL: tokenTTL}
	m := &meter.Meter{Store: s, DefaultCap: defaultCap}
```

(`eng` construction moves BELOW `local := compute.NewLocal(...)`; the httpapi Deps literal becomes `{Store: s, SessionKey: sessionKey, Engine: eng, Vault: master}`.)

3. The proxy's Hook stops being a no-op: `Hook: m,` replaces `Hook: proxy.NopHook{},`.

4. Before `srv.ListenAndServe()`, start the loops on a servable context:

```go
	loopCtx, cancelLoops := context.WithCancel(context.Background())
	defer cancelLoops()
	sched := &engine.Scheduler{Engine: eng, Store: s, Caps: m}
	reaper := &engine.Reaper{Store: s, Deadline: runDeadline}
	go sched.Run(loopCtx)
	go reaper.Run(loopCtx)
```

5. Doc comment at the top of main.go gains the three new env vars with their defaults and the `deadline > TTL` rule. Imports gain `engine`, `meter`, `strconv`.

- [ ] **Step 2: Write the scheduled-run e2e test**

Append to `server/e2e_test.go` (reuses `newDoHelper` and the fake-upstream pattern from `TestEndToEndRunThroughProxy`; scripted-provider variant keeps it simple):

```go
// TestEndToEndScheduledRun proves a schedule fires a run with no HTTP fire
// call: workflow with a cron schedule -> Scheduler.Tick (injected clock) ->
// engine -> harness -> run record, visible via the public API with
// fire_reason "schedule". A second tick in the same window fires nothing.
func TestEndToEndScheduledRun(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()

	sessionKey := bytes.Repeat([]byte{1}, 32)
	signer := token.New(bytes.Repeat([]byte{2}, 32))
	provider := &llmtest.Scripted{
		Response: "scheduled digest",
		Usage:    llm.Usage{InputTokens: 10, OutputTokens: 5},
	}
	factory := func(string) (llm.Provider, error) { return provider, nil }

	var baseURL string
	local := compute.NewLocal(t.TempDir(), func(ctx context.Context, req compute.InvokeRequest, stateDir string) {
		client := harness.NewClient(baseURL, req.RunID, req.RunToken)
		steps, err := client.Context(ctx)
		if err != nil {
			t.Errorf("harness context: %v", err)
			return
		}
		_, _ = harness.Run(ctx, harness.Input{Steps: steps, RunToken: req.RunToken}, harness.Deps{
			ProviderFactory: factory,
			Sink:            client,
		})
	})
	eng := &engine.Engine{Store: s, Signer: signer, Compute: local}

	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, httpapi.Deps{Store: s, SessionKey: sessionKey, Engine: eng})
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	baseURL = ts.URL

	master, err := vault.NewMaster(bytes.Repeat([]byte{3}, 32))
	require.NoError(t, err)
	wrapped, err := master.NewTenantKEK()
	require.NoError(t, err)
	tn, err := s.CreateTenant(ctx, "acme", wrapped)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	cookie, err := httpapi.SessionCookie(sessionKey,
		httpapi.SessionClaims{UserID: user.ID, TenantID: tn.ID, Role: "owner"}, time.Hour)
	require.NoError(t, err)
	do := newDoHelper(t, ts.URL, cookie)

	out := do("POST", "/v1/workflows", map[string]any{
		"name": "daily digest",
		"steps": map[string]any{
			"system_prompt": "You prepare the daily digest.",
			"kickoff":       "Summarize.",
			"provider":      "anthropic",
			"model":         "claude-sonnet-5",
			"max_tokens":    64,
		},
		"permit":   map[string]any{"v": 1, "llm": map[string]any{"providers": []string{"anthropic"}}, "connections": map[string]any{}},
		"schedule": map[string]any{"cron": "0 9 * * *", "tz": "UTC"},
	})
	wfID := out["workflow"].(map[string]any)["id"].(string)
	do("POST", "/v1/workflows/"+wfID+"/versions/1/approve", nil)

	now := time.Date(2026, 9, 7, 9, 0, 30, 0, time.UTC)
	sched := &engine.Scheduler{Engine: eng, Store: s, Now: func() time.Time { return now }}
	sched.Tick(ctx)
	local.Wait()

	out = do("GET", "/v1/workflows/"+wfID+"/runs", nil)
	runs := out["runs"].([]any)
	require.Len(t, runs, 1)
	run := runs[0].(map[string]any)
	require.Equal(t, "schedule", run["fire_reason"])
	require.Equal(t, "succeeded", run["status"])
	require.Contains(t, run["output"], "scheduled digest")

	sched.Tick(ctx) // same window: nothing new
	out = do("GET", "/v1/workflows/"+wfID+"/runs", nil)
	require.Len(t, out["runs"].([]any), 1)
}
```

- [ ] **Step 3: Run the e2e, then full verification**

Run: `cd server && go test . -run TestEndToEndScheduledRun -v`
Expected: PASS.
Then: `cd server && go build ./... && gofmt -l . && go vet ./... && go test ./...` and `npm test` from the repo root (46/46 unchanged).
Expected: all green.

- [ ] **Step 4: Update the docs**

- `docs/api/v1.md`: version wire shape gains `schedule` (with the cron/tz JSON and validation rules); permit section gains `spend.per_run_cents`; run JSON gains `fire_reason`/`fire_time`; `POST /v1/workflows/{id}/runs` gains the 409 "a run is already active" row and the unpriced-model 400 note on create/versions.
- `server/README.md`: the three new env vars (with the `deadline > TTL` rule); a short paragraph on the scheduler (versioned schedule artifact, skip catch-up, one-active-run admission) and the reaper; meter paragraph noting the monthly cap is enforced at the proxy hook.
- `docs/superpowers/plans/2026-08-30-nightshift-platform-roadmap.md`: append one sentence to decision 10: "Closed by Plan 3 (2026-08-31): reaper + deadline>TTL invariant shipped; queueing dissolved by the one-active-run admission index."

- [ ] **Step 5: Format docs and commit**

```bash
cd /home/tng/workspace/nightshift-worktrees/sched-spec
npx prettier --write docs/api/v1.md server/README.md docs/superpowers/plans/2026-08-30-nightshift-platform-roadmap.md
git add server docs
git commit -m "feat(server): wire scheduler, meter hook, and reaper — scheduled e2e green"
```

---

## Final verification (after all tasks)

1. `cd server && gofmt -l . && go vet ./... && go build ./... && go test ./...` — all green.
2. Spec boundary check: delivered = tenant cap at the hook, per-run cap with transactional detection, priced-model validation, scheduler with redispatch recovery + admission index, reaper; the delivery-boundary narrowings (pre-request per-run enforcement, fairness) remain documented, not implemented.
3. System-query check: `grep -n "System query" server/internal/store/*.go` shows exactly the three scheduler/reaper methods, each with the doc comment; no request-path handler calls them (`grep -rn "ListSchedulableWorkflows\|ListUndispatchedScheduledRuns\|ListStuckRuns" server/internal/httpapi server/internal/internalapi server/internal/proxy*` is empty).
4. Hook check: `grep -n "NopHook" server/cmd/nightshift/main.go` is empty (the meter is wired); `grep -rn "NopHook" server/internal/proxy` still shows the type (tests use it).
5. `npm test` from the repo root — 46/46.

## Deviations log

Executors: record deviations in `implementation-notes.md` at the worktree root; folded into the PR description at the end.

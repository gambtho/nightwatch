package engine_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/engine"
	"github.com/gambtho/tomte/server/internal/store"
)

func scheduledSetup(t *testing.T) (*store.Store, *engine.Engine, *fakeCompute, store.Tenant, store.Workflow) {
	t.Helper()
	s, eng, fc, tn, _ := setup(t)
	ctx := context.Background()
	user, err := s.UpsertUser(ctx, tn.ID, "sched@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "scheduled", store.VersionDoc{
		Steps:    testStepsDoc,
		Permit:   []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric:   []byte(`{}`),
		Schedule: json.RawMessage(`{"cron":"0 9 * * *","tz":"UTC"}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID, testCompiledDoc)
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

// No heartbeat (a fresh install's very first tick): the default window
// applies and an hours-old occurrence does not fire — there was no
// running scheduler that could have owed it.
func TestTickNoHeartbeatKeepsDefaultWindow(t *testing.T) {
	s, eng, _, tn, wf := scheduledSetup(t)
	ctx := context.Background()

	now := time.Date(2026, 9, 7, 15, 0, 0, 0, time.UTC)
	sched := &engine.Scheduler{Engine: eng, Store: s, Now: func() time.Time { return now }}
	sched.Tick(ctx)
	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Empty(t, runs)

	// The completed tick persisted its heartbeat.
	hb, err := s.GetSchedulerHeartbeat(ctx)
	require.NoError(t, err)
	require.NotNil(t, hb)
	require.True(t, hb.Equal(now))
}

// The fire-on-wake case that never fires on main: the machine slept (or
// the app was quit) from before the 09:00 occurrence until 15:04. The
// persisted heartbeat widens the lookback past the gap, the occurrence
// fires exactly once, and the run records its scheduled fire_time.
func TestTickFiresOccurrenceHoursOldAfterWake(t *testing.T) {
	s, eng, fc, tn, wf := scheduledSetup(t)
	ctx := context.Background()

	require.NoError(t, s.SetSchedulerHeartbeat(ctx, time.Date(2026, 9, 7, 8, 59, 0, 0, time.UTC)))
	now := time.Date(2026, 9, 7, 15, 4, 0, 0, time.UTC)
	sched := &engine.Scheduler{Engine: eng, Store: s, Now: func() time.Time { return now }}

	sched.Tick(ctx)
	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "schedule", runs[0].FireReason)
	require.Equal(t, time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC).Unix(), runs[0].FireTime.Unix())
	require.Len(t, fc.invokes, 1)

	// Once, not twice: the next tick owes nothing.
	sched.Tick(ctx)
	runs, err = s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
}

// A week of downtime fires each workflow at most once — mostRecentDue
// returns only the latest occurrence of the daily schedule.
func TestTickWakeAfterLongDowntimeFiresLatestOnly(t *testing.T) {
	s, eng, _, tn, wf := scheduledSetup(t)
	ctx := context.Background()

	require.NoError(t, s.SetSchedulerHeartbeat(ctx, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)))
	now := time.Date(2026, 9, 7, 15, 4, 0, 0, time.UTC)
	sched := &engine.Scheduler{Engine: eng, Store: s, Now: func() time.Time { return now }}

	sched.Tick(ctx)
	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC).Unix(), runs[0].FireTime.Unix())
}

// A failed create pass must not advance the heartbeat: the missed
// occurrence would land permanently outside the next lookback.
func TestTickFailedCreatePassKeepsHeartbeat(t *testing.T) {
	s, eng, _, tn, wf := scheduledSetup(t)
	ctx := context.Background()

	// A canceled context fails the pass at the store, the simplest
	// infrastructure failure to inject.
	before := time.Date(2026, 9, 7, 8, 59, 0, 0, time.UTC)
	require.NoError(t, s.SetSchedulerHeartbeat(ctx, before))
	now := time.Date(2026, 9, 7, 15, 4, 0, 0, time.UTC)
	sched := &engine.Scheduler{Engine: eng, Store: s, Now: func() time.Time { return now }}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	sched.Tick(canceled)

	hb, err := s.GetSchedulerHeartbeat(ctx)
	require.NoError(t, err)
	require.True(t, hb.Equal(before), "heartbeat must not advance past a failed pass")

	// The next healthy tick still fires the owed occurrence.
	sched.Tick(ctx)
	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
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

// TestTickDoesNotWedgeOnImpossibleCron guards against a scheduler that
// busy-loops forever on a stored schedule whose cron is syntactically
// valid but never occurs (e.g. Feb 30). schedule.Parse now rejects such
// crons at the API boundary, but a row can still reach the store below
// that validation (e.g. data written before the guard existed, or
// through a path that skips it) — the store layer itself does not
// re-validate. mostRecentDue must stay defensive regardless of how the
// row got there. Tick is run with a timeout guard so a regression fails
// the test instead of hanging the suite.
func TestTickDoesNotWedgeOnImpossibleCron(t *testing.T) {
	s, eng, _, tn, _ := setup(t)
	ctx := context.Background()
	user, err := s.UpsertUser(ctx, tn.ID, "wedge@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "wedge", store.VersionDoc{
		Steps:  testStepsDoc,
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric: []byte(`{}`),
		// Bypasses schedule.Parse's validation (only enforced at the HTTP
		// layer, in decodeDoc) to reach the store with an impossible cron,
		// the way a pre-guard row or any other store-level write could.
		Schedule: json.RawMessage(`{"cron":"0 0 30 2 *","tz":"UTC"}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID, testCompiledDoc)
	require.NoError(t, err)

	now := time.Date(2026, 9, 7, 9, 0, 30, 0, time.UTC)
	sched := &engine.Scheduler{Engine: eng, Store: s, Now: func() time.Time { return now }}

	done := make(chan struct{})
	go func() {
		sched.Tick(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Tick did not return: scheduler wedged on an impossible-cron schedule")
	}

	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Empty(t, runs) // never due, so nothing fires
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

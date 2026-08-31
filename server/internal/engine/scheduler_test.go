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

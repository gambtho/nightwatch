package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

func setupApproved(t *testing.T, s *store.Store) (store.Tenant, store.Workflow) {
	t.Helper()
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "weekly digest", testDoc())
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID)
	require.NoError(t, err)
	return tn, wf
}

func TestRunLifecycle(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, wf := setupApproved(t, s)

	runID := uuid.New()
	run, err := s.CreateRun(ctx, tn.ID, wf.ID, runID, 1, "hash123", "manual", nil)
	require.NoError(t, err)
	require.Equal(t, "pending", run.Status)

	require.NoError(t, s.MarkRunRunning(ctx, tn.ID, runID))
	require.NoError(t, s.AppendRunEvent(ctx, tn.ID, runID, "run.start", json.RawMessage(`{}`)))

	final, err := s.FinalizeRun(ctx, tn.ID, runID, store.RunFinal{
		Status: "succeeded", Output: "the digest",
		TokensIn: 100, TokensOut: 50, CostCents: 3,
	}, 0)
	require.NoError(t, err)
	require.Equal(t, "succeeded", final.Status)
	require.NotNil(t, final.FinishedAt)
	require.Equal(t, "the digest", *final.Output)
	require.Equal(t, 100, *final.TokensIn)

	// Finalization revokes the run token: the stored hash is cleared in the
	// same UPDATE that sets the terminal status.
	require.Empty(t, final.TokenHash)
	got, err := s.GetRun(ctx, tn.ID, runID)
	require.NoError(t, err)
	require.Empty(t, got.TokenHash)

	events, err := s.ListRunEvents(ctx, tn.ID, runID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "run.start", events[0].Type)

	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)

	// Terminal runs are immutable: no late events, no re-finalization.
	err = s.AppendRunEvent(ctx, tn.ID, runID, "late", json.RawMessage(`{}`))
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.FinalizeRun(ctx, tn.ID, runID, store.RunFinal{Status: "failed"}, 0)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestRunCrossTenantIsolation(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, wf := setupApproved(t, s)
	other, err := s.CreateTenant(ctx, "other", testKEK)
	require.NoError(t, err)

	runID := uuid.New()
	_, err = s.CreateRun(ctx, tn.ID, wf.ID, runID, 1, "hash123", "manual", nil)
	require.NoError(t, err)

	_, err = s.GetRun(ctx, other.ID, runID)
	require.ErrorIs(t, err, store.ErrNotFound)
	err = s.AppendRunEvent(ctx, other.ID, runID, "x", json.RawMessage(`{}`))
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.FinalizeRun(ctx, other.ID, runID, store.RunFinal{Status: "succeeded"}, 0)
	require.ErrorIs(t, err, store.ErrNotFound)

	// Creating a run against another tenant's workflow must fail.
	_, err = s.CreateRun(ctx, other.ID, wf.ID, uuid.New(), 1, "h", "manual", nil)
	require.Error(t, err)
}

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

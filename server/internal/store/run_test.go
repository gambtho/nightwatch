package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

func setupApproved(t *testing.T, s *store.Store) (store.Tenant, store.Workflow) {
	t.Helper()
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme")
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
	run, err := s.CreateRun(ctx, tn.ID, wf.ID, runID, 1, "hash123")
	require.NoError(t, err)
	require.Equal(t, "pending", run.Status)

	require.NoError(t, s.MarkRunRunning(ctx, tn.ID, runID))
	require.NoError(t, s.AppendRunEvent(ctx, tn.ID, runID, "run.start", json.RawMessage(`{}`)))

	final, err := s.FinalizeRun(ctx, tn.ID, runID, store.RunFinal{
		Status: "succeeded", Output: "the digest",
		TokensIn: 100, TokensOut: 50, CostCents: 3,
	})
	require.NoError(t, err)
	require.Equal(t, "succeeded", final.Status)
	require.NotNil(t, final.FinishedAt)
	require.Equal(t, "the digest", *final.Output)
	require.Equal(t, 100, *final.TokensIn)

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
	_, err = s.FinalizeRun(ctx, tn.ID, runID, store.RunFinal{Status: "failed"})
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestRunCrossTenantIsolation(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, wf := setupApproved(t, s)
	other, err := s.CreateTenant(ctx, "other")
	require.NoError(t, err)

	runID := uuid.New()
	_, err = s.CreateRun(ctx, tn.ID, wf.ID, runID, 1, "hash123")
	require.NoError(t, err)

	_, err = s.GetRun(ctx, other.ID, runID)
	require.ErrorIs(t, err, store.ErrNotFound)
	err = s.AppendRunEvent(ctx, other.ID, runID, "x", json.RawMessage(`{}`))
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.FinalizeRun(ctx, other.ID, runID, store.RunFinal{Status: "succeeded"})
	require.ErrorIs(t, err, store.ErrNotFound)

	// Creating a run against another tenant's workflow must fail.
	_, err = s.CreateRun(ctx, other.ID, wf.ID, uuid.New(), 1, "h")
	require.Error(t, err)
}

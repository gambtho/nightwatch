package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/engine"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

func TestReaperSweepsStuckRuns(t *testing.T) {
	pool := testpg.New(t)
	s := store.New(pool)
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "wf", store.VersionDoc{
		Steps:  testStepsDoc,
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID, testCompiledDoc)
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

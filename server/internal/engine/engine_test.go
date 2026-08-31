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
	// onInvoke, if set, runs (under the same lock) right after a
	// successful Invoke is recorded — used to simulate the caller's
	// context being canceled the instant the harness accepts the run,
	// before dispatch's post-Invoke store write runs.
	onInvoke func()
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
	if f.onInvoke != nil {
		f.onInvoke()
	}
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
		Steps:  testStepsDoc,
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID, testCompiledDoc)
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

// TestDispatchRetriesMarkRunDispatchedAfterContextCancel guards the path
// where the caller's context is canceled the instant Invoke succeeds (a
// client disconnect, or — as here — the scheduler's context ending
// between ticks): the harness already holds the bearer and is running,
// but the immediately-following MarkRunDispatched(ctx, ...) would fail
// on the now-canceled context. Without a cancel-free retry the run stays
// pending with dispatched_at NULL, so the next tick's dispatchPending
// would Redispatch it — invalidating the live token and double-invoking.
// The retry (on context.WithoutCancel) must succeed, land dispatched_at,
// and never cause a second Invoke.
func TestDispatchRetriesMarkRunDispatchedAfterContextCancel(t *testing.T) {
	s, eng, fc, tn, wf := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	fc.onInvoke = cancel // simulates the caller disconnecting right after the harness accepts the run

	run, err := eng.Fire(ctx, tn.ID, wf.ID, 1, "manual", nil)
	require.NoError(t, err)
	require.Len(t, fc.invokes, 1) // exactly one Invoke, no duplicate dispatch

	got, err := s.GetRun(context.Background(), tn.ID, run.ID)
	require.NoError(t, err)
	require.NotNil(t, got.DispatchedAt, "MarkRunDispatched retry on a cancel-free context must still land")
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

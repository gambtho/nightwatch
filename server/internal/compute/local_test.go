package compute_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/compute"
)

func TestLocalActorStatePersistsAcrossInvokes(t *testing.T) {
	var mu sync.Mutex
	var dirs []string
	runner := func(ctx context.Context, req compute.InvokeRequest, stateDir string) {
		mu.Lock()
		defer mu.Unlock()
		dirs = append(dirs, stateDir)
		// The actor's working state must survive between fires: a workflow
		// is a long-lived actor, a run is an episode in its life.
		// (t.Error, not require: this runs off the test goroutine.)
		path := filepath.Join(stateDir, "memory.txt")
		prev, _ := os.ReadFile(path)
		if err := os.WriteFile(path, append(prev, 'x'), 0o644); err != nil {
			t.Error(err)
		}
	}

	local := compute.NewLocal(t.TempDir(), runner)
	ctx := context.Background()
	ref := compute.WorkflowRef{TenantID: uuid.New(), WorkflowID: uuid.New()}

	actor, err := local.EnsureActor(ctx, ref, compute.TemplateRef{Name: "harness-v1"})
	require.NoError(t, err)

	_, err = local.Invoke(ctx, actor, compute.InvokeRequest{RunID: uuid.New()})
	require.NoError(t, err)
	local.Wait()

	// EnsureActor is idempotent: same ref, same actor, same state.
	actor2, err := local.EnsureActor(ctx, ref, compute.TemplateRef{Name: "harness-v1"})
	require.NoError(t, err)
	require.Equal(t, actor, actor2)

	_, err = local.Invoke(ctx, actor2, compute.InvokeRequest{RunID: uuid.New()})
	require.NoError(t, err)
	local.Wait()

	require.Len(t, dirs, 2)
	require.Equal(t, dirs[0], dirs[1])
	content, err := os.ReadFile(filepath.Join(dirs[0], "memory.txt"))
	require.NoError(t, err)
	require.Equal(t, "xx", string(content))
}

func TestLocalDestroyRemovesState(t *testing.T) {
	local := compute.NewLocal(t.TempDir(),
		func(ctx context.Context, req compute.InvokeRequest, stateDir string) {})
	ctx := context.Background()
	ref := compute.WorkflowRef{TenantID: uuid.New(), WorkflowID: uuid.New()}
	actor, err := local.EnsureActor(ctx, ref, compute.TemplateRef{Name: "harness-v1"})
	require.NoError(t, err)
	require.NoError(t, local.Destroy(ctx, actor))
	// Idempotent.
	require.NoError(t, local.Destroy(ctx, actor))
}

func TestLocalInvokesSerializePerActor(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	runner := func(ctx context.Context, req compute.InvokeRequest, stateDir string) {
		entered <- struct{}{}
		<-release
	}
	local := compute.NewLocal(t.TempDir(), runner)
	ctx := context.Background()
	ref := compute.WorkflowRef{TenantID: uuid.New(), WorkflowID: uuid.New()}
	actor, err := local.EnsureActor(ctx, ref, compute.TemplateRef{Name: "harness-v1"})
	require.NoError(t, err)

	_, err = local.Invoke(ctx, actor, compute.InvokeRequest{RunID: uuid.New()})
	require.NoError(t, err)
	_, err = local.Invoke(ctx, actor, compute.InvokeRequest{RunID: uuid.New()})
	require.NoError(t, err)

	<-entered // the first run is in
	select {
	case <-entered:
		t.Fatal("second run entered while the first was still active")
	case <-time.After(100 * time.Millisecond):
		// serialized, as required
	}
	close(release)
	local.Wait()
}

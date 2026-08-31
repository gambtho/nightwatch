package httpapi_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/compute"
)

// fakeCompute records invocations instead of running anything.
type fakeCompute struct {
	mu        sync.Mutex
	invokes   []compute.InvokeRequest
	invokeErr error // when set, Invoke fails
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

func TestFireRunRequiresApprovedVersion(t *testing.T) {
	e := newEnv(t)

	resp, out := e.do(t, "POST", "/v1/workflows", workflowBody())
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)

	// Draft only: firing is refused.
	resp, _ = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/runs", id), nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode)

	resp, _ = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/versions/1/approve", id), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, out = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/runs", id), nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	run := out["run"].(map[string]any)
	require.Equal(t, "pending", run["status"])
	require.Equal(t, float64(1), run["version"])

	// The seam was invoked with a signed token for this run.
	require.Len(t, e.compute.invokes, 1)
	require.Equal(t, run["id"], e.compute.invokes[0].RunID.String())
	require.NotEmpty(t, e.compute.invokes[0].RunToken)

	// The run is visible through the read endpoints.
	resp, out = e.do(t, "GET", "/v1/runs/"+run["id"].(string), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, out = e.do(t, "GET", fmt.Sprintf("/v1/workflows/%s/runs", id), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out["runs"], 1)
}

func TestFireRunDispatchFailureMarksRunFailed(t *testing.T) {
	e := newEnv(t)

	resp, out := e.do(t, "POST", "/v1/workflows", workflowBody())
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)
	resp, _ = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/versions/1/approve", id), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	e.compute.invokeErr = errors.New("no workers")
	resp, _ = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/runs", id), nil)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// A dispatch failure must not leave the run pending forever.
	resp, out = e.do(t, "GET", fmt.Sprintf("/v1/workflows/%s/runs", id), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	runs := out["runs"].([]any)
	require.Len(t, runs, 1)
	require.Equal(t, "failed", runs[0].(map[string]any)["status"])
	require.Equal(t, "dispatch_failed", runs[0].(map[string]any)["error_kind"])
}

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

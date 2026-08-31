package internalapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/harness"
	"github.com/gambtho/nightwatch/server/internal/internalapi"
	"github.com/gambtho/nightwatch/server/internal/llm"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
	"github.com/gambtho/nightwatch/server/internal/token"
)

func setup(t *testing.T) (*store.Store, *token.Signer, *httptest.Server, store.Tenant, store.Workflow) {
	t.Helper()
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme")
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "weekly digest", store.VersionDoc{
		Steps: store.StepsDoc{
			SystemPrompt: "You prepare the weekly support digest.",
			Kickoff:      "Summarize last week's tickets.",
			Provider:     "anthropic",
			Model:        "claude-sonnet-5",
			MaxTokens:    2048,
		},
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`), Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID)
	require.NoError(t, err)

	signer := token.New([]byte("0123456789abcdef0123456789abcdef"))
	mux := http.NewServeMux()
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return s, signer, ts, tn, wf
}

func mintRun(t *testing.T, s *store.Store, signer *token.Signer, tn store.Tenant, wf store.Workflow) (uuid.UUID, string) {
	t.Helper()
	runID := uuid.New()
	bearer, hash, err := signer.Sign(token.RunClaims{
		RunID: runID, TenantID: tn.ID, ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	_, err = s.CreateRun(context.Background(), tn.ID, wf.ID, runID, 1, hash)
	require.NoError(t, err)
	return runID, bearer
}

func TestHarnessClientAgainstInternalAPI(t *testing.T) {
	s, signer, ts, tn, wf := setup(t)
	ctx := context.Background()
	runID, bearer := mintRun(t, s, signer, tn, wf)

	client := harness.NewClient(ts.URL, runID, bearer)

	steps, err := client.Context(ctx)
	require.NoError(t, err)
	require.Equal(t, "anthropic", steps.Provider)

	// Context fetch marked the run running.
	run, err := s.GetRun(ctx, tn.ID, runID)
	require.NoError(t, err)
	require.Equal(t, "running", run.Status)

	require.NoError(t, client.Event(ctx, harness.RunEvent{Type: "run.start"}))
	require.NoError(t, client.Finalize(ctx, harness.Result{
		Status: harness.StatusSucceeded, Output: "the digest",
		Usage: llm.Usage{InputTokens: 100, OutputTokens: 50}, CostCents: 3,
	}))

	run, err = s.GetRun(ctx, tn.ID, runID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", run.Status)
	require.Equal(t, "the digest", *run.Output)
	require.Equal(t, 100, *run.TokensIn)

	events, err := s.ListRunEvents(ctx, tn.ID, runID)
	require.NoError(t, err)
	require.Len(t, events, 1)
}

func TestInternalAPIAuth(t *testing.T) {
	s, signer, ts, tn, wf := setup(t)
	runID, _ := mintRun(t, s, signer, tn, wf)
	otherRunID, otherBearer := mintRun(t, s, signer, tn, wf)
	_ = otherRunID

	// No bearer: 401.
	noAuthReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/internal/runs/"+runID.String()+"/context", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(noAuthReq)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// A token for another run must not open this run.
	req, err := http.NewRequestWithContext(context.Background(), "GET", ts.URL+"/internal/runs/"+runID.String()+"/context", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+otherBearer)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestInternalAPIRejectsMismatchedTokenHash(t *testing.T) {
	// A structurally valid JWT is not enough: the bearer must be the exact
	// token minted for the run (the stored hash is the revocation lever).
	s, signer, ts, tn, wf := setup(t)
	runID := uuid.New()
	bearer, _, err := signer.Sign(token.RunClaims{
		RunID: runID, TenantID: tn.ID, ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	_, err = s.CreateRun(context.Background(), tn.ID, wf.ID, runID, 1, "not-the-real-hash")
	require.NoError(t, err)

	req, err := http.NewRequest("GET", ts.URL+"/internal/runs/"+runID.String()+"/context", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

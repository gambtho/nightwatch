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

var testKEK = []byte("test-wrapped-kek") // opaque to the store; real KEKs arrive with vault tests

func setup(t *testing.T) (*store.Store, *token.Signer, *httptest.Server, store.Tenant, store.Workflow) {
	t.Helper()
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "weekly digest", store.VersionDoc{
		Steps:  testStepsDoc,
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`), Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID, testCompiledDoc)
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
	_, err = s.CreateRun(context.Background(), tn.ID, wf.ID, runID, 1, hash, "manual", nil)
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
	// The context serves the compiled execution form (decision 9), with the
	// fire occasion appended to the compiled kickoff.
	require.Equal(t, "anthropic", steps.Provider)
	require.Equal(t, "You prepare the weekly support digest.", steps.SystemPrompt)
	require.Contains(t, steps.Kickoff, "Summarize last week's tickets.")
	require.Contains(t, steps.Kickoff, "fired manually")

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

	// The admission index allows only one active run per workflow, so the
	// second run needs its own workflow.
	ctx2 := context.Background()
	user2, err := s.UpsertUser(ctx2, tn.ID, "second@acme.test")
	require.NoError(t, err)
	wf2, _, err := s.CreateWorkflow(ctx2, tn.ID, "second workflow", store.VersionDoc{
		Steps:  testStepsDoc,
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx2, tn.ID, wf2.ID, 1, user2.ID, testCompiledDoc)
	require.NoError(t, err)
	otherRunID, otherBearer := mintRun(t, s, signer, tn, wf2)
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
	_, err = s.CreateRun(context.Background(), tn.ID, wf.ID, runID, 1, "not-the-real-hash", "manual", nil)
	require.NoError(t, err)

	req, err := http.NewRequest("GET", ts.URL+"/internal/runs/"+runID.String()+"/context", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestFinalizeResolvesPerRunCap(t *testing.T) {
	s, signer, ts, tn, wf := setup(t)
	_ = wf
	ctx := context.Background()
	user, err := s.UpsertUser(ctx, tn.ID, "cap@acme.test")
	require.NoError(t, err)
	capped, _, err := s.CreateWorkflow(ctx, tn.ID, "capped", store.VersionDoc{
		Steps:  testStepsDoc,
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"spend":{"per_run_cents":5},"connections":{}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, capped.ID, 1, user.ID, testCompiledDoc)
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

// TestContextServesScheduledOccasion: a scheduled run's kickoff names the
// occurrence it is firing for.
func TestContextServesScheduledOccasion(t *testing.T) {
	s, signer, ts, tn, wf := setup(t)
	ctx := context.Background()
	runID := uuid.New()
	bearer, hash, err := signer.Sign(token.RunClaims{
		RunID: runID, TenantID: tn.ID, ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	fireTime := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	_, err = s.CreateRun(ctx, tn.ID, wf.ID, runID, 1, hash, "schedule", &fireTime)
	require.NoError(t, err)

	client := harness.NewClient(ts.URL, runID, bearer)
	steps, err := client.Context(ctx)
	require.NoError(t, err)
	require.Contains(t, steps.Kickoff, "2026-09-07T09:00:00Z")
	require.Contains(t, steps.Kickoff, "scheduled occurrence")
}

// TestContextFailsWithoutCompiled: a run pointing at a version with no
// compiled document (a draft — approval is what writes compiled) must not
// serve a context; there is no execution form to run from.
func TestContextFailsWithoutCompiled(t *testing.T) {
	s, signer, ts, tn, _ := setup(t)
	ctx := context.Background()
	draft, _, err := s.CreateWorkflow(ctx, tn.ID, "still a draft", store.VersionDoc{
		Steps:  testStepsDoc,
		Permit: []byte(`{"v":1,"connections":{}}`), Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	runID, bearer := mintRun(t, s, signer, tn, draft)
	client := harness.NewClient(ts.URL, runID, bearer)
	_, err = client.Context(ctx)
	require.Error(t, err)
}

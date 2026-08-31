package internalapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/catalog"
	"github.com/gambtho/nightwatch/server/internal/harness"
	"github.com/gambtho/nightwatch/server/internal/internalapi"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
	"github.com/gambtho/nightwatch/server/internal/token"
)

// The run context's tools array is server-derived from the approved
// permit joined with the catalog; the harness cannot grant itself a
// tool the control plane did not project.
func TestRunContextProjectsTools(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "digest", store.VersionDoc{
		Steps: testStepsDoc,
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{
			"slack":{"kind":"http","ops":["post_message","list_channels"],
			         "resources":{"post_message":{"channel":["C1"]}}}
		}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID, testCompiledDoc)
	require.NoError(t, err)

	cat, err := catalog.Load()
	require.NoError(t, err)
	signer := token.New([]byte("0123456789abcdef0123456789abcdef"))
	mux := http.NewServeMux()
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer, Catalog: cat})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	runID, bearer := mintRun(t, s, signer, tn, wf)
	client := harness.NewClient(ts.URL, runID, bearer)
	_, tools, err := client.Context(ctx)
	require.NoError(t, err)

	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	// Stable order: connector then op, alphabetical.
	require.Equal(t, []string{"slack__list_channels", "slack__post_message"}, names)
	for _, tool := range tools {
		require.NotEmpty(t, tool.Description, tool.Name)
		require.NotEmpty(t, tool.InputSchema, tool.Name)
	}
}

func TestRunContextNoConnectionsNoTools(t *testing.T) {
	s, signer, ts, tn, wf := setup(t)
	runID, bearer := mintRun(t, s, signer, tn, wf)
	client := harness.NewClient(ts.URL, runID, bearer)
	_, tools, err := client.Context(context.Background())
	require.NoError(t, err)
	require.Empty(t, tools)
}

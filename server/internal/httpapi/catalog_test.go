package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/catalog"
	"github.com/gambtho/tomte/server/internal/httpapi"
)

func withCatalog(t *testing.T) func(*httpapi.Deps) {
	t.Helper()
	cat, err := catalog.Load()
	require.NoError(t, err)
	return func(d *httpapi.Deps) { d.Catalog = cat }
}

func TestGetCatalog(t *testing.T) {
	e := newEnv(t, withCatalog(t))

	resp, out := e.do(t, "GET", "/v1/catalog", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	connectors := out["connectors"].([]any)
	require.Len(t, connectors, 2)

	byID := map[string]map[string]any{}
	for _, c := range connectors {
		cm := c.(map[string]any)
		byID[cm["id"].(string)] = cm
	}
	slack := byID["slack"]
	require.Equal(t, "slack", slack["auth_provider"])
	require.Equal(t, false, slack["connected"])

	var post map[string]any
	for _, o := range slack["ops"].([]any) {
		om := o.(map[string]any)
		if om["name"] == "post_message" {
			post = om
		}
	}
	require.NotNil(t, post)
	require.Equal(t, "write", post["effect"])
	require.Equal(t, []any{"channel"}, post["constraints"])
	require.NotEmpty(t, post["description"])
	require.NotNil(t, post["args_schema"])
}

func TestGetCatalogConnectedFlag(t *testing.T) {
	e := newEnv(t, withCatalog(t))

	resp, _ := e.do(t, "PUT", "/v1/connections/default",
		map[string]any{"provider": "slack", "value": "xoxb-secret"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	_, out := e.do(t, "GET", "/v1/catalog", nil)
	for _, c := range out["connectors"].([]any) {
		cm := c.(map[string]any)
		want := cm["id"] == "slack" // Google Calendar stays unconnected.
		require.Equal(t, want, cm["connected"], cm["id"])
	}
}

func TestGetCatalogRequiresSession(t *testing.T) {
	e := newEnv(t, withCatalog(t))
	req, err := http.NewRequest("GET", e.ts.URL+"/v1/catalog", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func connectionsBody(connections map[string]any) map[string]any {
	b := workflowBody()
	b["permit"] = map[string]any{
		"v":           1,
		"llm":         map[string]any{"providers": []string{"anthropic"}},
		"connections": connections,
	}
	return b
}

// Version writes check permit connections against the running catalog —
// a permit cannot reference reach that does not exist or that the proxy
// could not enforce.
func TestCreateWorkflowValidatesConnectionsAgainstCatalog(t *testing.T) {
	e := newEnv(t, withCatalog(t))

	ok := connectionsBody(map[string]any{
		"slack": map[string]any{
			"kind": "http",
			"ops":  []string{"list_channels", "post_message"},
			"resources": map[string]any{
				"post_message": map[string]any{"channel": []string{"C0123ABC"}},
			},
		},
	})
	resp, _ := e.do(t, "POST", "/v1/workflows", ok)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	cases := []struct {
		name        string
		connections map[string]any
		wantErr     string
	}{
		{"unknown connector",
			map[string]any{"zendesk": map[string]any{"kind": "http", "ops": []string{"a"}}},
			"unknown connector"},
		{"unknown op",
			map[string]any{"slack": map[string]any{"kind": "http", "ops": []string{"delete_workspace"}}},
			"no op"},
		{"constrained op without resources",
			map[string]any{"slack": map[string]any{"kind": "http", "ops": []string{"post_message"}}},
			"requires an approved resource list"},
		{"resources on unconstrained field",
			map[string]any{"slack": map[string]any{
				"kind": "http", "ops": []string{"post_message"},
				"resources": map[string]any{"post_message": map[string]any{
					"channel": []string{"C1"}, "text": []string{"hi"},
				}},
			}},
			"no constraint"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := e.do(t, "POST", "/v1/workflows", connectionsBody(tc.connections))
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Contains(t, out["error"], tc.wantErr)
		})
	}
}

// The steps-v1 gap, closed: approval cross-checks the platform run
// provider against the permit's llm.providers, so a mismatch is a 400
// here instead of a proxy denial on every run.
func TestApproveRejectsPermitWithoutPlatformProvider(t *testing.T) {
	e := newEnv(t, withCatalog(t))

	b := workflowBody()
	b["permit"] = map[string]any{"v": 1, "llm": map[string]any{"providers": []string{"openai"}}}
	resp, out := e.do(t, "POST", "/v1/workflows", b)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)

	resp, out = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, out["error"], "platform run provider")
}

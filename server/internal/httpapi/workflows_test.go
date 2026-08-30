package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/httpapi"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

type env struct {
	ts     *httptest.Server
	store  *store.Store
	key    []byte
	cookie *http.Cookie
	tenant store.Tenant
	user   store.User
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme")
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)

	key := testKey(t)
	cookie, err := httpapi.SessionCookie(key,
		httpapi.SessionClaims{UserID: user.ID, TenantID: tn.ID, Role: "owner"},
		time.Hour)
	require.NoError(t, err)

	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, httpapi.Deps{Store: s, SessionKey: key})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &env{ts: ts, store: s, key: key, cookie: cookie, tenant: tn, user: user}
}

func (e *env) do(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req, err := http.NewRequest(method, e.ts.URL+path, &buf)
	require.NoError(t, err)
	req.AddCookie(e.cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func workflowBody() map[string]any {
	return map[string]any{
		"name": "weekly digest",
		"steps": map[string]any{
			"system_prompt": "You prepare the weekly support digest.",
			"kickoff":       "Summarize last week's tickets.",
			"provider":      "anthropic",
			"model":         "claude-sonnet-5",
			"max_tokens":    2048,
		},
		"permit": map[string]any{"read": []string{"zendesk"}},
		"rubric": map[string]any{"rules": []string{"under a page"}},
	}
}

func TestWorkflowEndpoints(t *testing.T) {
	e := newEnv(t)

	resp, out := e.do(t, "POST", "/v1/workflows", workflowBody())
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	wf := out["workflow"].(map[string]any)
	id := wf["id"].(string)
	require.Equal(t, float64(1), out["version"].(map[string]any)["number"])

	resp, out = e.do(t, "GET", "/v1/workflows", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out["workflows"], 1)

	resp, out = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/versions/1/approve", id), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "approved", out["version"].(map[string]any)["status"])

	resp, out = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/versions", id), workflowBody())
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.Equal(t, float64(2), out["version"].(map[string]any)["number"])

	resp, out = e.do(t, "GET", "/v1/workflows/"+id, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out["versions"], 2)
}

func TestWorkflowAPIRequiresSession(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Get(e.ts.URL + "/v1/workflows")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestWorkflowAPINotFoundAndBadInput(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.do(t, "GET", "/v1/workflows/"+uuid.NewString(), nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, _ = e.do(t, "GET", "/v1/workflows/not-a-uuid", nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

package httpapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/engine"
	"github.com/gambtho/tomte/server/internal/httpapi"
	"github.com/gambtho/tomte/server/internal/steps"
	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/testpg"
	"github.com/gambtho/tomte/server/internal/token"
	"github.com/gambtho/tomte/server/internal/vault"
)

type env struct {
	ts      *httptest.Server
	store   *store.Store
	pool    *pgxpool.Pool
	cookie  *http.Cookie
	tenant  store.Tenant
	user    store.User
	compute *fakeCompute
	vault   *vault.Master
	baseURL *url.URL
}

// mintSessionCookie inserts a session row and returns the cookie carrying
// its opaque token — the DB-backed replacement for signing claims.
func mintSessionCookie(t *testing.T, s *store.Store, tenantID, userID uuid.UUID) *http.Cookie {
	t.Helper()
	value, tokenHash, err := httpapi.NewOpaqueToken()
	require.NoError(t, err)
	err = s.CreateSession(context.Background(), tokenHash, tenantID, userID)
	require.NoError(t, err)
	return httpapi.SessionCookie(value)
}

func newEnv(t *testing.T, mods ...func(*httpapi.Deps)) *env {
	t.Helper()
	pool := testpg.New(t)
	s := store.New(pool)
	ctx := context.Background()

	vkey := make([]byte, vault.KeyLen)
	_, err := rand.Read(vkey)
	require.NoError(t, err)
	master, err := vault.NewMaster(vkey)
	require.NoError(t, err)
	wrapped, err := master.NewTenantKEK()
	require.NoError(t, err)

	tn, err := s.CreateTenant(ctx, "acme", wrapped)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)

	cookie := mintSessionCookie(t, s, tn.ID, user.ID)

	fc := &fakeCompute{}
	signer := token.New([]byte("0123456789abcdef0123456789abcdef"))
	eng := &engine.Engine{Store: s, Signer: signer, Compute: fc}

	base := &url.URL{Scheme: "https", Host: "app.tomte.test"}
	mux := http.NewServeMux()
	deps := httpapi.Deps{Store: s, Engine: eng, Vault: master, PublicBaseURL: base}
	for _, mod := range mods {
		mod(&deps)
	}
	httpapi.RegisterRoutes(mux, deps)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &env{ts: ts, store: s, pool: pool, cookie: cookie, tenant: tn, user: user, compute: fc, vault: master, baseURL: base}
}

func (e *env) do(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.ts.URL+path, &buf)
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
			"v": 1,
			"steps": []map[string]any{
				{"id": "gather", "text": "Look at last week's support tickets."},
				{"id": "post-digest", "text": "Post a short digest in #team-digest."},
			},
		},
		"permit": map[string]any{"v": 1, "llm": map[string]any{"providers": []string{"anthropic"}}, "connections": map[string]any{}},
		"rubric": map[string]any{"rules": []string{"under a page"}},
	}
}

// TestApproveRejectsUnpricedPlatformModel: since decision 9 the model is
// platform policy, so the unpriced-model 400 fires at approval against the
// configured run model, and a misconfigured platform cannot promote drafts.
func TestApproveRejectsUnpricedPlatformModel(t *testing.T) {
	e := newEnv(t, func(d *httpapi.Deps) {
		d.RunProvider, d.RunModel = "anthropic", "claude-imaginary-9"
	})

	resp, out := e.do(t, "POST", "/v1/workflows", workflowBody())
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)

	resp, out = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/versions/1/approve", id), nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, out["error"], "no pricing")

	// The draft was not promoted.
	v, err := e.store.GetVersion(context.Background(), e.tenant.ID, uuid.MustParse(id), 1)
	require.NoError(t, err)
	require.Equal(t, "draft", v.Status)
}

// TestApproveCompilesExecutionForm: approval writes the server-derived
// compiled document (the approved step text verbatim, platform model,
// compiler_v stamp) while the public API keeps returning the user-facing
// artifact and never the compiled form.
func TestApproveCompilesExecutionForm(t *testing.T) {
	e := newEnv(t)

	resp, out := e.do(t, "POST", "/v1/workflows", workflowBody())
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)

	resp, out = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/versions/1/approve", id), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	version := out["version"].(map[string]any)
	require.NotContains(t, version, "compiled")
	stepsDoc := version["steps"].(map[string]any)
	require.Equal(t, float64(1), stepsDoc["v"])
	require.Len(t, stepsDoc["steps"], 2)

	v, err := e.store.GetVersion(context.Background(), e.tenant.ID, uuid.MustParse(id), 1)
	require.NoError(t, err)
	var compiled map[string]any
	require.NoError(t, json.Unmarshal(v.Compiled, &compiled))
	require.Equal(t, float64(steps.CompilerV), compiled["compiler_v"])
	require.Equal(t, httpapi.DefaultRunProvider, compiled["provider"])
	require.Equal(t, httpapi.DefaultRunModel, compiled["model"])
	require.Contains(t, compiled["system_prompt"], "1. Look at last week's support tickets.")
	require.Contains(t, compiled["system_prompt"], "2. Post a short digest in #team-digest.")
	require.Contains(t, compiled["system_prompt"], "under a page")
	require.Greater(t, compiled["max_tokens"], float64(0))
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
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, e.ts.URL+"/v1/workflows", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
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

func TestCreateWorkflowRejectsInvalidPermit(t *testing.T) {
	e := newEnv(t)
	body := workflowBody()
	body["permit"] = map[string]any{"v": 2}
	resp, _ := e.do(t, "POST", "/v1/workflows", body)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// Execution fields in a steps document are the reserved v1 break, spent:
// rejected, not ignored (decision 9).
func TestCreateWorkflowRejectsExecutionFields(t *testing.T) {
	e := newEnv(t)
	for name, doc := range map[string]map[string]any{
		"system_prompt": {"v": 1, "system_prompt": "x", "steps": []map[string]any{{"id": "a", "text": "b"}}},
		"provider":      {"v": 1, "provider": "anthropic", "steps": []map[string]any{{"id": "a", "text": "b"}}},
		"model":         {"v": 1, "model": "claude-sonnet-5", "steps": []map[string]any{{"id": "a", "text": "b"}}},
		"max_tokens":    {"v": 1, "max_tokens": 2048, "steps": []map[string]any{{"id": "a", "text": "b"}}},
	} {
		body := workflowBody()
		body["steps"] = doc
		resp, _ := e.do(t, "POST", "/v1/workflows", body)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, name)
	}
}

func TestCreateWorkflowRejectsInvalidSteps(t *testing.T) {
	e := newEnv(t)
	for name, stepsVal := range map[string]any{
		"missing":      nil,
		"empty list":   map[string]any{"v": 1, "steps": []map[string]any{}},
		"bad id":       map[string]any{"v": 1, "steps": []map[string]any{{"id": "Not A Slug", "text": "b"}}},
		"duplicate id": map[string]any{"v": 1, "steps": []map[string]any{{"id": "a", "text": "b"}, {"id": "a", "text": "c"}}},
	} {
		body := workflowBody()
		if stepsVal == nil {
			delete(body, "steps")
		} else {
			body["steps"] = stepsVal
		}
		resp, _ := e.do(t, "POST", "/v1/workflows", body)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, name)
	}
}

func TestWorkflowScheduleValidation(t *testing.T) {
	e := newEnv(t)

	body := workflowBody()
	body["schedule"] = map[string]any{"cron": "not cron", "tz": "UTC"}
	resp, _ := e.do(t, "POST", "/v1/workflows", body)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body["schedule"] = map[string]any{"cron": "0 9 * * MON", "tz": "America/New_York"}
	resp, out := e.do(t, "POST", "/v1/workflows", body)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, out["version"].(map[string]any)["schedule"])
}

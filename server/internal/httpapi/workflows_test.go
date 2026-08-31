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

	"github.com/gambtho/nightwatch/server/internal/engine"
	"github.com/gambtho/nightwatch/server/internal/httpapi"
	"github.com/gambtho/nightwatch/server/internal/mail"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
	"github.com/gambtho/nightwatch/server/internal/token"
	"github.com/gambtho/nightwatch/server/internal/vault"
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
	mailer  *mail.Recorder
	baseURL *url.URL
}

// mintSessionCookie inserts a session row and returns the cookie carrying
// its opaque token — the DB-backed replacement for signing claims.
func mintSessionCookie(t *testing.T, s *store.Store, tenantID, userID uuid.UUID) *http.Cookie {
	t.Helper()
	value, tokenHash, err := httpapi.NewSessionToken()
	require.NoError(t, err)
	_, err = s.CreateSession(context.Background(), tokenHash, tenantID, userID)
	require.NoError(t, err)
	return httpapi.SessionCookie(value)
}

func newEnv(t *testing.T) *env {
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

	base := &url.URL{Scheme: "https", Host: "app.nightshift.test"}
	mailer := &mail.Recorder{}
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, httpapi.Deps{Store: s, Engine: eng, Vault: master, PublicBaseURL: base, Mailer: mailer})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &env{ts: ts, store: s, pool: pool, cookie: cookie, tenant: tn, user: user, compute: fc, vault: master, mailer: mailer, baseURL: base}
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
			"system_prompt": "You prepare the weekly support digest.",
			"kickoff":       "Summarize last week's tickets.",
			"provider":      "anthropic",
			"model":         "claude-sonnet-5",
			"max_tokens":    2048,
		},
		"permit": map[string]any{"v": 1, "llm": map[string]any{"providers": []string{"anthropic"}}, "connections": map[string]any{}},
		"rubric": map[string]any{"rules": []string{"under a page"}},
	}
}

// TestApproveRejectsUnpricedDraft: a draft persisted before a price-table
// change must not become approved. decodeDoc blocks writing an unpriced
// model through the API, so simulate the stale draft with direct SQL.
func TestApproveRejectsUnpricedDraft(t *testing.T) {
	e := newEnv(t)

	resp, out := e.do(t, "POST", "/v1/workflows", workflowBody())
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)

	_, err := e.pool.Exec(context.Background(),
		`UPDATE workflow_version SET steps = jsonb_set(steps, '{model}', '"claude-legacy-1"')
		 WHERE workflow_id = $1`, id)
	require.NoError(t, err)

	resp, out = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/versions/1/approve", id), nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, out["error"], "no pricing")

	// The draft was not promoted.
	v, err := e.store.GetVersion(context.Background(), e.tenant.ID, uuid.MustParse(id), 1)
	require.NoError(t, err)
	require.Equal(t, "draft", v.Status)
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

func TestCreateWorkflowRejectsUnpricedModel(t *testing.T) {
	e := newEnv(t)
	body := workflowBody()
	body["steps"].(map[string]any)["model"] = "claude-imaginary-9"
	resp, out := e.do(t, "POST", "/v1/workflows", body)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, out["error"], "pricing")
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

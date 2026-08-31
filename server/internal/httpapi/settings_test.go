package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/httpapi"
	"github.com/gambtho/tomte/server/internal/llmverify"
	"github.com/gambtho/tomte/server/internal/meter"
	"github.com/gambtho/tomte/server/internal/steps"
)

// putLLMKey pastes an llm_api_key connection over the API for a provider.
func putLLMKey(t *testing.T, e *env, provider, name string) {
	t.Helper()
	resp, _ := e.do(t, "PUT", "/v1/connections/"+name,
		map[string]any{"provider": provider, "value": "sk-" + provider})
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestEndpointSettingsCRUDAndValidation(t *testing.T) {
	e := newEnv(t, withCatalog(t))

	resp, _ := e.do(t, "GET", "/v1/settings/endpoint", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	// A switch to an endpoint with no pasted key is refused.
	resp, out := e.do(t, "PUT", "/v1/settings/endpoint",
		map[string]any{"preset": "openai", "connection": "default", "run_model": "gpt-4o-mini"})
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	require.Equal(t, "connection_missing", out["error"])

	putLLMKey(t, e, "openai", "default")
	resp, out = e.do(t, "PUT", "/v1/settings/endpoint",
		map[string]any{"preset": "openai", "connection": "default", "run_model": "gpt-4o-mini"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	ep := out["endpoint"].(map[string]any)
	require.Equal(t, "https://api.openai.com/v1", ep["base_url"])
	require.Equal(t, "openai_compatible", ep["kind"])
	require.Equal(t, false, ep["zero_cost"])

	resp, out = e.do(t, "GET", "/v1/settings/endpoint", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, true, out["endpoint"].(map[string]any)["connected"])

	// Validation rejects: bad azure host, non-loopback local, custom http.
	for _, body := range []map[string]any{
		{"preset": "azure", "base_url": "https://x.example.com/openai/v1", "connection": "default", "run_model": "m"},
		{"preset": "local", "base_url": "https://api.example.com/v1", "run_model": "m"},
		{"preset": "custom", "base_url": "http://corp.internal/v1", "connection": "default", "run_model": "m"},
	} {
		resp, _ = e.do(t, "PUT", "/v1/settings/endpoint", body)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, body["preset"])
	}

	// The switch was recorded as a governance event.
	evs, err := e.store.ListTenantEvents(context.Background(), e.tenant.ID)
	require.NoError(t, err)
	require.Len(t, evs, 1)
	require.Equal(t, "endpoint.switched", evs[0].Type)
}

func TestApproveOnCustomEndpointNeedsUserPrice(t *testing.T) {
	e := newEnv(t, withCatalog(t))
	putLLMKey(t, e, "custom", "default")
	resp, _ := e.do(t, "PUT", "/v1/settings/endpoint",
		map[string]any{"preset": "custom", "base_url": "https://llm.example/v1", "connection": "default", "run_model": "my-model"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	b := workflowBody()
	b["permit"] = map[string]any{"v": 1, "llm": map[string]any{"providers": []string{"custom"}},
		"spend": map[string]any{"per_run_cents": 10}}
	resp, out := e.do(t, "POST", "/v1/workflows", b)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)

	// Unpriced: the gate holds, as one form instead of a dead end.
	resp, out = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Equal(t, "unpriced_model", out["error"])
	require.Equal(t, "my-model", out["model"])
	require.Equal(t, "https://llm.example/v1", out["base_url"])

	// Enter the two numbers; approval compiles them in.
	resp, _ = e.do(t, "PUT", "/v1/settings/prices",
		map[string]any{"base_url": "https://llm.example/v1", "model": "my-model",
			"input_cents_per_1m": 100, "output_cents_per_1m": 400})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	v, err := e.store.GetVersion(context.Background(), e.tenant.ID, uuid.MustParse(id), 1)
	require.NoError(t, err)
	var compiled steps.Compiled
	require.NoError(t, json.Unmarshal(v.Compiled, &compiled))
	require.Equal(t, "custom", compiled.Provider)
	require.Equal(t, "my-model", compiled.Model)
	require.Equal(t, "https://llm.example/v1", compiled.Endpoint)
	require.Equal(t, "custom", compiled.EndpointPreset)
	require.Equal(t, 100, compiled.PriceInCentsPer1M)
	require.Equal(t, 400, compiled.PriceOutCentsPer1M)
	// max_tokens derived from the user-entered out-price: 10c * 1e6 / 400.
	require.Equal(t, 8192, compiled.MaxTokens) // clamped to the ceiling
}

func TestApproveOnLocalEndpointSkipsGate(t *testing.T) {
	e := newEnv(t, withCatalog(t))
	resp, _ := e.do(t, "PUT", "/v1/settings/endpoint",
		map[string]any{"preset": "local", "base_url": "http://127.0.0.1:11434/v1", "run_model": "llama3"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	b := workflowBody()
	b["permit"] = map[string]any{"v": 1, "llm": map[string]any{"providers": []string{"local"}}}
	resp, out := e.do(t, "POST", "/v1/workflows", b)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)

	resp, _ = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	v, err := e.store.GetVersion(context.Background(), e.tenant.ID, uuid.MustParse(id), 1)
	require.NoError(t, err)
	var compiled steps.Compiled
	require.NoError(t, json.Unmarshal(v.Compiled, &compiled))
	require.Equal(t, "local", compiled.EndpointPreset)
	require.Zero(t, compiled.PriceInCentsPer1M)
	require.Zero(t, compiled.PriceOutCentsPer1M)
	require.Equal(t, 4096, compiled.MaxTokens, "local falls back to the fixed default")
}

func TestEndpointSwitchGatesAndRecompiles(t *testing.T) {
	e := newEnv(t, withCatalog(t))
	putLLMKey(t, e, "anthropic", "default")
	resp, _ := e.do(t, "PUT", "/v1/settings/endpoint",
		map[string]any{"preset": "anthropic", "connection": "default", "run_model": "claude-haiku-4-5"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Approve a workflow whose permit allows anthropic + custom.
	b := workflowBody()
	b["permit"] = map[string]any{"v": 1, "llm": map[string]any{"providers": []string{"anthropic", "custom"}}}
	resp, out := e.do(t, "POST", "/v1/workflows", b)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)
	resp, _ = e.do(t, "POST", "/v1/workflows/"+id+"/versions/1/approve", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Switch to an unpriced custom endpoint: 409, endpoint unchanged.
	putLLMKey(t, e, "custom", "default")
	resp, out = e.do(t, "PUT", "/v1/settings/endpoint",
		map[string]any{"preset": "custom", "base_url": "https://llm.example/v1", "connection": "default", "run_model": "my-model"})
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	require.Equal(t, "unpriced_models", out["error"])
	_, out = e.do(t, "GET", "/v1/settings/endpoint", nil)
	require.Equal(t, "https://api.anthropic.com", out["endpoint"].(map[string]any)["base_url"])

	// Price it, switch again: 200, approved version recompiled, event recorded.
	resp, _ = e.do(t, "PUT", "/v1/settings/prices",
		map[string]any{"base_url": "https://llm.example/v1", "model": "my-model",
			"input_cents_per_1m": 50, "output_cents_per_1m": 200})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = e.do(t, "PUT", "/v1/settings/endpoint",
		map[string]any{"preset": "custom", "base_url": "https://llm.example/v1", "connection": "default", "run_model": "my-model"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	v, err := e.store.GetVersion(context.Background(), e.tenant.ID, uuid.MustParse(id), 1)
	require.NoError(t, err)
	var compiled steps.Compiled
	require.NoError(t, json.Unmarshal(v.Compiled, &compiled))
	require.Equal(t, "custom", compiled.Provider)
	require.Equal(t, "https://llm.example/v1", compiled.Endpoint)
	require.Equal(t, 200, compiled.PriceOutCentsPer1M)

	evs, err := e.store.ListTenantEvents(context.Background(), e.tenant.ID)
	require.NoError(t, err)
	require.Len(t, evs, 2)

	// A switch to a provider the approved permit does not allow: 409.
	putLLMKey(t, e, "openai", "default")
	resp, out = e.do(t, "PUT", "/v1/settings/endpoint",
		map[string]any{"preset": "openai", "connection": "default", "run_model": "gpt-4o-mini"})
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	require.Equal(t, "provider_not_permitted", out["error"])
	require.Contains(t, out["workflows"], id)
}

func TestBudgetSettings(t *testing.T) {
	e := newEnv(t, withCatalog(t))
	resp, out := e.do(t, "GET", "/v1/settings/budget", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Nil(t, out["monthly_cap_cents"])

	resp, _ = e.do(t, "PUT", "/v1/settings/budget", map[string]any{"monthly_cap_cents": 500})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, out = e.do(t, "GET", "/v1/settings/budget", nil)
	require.Equal(t, float64(500), out["monthly_cap_cents"])

	resp, _ = e.do(t, "PUT", "/v1/settings/budget", map[string]any{"monthly_cap_cents": -1})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// fakeLLM answers the verify call's one minimal chat request.
func fakeLLM(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func verifyEnv(t *testing.T, capCents int) *env {
	t.Helper()
	return newEnv(t, func(d *httpapi.Deps) {
		d.LLMVerify = &llmverify.Client{}
		d.Meter = &meter.Meter{Store: d.Store, DefaultCap: capCents}
	})
}

func TestEndpointVerifyRecordsMeteredCost(t *testing.T) {
	ts := fakeLLM(t, 200, `{"usage":{"prompt_tokens":20000,"completion_tokens":1000}}`)
	e := verifyEnv(t, 0)

	// A user-entered price for the candidate (endpoint, model): $1/M in,
	// $5/M out — 20000*100 + 1000*500 = 2.5M micro-cents → 2 cents,
	// floored once on the combined numerator like llm.CostCents.
	resp, _ := e.do(t, "PUT", "/v1/settings/prices", map[string]any{
		"base_url": ts.URL, "model": "test-model",
		"input_cents_per_1m": 100, "output_cents_per_1m": 500,
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, out := e.do(t, "POST", "/v1/settings/endpoint/verify", map[string]any{
		"preset": "custom", "base_url": ts.URL, "run_model": "test-model", "value": "sk-candidate",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, true, out["ok"])
	require.Equal(t, float64(2), out["cost_cents"])
	require.Equal(t, true, out["recorded"])

	spent, err := e.store.MonthSpendCents(context.Background(), e.tenant.ID, meter.MonthStartUTC(time.Now().UTC()))
	require.NoError(t, err)
	require.Equal(t, 2, spent)
}

func TestEndpointVerifyUnpricedRecordsZeroWithTokens(t *testing.T) {
	ts := fakeLLM(t, 200, `{"usage":{"prompt_tokens":12,"completion_tokens":1}}`)
	e := verifyEnv(t, 0)

	resp, out := e.do(t, "POST", "/v1/settings/endpoint/verify", map[string]any{
		"preset": "custom", "base_url": ts.URL, "run_model": "unpriced-model", "value": "sk-candidate",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, true, out["ok"])
	require.Equal(t, float64(0), out["cost_cents"])

	// The row exists with honest token counts even at zero cost.
	var n, inTok int
	err := e.pool.QueryRow(context.Background(),
		`SELECT COUNT(*), COALESCE(MAX(input_tokens),0) FROM spend_entry WHERE tenant_id=$1`,
		e.tenant.ID).Scan(&n, &inTok)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 12, inTok)
}

func TestEndpointVerifyBadKeyRecordsNothing(t *testing.T) {
	ts := fakeLLM(t, 401, `{"error":{"message":"Incorrect API key"}}`)
	e := verifyEnv(t, 0)

	resp, out := e.do(t, "POST", "/v1/settings/endpoint/verify", map[string]any{
		"preset": "custom", "base_url": ts.URL, "run_model": "m", "value": "sk-bad",
	})
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Equal(t, "verify_failed", out["error"])

	var n int
	err := e.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM spend_entry WHERE tenant_id=$1`, e.tenant.ID).Scan(&n)
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestEndpointVerifyRefusedOverBudget(t *testing.T) {
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	t.Cleanup(ts.Close)
	e := verifyEnv(t, 1) // 1-cent budget…
	require.NoError(t, e.store.RecordSpend(context.Background(), e.tenant.ID,
		"endpoint_verify", 1, 0, 0, "https://x", "m")) // …already spent

	resp, out := e.do(t, "POST", "/v1/settings/endpoint/verify", map[string]any{
		"preset": "custom", "base_url": ts.URL, "run_model": "m", "value": "sk-x",
	})
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.Contains(t, out["message"], "budget")
	require.Zero(t, hits, "an over-budget verify must not spend")
}

func TestEndpointVerifyLocalNeedsNoKey(t *testing.T) {
	ts := fakeLLM(t, 200, `{"usage":{"prompt_tokens":5,"completion_tokens":1}}`)
	e := verifyEnv(t, 0)

	resp, out := e.do(t, "POST", "/v1/settings/endpoint/verify", map[string]any{
		"preset": "local", "base_url": ts.URL, "run_model": "llama",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, true, out["ok"])
	require.Equal(t, float64(0), out["cost_cents"])
}

func TestEndpointVerifyValidation(t *testing.T) {
	e := verifyEnv(t, 0)
	resp, _ := e.do(t, "POST", "/v1/settings/endpoint/verify", map[string]any{
		"preset": "custom", "base_url": "https://x.example.com", "value": "k",
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "run_model required")
	resp, _ = e.do(t, "POST", "/v1/settings/endpoint/verify", map[string]any{
		"preset": "nope", "base_url": "https://x.example.com", "run_model": "m", "value": "k",
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "unknown preset")
}

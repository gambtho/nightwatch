// Settings surface: the configured LLM endpoint (switching it is a
// recorded governance act that re-runs the pricing gate and recompiles),
// user-entered model prices, and the user's local budget.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/gambtho/tomte/server/internal/endpoint"
	"github.com/gambtho/tomte/server/internal/llm"
	"github.com/gambtho/tomte/server/internal/permit"
	"github.com/gambtho/tomte/server/internal/steps"
	"github.com/gambtho/tomte/server/internal/store"
)

// currentEndpoint loads the tenant's endpoint; (nil, nil) is legacy env
// mode (no record configured).
func (d Deps) currentEndpoint(r *http.Request, tenantID uuid.UUID) (*endpoint.Endpoint, error) {
	le, err := d.Store.GetLLMEndpoint(r.Context(), tenantID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	conn := ""
	if le.ConnectionName != nil {
		conn = *le.ConnectionName
	}
	return &endpoint.Endpoint{
		Preset: le.Preset, Kind: le.Kind, BaseURL: le.BaseURL,
		Connection: conn, RunModel: le.RunModel, ZeroCost: le.ZeroCost,
	}, nil
}

// resolvePrices runs the reworked priced-pair gate for one endpoint:
// zero-cost endpoints skip it (prices 0/0); priced presets consult the
// bundled table; misses fall through to the user-entered row keyed by the
// endpoint's canonical base URL. A miss everywhere returns the
// machine-readable body the frontend turns into the inline price form.
func (d Deps) resolvePrices(r *http.Request, tenantID uuid.UUID, e *endpoint.Endpoint) (inCents, outCents int, errBody map[string]any, err error) {
	if e.ZeroCost {
		return 0, 0, nil, nil
	}
	if in, out, ok := llm.Price(e.Provider(), e.RunModel); ok {
		return in, out, nil, nil
	}
	in, out, gerr := d.Store.GetModelPrice(r.Context(), tenantID, e.BaseURL, e.RunModel)
	if errors.Is(gerr, store.ErrNotFound) {
		return 0, 0, map[string]any{
			"error": "unpriced_model", "model": e.RunModel, "base_url": e.BaseURL,
		}, nil
	}
	if gerr != nil {
		return 0, 0, nil, gerr
	}
	return in, out, nil, nil
}

// platformFor builds the compile-time Platform for one approval on this
// endpoint, with the gate's prices already resolved.
func platformFor(e *endpoint.Endpoint, inCents, outCents, perRunCents int) steps.Platform {
	return steps.Platform{
		Provider:           e.Provider(),
		Model:              e.RunModel,
		MaxTokens:          llm.MaxTokensForOutPrice(outCents, perRunCents),
		Endpoint:           e.BaseURL,
		EndpointPreset:     e.Preset,
		PriceInCentsPer1M:  inCents,
		PriceOutCentsPer1M: outCents,
	}
}

func endpointJSON(e *endpoint.Endpoint, connected bool) map[string]any {
	return map[string]any{
		"preset": e.Preset, "kind": e.Kind, "base_url": e.BaseURL,
		"connection": e.Connection, "run_model": e.RunModel,
		"zero_cost": e.ZeroCost, "connected": connected,
	}
}

func (d Deps) getEndpoint(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	e, err := d.currentEndpoint(r, claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no endpoint configured"})
		return
	}
	connected := e.Preset == endpoint.PresetLocal
	if !connected {
		_, cerr := d.Store.GetConnection(r.Context(), claims.TenantID, e.Provider(), e.Connection)
		connected = cerr == nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoint": endpointJSON(e, connected)})
}

// putEndpoint is the governance switch ("Endpoint agnosticism"): validate,
// gate every approved version against the NEW endpoint (prices, permit
// provider allowlists, credential), then apply endpoint + recompilations +
// the endpoint.switched event in one store transaction — or nothing.
func (d Deps) putEndpoint(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, maxDocBytes)
	var body struct {
		Preset     string `json:"preset"`
		BaseURL    string `json:"base_url"`
		Connection string `json:"connection"`
		RunModel   string `json:"run_model"`
		ZeroCost   bool   `json:"zero_cost"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	e, err := endpoint.Validate(endpoint.Endpoint{
		Preset: body.Preset, BaseURL: body.BaseURL, Connection: body.Connection,
		RunModel: body.RunModel, ZeroCost: body.ZeroCost,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Credential first: a switch to an endpoint whose key was never
	// pasted would approve nothing and break every run.
	if e.Preset != endpoint.PresetLocal {
		if _, cerr := d.Store.GetConnection(r.Context(), claims.TenantID, e.Provider(), e.Connection); cerr != nil {
			if errors.Is(cerr, store.ErrNotFound) {
				writeJSON(w, http.StatusConflict, map[string]any{
					"error": "connection_missing", "provider": e.Provider(), "connection": e.Connection,
				})
				return
			}
			writeErr(w, cerr)
			return
		}
	}

	// The switch-time gate + recompilation, per approved version.
	versions, err := d.Store.ListApprovedVersions(r.Context(), claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	inCents, outCents, priceErr, err := d.resolvePrices(r, claims.TenantID, &e)
	if err != nil {
		writeErr(w, err)
		return
	}
	if priceErr != nil && len(versions) > 0 {
		priceErr["error"] = "unpriced_models"
		priceErr["models"] = []map[string]any{{"model": e.RunModel, "base_url": e.BaseURL}}
		writeJSON(w, http.StatusConflict, priceErr)
		return
	}
	var notPermitted []string
	var updates []store.CompiledUpdate
	for _, v := range versions {
		p, perr := permit.Parse(v.Doc.Permit)
		if perr != nil || !p.AllowsProvider(e.Provider()) {
			notPermitted = append(notPermitted, v.WorkflowID.String())
			continue
		}
		doc, derr := steps.Parse(v.Doc.Steps)
		if derr != nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "invalid_steps", "workflow": v.WorkflowID.String(),
			})
			return
		}
		perRunCents := 0
		if p.Spend != nil {
			perRunCents = p.Spend.PerRunCents
		}
		compiled, merr := json.Marshal(steps.Compile(doc, v.Doc.Rubric, platformFor(&e, inCents, outCents, perRunCents)))
		if merr != nil {
			writeErr(w, merr)
			return
		}
		updates = append(updates, store.CompiledUpdate{
			WorkflowID: v.WorkflowID, Version: v.Number, Compiled: compiled,
		})
	}
	if len(notPermitted) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "provider_not_permitted", "provider": e.Provider(), "workflows": notPermitted,
		})
		return
	}

	old, err := d.currentEndpoint(r, claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	payload := map[string]any{
		"to": e.BaseURL, "preset": e.Preset, "zero_cost": e.ZeroCost,
		"run_model": e.RunModel, "by": claims.UserID,
	}
	if old != nil {
		payload["from"] = old.BaseURL
		payload["from_preset"] = old.Preset
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		writeErr(w, err)
		return
	}
	le := store.LLMEndpoint{
		Preset: e.Preset, Kind: e.Kind, BaseURL: e.BaseURL,
		RunModel: e.RunModel, ZeroCost: e.ZeroCost,
	}
	if e.Connection != "" {
		le.ConnectionName = &e.Connection
	}
	if err := d.Store.SwitchLLMEndpoint(r.Context(), claims.TenantID, le, updates, rawPayload); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoint": endpointJSON(&e, true)})
}

func (d Deps) listPrices(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	prices, err := d.Store.ListModelPrices(r.Context(), claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(prices))
	for _, p := range prices {
		out = append(out, map[string]any{
			"base_url": p.BaseURL, "model": p.Model,
			"input_cents_per_1m": p.InputCentsPer1M, "output_cents_per_1m": p.OutputCentsPer1M,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"prices": out})
}

// putPrice stores a user-entered price. base_url is explicit — never "the
// current endpoint" — so switch-time pricing can target the endpoint
// being switched TO before the switch happens.
func (d Deps) putPrice(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, maxDocBytes)
	var body struct {
		BaseURL          string `json:"base_url"`
		Model            string `json:"model"`
		InputCentsPer1M  *int   `json:"input_cents_per_1m"`
		OutputCentsPer1M *int   `json:"output_cents_per_1m"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.Model == "" || body.InputCentsPer1M == nil || body.OutputCentsPer1M == nil ||
		*body.InputCentsPer1M < 0 || *body.OutputCentsPer1M < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "base_url, model, input_cents_per_1m, output_cents_per_1m required"})
		return
	}
	canon, err := endpoint.Canonical(body.BaseURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := d.Store.UpsertModelPrice(r.Context(), claims.TenantID, canon, body.Model,
		*body.InputCentsPer1M, *body.OutputCentsPer1M); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"price": map[string]any{
		"base_url": canon, "model": body.Model,
		"input_cents_per_1m": *body.InputCentsPer1M, "output_cents_per_1m": *body.OutputCentsPer1M,
	}})
}

// The budget: tenant.monthly_cap_cents reinterpreted as the user's local
// budget — how much Tomte may spend from their key per month. Same
// enforcement as always (meter pre-call check, scheduler skip).
func (d Deps) getBudget(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	tn, err := d.Store.GetTenant(r.Context(), claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"monthly_cap_cents": tn.MonthlyCapCents})
}

func (d Deps) putBudget(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, maxDocBytes)
	var body struct {
		MonthlyCapCents *int `json:"monthly_cap_cents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		(body.MonthlyCapCents != nil && *body.MonthlyCapCents < 0) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "monthly_cap_cents must be a non-negative integer or null"})
		return
	}
	if err := d.Store.SetTenantMonthlyCap(r.Context(), claims.TenantID, body.MonthlyCapCents); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"monthly_cap_cents": body.MonthlyCapCents})
}

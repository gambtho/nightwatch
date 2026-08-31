package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/gambtho/tomte/server/internal/endpoint"
)

// verifyEndpoint is the first-run key check (pivot spec, "First run"):
// one disclosed, minimal live call with the CANDIDATE endpoint and key —
// nothing is saved by this handler. The call is metered: its cost lands
// in the spend ledger so it counts against the month once a budget
// exists, and an exhausted budget refuses the call outright (fail-closed
// consistency with every other spend path).
func (d Deps) verifyEndpoint(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, maxDocBytes)
	var body struct {
		Preset   string `json:"preset"`
		BaseURL  string `json:"base_url"`
		RunModel string `json:"run_model"`
		ZeroCost bool   `json:"zero_cost"`
		Value    string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	// The candidate endpoint passes the same validation the settings
	// switch applies; the connection name is a placeholder — the key is
	// inline and deliberately not stored yet.
	conn := "candidate"
	if body.Preset == endpoint.PresetLocal {
		conn = ""
	}
	e, err := endpoint.Validate(endpoint.Endpoint{
		Preset: body.Preset, BaseURL: body.BaseURL, Connection: conn,
		RunModel: body.RunModel, ZeroCost: body.ZeroCost,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if e.CredentialHeader() != "" && body.Value == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "value required"})
		return
	}

	// Budget first: an exhausted budget refuses even the sub-cent verify.
	if d.Meter != nil {
		over, oerr := d.Meter.OverCap(r.Context(), claims.TenantID)
		if oerr != nil {
			writeErr(w, oerr)
			return
		}
		if over {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error":   "verify_failed",
				"message": "Your monthly budget is used up, so the test call wasn't made. Raise the budget in settings or wait for the 1st.",
			})
			return
		}
	}

	if d.LLMVerify == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "verification unavailable"})
		return
	}
	res, err := d.LLMVerify.Verify(r.Context(), e, body.Value)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !res.OK {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error": "verify_failed", "message": res.Message,
		})
		return
	}

	// Price the call like a run would be priced: zero-cost endpoints and
	// bundled/user-entered rows via the same gate helper. An unpriced
	// model records honest token counts at zero cents — the price form
	// hasn't been offered yet at first run.
	inCents, outCents, priceMiss, err := d.resolvePrices(r, claims.TenantID, &e)
	if err != nil {
		writeErr(w, err)
		return
	}
	cost := 0
	if priceMiss == nil {
		cost = costCentsFor(inCents, outCents, res.Usage.InputTokens, res.Usage.OutputTokens)
	}
	if err := d.Store.RecordSpend(r.Context(), claims.TenantID, "endpoint_verify",
		cost, res.Usage.InputTokens, res.Usage.OutputTokens, e.BaseURL, e.RunModel); err != nil {
		// A metered call must be recorded; failing open here would be an
		// unrecorded spend.
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cost_cents": cost, "recorded": true})
}

// costCentsFor floors once on the combined numerator, like llm.CostCents.
func costCentsFor(inCentsPer1M, outCentsPer1M, inTokens, outTokens int) int {
	numerator := int64(inTokens)*int64(inCentsPer1M) + int64(outTokens)*int64(outCentsPer1M)
	return int(numerator / 1_000_000)
}

package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
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

	// The budget gate is load-bearing here; a wiring hole must fail
	// closed, not skip the check.
	if d.LLMVerify == nil || d.Meter == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "verification unavailable"})
		return
	}
	res, err := d.LLMVerify.Verify(r.Context(), e, body.Value)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Price the call like a run would be priced (same gate helper). From
	// here to the ledger write, nothing may skip recording a BILLED call:
	// the upstream already executed it, so a pricing error degrades to an
	// unpriced (zero-cent, flagged) entry rather than an unrecorded one.
	cost, unpriced := 0, false
	inCents, outCents, priceMiss, perr := d.resolvePrices(r, claims.TenantID, &e)
	switch {
	case perr != nil:
		slog.Error("llmverify: price resolution failed; recording verify at zero",
			"tenant", claims.TenantID, "base_url", e.BaseURL, "model", e.RunModel,
			"input_tokens", res.Usage.InputTokens, "output_tokens", res.Usage.OutputTokens, "err", perr)
		unpriced = true
	case priceMiss != nil:
		// The price form hasn't been offered yet at first run; honest
		// token counts at zero cents, flagged so "0" never reads as free.
		unpriced = true
	default:
		cost = costCentsFor(inCents, outCents, res.Usage.InputTokens, res.Usage.OutputTokens)
		if !e.ZeroCost && cost == 0 {
			// The ledger rounds up: a 1-token verify on a priced endpoint
			// is sub-cent, and flooring it to free would let repeated
			// verifies spend real money without ever advancing the budget.
			cost = 1
		}
	}

	if res.Billed {
		// Disconnect-immune: the provider call already happened, so a
		// client that gave up must not leave the spend unrecorded.
		if rerr := d.Store.RecordSpend(context.WithoutCancel(r.Context()), claims.TenantID,
			"endpoint_verify", cost, res.Usage.InputTokens, res.Usage.OutputTokens,
			e.BaseURL, e.RunModel); rerr != nil {
			slog.Error("llmverify: spend record failed",
				"tenant", claims.TenantID, "base_url", e.BaseURL, "model", e.RunModel,
				"cost_cents", cost, "input_tokens", res.Usage.InputTokens,
				"output_tokens", res.Usage.OutputTokens, "err", rerr)
			if res.OK {
				// A metered success must be recorded; failing open would
				// be an unrecorded spend.
				writeErr(w, rerr)
				return
			}
		}
	}

	if !res.OK {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error": "verify_failed", "message": res.Message,
		})
		return
	}
	out := map[string]any{"ok": true, "cost_cents": cost, "recorded": true}
	if unpriced {
		out["unpriced"] = true
	}
	writeJSON(w, http.StatusOK, out)
}

// costCentsFor rounds UP on the combined numerator — the ledger's safe
// direction (llm.CostCents floors for runs; a verify must never record a
// paid call as free).
func costCentsFor(inCentsPer1M, outCentsPer1M, inTokens, outTokens int) int {
	numerator := int64(inTokens)*int64(inCentsPer1M) + int64(outTokens)*int64(outCentsPer1M)
	return int((numerator + 999_999) / 1_000_000)
}

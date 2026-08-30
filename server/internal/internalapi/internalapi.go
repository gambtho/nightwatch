// Package internalapi is the harness-facing API: run context out, run
// records in. Substrate exposes no log or event retrieval API, so run
// records exist only because the harness pushes them here.
package internalapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/token"
)

type Deps struct {
	Store  *store.Store
	Signer *token.Signer
}

func RegisterRoutes(mux *http.ServeMux, d Deps) {
	mux.Handle("GET /internal/runs/{id}/context", d.auth(d.runContext))
	mux.Handle("POST /internal/runs/{id}/events", d.auth(d.appendEvent))
	mux.Handle("POST /internal/runs/{id}/finalize", d.auth(d.finalize))
}

type authedHandler func(w http.ResponseWriter, r *http.Request, claims token.RunClaims)

// The harness pushes small JSON records, not data; cap every body. Per-run
// event and output budgets are the metering plan's concern (Plan 3).
const maxBodyBytes = 1 << 20

// auth verifies the bearer run-JWT, requires that the token's run is the
// run in the path (a runner can only touch its own run), and requires the
// bearer to be the exact token minted for that run — the stored hash binds
// the JWT to the row and clearing it revokes the token.
func (d Deps) auth(next authedHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims, err := d.Signer.Verify(bearer)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		pathID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad run id", http.StatusBadRequest)
			return
		}
		if claims.RunID != pathID {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		run, err := d.Store.GetRun(r.Context(), claims.TenantID, claims.RunID)
		if err != nil {
			d.fail(w, err)
			return
		}
		if !token.EqualHash(d.Signer.HashToken(bearer), run.TokenHash) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r, claims)
	})
}

func (d Deps) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	slog.Error("internalapi: internal error", "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (d Deps) runContext(w http.ResponseWriter, r *http.Request, claims token.RunClaims) {
	ctx := r.Context()
	run, err := d.Store.GetRun(ctx, claims.TenantID, claims.RunID)
	if err != nil {
		d.fail(w, err)
		return
	}
	version, err := d.Store.GetVersion(ctx, claims.TenantID, run.WorkflowID, run.Version)
	if err != nil {
		d.fail(w, err)
		return
	}
	if err := d.Store.MarkRunRunning(ctx, claims.TenantID, claims.RunID); err != nil {
		d.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"run_id": run.ID,
		"steps":  version.Doc.Steps,
	}); err != nil {
		slog.Error("internalapi: encode context", "err", err)
	}
}

func (d Deps) appendEvent(w http.ResponseWriter, r *http.Request, claims token.RunClaims) {
	var body struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Type == "" {
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}
	if err := d.Store.AppendRunEvent(r.Context(), claims.TenantID, claims.RunID, body.Type, body.Payload); err != nil {
		d.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) finalize(w http.ResponseWriter, r *http.Request, claims token.RunClaims) {
	var body struct {
		Status    string `json:"status"`
		ErrorKind string `json:"error_kind"`
		ErrorMsg  string `json:"error_msg"`
		Output    string `json:"output"`
		TokensIn  int    `json:"tokens_in"`
		TokensOut int    `json:"tokens_out"`
		CostCents int    `json:"cost_cents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad finalize", http.StatusBadRequest)
		return
	}
	if body.Status != "succeeded" && body.Status != "failed" {
		http.Error(w, "bad status", http.StatusBadRequest)
		return
	}
	_, err := d.Store.FinalizeRun(r.Context(), claims.TenantID, claims.RunID, store.RunFinal{
		Status: body.Status, ErrorKind: body.ErrorKind, ErrorMsg: body.ErrorMsg,
		Output: body.Output, TokensIn: body.TokensIn, TokensOut: body.TokensOut,
		CostCents: body.CostCents,
	})
	if err != nil {
		d.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

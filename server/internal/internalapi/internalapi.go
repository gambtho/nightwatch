// Package internalapi is the harness-facing API: run context out, run
// records in. Nothing scrapes the compute host's logs, so run records
// exist only because the harness pushes them here.
package internalapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/tomte/server/internal/catalog"
	"github.com/gambtho/tomte/server/internal/permit"
	"github.com/gambtho/tomte/server/internal/steps"
	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/token"
)

type Deps struct {
	Store  *store.Store
	Signer *token.Signer
	// Catalog backs the run context's tools projection. Nil serves runs
	// with no tools (permits without connections work unchanged).
	Catalog *catalog.Catalog
}

func RegisterRoutes(mux *http.ServeMux, d Deps) {
	mux.Handle("GET /internal/runs/{id}/context", d.auth(d.runContext))
	mux.Handle("POST /internal/runs/{id}/events", d.auth(d.appendEvent))
	mux.Handle("POST /internal/runs/{id}/finalize", d.auth(d.finalize))
}

type authedHandler func(w http.ResponseWriter, r *http.Request, claims token.RunClaims)

// The harness pushes small JSON records, not data; cap every body. Per-run
// event and output budgets are the meter's concern.
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
	// The context serves the compiled execution form, never the user-facing
	// steps artifact (decision 9). Runs fire only approved versions and
	// approval writes compiled transactionally, so a missing document is
	// corrupt data, not a state to run from.
	if len(version.Compiled) == 0 {
		d.fail(w, fmt.Errorf("run %s: version %d has no compiled document", run.ID, run.Version))
		return
	}
	var compiled steps.Compiled
	if err := json.Unmarshal(version.Compiled, &compiled); err != nil {
		d.fail(w, err)
		return
	}
	// Unmarshal accepts JSONB null and shape-mismatched documents by
	// zeroing fields; an empty provider or model cannot run, so refuse
	// before marking the run running rather than hand the harness an
	// unusable context. (Migrated compiler_v 0 rows always carry both —
	// the old API's pricing gate required them.)
	if compiled.Provider == "" || compiled.Model == "" {
		d.fail(w, fmt.Errorf("run %s: version %d compiled document lacks provider/model", run.ID, run.Version))
		return
	}
	compiled.Kickoff += "\n\n" + occasion(run)

	// The tools array is server-derived from the approved permit joined
	// with the running catalog: the harness stays a dumb executor — it
	// never sees the permit, never holds a credential, and cannot grant
	// itself a tool the control plane did not project.
	tools, err := d.projectTools(version.Doc.Permit)
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
		"steps":  compiled,
		"tools":  tools,
	}); err != nil {
		slog.Error("internalapi: encode context", "err", err)
	}
}

type toolJSON struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// projectTools turns permit connections into the harness tool list:
// {connector}__{op}, catalog copy and schema verbatim, in stable order.
// An op the catalog no longer defines projects nothing — its invocation
// would 403 at the proxy anyway, and a missing tool is honest about
// that; the drop is logged because it means a permit outlived its op.
func (d Deps) projectTools(rawPermit []byte) ([]toolJSON, error) {
	p, err := permit.Parse(rawPermit)
	if err != nil {
		return nil, fmt.Errorf("projecting tools: %w", err)
	}
	if len(p.Connections) == 0 {
		return []toolJSON{}, nil
	}
	if d.Catalog == nil {
		return nil, fmt.Errorf("projecting tools: permit has connections but no catalog is configured")
	}
	keys := make([]string, 0, len(p.Connections))
	for key := range p.Connections {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []toolJSON{}
	for _, key := range keys {
		entry := p.Connections[key]
		ops := append([]string(nil), entry.Ops...)
		sort.Strings(ops)
		for _, opName := range ops {
			_, op, ok := d.Catalog.Op(key, opName)
			if !ok {
				slog.Warn("internalapi: permit op absent from catalog, not projected",
					"connector", key, "op", opName)
				continue
			}
			out = append(out, toolJSON{
				Name:        key + "__" + opName,
				Description: op.Description,
				InputSchema: op.ArgsSchema,
			})
		}
	}
	return out, nil
}

// occasion names what fired the run — fixed text assembled from the run
// row, so the kickoff stays deterministic template output.
func occasion(run store.Run) string {
	if run.FireReason == "schedule" && run.FireTime != nil {
		return "This run is the scheduled occurrence for " +
			run.FireTime.UTC().Format(time.RFC3339) + "."
	}
	return "This run was fired manually."
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
	perRunCap := 0
	if run, err := d.Store.GetRun(r.Context(), claims.TenantID, claims.RunID); err == nil {
		if version, err := d.Store.GetVersion(r.Context(), claims.TenantID, run.WorkflowID, run.Version); err == nil {
			if p, err := permit.Parse(version.Doc.Permit); err == nil && p.Spend != nil {
				perRunCap = p.Spend.PerRunCents
			}
		}
	}
	// Cap resolution is best-effort: permits were validated at creation, so
	// a parse failure here means corrupt data — finalization must still
	// succeed (with no overrun event) rather than strand the run.
	_, err := d.Store.FinalizeRun(r.Context(), claims.TenantID, claims.RunID, store.RunFinal{
		Status: body.Status, ErrorKind: body.ErrorKind, ErrorMsg: body.ErrorMsg,
		Output: body.Output, TokensIn: body.TokensIn, TokensOut: body.TokensOut,
		CostCents: body.CostCents,
	}, perRunCap)
	if err != nil {
		d.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

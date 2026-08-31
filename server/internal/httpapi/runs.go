package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/compute"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/token"
)

const runTokenTTL = time.Hour

type runJSON struct {
	ID         uuid.UUID  `json:"id"`
	WorkflowID uuid.UUID  `json:"workflow_id"`
	Version    int        `json:"version"`
	Status     string     `json:"status"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	TokensIn   *int       `json:"tokens_in,omitempty"`
	TokensOut  *int       `json:"tokens_out,omitempty"`
	CostCents  *int       `json:"cost_cents,omitempty"`
	ErrorKind  *string    `json:"error_kind,omitempty"`
	ErrorMsg   *string    `json:"error_msg,omitempty"`
	Output     *string    `json:"output,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toRunJSON(r store.Run) runJSON {
	return runJSON{
		ID: r.ID, WorkflowID: r.WorkflowID, Version: r.Version, Status: r.Status,
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		TokensIn: r.TokensIn, TokensOut: r.TokensOut, CostCents: r.CostCents,
		ErrorKind: r.ErrorKind, ErrorMsg: r.ErrorMsg, Output: r.Output,
		CreatedAt: r.CreatedAt,
	}
}

func (d Deps) fireRun(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	wfID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	version, err := d.Store.GetApprovedVersion(r.Context(), claims.TenantID, wfID)
	if errors.Is(err, store.ErrNotFound) {
		// Distinguish "no workflow" (404) from "no approved version" (409).
		if _, wfErr := d.Store.GetWorkflow(r.Context(), claims.TenantID, wfID); wfErr != nil {
			writeErr(w, wfErr)
			return
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "workflow has no approved version"})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	runID := uuid.New()
	bearer, hash, err := d.Signer.Sign(token.RunClaims{
		RunID: runID, TenantID: claims.TenantID,
		ExpiresAt: time.Now().Add(runTokenTTL),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	run, err := d.Store.CreateRun(r.Context(), claims.TenantID, wfID, runID, version.Number, hash, "manual", nil)
	if err != nil {
		writeErr(w, err)
		return
	}

	actor, err := d.Compute.EnsureActor(r.Context(),
		compute.WorkflowRef{TenantID: claims.TenantID, WorkflowID: wfID},
		compute.TemplateRef{Name: "harness-v1"})
	if err != nil {
		d.failDispatch(r.Context(), claims.TenantID, runID, err)
		writeErr(w, err)
		return
	}
	if _, err := d.Compute.Invoke(r.Context(), actor,
		compute.InvokeRequest{RunID: runID, RunToken: bearer}); err != nil {
		d.failDispatch(r.Context(), claims.TenantID, runID, err)
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run": toRunJSON(run)})
}

// failDispatch records a run that never reached its actor; without this a
// dispatch failure would leave the run pending forever. It strips
// cancellation from the request context so a client disconnect mid-fire
// doesn't also abort the finalize write.
func (d Deps) failDispatch(ctx context.Context, tenantID, runID uuid.UUID, cause error) {
	ctx = context.WithoutCancel(ctx)
	if _, err := d.Store.FinalizeRun(ctx, tenantID, runID, store.RunFinal{
		Status: "failed", ErrorKind: "dispatch_failed", ErrorMsg: cause.Error(),
	}, 0); err != nil {
		slog.Error("httpapi: record dispatch failure", "run", runID, "err", err)
	}
}

func (d Deps) getRun(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	run, err := d.Store.GetRun(r.Context(), claims.TenantID, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": toRunJSON(run)})
}

func (d Deps) listRuns(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	wfID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if _, err := d.Store.GetWorkflow(r.Context(), claims.TenantID, wfID); err != nil {
		writeErr(w, err)
		return
	}
	runs, err := d.Store.ListRuns(r.Context(), claims.TenantID, wfID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]runJSON, 0, len(runs))
	for _, run := range runs {
		out = append(out, toRunJSON(run))
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

func (d Deps) listRunEvents(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if _, err := d.Store.GetRun(r.Context(), claims.TenantID, id); err != nil {
		writeErr(w, err)
		return
	}
	events, err := d.Store.ListRunEvents(r.Context(), claims.TenantID, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	type eventJSON struct {
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
		CreatedAt time.Time       `json:"created_at"`
	}
	out := make([]eventJSON, 0, len(events))
	for _, e := range events {
		out = append(out, eventJSON{Type: e.Type, Payload: e.Payload, CreatedAt: e.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

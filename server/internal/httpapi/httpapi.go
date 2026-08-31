package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/engine"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/vault"
)

type Deps struct {
	Store      *store.Store
	SessionKey []byte
	Engine     *engine.Engine
	Vault      *vault.Master
}

func RegisterRoutes(mux *http.ServeMux, d Deps) {
	auth := func(h http.HandlerFunc) http.Handler {
		return RequireSession(d.SessionKey, h)
	}
	mux.Handle("POST /v1/workflows", auth(d.createWorkflow))
	mux.Handle("GET /v1/workflows", auth(d.listWorkflows))
	mux.Handle("GET /v1/workflows/{id}", auth(d.getWorkflow))
	mux.Handle("POST /v1/workflows/{id}/versions", auth(d.addVersion))
	mux.Handle("POST /v1/workflows/{id}/versions/{version}/approve", auth(d.approveVersion))
	mux.Handle("POST /v1/workflows/{id}/runs", auth(d.fireRun))
	mux.Handle("GET /v1/workflows/{id}/runs", auth(d.listRuns))
	mux.Handle("GET /v1/runs/{id}", auth(d.getRun))
	mux.Handle("GET /v1/runs/{id}/events", auth(d.listRunEvents))
	mux.Handle("PUT /v1/connections/{name}", auth(d.putConnection))
	mux.Handle("GET /v1/connections", auth(d.listConnections))
	mux.Handle("DELETE /v1/connections/{name}", auth(d.deleteConnection))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("httpapi: encode response", "err", err)
	}
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		slog.Error("httpapi: internal error", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid " + name})
		return uuid.Nil, false
	}
	return id, true
}

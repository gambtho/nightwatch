package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"github.com/gambtho/tomte/server/internal/captureverify"
	"github.com/gambtho/tomte/server/internal/catalog"
	"github.com/gambtho/tomte/server/internal/engine"
	"github.com/gambtho/tomte/server/internal/llmverify"
	"github.com/gambtho/tomte/server/internal/meter"
	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/vault"
)

type Deps struct {
	Store  *store.Store
	Engine *engine.Engine
	Vault  *vault.Master
	// PublicBaseURL is the app's canonical origin — normally the
	// auto-configured loopback origin derived from the bound listener,
	// overridable via TOMTE_PUBLIC_BASE_URL (dev topologies like
	// Vite-as-origin). Single source for the Origin comparison and
	// redirect joining; never inferred from Host or proxy headers.
	PublicBaseURL *url.URL
	// RunProvider/RunModel (TOMTE_RUN_PROVIDER / TOMTE_RUN_MODEL) are the
	// legacy env-mode execution pair, used only while no endpoint record
	// is configured — the configured endpoint otherwise decides provider
	// and model (decision 9 still holds: never the user, per approval).
	// Empty values fall back to the defaults below.
	RunProvider string
	RunModel    string
	// Catalog is the validated curated connector catalog. Version writes
	// check permit connections against it; GET /v1/catalog serves it.
	Catalog *catalog.Catalog
	// CaptureVerify checks a pasted connector token upstream, before it
	// is stored — the paste path is session-authed only, never the run
	// path.
	CaptureVerify *captureverify.Client
	// LLMVerify makes the first-run disclosed, metered one-call check of
	// a candidate LLM endpoint + key, before anything is saved.
	LLMVerify *llmverify.Client
	// Meter guards the verify call with the same monthly-budget check
	// every other spend path gets (nil skips the check — tests only).
	Meter *meter.Meter
}

// Platform run-model defaults: the cheapest priced Anthropic pair. Used
// only in legacy env mode (no endpoint record, env leaves the choice to
// us); a configured endpoint decides provider and model itself.
const (
	DefaultRunProvider = "anthropic"
	DefaultRunModel    = "claude-haiku-4-5"
)

func (d Deps) runModel() (provider, model string) {
	provider, model = d.RunProvider, d.RunModel
	if provider == "" {
		provider = DefaultRunProvider
	}
	if model == "" {
		model = DefaultRunModel
	}
	return provider, model
}

func RegisterRoutes(mux *http.ServeMux, d Deps) {
	// mut is the CSRF Origin policy, applied to every mutating /v1 route
	// (defence in depth over SameSite=Lax): Origin absent → allowed
	// (non-browser clients and same-origin navigations; routes that also
	// require a session still get RequireSession), exactly the configured
	// public origin → allowed, anything else → 403 before the handler
	// runs.
	mut := func(h http.Handler) http.Handler {
		return checkOrigin(d.PublicBaseURL, h)
	}
	auth := func(h http.HandlerFunc) http.Handler {
		return RequireSession(d.Store, h)
	}
	a := &authHandlers{d: d}
	mux.Handle("GET /local/handoff", http.HandlerFunc(d.localHandoff))
	mux.Handle("POST /v1/auth/logout", mut(http.HandlerFunc(a.logout)))
	mux.Handle("GET /v1/me", auth(a.me))
	mux.Handle("POST /v1/workflows", mut(auth(d.createWorkflow)))
	mux.Handle("GET /v1/workflows", auth(d.listWorkflows))
	mux.Handle("GET /v1/workflows/{id}", auth(d.getWorkflow))
	mux.Handle("POST /v1/workflows/{id}/versions", mut(auth(d.addVersion)))
	mux.Handle("POST /v1/workflows/{id}/versions/{version}/approve", mut(auth(d.approveVersion)))
	mux.Handle("POST /v1/workflows/{id}/runs", mut(auth(d.fireRun)))
	mux.Handle("GET /v1/workflows/{id}/runs", auth(d.listRuns))
	mux.Handle("GET /v1/runs/{id}", auth(d.getRun))
	mux.Handle("GET /v1/runs/{id}/events", auth(d.listRunEvents))
	mux.Handle("GET /v1/catalog", auth(d.getCatalog))
	mux.Handle("GET /v1/settings/endpoint", auth(d.getEndpoint))
	mux.Handle("PUT /v1/settings/endpoint", mut(auth(d.putEndpoint)))
	mux.Handle("POST /v1/settings/endpoint/verify", mut(auth(d.verifyEndpoint)))
	mux.Handle("GET /v1/settings/prices", auth(d.listPrices))
	mux.Handle("PUT /v1/settings/prices", mut(auth(d.putPrice)))
	mux.Handle("GET /v1/settings/budget", auth(d.getBudget))
	mux.Handle("PUT /v1/settings/budget", mut(auth(d.putBudget)))
	mux.Handle("PUT /v1/connections/{name}", mut(auth(d.putConnection)))
	mux.Handle("GET /v1/connections", auth(d.listConnections))
	mux.Handle("DELETE /v1/connections/{name}", mut(auth(d.deleteConnection)))
}

func checkOrigin(public *url.URL, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != public.String() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
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

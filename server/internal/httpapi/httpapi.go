package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/engine"
	"github.com/gambtho/nightwatch/server/internal/mail"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/vault"
)

type Deps struct {
	Store  *store.Store
	Engine *engine.Engine
	Vault  *vault.Master
	// PublicBaseURL is the canonical customer-facing origin
	// (NIGHTSHIFT_PUBLIC_BASE_URL): the single source for magic-link
	// URLs, the Origin comparison, and redirect joining. Never inferred
	// from Host or proxy headers.
	PublicBaseURL *url.URL
	Mailer        mail.Sender
	// RunProvider/RunModel are the platform-selected execution model
	// (NIGHTSHIFT_RUN_PROVIDER / NIGHTSHIFT_RUN_MODEL) baked into the
	// compiled document at approval time — decision 9 took provider and
	// model out of the user's hands. Empty values fall back to the
	// defaults below.
	RunProvider string
	RunModel    string
}

// Platform run-model defaults, used when the env leaves the choice to us:
// the cheapest priced Anthropic pair. Per-tenant override is a
// designed-for seam, not built.
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
	a := &authHandlers{d: d, ips: newIPLimiter(ipLimitMax, ipLimitWindow)}
	mux.Handle("POST /v1/auth/magic-link", mut(http.HandlerFunc(a.magicLink)))
	mux.Handle("GET /auth/verify", http.HandlerFunc(a.verifyPage))
	mux.Handle("POST /v1/auth/verify", mut(http.HandlerFunc(a.verify)))
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

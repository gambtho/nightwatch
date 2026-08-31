package proxy

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gambtho/nightwatch/server/internal/permit"
)

type handler struct {
	d Deps
}

func RegisterRoutes(mux *http.ServeMux, d Deps) {
	h := &handler{d: d}
	mux.HandleFunc("/proxy/llm/{provider}/{path...}", h.llm)
	mux.HandleFunc("/proxy/internal/{path...}", h.internal)
}

// extractRunToken pulls the run token from the provider-native auth-header
// slot: the SDKs can send exactly one credential header, so the run token
// travels where the API key would.
func extractRunToken(provider string, r *http.Request) string {
	switch provider {
	case "anthropic":
		return r.Header.Get("x-api-key")
	default: // openai, openrouter (OpenAI-shaped)
		bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		return bearer
	}
}

func (h *handler) emit(r *http.Request, id RunIdentity, typ string, payload map[string]any) {
	// Best-effort: a denial is enforced even if recording it fails.
	if err := h.d.Events.AppendEvent(r.Context(), id.TenantID, id.RunID, typ, payload); err != nil {
		slog.Error("proxy: append event", "type", typ, "run", id.RunID, "err", err)
	}
}

// authorize runs the front half of every LLM request: authenticate,
// resolve the permit (per request — no cache, by design), check the
// provider allowlist, and check the provider's one allowed (method, path).
func (h *handler) authorize(w http.ResponseWriter, r *http.Request, provider string) (RunIdentity, permit.Permit, ProviderRoute, bool) {
	tok := extractRunToken(provider, r)
	if tok == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, false
	}
	id, err := h.d.Auth.VerifyRunToken(r.Context(), tok)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, false
	}

	p, err := h.d.Permits.PermitForRun(r.Context(), id.TenantID, id.RunID)
	if err != nil {
		// Fail closed: no permit, no egress.
		http.Error(w, "forbidden", http.StatusForbidden)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, false
	}

	route, known := h.d.Config.Providers[provider]
	if !known || !p.AllowsProvider(provider) {
		h.emit(r, id, "proxy.denied", map[string]any{"provider": provider})
		http.Error(w, "forbidden", http.StatusForbidden)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, false
	}
	// One operation per provider is the whole v1 blast radius: without
	// this, a run token buys the entire provider origin.
	if r.Method != route.Method || r.PathValue("path") != route.Path {
		h.emit(r, id, "proxy.denied", map[string]any{
			"provider": provider, "reason": "path", "method": r.Method, "path": r.PathValue("path"),
		})
		http.Error(w, "forbidden", http.StatusForbidden)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, false
	}
	return id, p, route, true
}

func (h *handler) llm(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	id, p, route, ok := h.authorize(w, r, provider)
	if !ok {
		return
	}
	h.forward(w, r, provider, route, id, p) // Task 7
}

func (h *handler) internal(w http.ResponseWriter, r *http.Request) {
	h.passthrough(w, r) // Task 7
}

func (h *handler) forward(w http.ResponseWriter, r *http.Request, provider string, route ProviderRoute, id RunIdentity, p permit.Permit) {
	http.Error(w, "not implemented", http.StatusBadGateway)
}

func (h *handler) passthrough(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusBadGateway)
}

package proxy

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

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
	if err := h.d.Hook.Before(r.Context(), HookRequest{Identity: id, Provider: provider}); err != nil {
		// The typed HookError picks 403 vs 429 (Plan 3 metering); anything
		// else fails closed as 403.
		status := http.StatusForbidden
		var he HookError
		if errors.As(err, &he) && (he.Status == http.StatusForbidden || he.Status == http.StatusTooManyRequests) {
			status = he.Status
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	secret, err := h.d.Credentials.Credential(r.Context(), id.TenantID, p.LLM.Connection, provider)
	if err != nil {
		h.emit(r, id, "proxy.error", map[string]any{"provider": provider, "stage": "credential"})
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	upstream, err := url.Parse(route.Base)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	start := time.Now()
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	rp := &httputil.ReverseProxy{
		// FlushInterval is deliberately left at its zero value: Go's
		// ReverseProxy already auto-detects text/event-stream responses
		// (and responses with unknown Content-Length) and flushes those
		// immediately regardless of this field, which is what the SSE
		// test relies on. Setting it to -1 here would additionally force
		// eager per-write flushing of ordinary buffered JSON responses,
		// racing the client's read of the response against the
		// proxy.request audit event emitted below.
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			// SetURL joins the inbound path (which still carries the
			// /proxy/llm/{provider} prefix); replace it with the upstream's
			// own path prefix + the forwarded remainder.
			pr.Out.URL.Path = singleJoin(upstream.Path, pr.In.PathValue("path"))
			pr.Out.URL.RawPath = ""
			pr.Out.Host = upstream.Host
			// The run token must never reach the provider; the real
			// credential goes in the provider's native slot only.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("x-api-key")
			switch provider {
			case "anthropic":
				pr.Out.Header.Set("x-api-key", secret.Value)
			default:
				pr.Out.Header.Set("Authorization", "Bearer "+secret.Value)
			}
		},
	}
	rp.ServeHTTP(sw, r)
	h.emit(r, id, "proxy.request", map[string]any{
		"provider":    provider,
		"status":      sw.status,
		"duration_ms": time.Since(start).Milliseconds(),
	})
}

// passthrough forwards /proxy/internal/{path...} to the internal API
// unchanged (method, body, bearer). The internal API performs its own
// run-token auth, so this route adds reachability, not authority — it
// exists so a sandboxed actor whose only egress is the proxy can still
// deliver run records.
func (h *handler) passthrough(w http.ResponseWriter, r *http.Request) {
	base, err := url.Parse(h.d.Config.InternalBase)
	if err != nil || h.d.Config.InternalBase == "" {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	rp := &httputil.ReverseProxy{
		// See the comment in forward: leave FlushInterval unset so
		// ordinary responses stay buffered until the handler returns.
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(base)
			pr.Out.URL.Path = singleJoin(base.Path, pr.In.PathValue("path"))
			pr.Out.URL.RawPath = ""
			pr.Out.Host = base.Host
		},
	}
	rp.ServeHTTP(w, r)
}

// singleJoin joins an upstream base path and a forwarded remainder with
// exactly one slash between them.
func singleJoin(basePath, rest string) string {
	return strings.TrimSuffix(basePath, "/") + "/" + rest
}

// statusWriter records the upstream status for the audit event.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

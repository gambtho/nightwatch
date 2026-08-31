package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gambtho/tomte/server/internal/endpoint"
	"github.com/gambtho/tomte/server/internal/permit"
)

type handler struct {
	d Deps
}

func RegisterRoutes(mux *http.ServeMux, d Deps) {
	h := &handler{d: d}
	mux.HandleFunc("/proxy/llm/{provider}/{path...}", h.llm)
	mux.HandleFunc("/proxy/internal/{path...}", h.internal)
	// Curated op invocation is always POST + JSON args; the compiled
	// upstream method comes from the catalog binding, not the caller.
	mux.HandleFunc("POST /proxy/connector/{connector}/{op}", h.connector)
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
	// Best-effort: a denial is enforced even if recording it fails. Use a
	// cancel-free context: a client disconnect must not silently drop the
	// audit event for a request the proxy already finished handling (same
	// class of fix as main's failDispatch — audit delivery must survive
	// client disconnect). Bounded to 5s so a stalled pool cannot hang the
	// denial response indefinitely.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if err := h.d.Events.AppendEvent(ctx, id.TenantID, id.RunID, typ, payload); err != nil {
		slog.Error("proxy: append event", "type", typ, "run", id.RunID, "err", err)
	}
}

// authorize runs the front half of every LLM request: authenticate,
// resolve the permit (per request — no cache, by design), check the
// provider allowlist, and check the provider's one allowed (method, path).
func (h *handler) authorize(w http.ResponseWriter, r *http.Request, provider string) (RunIdentity, permit.Permit, ProviderRoute, *endpoint.Endpoint, bool) {
	tok := extractRunToken(provider, r)
	if tok == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, nil, false
	}
	id, err := h.d.Auth.VerifyRunToken(r.Context(), tok)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, nil, false
	}

	p, err := h.d.Permits.PermitForRun(r.Context(), id.TenantID, id.RunID)
	if err != nil {
		// Fail closed: no permit, no egress.
		h.emit(r, id, "proxy.denied", map[string]any{"provider": provider, "reason": "permit"})
		http.Error(w, "forbidden", http.StatusForbidden)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, nil, false
	}

	// The tenant's configured endpoint, when present and matching the
	// requested provider name, IS the route — its base URL replaces the
	// static table's. A lookup failure fails closed.
	var ep *endpoint.Endpoint
	if h.d.Endpoints != nil {
		ep, err = h.d.Endpoints.EndpointFor(r.Context(), id.TenantID)
		if err != nil {
			h.emit(r, id, "proxy.error", map[string]any{"provider": provider, "stage": "endpoint"})
			http.Error(w, "internal error", http.StatusInternalServerError)
			return RunIdentity{}, permit.Permit{}, ProviderRoute{}, nil, false
		}
	}
	route, known := h.d.Config.Providers[provider]
	if ep != nil && ep.Provider() == provider {
		base, method, path := ep.Route()
		route, known = ProviderRoute{Base: base, Method: method, Path: path}, true
	} else {
		ep = nil // an endpoint for a different provider plays no part in this request
	}
	if !known || !p.AllowsProvider(provider) {
		h.emit(r, id, "proxy.denied", map[string]any{"provider": provider})
		http.Error(w, "forbidden", http.StatusForbidden)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, nil, false
	}
	// One operation per provider is the whole v1 blast radius: without
	// this, a run token buys the entire provider origin.
	if r.Method != route.Method || r.PathValue("path") != route.Path {
		h.emit(r, id, "proxy.denied", map[string]any{
			"provider": provider, "reason": "path", "method": r.Method, "path": r.PathValue("path"),
		})
		http.Error(w, "forbidden", http.StatusForbidden)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, nil, false
	}
	return id, p, route, ep, true
}

func (h *handler) llm(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	id, p, route, ep, ok := h.authorize(w, r, provider)
	if !ok {
		return
	}
	h.forward(w, r, provider, route, id, p, ep)
}

func (h *handler) internal(w http.ResponseWriter, r *http.Request) {
	h.passthrough(w, r) // Task 7
}

func (h *handler) forward(w http.ResponseWriter, r *http.Request, provider string, route ProviderRoute, id RunIdentity, p permit.Permit, ep *endpoint.Endpoint) {
	if err := h.d.Hook.Before(r.Context(), HookRequest{Identity: id, Provider: provider}); err != nil {
		// The typed HookError picks 403 vs 429 (Plan 3 metering); anything
		// else fails closed as 403.
		status := http.StatusForbidden
		var he HookError
		if errors.As(err, &he) && (he.Status == http.StatusForbidden || he.Status == http.StatusTooManyRequests) {
			status = he.Status
		}
		h.emit(r, id, "proxy.denied", map[string]any{"provider": provider, "reason": "hook", "status": status})
		http.Error(w, http.StatusText(status), status)
		return
	}
	// Credential resolution. On a local endpoint there is no credential
	// at all — resolution and injection are skipped by contract, not by
	// error ("Endpoint agnosticism"). On any other configured endpoint,
	// the ENDPOINT's connection names the credential; the permit's
	// llm.connection is legacy-env-mode-only.
	var secret Secret
	local := ep != nil && ep.Preset == endpoint.PresetLocal
	if !local {
		connName := p.LLM.Connection
		if ep != nil && ep.Connection != "" {
			connName = ep.Connection
		}
		var err error
		secret, err = h.d.Credentials.Credential(r.Context(), id.TenantID, connName, provider)
		if err != nil {
			slog.Error("proxy: credential resolution", "provider", provider, "err", err)
			h.emit(r, id, "proxy.error", map[string]any{"provider": provider, "stage": "credential"})
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
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
			// credential goes in the endpoint's native slot only — and on
			// a local endpoint, nowhere: the request is forwarded bare.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("x-api-key")
			pr.Out.Header.Del("api-key")
			header := "authorization"
			if provider == "anthropic" {
				header = "x-api-key"
			}
			if ep != nil {
				header = ep.CredentialHeader()
			}
			switch header {
			case "":
				// local: nothing injected
			case "authorization":
				pr.Out.Header.Set("Authorization", "Bearer "+secret.Value)
			default:
				pr.Out.Header.Set(header, secret.Value)
			}
		},
		// Route transport errors through the redacting slog handler rather
		// than the stdlib default logger.
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
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
//
// InternalBase is the control plane's own base URL — the same origin that
// serves the public /v1 API — so the forwarded remainder is restricted to
// paths starting with "internal/". Without this a run token could reach
// /v1/... on the control plane, or recurse into /proxy/internal/... itself.
func (h *handler) passthrough(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.PathValue("path"), "internal/") {
		http.NotFound(w, r)
		return
	}
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
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
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

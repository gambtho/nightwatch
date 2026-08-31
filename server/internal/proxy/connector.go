package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gambtho/nightwatch/server/internal/catalog"
)

// The curated op-invocation gateway: the harness posts {args} naming an
// operation; the proxy validates, permit-checks, and compiles the
// upstream request from the catalog binding. Enforcement operates on
// exactly the vocabulary the approval diagram showed — the harness never
// constructs upstream URLs.

// maxArgsBytes bounds the op-invocation body. Curated op args are small
// structured values; 64 KiB matches the MCP envelope scan cap.
const maxArgsBytes = 64 << 10

const defaultConnectorTimeout = 60 * time.Second

func (h *handler) connector(w http.ResponseWriter, r *http.Request) {
	connector, op := r.PathValue("connector"), r.PathValue("op")
	if h.d.Catalog == nil {
		http.NotFound(w, r)
		return
	}

	// Auth: the run token rides a plain bearer — no SDK dictates the
	// header slot here.
	bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if bearer == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id, err := h.d.Auth.VerifyRunToken(r.Context(), bearer)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	deny := func(reason string) {
		h.emit(r, id, "proxy.denied", map[string]any{
			"connector": connector, "op": op, "reason": reason,
		})
		http.Error(w, "forbidden", http.StatusForbidden)
	}

	p, err := h.d.Permits.PermitForRun(r.Context(), id.TenantID, id.RunID)
	if err != nil {
		deny("permit")
		return
	}
	constraints, ok := p.AllowsOp(connector, op)
	if !ok {
		deny("op")
		return
	}
	// The permit granted it, but the running catalog is authoritative: a
	// removed op fails closed, visibly.
	con, opDef, ok := h.d.Catalog.Op(connector, op)
	if !ok {
		deny("unknown_op")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxArgsBytes+1))
	if err != nil || len(body) > maxArgsBytes {
		deny("args_size")
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte(`{}`)
	}
	args, err := opDef.Schema().Validate(body)
	if err != nil {
		deny("args_schema")
		return
	}

	// Constraint bindings: each extracted value must appear in the
	// permit's resource list. Fail closed when the permit recorded no
	// list for a constrained field. The denial names the failing check,
	// never the offending value — denials are audit content.
	for _, c := range opDef.Constraints {
		v, _ := args[c.Field].(string)
		if !contains(constraints[c.Field], v) {
			deny("constraint:" + c.Field)
			return
		}
	}

	if err := h.d.Hook.Before(r.Context(), HookRequest{Identity: id, Connector: connector, Op: op}); err != nil {
		status := http.StatusForbidden
		var he HookError
		if errors.As(err, &he) && (he.Status == http.StatusForbidden || he.Status == http.StatusTooManyRequests) {
			status = he.Status
		}
		h.emit(r, id, "proxy.denied", map[string]any{
			"connector": connector, "op": op, "reason": "hook", "status": status,
		})
		http.Error(w, http.StatusText(status), status)
		return
	}

	entry := p.Connections[connector]
	secret, err := h.d.Credentials.Credential(r.Context(), id.TenantID, entry.Connection, con.Auth.Provider)
	if err != nil {
		slog.Error("proxy: connector credential resolution", "connector", connector, "op", op, "err", err)
		h.emit(r, id, "proxy.error", map[string]any{"connector": connector, "op": op, "stage": "credential"})
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	compiled, err := catalog.Compile(opDef, args)
	if err != nil {
		// Compile rejections (traversal segments) are enforcement, not
		// server faults.
		deny("compile")
		return
	}
	target := compiled.URL
	if base, ok := h.d.Config.ConnectorUpstreams[connector]; ok {
		rewritten, err := rewriteUpstream(compiled.URL, base)
		if err != nil {
			slog.Error("proxy: connector upstream override", "connector", connector, "op", op, "err", err)
			h.emit(r, id, "proxy.error", map[string]any{"connector": connector, "op": op, "stage": "upstream_override"})
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		target = rewritten
	}

	timeout := h.d.Config.ConnectorTimeout
	if timeout <= 0 {
		timeout = defaultConnectorTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	var reqBody io.Reader
	if compiled.Body != nil {
		reqBody = bytes.NewReader(compiled.Body)
	}
	upReq, err := http.NewRequestWithContext(ctx, compiled.Method, target, reqBody)
	if err != nil {
		slog.Error("proxy: connector request build", "connector", connector, "op", op, "err", err)
		h.emit(r, id, "proxy.error", map[string]any{"connector": connector, "op": op, "stage": "request"})
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Headers are constructed from an allowlist, never forwarded: the
	// binding's needs plus the injected credential. Cookies, auth
	// headers, and anything harness-supplied never reach the upstream.
	upReq.Header.Set("Accept", "application/json")
	if compiled.Body != nil {
		upReq.Header.Set("Content-Type", "application/json")
	}
	upReq.Header.Set("Authorization", "Bearer "+secret.Value)

	start := time.Now()
	resp, err := connectorClient.Do(upReq)
	if err != nil {
		slog.Error("proxy: connector upstream", "connector", connector, "op", op, "err", err)
		h.emit(r, id, "proxy.error", map[string]any{"connector": connector, "op": op, "stage": "upstream"})
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// A 3xx is relayed verbatim, Location included — the credential was
	// attached only to the original vetted request, and if the caller
	// chases the redirect its new request can only re-enter the proxy.
	if loc := resp.Header.Get("Location"); loc != "" {
		w.Header().Set("Location", loc)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	h.emit(r, id, "proxy.request", map[string]any{
		"connector":   connector,
		"op":          op,
		"status":      resp.StatusCode,
		"duration_ms": time.Since(start).Milliseconds(),
	})
}

func rewriteUpstream(compiledURL, base string) (string, error) {
	u, err := url.Parse(compiledURL)
	if err != nil {
		return "", err
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if b.Scheme == "" || b.Host == "" {
		return "", errors.New("override base must be scheme://host")
	}
	u.Scheme, u.Host = b.Scheme, b.Host
	return u.String(), nil
}

// connectorClient never follows redirects: a 3xx is relayed verbatim,
// and the injected credential was attached only to the original vetted
// request — same posture as the LLM routes.
var connectorClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

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

	"github.com/gambtho/tomte/server/internal/catalog"
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

	upReq, err := http.NewRequestWithContext(ctx, compiled.Method, target, bodyReader(compiled.Body))
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

	// An upstream 401 means the injected credential was rejected. Demote
	// the connection via the secret's epoch-CAS hook; when the CAS
	// misses, the token we used was already superseded by a refresh, so
	// re-resolve once and retry — the one place a stale token gets a
	// second chance against the current bundle.
	if resp.StatusCode == http.StatusUnauthorized && secret.MarkBroken != nil {
		applied, merr := secret.MarkBroken(ctx)
		switch {
		case merr != nil:
			slog.Error("proxy: connector mark broken", "connector", connector, "op", op, "err", merr)
		case applied:
			h.emit(r, id, "connection.broken", map[string]any{
				"connector": connector, "op": op, "provider": con.Auth.Provider,
				"connection": entry.Connection,
			})
		default: // CAS miss: a newer credential exists — retry once with it.
			_ = resp.Body.Close()
			fresh, cerr := h.d.Credentials.Credential(ctx, id.TenantID, entry.Connection, con.Auth.Provider)
			if cerr != nil {
				slog.Error("proxy: connector credential re-resolution", "connector", connector, "op", op, "err", cerr)
				h.emit(r, id, "proxy.error", map[string]any{"connector": connector, "op": op, "stage": "credential"})
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			retryReq, rerr := http.NewRequestWithContext(ctx, compiled.Method, target, bodyReader(compiled.Body))
			if rerr != nil {
				slog.Error("proxy: connector retry build", "connector", connector, "op", op, "err", rerr)
				h.emit(r, id, "proxy.error", map[string]any{"connector": connector, "op": op, "stage": "request"})
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			retryReq.Header = upReq.Header.Clone()
			retryReq.Header.Set("Authorization", "Bearer "+fresh.Value)
			resp, err = connectorClient.Do(retryReq)
			if err != nil {
				slog.Error("proxy: connector upstream retry", "connector", connector, "op", op, "err", err)
				h.emit(r, id, "proxy.error", map[string]any{"connector": connector, "op": op, "stage": "upstream"})
				http.Error(w, "bad gateway", http.StatusBadGateway)
				return
			}
			if resp.StatusCode == http.StatusUnauthorized && fresh.MarkBroken != nil {
				if applied, merr := fresh.MarkBroken(ctx); merr != nil {
					slog.Error("proxy: connector mark broken", "connector", connector, "op", op, "err", merr)
				} else if applied {
					h.emit(r, id, "connection.broken", map[string]any{
						"connector": connector, "op": op, "provider": con.Auth.Provider,
						"connection": entry.Connection,
					})
				}
			}
		}
	}
	defer resp.Body.Close()

	// Everything from here on is the UPSTREAM's response. The marker is
	// what lets the harness tell a relayed upstream 401 (broken
	// connector credential — a tool-level failure the model sees) from
	// the proxy's own 401 (dead run token — fatal to the run).
	w.Header().Set("Tomte-Upstream", "1")
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
	_, copyErr := io.Copy(w, resp.Body)
	payload := map[string]any{
		"connector":   connector,
		"op":          op,
		"status":      resp.StatusCode,
		"duration_ms": time.Since(start).Milliseconds(),
	}
	// Nothing can be re-sent once headers are out, but the audit record
	// must not claim a delivery that broke mid-body.
	if copyErr != nil {
		payload["truncated"] = true
	}
	h.emit(r, id, "proxy.request", payload)
}

// bodyReader returns a fresh reader per attempt (the 401 retry re-sends
// the compiled body), or nil for body-less bindings.
func bodyReader(b []byte) io.Reader {
	if b == nil {
		return nil
	}
	return bytes.NewReader(b)
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

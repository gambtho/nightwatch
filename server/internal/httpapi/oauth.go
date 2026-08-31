package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/gambtho/nightwatch/server/internal/oauth"
	"github.com/gambtho/nightwatch/server/internal/store"
)

// The platform-app connect flow (connectors spec, vault section).
// start is session-authed and CSRF-guarded like every mutation; the
// callback is necessarily public (the user arrives on a provider
// redirect, mid-flow, without a fetch) — the signed state nonce carries
// tenant, user, connector, scopes, and the front-end return path.

const oauthCallbackPath = "/auth/oauth/callback"

func (d Deps) redirectURI() string {
	return d.PublicBaseURL.String() + oauthCallbackPath
}

// oauthStart: POST /v1/connections/oauth/{connector}/start.
// Body: {"ops": [...], "return_to": "/path"} — both optional. Requested
// scopes are the union of the scopes the named ops need (all of the
// connector's ops when omitted) plus whatever the shared connection
// already holds, so a later workflow needing more triggers re-consent
// on the same connection instead of a second credential.
func (d Deps) oauthStart(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	if d.Catalog == nil || d.OAuth == nil || d.StateSigner == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "oauth unavailable"})
		return
	}
	connectorID := r.PathValue("connector")
	con, ok := d.Catalog.Connector(connectorID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown connector"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxDocBytes)
	var body struct {
		Ops      []string `json:"ops"`
		ReturnTo string   `json:"return_to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	scopes := map[string]bool{}
	if len(body.Ops) == 0 {
		for _, op := range con.Ops {
			for _, sc := range op.Scopes {
				scopes[sc] = true
			}
		}
	} else {
		for _, name := range body.Ops {
			_, op, ok := d.Catalog.Op(con.ID, name)
			if !ok {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown op " + name})
				return
			}
			for _, sc := range op.Scopes {
				scopes[sc] = true
			}
		}
	}
	// Union with what the shared connection already holds (incremental
	// auth: re-consent must never shrink an existing grant). Only a
	// definite not-found skips the union — a transient DB failure must
	// not silently narrow the consent.
	existing, err := d.Store.GetConnection(r.Context(), claims.TenantID, con.Auth.Provider, "default")
	switch {
	case err == nil:
		var meta oauth.Metadata
		if len(existing.Metadata) > 0 && json.Unmarshal(existing.Metadata, &meta) == nil {
			for _, sc := range meta.Scopes {
				scopes[sc] = true
			}
		}
	case errors.Is(err, store.ErrNotFound):
	default:
		writeErr(w, err)
		return
	}
	requested := make([]string, 0, len(scopes))
	for sc := range scopes {
		requested = append(requested, sc)
	}
	sort.Strings(requested)

	state, err := d.StateSigner.Sign(oauth.State{
		TenantID: claims.TenantID, UserID: claims.UserID,
		Connector: con.ID, Provider: con.Auth.Provider,
		Scopes: requested, ReturnTo: body.ReturnTo,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	authURL, err := d.OAuth.AuthURL(r.Context(), con.Auth.Provider, d.redirectURI(), state, requested)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"auth_url": authURL})
}

// oauthCallback: GET /auth/oauth/callback — the one public leg. Errors
// after a valid state redirect back to the app with ?connect=error so
// the user is never stranded on a JSON page mid-flow; an invalid state
// is a 400 (there is nowhere trustworthy to send them).
func (d Deps) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if d.OAuth == nil || d.StateSigner == nil {
		http.Error(w, "oauth unavailable", http.StatusInternalServerError)
		return
	}
	st, err := d.StateSigner.Verify(r.URL.Query().Get("state"))
	if err != nil {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	returnTo := st.ReturnTo
	if returnTo == "" {
		returnTo = "/"
	}
	fail := func(stage string, err error) {
		slog.Error("oauth callback", "stage", stage, "connector", st.Connector, "err", err)
		http.Redirect(w, r, d.appRedirect(returnTo, "error"), http.StatusSeeOther)
	}
	if e := r.URL.Query().Get("error"); e != "" {
		fail("provider", errors.New(e))
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		fail("code", errors.New("missing code"))
		return
	}

	bundle, err := d.OAuth.Exchange(r.Context(), st.Provider, code, d.redirectURI(), st.Scopes)
	if err != nil {
		fail("exchange", err)
		return
	}
	bundleJSON, err := json.Marshal(bundle)
	if err != nil {
		fail("encode", err)
		return
	}
	wrappedKEK, kekVersion, err := d.Store.TenantKEK(r.Context(), st.TenantID)
	if err != nil {
		fail("kek", err)
		return
	}
	dek, ct, nonce, err := d.Vault.EncryptSecret(wrappedKEK, string(bundleJSON))
	if err != nil {
		fail("encrypt", err)
		return
	}
	meta, err := json.Marshal(oauth.Metadata{Scopes: bundle.Scopes})
	if err != nil {
		fail("encode", err)
		return
	}
	if _, err := d.Store.UpsertConnectionBundle(r.Context(), st.TenantID, "default", st.Provider,
		store.BundleUpdate{Kind: "oauth", DEKWrapped: dek, Ciphertext: ct, Nonce: nonce,
			KEKVersion: kekVersion, Metadata: meta}); err != nil {
		fail("store", err)
		return
	}
	http.Redirect(w, r, d.appRedirect(returnTo, "ok"), http.StatusSeeOther)
}

// appRedirect joins a validated same-origin path with the public base
// and a connect=... outcome the frontend can read.
func (d Deps) appRedirect(returnTo, outcome string) string {
	u := *d.PublicBaseURL
	if parsed, err := url.Parse(returnTo); err == nil {
		u.Path = parsed.Path
		q := parsed.Query()
		q.Set("connect", outcome)
		u.RawQuery = q.Encode()
	} else {
		u.Path = "/"
		u.RawQuery = "connect=" + outcome
	}
	return u.String()
}

// revokeOAuth is best-effort provider-side revocation, run after the
// row is already deleted: decrypt the departed bundle (a control-plane
// decrypt, the revocation exception to the proxy-only rule) and tell
// the provider. Failure is only logged — the database check, not the
// provider, is what nothing outlives.
func (d Deps) revokeOAuth(r *http.Request, conn store.Connection) {
	if d.OAuth == nil || conn.Kind != "oauth" {
		return
	}
	// Best-effort work survives the client hanging up mid-delete.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	wrapped, err := d.Store.TenantKEKAt(ctx, conn.TenantID, conn.KEKVersion)
	if err != nil {
		slog.Error("oauth revoke: kek", "provider", conn.Provider, "err", err)
		return
	}
	raw, err := d.Vault.DecryptSecret(wrapped, conn.DEKWrapped, conn.Ciphertext, conn.Nonce)
	if err != nil {
		slog.Error("oauth revoke: decrypt", "provider", conn.Provider, "err", err)
		return
	}
	var b oauth.Bundle
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		slog.Error("oauth revoke: bundle", "provider", conn.Provider, "err", err)
		return
	}
	if err := d.OAuth.Revoke(ctx, conn.Provider, b); err != nil {
		slog.Error("oauth revoke: provider", "provider", conn.Provider, "err", err)
	}
}

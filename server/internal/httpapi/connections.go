package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gambtho/tomte/server/internal/catalog"
	"github.com/gambtho/tomte/server/internal/store"
)

// connectionJSON deliberately has no field that could carry the secret.
type connectionJSON struct {
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	Provider   string     `json:"provider"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func toConnectionJSON(c store.Connection) connectionJSON {
	return connectionJSON{
		Name: c.Name, Kind: c.Kind, Provider: c.Provider, Status: c.Status,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, LastUsedAt: c.LastUsedAt,
	}
}

func (d Deps) putConnection(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	name := r.PathValue("name")
	r.Body = http.MaxBytesReader(w, r.Body, maxDocBytes)
	var body struct {
		Provider string `json:"provider"`
		Value    string `json:"value"`
		// Kind defaults to llm_api_key (the pre-connector contract);
		// api_key is a pasted connector token, verified before storing.
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider and value required"})
		return
	}
	if body.Provider == "" || body.Value == "" || name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider and value required"})
		return
	}
	if body.Kind == "" {
		body.Kind = "llm_api_key"
	}
	if body.Kind != "llm_api_key" && body.Kind != "api_key" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kind must be llm_api_key or api_key"})
		return
	}

	// An api_key is a connector credential: its provider must be a
	// catalog namespace (fail-closed sequencing — mcp:{uuid} namespaces
	// arrive with MCP registration, where the row they qualify exists).
	var connector *catalog.Connector
	if body.Kind == "api_key" {
		connector = d.connectorForProvider(body.Provider)
		if connector == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown connector provider " + body.Provider})
			return
		}
	}

	// Silently flipping an existing credential's kind would repurpose its
	// secret. Replacing it is a delete + re-paste, never this PUT.
	if existing, gerr := d.Store.GetConnection(r.Context(), claims.TenantID, body.Provider, name); gerr == nil && existing.Kind != body.Kind {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "connection exists with kind " + existing.Kind + " — delete it first",
		})
		return
	}

	// Verify-then-store: when the connector declares a verify op, the
	// pasted value is checked live before anything persists — a re-paste
	// re-verifies every time. Missing scopes warn; they never fail.
	var missingScopes []string
	if connector != nil && connector.Auth.Capture != nil && connector.Auth.Capture.VerifyOp != "" {
		if d.CaptureVerify == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "verification unavailable"})
			return
		}
		res, err := d.CaptureVerify.Verify(r.Context(), connector, body.Value)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !res.OK {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "verify_failed", "message": res.Message,
			})
			return
		}
		missingScopes = res.MissingScopes
	}

	wrappedKEK, kekVersion, err := d.Store.TenantKEK(r.Context(), claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	dek, ct, nonce, err := d.Vault.EncryptSecret(wrappedKEK, body.Value)
	if err != nil {
		writeErr(w, err)
		return
	}
	c, err := d.Store.UpsertConnection(r.Context(), claims.TenantID, name, body.Kind,
		body.Provider, dek, ct, nonce, kekVersion)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := map[string]any{"connection": toConnectionJSON(c)}
	if len(missingScopes) > 0 {
		out["missing_scopes"] = missingScopes
	}
	writeJSON(w, http.StatusOK, out)
}

// connectorForProvider finds the catalog connector owning a credential
// namespace. Connectors may share one (one pasted token covers them all);
// one that declares a verify op wins so a paste is always checked.
func (d Deps) connectorForProvider(provider string) *catalog.Connector {
	if d.Catalog == nil {
		return nil
	}
	var found *catalog.Connector
	for _, con := range d.Catalog.Connectors() {
		if con.Auth.Provider != provider {
			continue
		}
		if found == nil || (con.Auth.Capture != nil && con.Auth.Capture.VerifyOp != "") {
			found = con
		}
		if found.Auth.Capture != nil && found.Auth.Capture.VerifyOp != "" {
			break
		}
	}
	return found
}

func (d Deps) listConnections(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	cs, err := d.Store.ListConnections(r.Context(), claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]connectionJSON, 0, len(cs))
	for _, c := range cs {
		out = append(out, toConnectionJSON(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": out})
}

func (d Deps) deleteConnection(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	name := r.PathValue("name")
	provider := r.URL.Query().Get("provider")
	if provider == "" || name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider query parameter required"})
		return
	}
	// In-flight runs lose the credential on their next request; nothing
	// provider-side to revoke for pasted tokens.
	if err := d.Store.DeleteConnection(r.Context(), claims.TenantID, provider, name); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

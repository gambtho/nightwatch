package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gambtho/nightwatch/server/internal/store"
)

// connectionJSON deliberately has no field that could carry the secret;
// metadata is the vetted non-secret face (granted scopes).
type connectionJSON struct {
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	Provider   string          `json:"provider"`
	Status     string          `json:"status"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	LastUsedAt *time.Time      `json:"last_used_at,omitempty"`
}

func toConnectionJSON(c store.Connection) connectionJSON {
	var meta json.RawMessage
	if len(c.Metadata) > 0 && string(c.Metadata) != "{}" {
		meta = c.Metadata
	}
	return connectionJSON{
		Name: c.Name, Kind: c.Kind, Provider: c.Provider,
		Status: c.Status, Metadata: meta,
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
	// A PUT stores llm_api_key material; silently flipping an existing
	// oauth connection's kind would destroy its bundle outside the
	// refresh lock. Rotating an oauth credential is a re-consent (or a
	// delete + reconnect), never a PUT.
	if existing, gerr := d.Store.GetConnection(r.Context(), claims.TenantID, body.Provider, name); gerr == nil && existing.Kind != "llm_api_key" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "connection exists with kind " + existing.Kind + " — delete it first or re-connect via oauth",
		})
		return
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
	c, err := d.Store.UpsertConnection(r.Context(), claims.TenantID, name, "llm_api_key",
		body.Provider, dek, ct, nonce, kekVersion)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection": toConnectionJSON(c)})
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
	// Delete under the refresh advisory lock; revoke provider-side
	// best-effort after the row is gone (in-flight runs lose the
	// credential on their next request either way).
	deleted, err := d.Store.DeleteConnectionLocked(r.Context(), claims.TenantID, provider, name)
	if err != nil {
		writeErr(w, err)
		return
	}
	d.revokeOAuth(r, deleted)
	w.WriteHeader(http.StatusNoContent)
}

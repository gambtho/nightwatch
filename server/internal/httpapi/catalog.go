package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gambtho/tomte/server/internal/catalog"
	"github.com/gambtho/tomte/server/internal/permit"
)

// The discovery surface: what the build conversation (and the approval
// diagram behind it) learns the platform can reach. Copy comes verbatim
// from the catalog — plain-language descriptions written once, for this
// surface.

type catalogOpJSON struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Effect      string          `json:"effect"`
	Scopes      []string        `json:"scopes"`
	ArgsSchema  json.RawMessage `json:"args_schema"`
	Constraints []string        `json:"constraints,omitempty"`
}

type catalogConnectorJSON struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	AuthProvider string `json:"auth_provider"`
	// Connected reports whether the tenant holds any credential in the
	// connector's namespace; Status is that credential's health ("ok" |
	// "needs_reauth"), empty when not connected.
	Connected bool            `json:"connected"`
	Status    string          `json:"status,omitempty"`
	Ops       []catalogOpJSON `json:"ops"`
	// Capture is the guided token-capture card, verbatim from the
	// catalog def — copy the paste surface renders as data.
	Capture *catalog.Capture `json:"capture,omitempty"`
}

func (d Deps) getCatalog(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	if d.Catalog == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "catalog unavailable"})
		return
	}
	conns, err := d.Store.ListConnections(r.Context(), claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	// The "default" connection is the shared-connection convention; it
	// wins when a tenant somehow holds several credentials in one
	// namespace.
	status := map[string]string{}
	for _, c := range conns {
		if _, seen := status[c.Provider]; !seen || c.Name == "default" {
			status[c.Provider] = c.Status
		}
	}

	out := []catalogConnectorJSON{}
	for _, con := range d.Catalog.Connectors() {
		cj := catalogConnectorJSON{
			ID: con.ID, Name: con.Name, Description: con.Description,
			AuthProvider: con.Auth.Provider,
			Connected:    status[con.Auth.Provider] != "",
			Status:       status[con.Auth.Provider],
			Capture:      con.Auth.Capture,
		}
		for _, op := range con.Ops {
			oj := catalogOpJSON{
				Name: op.Name, Description: op.Description, Effect: op.Effect,
				Scopes: op.Scopes, ArgsSchema: op.ArgsSchema,
			}
			for _, c := range op.Constraints {
				oj.Constraints = append(oj.Constraints, c.Field)
			}
			cj.Ops = append(cj.Ops, oj)
		}
		out = append(out, cj)
	}
	writeJSON(w, http.StatusOK, map[string]any{"connectors": out})
}

// validateConnections checks a permit's connections against the running
// catalog at version-write time, after structural validation
// (permit.Parse) and authentication. Fail closed on anything the
// catalog cannot enforce:
//   - a connector or op the catalog does not define,
//   - resources naming an arg field the op has no constraint binding for
//     (narrowing nothing enforces would be false security),
//   - a granted op with a constraint binding the permit records no
//     resource list for (the proxy would deny every invocation — surface
//     that at write time, not run time).
func validateConnections(cat *catalog.Catalog, p permit.Permit) error {
	if cat == nil && len(p.Connections) > 0 {
		return fmt.Errorf("permit: connections are not supported (no catalog configured)")
	}
	for key, entry := range p.Connections {
		con, ok := cat.Connector(key)
		if !ok {
			return fmt.Errorf("permit: unknown connector %q", key)
		}
		for _, opName := range entry.Ops {
			_, op, ok := cat.Op(con.ID, opName)
			if !ok {
				return fmt.Errorf("permit: connector %q has no op %q", key, opName)
			}
			constrained := map[string]bool{}
			for _, c := range op.Constraints {
				constrained[c.Field] = true
			}
			for field := range constrained {
				if len(entry.Resources[opName][field]) == 0 {
					return fmt.Errorf("permit: op %q of %q requires an approved resource list for %q", opName, key, field)
				}
			}
			for field := range entry.Resources[opName] {
				if !constrained[field] {
					return fmt.Errorf("permit: op %q of %q has no constraint on %q", opName, key, field)
				}
			}
		}
	}
	return nil
}

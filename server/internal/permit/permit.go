// Package permit defines the workflow permit document, schema v1. The
// permit is the approved blast radius: what a workflow's runs may reach.
// v1 governs LLM provider egress, the approved spend caps, and the
// connections map: per-connector grants at operation granularity.
package permit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

type Permit struct {
	V           int                   `json:"v"`
	LLM         LLM                   `json:"llm"`
	Spend       *Spend                `json:"spend,omitempty"`
	Connections map[string]Connection `json:"connections,omitempty"`
}

// Connection is one connector grant. Keys of the Connections map are
// curated connector ids; `mcp:{server-uuid}` keys are reserved for
// remote MCP servers and rejected until their enforcement path exists —
// a permit must never name reach nothing enforces.
type Connection struct {
	Kind       string   `json:"kind"`
	Connection string   `json:"connection,omitempty"`
	Ops        []string `json:"ops"`
	// Resources lists, per op and per constrained arg field, the exact
	// values the field may take ("only our support channel"). Exact
	// string match only; no predicate language.
	Resources map[string]map[string][]string `json:"resources,omitempty"`
}

type LLM struct {
	Providers  []string `json:"providers,omitempty"`
	Connection string   `json:"connection,omitempty"`
}

// Spend is the approved per-run budget — the number the blast-radius
// diagram shows. Detection is transactional at finalization (see the
// scheduling+metering spec); pre-request enforcement arrives with
// multi-turn runs.
type Spend struct {
	PerRunCents int `json:"per_run_cents"`
}

// Empty is the canonical deny-all permit: valid v1, no egress allowed.
var Empty = json.RawMessage(`{"v":1}`)

// KindHTTP is the curated connector kind. remote_mcp arrives with its
// enforcement path.
const KindHTTP = "http"

// Parse validates raw as a v1 permit. Fail closed: anything unrecognized
// is an error, not a warning.
func Parse(raw []byte) (Permit, error) {
	var p Permit
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Permit{}, fmt.Errorf("permit: %w", err)
	}
	// Reject trailing garbage after the JSON document (e.g. a second
	// concatenated object) — Decode stops after one value and silently
	// ignores whatever follows unless we check for it explicitly.
	if err := dec.Decode(new(struct{})); err != io.EOF {
		return Permit{}, fmt.Errorf("permit: trailing data after document")
	}
	if p.V != 1 {
		return Permit{}, fmt.Errorf("permit: unsupported version %d", p.V)
	}
	for _, name := range p.LLM.Providers {
		if name == "" {
			return Permit{}, fmt.Errorf("permit: empty provider name")
		}
	}
	if p.Spend != nil && p.Spend.PerRunCents <= 0 {
		return Permit{}, fmt.Errorf("permit: spend.per_run_cents must be positive")
	}
	if p.LLM.Connection == "" {
		p.LLM.Connection = "default"
	}
	for key, c := range p.Connections {
		if strings.HasPrefix(key, "mcp:") {
			return Permit{}, fmt.Errorf("permit: connection %q: remote MCP connections are not supported yet", key)
		}
		if key == "" {
			return Permit{}, fmt.Errorf("permit: empty connection key")
		}
		if c.Kind != KindHTTP {
			return Permit{}, fmt.Errorf("permit: connection %q: unknown kind %q", key, c.Kind)
		}
		// An entry allowing nothing is a mistake, not a deny-all —
		// deny-all is the entry's absence.
		if len(c.Ops) == 0 {
			return Permit{}, fmt.Errorf("permit: connection %q: ops must be non-empty", key)
		}
		for _, op := range c.Ops {
			if op == "" {
				return Permit{}, fmt.Errorf("permit: connection %q: empty op name", key)
			}
		}
		for op, fields := range c.Resources {
			if !slices.Contains(c.Ops, op) {
				return Permit{}, fmt.Errorf("permit: connection %q: resources name unlisted op %q", key, op)
			}
			if len(fields) == 0 {
				return Permit{}, fmt.Errorf("permit: connection %q: resources for %q are empty", key, op)
			}
			for field, values := range fields {
				if field == "" {
					return Permit{}, fmt.Errorf("permit: connection %q: empty resource field for %q", key, op)
				}
				if len(values) == 0 {
					return Permit{}, fmt.Errorf("permit: connection %q: empty resource list for %q.%s", key, op, field)
				}
				for _, v := range values {
					if v == "" {
						return Permit{}, fmt.Errorf("permit: connection %q: empty resource value for %q.%s", key, op, field)
					}
				}
			}
		}
		if c.Connection == "" {
			c.Connection = "default"
			p.Connections[key] = c
		}
	}
	return p, nil
}

func (p Permit) AllowsProvider(name string) bool {
	return slices.Contains(p.LLM.Providers, name)
}

// AllowsOp reports whether the permit grants the connector op, returning
// the approved resource lists (constrained arg field -> allowed values)
// when it does. A nil Constraints map with ok=true means the op is
// granted with no resource narrowing recorded — catalog constraint
// bindings still fail closed against it at enforcement time.
func (p Permit) AllowsOp(connector, op string) (map[string][]string, bool) {
	c, ok := p.Connections[connector]
	if !ok || c.Kind != KindHTTP || !slices.Contains(c.Ops, op) {
		return nil, false
	}
	return c.Resources[op], true
}

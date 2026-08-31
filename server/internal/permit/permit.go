// Package permit defines the workflow permit document, schema v1. The
// permit is the approved blast radius: what a workflow's runs may reach.
// v1 governs LLM provider egress only; the connections map is reserved
// for the connector catalog and must be empty.
package permit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

type Permit struct {
	V           int                        `json:"v"`
	LLM         LLM                        `json:"llm"`
	Connections map[string]json.RawMessage `json:"connections,omitempty"`
}

type LLM struct {
	Providers  []string `json:"providers,omitempty"`
	Connection string   `json:"connection,omitempty"`
}

// Empty is the canonical deny-all permit: valid v1, no egress allowed.
var Empty = json.RawMessage(`{"v":1}`)

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
	if len(p.Connections) != 0 {
		return Permit{}, fmt.Errorf("permit: connections must be empty in v1")
	}
	for _, name := range p.LLM.Providers {
		if name == "" {
			return Permit{}, fmt.Errorf("permit: empty provider name")
		}
	}
	if p.LLM.Connection == "" {
		p.LLM.Connection = "default"
	}
	return p, nil
}

func (p Permit) AllowsProvider(name string) bool {
	return slices.Contains(p.LLM.Providers, name)
}

package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Schema is a deliberately strict subset of JSON Schema: an object root
// with flat, scalar properties. The catalog is authored in-repo, so the
// subset constrains us, not vendors; validating harness-supplied args
// against a subset we fully implement is what lets enforcement stay
// dependency-free and exactly specified. Anything outside the subset is
// rejected at startup, never silently ignored — a schema keyword we do
// not enforce would be reach we appear to constrain but do not.
//
// Supported: type:"object" root with properties, required,
// additionalProperties:false; property types string (with optional
// enum), integer, number, boolean.
type Schema struct {
	props    map[string]schemaProp
	order    []string
	required map[string]bool
}

type schemaProp struct {
	Type string
	Enum []string
}

type rawSchema struct {
	Type                 string             `json:"type"`
	Properties           map[string]rawProp `json:"properties"`
	Required             []string           `json:"required"`
	AdditionalProperties *bool              `json:"additionalProperties"`
}

type rawProp struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// ParseSchema validates raw against the supported subset.
func ParseSchema(raw json.RawMessage) (*Schema, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("required")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var rs rawSchema
	if err := dec.Decode(&rs); err != nil {
		return nil, fmt.Errorf("unsupported schema construct: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after schema")
	}
	if rs.Type != "object" {
		return nil, fmt.Errorf("root type must be object")
	}
	if rs.AdditionalProperties == nil || *rs.AdditionalProperties {
		return nil, fmt.Errorf("additionalProperties must be false")
	}
	s := &Schema{props: map[string]schemaProp{}, required: map[string]bool{}}
	for name, p := range rs.Properties {
		switch p.Type {
		case "string":
		case "integer", "number", "boolean":
			if len(p.Enum) > 0 {
				return nil, fmt.Errorf("property %q: enum is only supported on strings", name)
			}
		default:
			return nil, fmt.Errorf("property %q: unsupported type %q", name, p.Type)
		}
		for _, v := range p.Enum {
			if v == "" {
				return nil, fmt.Errorf("property %q: empty enum value", name)
			}
		}
		s.props[name] = schemaProp{Type: p.Type, Enum: p.Enum}
		s.order = append(s.order, name)
	}
	for _, name := range rs.Required {
		if _, ok := s.props[name]; !ok {
			return nil, fmt.Errorf("required names unknown property %q", name)
		}
		s.required[name] = true
	}
	return s, nil
}

// Validate checks harness-supplied args against the schema, fail closed:
// unknown fields, missing required fields, and type mismatches are all
// errors. It returns the args as a flat map for placement and constraint
// extraction.
func (s *Schema) Validate(raw json.RawMessage) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var args map[string]any
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("args must be a JSON object")
	}
	for name := range args {
		if _, ok := s.props[name]; !ok {
			return nil, fmt.Errorf("unknown arg %q", name)
		}
	}
	for name := range s.required {
		if _, ok := args[name]; !ok {
			return nil, fmt.Errorf("missing required arg %q", name)
		}
	}
	for name, v := range args {
		p := s.props[name]
		switch p.Type {
		case "string":
			sv, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("arg %q must be a string", name)
			}
			if len(p.Enum) > 0 {
				found := false
				for _, e := range p.Enum {
					if e == sv {
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("arg %q is not an allowed value", name)
				}
			}
		case "integer":
			n, ok := v.(json.Number)
			if !ok {
				return nil, fmt.Errorf("arg %q must be an integer", name)
			}
			if _, err := n.Int64(); err != nil {
				return nil, fmt.Errorf("arg %q must be an integer", name)
			}
		case "number":
			if _, ok := v.(json.Number); !ok {
				return nil, fmt.Errorf("arg %q must be a number", name)
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return nil, fmt.Errorf("arg %q must be a boolean", name)
			}
		}
	}
	return args, nil
}

// Has reports whether the schema declares the property.
func (s *Schema) Has(name string) bool { _, ok := s.props[name]; return ok }

// Required reports whether the property is required.
func (s *Schema) Required(name string) bool { return s.required[name] }

// Type returns the property's declared type ("" if unknown).
func (s *Schema) Type(name string) string { return s.props[name].Type }

// Properties returns the declared property names.
func (s *Schema) Properties() []string { return append([]string(nil), s.order...) }

// EnumOf returns the property's enum values (nil when unconstrained).
func (s *Schema) EnumOf(name string) []string { return s.props[name].Enum }

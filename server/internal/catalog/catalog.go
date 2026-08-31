// Package catalog is the in-repo declarative connector catalog: curated
// connector definitions embedded at build, validated at startup. The
// catalog is where enforcement vocabulary lives — an operation's binding
// is what the proxy compiles upstream requests from, and its args schema
// IS the LLM tool definition. Operations are append-only; an edit may
// only narrow or preserve reach (see gate.go).
package catalog

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
)

//go:embed defs/*.json
var defsFS embed.FS

//go:embed baseline/*.json
var baselineFS embed.FS

const (
	EffectRead  = "read"
	EffectWrite = "write"
)

// Catalog is the validated set of curated connectors.
type Catalog struct {
	connectors map[string]*Connector
	order      []string
}

// Connector is one curated (kind http) catalog entry.
type Connector struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Auth        Auth   `json:"auth"`
	// Hosts are catalog constants, never user-controlled destinations.
	// Templated tenant-parameter hosts ({workspace}.example.com) are in
	// the design but unsupported until a curated connector needs one;
	// validation rejects the syntax so no untested code path exists.
	Hosts []string `json:"hosts"`
	Ops   []*Op    `json:"ops"`

	ops map[string]*Op
}

// Auth names the credential namespace for the connector (the provider a
// pasted api_key connection is stored under). Distinct connectors may
// share one, so one pasted token can cover them all.
type Auth struct {
	Provider string `json:"provider"`
}

// Op is the atomic unit of enforcement, approval copy, and tool
// projection.
type Op struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Effect      string          `json:"effect"`
	Scopes      []string        `json:"scopes"`
	ArgsSchema  json.RawMessage `json:"args_schema"`
	Binding     Binding         `json:"binding"`
	Constraints []Constraint    `json:"constraints,omitempty"`

	schema *Schema
}

// Binding is how the proxy compiles the upstream request. Args are placed
// exactly once each: as a {name} path segment, a query parameter, or a
// JSON body field (dotted paths nest).
type Binding struct {
	Method string `json:"method"`
	Host   string `json:"host"`
	Path   string `json:"path"`
	// Query maps query parameter name -> arg name.
	Query map[string]string `json:"query,omitempty"`
	// Body maps a dotted JSON path in the upstream body -> arg name.
	Body map[string]string `json:"body,omitempty"`
}

// Constraint binds an arg field to the permit's resource list for the op:
// the field's value must appear in the approved list, exact string match
// only.
type Constraint struct {
	Field string `json:"field"`
}

var (
	idRe          = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	opNameRe      = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)
	hostRe        = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	placeholderRe = regexp.MustCompile(`\{([^{}/]*)\}`)
)

var allowedMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// Load parses and validates the embedded definitions, then verifies them
// against the committed baseline snapshot: a widening diff refuses to
// load (and therefore refuses to boot — see gate.go for why), and any
// other drift demands a reviewed baseline update.
func Load() (*Catalog, error) {
	cat, err := parseFS(defsFS, "defs")
	if err != nil {
		return nil, err
	}
	base, err := parseFS(baselineFS, "baseline")
	if err != nil {
		return nil, fmt.Errorf("catalog baseline: %w", err)
	}
	if err := CheckAgainstBaseline(base, cat); err != nil {
		return nil, err
	}
	return cat, nil
}

// ParseDefs builds a catalog from raw JSON definitions (one connector
// each), running full validation. Tests use it for small fixture
// catalogs; Load uses it for the embedded set.
func ParseDefs(defs ...[]byte) (*Catalog, error) {
	cat := &Catalog{connectors: map[string]*Connector{}}
	for _, raw := range defs {
		var c Connector
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("catalog: %w", err)
		}
		if dec.More() {
			return nil, fmt.Errorf("catalog: trailing data after definition %q", c.ID)
		}
		if err := validateConnector(&c); err != nil {
			return nil, fmt.Errorf("catalog %q: %w", c.ID, err)
		}
		if _, dup := cat.connectors[c.ID]; dup {
			return nil, fmt.Errorf("catalog: duplicate connector id %q", c.ID)
		}
		cat.connectors[c.ID] = &c
		cat.order = append(cat.order, c.ID)
	}
	sort.Strings(cat.order)
	return cat, nil
}

func parseFS(fsys fs.FS, dir string) (*Catalog, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("catalog: read %s: %w", dir, err)
	}
	var defs [][]byte
	for _, e := range entries {
		raw, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("catalog: read %s: %w", e.Name(), err)
		}
		defs = append(defs, raw)
	}
	return ParseDefs(defs...)
}

func validateConnector(c *Connector) error {
	if !idRe.MatchString(c.ID) {
		return fmt.Errorf("invalid id")
	}
	if c.Name == "" || c.Description == "" {
		return fmt.Errorf("name and description required")
	}
	if c.Auth.Provider == "" {
		return fmt.Errorf("auth.provider required")
	}
	if len(c.Hosts) == 0 {
		return fmt.Errorf("at least one host required")
	}
	for _, h := range c.Hosts {
		if strings.ContainsAny(h, "{}") {
			return fmt.Errorf("templated host %q: tenant-parameter hosts are not supported yet", h)
		}
		if !hostRe.MatchString(h) {
			return fmt.Errorf("invalid host %q", h)
		}
	}
	if len(c.Ops) == 0 {
		return fmt.Errorf("at least one op required")
	}
	c.ops = make(map[string]*Op, len(c.Ops))
	for _, op := range c.Ops {
		if err := validateOp(c, op); err != nil {
			return fmt.Errorf("op %q: %w", op.Name, err)
		}
		if _, dup := c.ops[op.Name]; dup {
			return fmt.Errorf("duplicate op name %q", op.Name)
		}
		c.ops[op.Name] = op
	}
	return nil
}

func validateOp(c *Connector, op *Op) error {
	if !opNameRe.MatchString(op.Name) {
		return fmt.Errorf("invalid name")
	}
	if op.Description == "" {
		return fmt.Errorf("description required")
	}
	if op.Effect != EffectRead && op.Effect != EffectWrite {
		return fmt.Errorf("effect must be read or write")
	}
	if len(op.Scopes) == 0 {
		return fmt.Errorf("at least one scope required")
	}
	schema, err := ParseSchema(op.ArgsSchema)
	if err != nil {
		return fmt.Errorf("args_schema: %w", err)
	}
	op.schema = schema

	b := op.Binding
	if !slices.Contains(allowedMethods, b.Method) {
		return fmt.Errorf("binding: method %q not allowed", b.Method)
	}
	if !slices.Contains(c.Hosts, b.Host) {
		return fmt.Errorf("binding: host %q not in connector hosts", b.Host)
	}
	if !strings.HasPrefix(b.Path, "/") {
		return fmt.Errorf("binding: path must start with /")
	}

	// Every schema property is placed exactly once; every placement
	// references a real property. Anything else is either silently
	// dropped input or a dangling reference — both authoring bugs.
	placed := map[string]int{}
	for _, m := range placeholderRe.FindAllStringSubmatch(b.Path, -1) {
		arg := m[1]
		if !schema.Has(arg) {
			return fmt.Errorf("binding: path placeholder {%s} not in args_schema", arg)
		}
		if !schema.Required(arg) {
			return fmt.Errorf("binding: path placeholder {%s} must be a required arg", arg)
		}
		if schema.Type(arg) != "string" {
			return fmt.Errorf("binding: path placeholder {%s} must be a string arg", arg)
		}
		placed[arg]++
	}
	for param, arg := range b.Query {
		if param == "" {
			return fmt.Errorf("binding: empty query parameter name")
		}
		if !schema.Has(arg) {
			return fmt.Errorf("binding: query arg %q not in args_schema", arg)
		}
		placed[arg]++
	}
	bodyPaths := make([]string, 0, len(b.Body))
	for path, arg := range b.Body {
		if path == "" {
			return fmt.Errorf("binding: empty body path")
		}
		if !schema.Has(arg) {
			return fmt.Errorf("binding: body arg %q not in args_schema", arg)
		}
		bodyPaths = append(bodyPaths, path)
		placed[arg]++
	}
	// Dotted body paths must be prefix-disjoint: with both "a" and
	// "a.b", whichever compiles second silently clobbers the first,
	// nondeterministically. An authoring bug, so it fails here.
	for i, a := range bodyPaths {
		for _, b2 := range bodyPaths[i+1:] {
			if strings.HasPrefix(a, b2+".") || strings.HasPrefix(b2, a+".") {
				return fmt.Errorf("binding: body paths %q and %q overlap", a, b2)
			}
		}
	}
	for _, prop := range schema.Properties() {
		switch placed[prop] {
		case 0:
			return fmt.Errorf("binding: arg %q is never placed", prop)
		case 1:
		default:
			return fmt.Errorf("binding: arg %q placed more than once", prop)
		}
	}

	if len(op.Constraints) > 0 && op.Effect != EffectWrite {
		return fmt.Errorf("constraints are only valid on write ops")
	}
	seen := map[string]bool{}
	for _, con := range op.Constraints {
		if !schema.Has(con.Field) {
			return fmt.Errorf("constraint field %q not in args_schema", con.Field)
		}
		if schema.Type(con.Field) != "string" {
			return fmt.Errorf("constraint field %q must be a string arg", con.Field)
		}
		if !schema.Required(con.Field) {
			return fmt.Errorf("constraint field %q must be a required arg", con.Field)
		}
		if seen[con.Field] {
			return fmt.Errorf("duplicate constraint on field %q", con.Field)
		}
		seen[con.Field] = true
	}
	return nil
}

// Connector returns a connector by id.
func (c *Catalog) Connector(id string) (*Connector, bool) {
	con, ok := c.connectors[id]
	return con, ok
}

// Connectors returns all connectors in stable id order.
func (c *Catalog) Connectors() []*Connector {
	out := make([]*Connector, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.connectors[id])
	}
	return out
}

// Op returns an operation by connector id and op name.
func (c *Catalog) Op(connector, name string) (*Connector, *Op, bool) {
	con, ok := c.connectors[connector]
	if !ok {
		return nil, nil, false
	}
	op, ok := con.ops[name]
	if !ok {
		return nil, nil, false
	}
	return con, op, true
}

// Schema returns the op's parsed args schema.
func (o *Op) Schema() *Schema { return o.schema }

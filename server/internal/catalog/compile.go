package catalog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// CompiledRequest is the upstream request shape the proxy (and the
// control-plane options client, which shares this path) sends. Headers
// are constructed by the caller from an allowlist — compilation produces
// only the parts the binding determines.
type CompiledRequest struct {
	Method string
	// URL is scheme+host+path+query. The scheme is always https; the
	// host comes from the binding, never from the caller.
	URL  string
	Body []byte // JSON body, nil when the binding places no body args
}

// Compile builds the upstream request parts from the op's binding and
// validated args. Callers MUST pass args returned by Schema.Validate —
// compilation places values, it does not re-validate them, with one
// exception: path-placed values are component-encoded after a hard
// rejection of traversal segments, so `/`, `?`, `#`, `.` and `..` can
// never alter the compiled path.
func Compile(op *Op, args map[string]any) (CompiledRequest, error) {
	b := op.Binding

	path := b.Path
	for _, m := range placeholderRe.FindAllStringSubmatch(b.Path, -1) {
		arg := m[1]
		v, ok := args[arg].(string)
		if !ok {
			return CompiledRequest{}, fmt.Errorf("compile: path arg %q missing or not a string", arg)
		}
		enc, err := encodePathSegment(v)
		if err != nil {
			return CompiledRequest{}, fmt.Errorf("compile: path arg %q: %w", arg, err)
		}
		path = strings.ReplaceAll(path, m[0], enc)
	}

	q := url.Values{}
	for param, arg := range b.Query {
		v, ok := args[arg]
		if !ok {
			continue // optional arg not supplied
		}
		q.Set(param, fmt.Sprintf("%v", v))
	}

	u := url.URL{Scheme: "https", Host: b.Host, RawPath: path, RawQuery: q.Encode()}
	// RawPath is authoritative here (segments are pre-encoded); Path must
	// hold the decoded form for the two to be consistent.
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return CompiledRequest{}, fmt.Errorf("compile: path: %w", err)
	}
	u.Path = decoded

	var body []byte
	if len(b.Body) > 0 {
		root := map[string]any{}
		for dotted, arg := range b.Body {
			v, ok := args[arg]
			if !ok {
				continue // optional arg not supplied
			}
			placeBody(root, dotted, v)
		}
		body, err = json.Marshal(root)
		if err != nil {
			return CompiledRequest{}, fmt.Errorf("compile: body: %w", err)
		}
	}

	return CompiledRequest{Method: b.Method, URL: u.String(), Body: body}, nil
}

// encodePathSegment percent-encodes v as a single path segment, rejecting
// values that are (or decode to) a traversal segment outright.
func encodePathSegment(v string) (string, error) {
	if v == "" {
		return "", fmt.Errorf("empty value")
	}
	// A value that percent-decodes to "." or ".." would be re-encoded
	// harmlessly below, but reject it anyway: a traversal segment has no
	// legitimate reading as a resource id.
	if dec, err := url.PathUnescape(v); err == nil {
		if dec == "." || dec == ".." {
			return "", fmt.Errorf("traversal segment rejected")
		}
	}
	if v == "." || v == ".." {
		return "", fmt.Errorf("traversal segment rejected")
	}
	return url.PathEscape(v), nil
}

func placeBody(root map[string]any, dotted string, v any) {
	parts := strings.Split(dotted, ".")
	m := root
	for _, p := range parts[:len(parts)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	m[parts[len(parts)-1]] = v
}

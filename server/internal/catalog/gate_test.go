package catalog

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func mustCat(t *testing.T, defs ...[]byte) *Catalog {
	t.Helper()
	cat, err := ParseDefs(defs...)
	require.NoError(t, err)
	return cat
}

func TestWideningsFlagsReachChanges(t *testing.T) {
	old := mustCat(t, def(t))
	cases := []struct {
		name   string
		mangle []string
		want   string
	}{
		{"method change", []string{`"binding": {"method":"GET"`, `"binding": {"method":"POST"`}, "method changed"},
		{"host change", []string{
			`"hosts": ["fake.example.com"]`, `"hosts": ["fake.example.com","evil.example.com"]`,
			`"binding": {"method":"POST","host":"fake.example.com"`, `"binding": {"method":"POST","host":"evil.example.com"`,
		}, "host changed"},
		{"path change", []string{`"path":"/api/things","query"`, `"path":"/api/everything","query"`}, "path template changed"},
		{"effect change", []string{`"effect": "read"`, `"effect": "write"`}, "effect changed"},
		{"scope added", []string{`"scopes": ["things:read"]`, `"scopes": ["things:read","things:admin"]`}, "scope things:admin added"},
		{"constraint removed", []string{`"constraints": [{"field":"box"}]`, `"constraints": []`}, "constraint on \"box\" removed"},
		{"schema property added", []string{
			`"properties":{"limit":{"type":"integer"}}`,
			`"properties":{"limit":{"type":"integer"},"verbose":{"type":"boolean"}}`,
			`"query":{"limit":"limit"}`, `"query":{"limit":"limit","verbose":"verbose"}`,
		}, "property \"verbose\" added"},
		{"required dropped", []string{`"required":["box","text"]`, `"required":["box"]`}, "no longer required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newCat, err := ParseDefs(def(t, tc.mangle...))
			require.NoError(t, err)
			ws := Widenings(old, newCat)
			found := false
			for _, w := range ws {
				if strings.Contains(w, tc.want) {
					found = true
				}
			}
			require.True(t, found, "want %q in %v", tc.want, ws)
		})
	}
}

func TestWideningsAllowsNarrowingAndRemoval(t *testing.T) {
	old := mustCat(t, def(t))

	// Narrowing edits: enum introduced, constraint added, scope removed,
	// op removed, connector removed.
	narrowed, err := ParseDefs(def(t,
		`"text":{"type":"string"}`, `"text":{"type":"string","enum":["ok"]}`,
	))
	require.NoError(t, err)
	require.Empty(t, Widenings(old, narrowed))

	require.Empty(t, Widenings(old, mustCat(t))) // whole catalog removed: fail-closed elsewhere
}

func TestCheckAgainstBaseline(t *testing.T) {
	base := mustCat(t, def(t))

	// Identical: passes.
	require.NoError(t, CheckAgainstBaseline(base, mustCat(t, def(t))))

	// Widening: refused with the violation named.
	wide, err := ParseDefs(def(t, `"binding": {"method":"POST"`, `"binding": {"method":"PUT"`))
	require.NoError(t, err)
	err = CheckAgainstBaseline(base, wide)
	require.ErrorContains(t, err, "reach-widening")
	require.ErrorContains(t, err, "method changed")

	// Non-widening drift: still refused until the baseline is updated.
	drift, err := ParseDefs(def(t, `"description": "Read things."`, `"description": "Read the things."`))
	require.NoError(t, err)
	err = CheckAgainstBaseline(base, drift)
	require.ErrorContains(t, err, "drifted from baseline")
}

// The embedded defs and baseline must stay in lockstep — this is the
// same check serve runs at startup.
func TestEmbeddedBaselineInLockstep(t *testing.T) {
	_, err := Load()
	require.NoError(t, err)
}

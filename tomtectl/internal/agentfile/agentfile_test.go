package agentfile

import (
	"strings"
	"testing"
)

func TestTemplateIsValid(t *testing.T) {
	a, err := Parse(Template)
	if err != nil {
		t.Fatalf("the init template must parse: %v", err)
	}
	if a.Metadata.Name != "hello" {
		t.Errorf("template name = %q, want hello", a.Metadata.Name)
	}
	if len(a.Spec.Steps) != 1 || a.Spec.Steps[0].ID != "greet" {
		t.Errorf("template steps = %+v, want one step with id greet", a.Spec.Steps)
	}
	if a.Spec.Schedule.Every != "30s" {
		t.Errorf("template every = %q, want 30s", a.Spec.Schedule.Every)
	}
}

const valid = `
apiVersion: tomte.dev/v1alpha1
kind: Agent
metadata:
  name: hello
spec:
  steps:
    - id: greet
      text: Hello, world
  schedule:
    every: 30s
  llm: {}
  connectors: []
`

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(string) string
		wantErr string
	}{
		{"wrong apiVersion", func(s string) string {
			return strings.Replace(s, "tomte.dev/v1alpha1", "tomte.dev/v2", 1)
		}, "apiVersion"},
		{"wrong kind", func(s string) string {
			return strings.Replace(s, "kind: Agent", "kind: Robot", 1)
		}, "kind"},
		{"bad name", func(s string) string {
			return strings.Replace(s, "name: hello", "name: Hello_World", 1)
		}, "metadata.name"},
		{"no steps", func(s string) string {
			return strings.Replace(s, "    - id: greet\n      text: Hello, world\n", "", 1)
		}, "at least one step"},
		{"step missing text", func(s string) string {
			return strings.Replace(s, "      text: Hello, world\n", "", 1)
		}, "id and text"},
		{"bad every", func(s string) string {
			return strings.Replace(s, "every: 30s", "every: soonish", 1)
		}, "not a duration"},
		{"negative every", func(s string) string {
			return strings.Replace(s, "every: 30s", "every: -5s", 1)
		}, "must be positive"},
		{"llm set too early", func(s string) string {
			return strings.Replace(s, "llm: {}", "llm: {endpoint: {kind: anthropic}}", 1)
		}, "K2"},
		{"connectors set too early", func(s string) string {
			return strings.Replace(s, "connectors: []", "connectors: [slack]", 1)
		}, "K3"},
		{"unknown field", func(s string) string {
			return strings.Replace(s, "  schedule:", "  shedule:", 1)
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.mutate(valid)))
			if err == nil {
				t.Fatalf("want error, got nil")
			}
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestDuplicateStepIDs(t *testing.T) {
	doc := strings.Replace(valid,
		"    - id: greet\n      text: Hello, world\n",
		"    - id: greet\n      text: one\n    - id: greet\n      text: two\n", 1)
	if _, err := Parse([]byte(doc)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate-id error, got %v", err)
	}
}

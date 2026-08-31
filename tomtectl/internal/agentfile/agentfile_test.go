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
		}, "one unit"},
		{"negative every", func(s string) string {
			return strings.Replace(s, "every: 30s", "every: -5s", 1)
		}, "one unit"},
		{"compound every busybox cannot sleep", func(s string) string {
			return strings.Replace(s, "every: 30s", "every: 1m30s", 1)
		}, "one unit"},
		{"zero every", func(s string) string {
			return strings.Replace(s, "every: 30s", "every: 0s", 1)
		}, "must be positive"},
		{"K1's commented endpoint sketch is an unknown field now", func(s string) string {
			return strings.Replace(s, "llm: {}", "llm: {endpoint: {kind: anthropic}}", 1)
		}, ""},
		{"connectors set too early", func(s string) string {
			return strings.Replace(s, "connectors: []", "connectors: [slack]", 1)
		}, "K3"},
		{"unknown field", func(s string) string {
			return strings.Replace(s, "  schedule:", "  shedule:", 1)
		}, ""},
		{"bad label key", func(s string) string {
			return strings.Replace(s, "  name: hello", "  name: hello\n  labels:\n    team name: night", 1)
		}, "invalid key"},
		{"bad label value", func(s string) string {
			return strings.Replace(s, "  name: hello", "  name: hello\n  labels:\n    team: night shift", 1)
		}, "invalid value"},
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

const validLLM = `
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
  llm:
    kind: openai_compatible
    base_url: https://api.openai.com/v1
    model: gpt-4o-mini
    secretRef: hello-key
  connectors: []
`

func TestParseAcceptsLLM(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(string) string
	}{
		{"openai_compatible with secretRef", func(s string) string { return s }},
		{"anthropic with secretRef", func(s string) string {
			s = strings.Replace(s, "kind: openai_compatible", "kind: anthropic", 1)
			s = strings.Replace(s, "base_url: https://api.openai.com/v1", "base_url: https://api.anthropic.com", 1)
			return strings.Replace(s, "model: gpt-4o-mini", "model: claude-haiku-4-5", 1)
		}},
		{"local keyless over http loopback", func(s string) string {
			s = strings.Replace(s, "base_url: https://api.openai.com/v1", "base_url: http://127.0.0.1:11434/v1", 1)
			return strings.Replace(s, "secretRef: hello-key", "local: true", 1)
		}},
		{"keyed http to a short .svc name", func(s string) string {
			return strings.Replace(s, "base_url: https://api.openai.com/v1", "base_url: http://llm-stub.default.svc:8080/v1", 1)
		}},
		{"keyed http to a full .svc.cluster.local name", func(s string) string {
			return strings.Replace(s, "base_url: https://api.openai.com/v1", "base_url: http://llm-stub.default.svc.cluster.local:8080/v1", 1)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := Parse([]byte(tc.mutate(validLLM)))
			if err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !a.Spec.LLM.Enabled() {
				t.Fatalf("llm should be enabled")
			}
		})
	}
}

func TestParseRejectsBadLLM(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(string) string
		wantErr string
	}{
		{"unknown llm kind", func(s string) string {
			return strings.Replace(s, "kind: openai_compatible", "kind: copilot", 1)
		}, "spec.llm.kind"},
		{"missing model", func(s string) string {
			return strings.Replace(s, "    model: gpt-4o-mini\n", "", 1)
		}, "spec.llm.model"},
		{"missing base_url", func(s string) string {
			return strings.Replace(s, "    base_url: https://api.openai.com/v1\n", "", 1)
		}, "spec.llm.base_url"},
		{"missing secretRef without local", func(s string) string {
			return strings.Replace(s, "    secretRef: hello-key\n", "", 1)
		}, "secretRef"},
		{"local with secretRef", func(s string) string {
			return strings.Replace(s, "secretRef: hello-key", "secretRef: hello-key\n    local: true", 1)
		}, "keyless"},
		{"keyed plain http to the internet", func(s string) string {
			return strings.Replace(s, "base_url: https://api.openai.com/v1", "base_url: http://api.example.com/v1", 1)
		}, "https"},
		{"keyed plain http to a bare single-label host", func(s string) string {
			// A resolver search domain can send a bare name anywhere;
			// keyed http must spell out the .svc form.
			return strings.Replace(s, "base_url: https://api.openai.com/v1", "base_url: http://llm-stub:8080/v1", 1)
		}, "https"},
		{"userinfo in base_url", func(s string) string {
			return strings.Replace(s, "base_url: https://api.openai.com/v1", "base_url: https://user:pw@api.openai.com/v1", 1)
		}, "userinfo"},
		{"bad secret name", func(s string) string {
			return strings.Replace(s, "secretRef: hello-key", "secretRef: Hello_Key", 1)
		}, "secretRef"},
		{"unknown llm field", func(s string) string {
			return strings.Replace(s, "    model: gpt-4o-mini", "    model: gpt-4o-mini\n    api_key: sk-oops", 1)
		}, ""},
		{"connectors still reject at K2", func(s string) string {
			return strings.Replace(s, "connectors: []", "connectors: [slack]", 1)
		}, "K3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.mutate(validLLM)))
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

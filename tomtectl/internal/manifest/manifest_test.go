package manifest

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/gambtho/tomte/tomtectl/internal/agentfile"
)

func TestObjectsDeriveFromTheFile(t *testing.T) {
	a, err := agentfile.Parse(agentfile.Template)
	if err != nil {
		t.Fatal(err)
	}
	cm, dep := Objects(a, agentfile.Template, DefaultImage)

	if cm.Name != "hello" || dep.Name != "hello" {
		t.Errorf("objects named %q/%q, want the agent name", cm.Name, dep.Name)
	}
	if cm.Data["agent.yaml"] != string(agentfile.Template) {
		t.Error("ConfigMap must carry the agent.yaml byte-for-byte, comments included")
	}
	if _, ok := cm.Data["run.sh"]; ok {
		t.Error("the K1 placeholder script must be gone — the runtime image replaced it")
	}
	if got := dep.Spec.Selector.MatchLabels[AgentLabel]; got != "hello" {
		t.Errorf("selector %s = %q, want hello", AgentLabel, got)
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if c.Image != DefaultImage {
		t.Errorf("image = %q, want %q", c.Image, DefaultImage)
	}
	if c.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Errorf("pull policy = %q, want IfNotPresent (local kind-loaded images)", c.ImagePullPolicy)
	}
	vol := dep.Spec.Template.Spec.Volumes[0]
	if vol.ConfigMap == nil || vol.ConfigMap.Name != cm.Name {
		t.Error("deployment must mount the agent ConfigMap")
	}
	if len(c.Env) != 0 {
		t.Errorf("a keyless agent must get no env, got %v", c.Env)
	}
}

func TestImageOverride(t *testing.T) {
	a, _ := agentfile.Parse(agentfile.Template)
	_, dep := Objects(a, agentfile.Template, "example.com/other:1")
	if got := dep.Spec.Template.Spec.Containers[0].Image; got != "example.com/other:1" {
		t.Errorf("image = %q, want the override", got)
	}
}

const llmDoc = `
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
`

// TestSecretRefBecomesEnv: the key arrives as env from the named
// Secret — never in the ConfigMap, never in the file.
func TestSecretRefBecomesEnv(t *testing.T) {
	a, err := agentfile.Parse([]byte(llmDoc))
	if err != nil {
		t.Fatal(err)
	}
	cm, dep := Objects(a, []byte(llmDoc), DefaultImage)
	c := dep.Spec.Template.Spec.Containers[0]
	if len(c.Env) != 1 || c.Env[0].Name != APIKeyEnv {
		t.Fatalf("want exactly one %s env, got %v", APIKeyEnv, c.Env)
	}
	src := c.Env[0].ValueFrom
	if src == nil || src.SecretKeyRef == nil ||
		src.SecretKeyRef.Name != "hello-key" || src.SecretKeyRef.Key != SecretKey {
		t.Errorf("env must come from secret %q key %q, got %+v", "hello-key", SecretKey, src)
	}
	for k, v := range cm.Data {
		if strings.Contains(v, "hello-key") && k != "agent.yaml" {
			t.Errorf("secret name may only appear in the agent.yaml itself")
		}
	}
}

func TestUserLabelsPropagateButNeverWin(t *testing.T) {
	doc := strings.Replace(string(agentfile.Template),
		"metadata:\n  name: hello",
		"metadata:\n  name: hello\n  labels:\n    team: night\n    tomte.dev/agent: spoofed", 1)
	a, err := agentfile.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	cm, dep := Objects(a, []byte(doc), DefaultImage)
	if cm.Labels["team"] != "night" || dep.Labels["team"] != "night" {
		t.Errorf("user label did not propagate: cm=%v dep=%v", cm.Labels, dep.Labels)
	}
	if cm.Labels[AgentLabel] != "hello" || dep.Spec.Template.Labels[AgentLabel] != "hello" {
		t.Errorf("tomtectl labels must win over user labels: cm=%v pod=%v", cm.Labels, dep.Spec.Template.Labels)
	}
	if got := dep.Spec.Selector.MatchLabels; len(got) != 1 || got[AgentLabel] != "hello" {
		t.Errorf("selector must stay %s alone, got %v", AgentLabel, got)
	}
}

func TestSecretForKey(t *testing.T) {
	s := SecretForKey("hello-key", "hello", []byte("sk-test"))
	if s.Name != "hello-key" {
		t.Errorf("secret name = %q", s.Name)
	}
	if s.Labels[AgentLabel] != "hello" || s.Labels[ManagedByLabel] != ManagedByValue {
		t.Errorf("secret must carry ownership labels, got %v", s.Labels)
	}
	if string(s.Data[SecretKey]) != "sk-test" {
		t.Errorf("secret data[%s] wrong", SecretKey)
	}
}

package manifest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gambtho/tomte/tomtectl/internal/agentfile"
)

func TestObjectsDeriveFromTheFile(t *testing.T) {
	a, err := agentfile.Parse(agentfile.Template)
	if err != nil {
		t.Fatal(err)
	}
	cm, dep := Objects(a, agentfile.Template)

	if cm.Name != "hello" || dep.Name != "hello" {
		t.Errorf("objects named %q/%q, want the agent name", cm.Name, dep.Name)
	}
	if cm.Data["agent.yaml"] != string(agentfile.Template) {
		t.Error("ConfigMap must carry the agent.yaml byte-for-byte, comments included")
	}
	if !strings.Contains(cm.Data["run.sh"], "sleep") {
		t.Error("ConfigMap must carry the runtime script")
	}
	if got := dep.Spec.Selector.MatchLabels[AgentLabel]; got != "hello" {
		t.Errorf("selector %s = %q, want hello", AgentLabel, got)
	}
	if got := dep.Spec.Template.ObjectMeta.Labels[AgentLabel]; got != "hello" {
		t.Errorf("pod label %s = %q, want hello", AgentLabel, got)
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if c.Image != Image {
		t.Errorf("image = %q, want %q", c.Image, Image)
	}
	vol := dep.Spec.Template.Spec.Volumes[0]
	if vol.ConfigMap == nil || vol.ConfigMap.Name != cm.Name {
		t.Error("deployment must mount the agent ConfigMap")
	}
}

// TestRunScriptReadsTheMountedFile executes the placeholder runtime
// under sh against a mounted-style directory and checks the behavior
// really comes from the file, not the script.
func TestRunScriptReadsTheMountedFile(t *testing.T) {
	dir := t.TempDir()
	doc := strings.Replace(string(agentfile.Template), "every: 30s", "every: 1s", 1)
	doc = strings.Replace(doc, "text: Hello, world — from the hello agent.", "text: marker-from-the-yaml", 1)
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(RunScript, "/tomte/agent.yaml", filepath.Join(dir, "agent.yaml"))
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "sh", filepath.Join(dir, "run.sh")).CombinedOutput()

	got := string(out)
	if !strings.Contains(got, "waking every 1s") {
		t.Errorf("script did not read the schedule from the file:\n%s", got)
	}
	if strings.Count(got, "marker-from-the-yaml") < 2 {
		t.Errorf("script did not loop the step text from the file:\n%s", got)
	}
}

package manifest

import (
	"context"
	"errors"
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

// runScriptAgainst executes the placeholder runtime under sh against a
// mounted-style file for a bounded time and returns its output plus
// whether it was still running when the deadline hit.
func runScriptAgainst(t *testing.T, doc string) (output string, ranUntilDeadline bool) {
	t.Helper()
	dir := t.TempDir()
	if doc != "" {
		if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := strings.ReplaceAll(RunScript, "/tomte/agent.yaml", filepath.Join(dir, "agent.yaml"))
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "sh", filepath.Join(dir, "run.sh")).CombinedOutput()
	return string(out), errors.Is(ctx.Err(), context.DeadlineExceeded)
}

// TestRunScriptReadsTheMountedFile checks the behavior really comes
// from the file, not the script — including quote stripping.
func TestRunScriptReadsTheMountedFile(t *testing.T) {
	doc := strings.Replace(string(agentfile.Template), "every: 30s", "every: 1s", 1)
	doc = strings.Replace(doc, "text: Hello, world — from the hello agent.", `text: "marker-from-the-yaml"`, 1)

	got, ranUntilDeadline := runScriptAgainst(t, doc)
	if !ranUntilDeadline {
		t.Fatalf("script exited on its own instead of looping:\n%s", got)
	}
	if !strings.Contains(got, "waking every 1s") {
		t.Errorf("script did not read the schedule from the file:\n%s", got)
	}
	if strings.Count(got, "marker-from-the-yaml") < 2 {
		t.Errorf("script did not loop the step text from the file:\n%s", got)
	}
	if strings.Contains(got, `"marker-from-the-yaml"`) {
		t.Errorf("script did not strip the surrounding quotes:\n%s", got)
	}
}

// TestRunScriptCleansScalarsLikeTheParser: trailing comments and
// single quotes must not leak into sleep arguments or printed text.
func TestRunScriptCleansScalarsLikeTheParser(t *testing.T) {
	doc := strings.Replace(string(agentfile.Template), "every: 30s", "every: 1s # wake fast", 1)
	doc = strings.Replace(doc, "text: Hello, world — from the hello agent.", "text: 'single-quoted' # note", 1)

	got, ranUntilDeadline := runScriptAgainst(t, doc)
	if !ranUntilDeadline {
		t.Fatalf("script exited on its own — the comment likely leaked into sleep:\n%s", got)
	}
	if !strings.Contains(got, "waking every 1s\n") {
		t.Errorf("trailing comment leaked into the schedule:\n%s", got)
	}
	if !strings.Contains(got, "Z single-quoted\n") {
		t.Errorf("single quotes or comment leaked into the text:\n%s", got)
	}
}

// TestRunScriptFailsLoudly: a missing file or missing schedule must
// crash the container visibly, never fall back to a silent default.
func TestRunScriptFailsLoudly(t *testing.T) {
	got, ranUntilDeadline := runScriptAgainst(t, "")
	if ranUntilDeadline || !strings.Contains(got, "not mounted") {
		t.Errorf("missing file: want fast loud exit, got (still running=%v):\n%s", ranUntilDeadline, got)
	}

	noEvery := strings.Replace(string(agentfile.Template), "every: 30s", "", 1)
	got, ranUntilDeadline = runScriptAgainst(t, noEvery)
	if ranUntilDeadline || !strings.Contains(got, "no schedule.every") {
		t.Errorf("missing every: want fast loud exit, got (still running=%v):\n%s", ranUntilDeadline, got)
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
	cm, dep := Objects(a, []byte(doc))
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

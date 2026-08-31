// Package agentfile defines the agent-as-code YAML: the one
// human-readable file that IS the agent. K1 carries identity, steps,
// and a schedule; the llm (K2) and connectors (K3) slots are named but
// must stay empty until their phases land.
package agentfile

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	APIVersion = "tomte.dev/v1alpha1"
	Kind       = "Agent"
)

// Template is the file `tomtectl init` scaffolds.
//
//go:embed template.yaml
var Template []byte

type Agent struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   Metadata `json:"metadata"`
	Spec       Spec     `json:"spec"`
}

type Metadata struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

type Spec struct {
	Steps    []Step   `json:"steps"`
	Schedule Schedule `json:"schedule"`
	// LLM and Connectors are decoded loosely on purpose: K1 only checks
	// that they are empty, so a K2/K3 file fails loudly instead of
	// running with half its topology ignored.
	LLM        map[string]any `json:"llm,omitempty"`
	Connectors []any          `json:"connectors,omitempty"`
}

type Step struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type Schedule struct {
	Every string `json:"every"`
}

// dns1123Label matches what Kubernetes accepts for object names derived
// from metadata.name.
var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// everyShape is deliberately narrower than Go's duration syntax: the
// K1 runtime hands the value to busybox sleep, which accepts a whole
// number with at most one unit. Compound forms like 1m30s would
// crash-loop the pod, so they are rejected here instead.
var everyShape = regexp.MustCompile(`^[0-9]+[smh]$`)

// Load reads, strictly parses, and validates an agent file. The raw
// bytes are returned too: the ConfigMap carries the file verbatim.
func Load(path string) (*Agent, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	a, err := Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return a, raw, nil
}

// Parse strictly decodes and validates agent YAML. Unknown fields are
// errors: a typo'd key must not silently drop part of the topology.
func Parse(raw []byte) (*Agent, error) {
	var a Agent
	if err := yaml.UnmarshalStrict(raw, &a); err != nil {
		return nil, err
	}
	if err := a.validate(); err != nil {
		return nil, err
	}
	return &a, nil
}

func (a *Agent) validate() error {
	if a.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion must be %q, got %q", APIVersion, a.APIVersion)
	}
	if a.Kind != Kind {
		return fmt.Errorf("kind must be %q, got %q", Kind, a.Kind)
	}
	if !dns1123Label.MatchString(a.Metadata.Name) {
		return fmt.Errorf("metadata.name %q must be a lowercase DNS label (a-z, 0-9, hyphens)", a.Metadata.Name)
	}
	if len(a.Spec.Steps) == 0 {
		return fmt.Errorf("spec.steps must list at least one step")
	}
	seen := map[string]bool{}
	for i, s := range a.Spec.Steps {
		if s.ID == "" || s.Text == "" {
			return fmt.Errorf("spec.steps[%d]: id and text are both required", i)
		}
		if seen[s.ID] {
			return fmt.Errorf("spec.steps[%d]: duplicate id %q", i, s.ID)
		}
		seen[s.ID] = true
	}
	if !everyShape.MatchString(a.Spec.Schedule.Every) {
		return fmt.Errorf("spec.schedule.every must be a whole number with one unit — 30s, 5m, 2h — got %q", a.Spec.Schedule.Every)
	}
	if d, _ := time.ParseDuration(a.Spec.Schedule.Every); d <= 0 {
		return fmt.Errorf("spec.schedule.every must be positive, got %q", a.Spec.Schedule.Every)
	}
	if len(a.Spec.LLM) != 0 {
		return fmt.Errorf("spec.llm must stay empty in K1 — the LLM endpoint arrives in K2")
	}
	if len(a.Spec.Connectors) != 0 {
		return fmt.Errorf("spec.connectors must stay empty in K1 — connectors arrive in K3")
	}
	return nil
}

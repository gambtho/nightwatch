// Package agentfile defines the agent-as-code YAML: the one
// human-readable file that IS the agent. K2 carries identity, steps, a
// schedule, and the llm endpoint the agent thinks with; the connectors
// (K3) slot is named but must stay empty until its phase lands.
package agentfile

import (
	_ "embed"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
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
	LLM      LLM      `json:"llm,omitempty"`
	// Connectors are still decoded loosely: K2 only checks emptiness,
	// so a K3 file fails loudly instead of running with half its
	// topology ignored.
	Connectors []any `json:"connectors,omitempty"`
}

// LLM is the model the agent thinks with. An all-zero value means no
// LLM: the agent is deterministic and prints its steps (K1 behavior).
type LLM struct {
	Kind    string `json:"kind,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	Model   string `json:"model,omitempty"`
	// SecretRef names the Kubernetes Secret (key "api_key") holding
	// the API key — the key never appears in this file.
	SecretRef string `json:"secretRef,omitempty"`
	// Local marks an explicitly keyless endpoint ("on this computer" /
	// in-cluster). A loopback URL alone never implies local.
	Local bool `json:"local,omitempty"`
}

// Enabled reports whether the file declares an LLM at all.
func (l LLM) Enabled() bool {
	return l != (LLM{})
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

// everyShape is deliberately narrower than Go's duration syntax: one
// whole number with one unit reads at a glance in the file, and the
// restriction is a published schema contract (K1 shipped it), so the
// Go runtime keeps it.
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
	// Labels are copied onto every derived object; an invalid one would
	// otherwise surface as an opaque API-server rejection at `up`.
	for k, v := range a.Metadata.Labels {
		if errs := validation.IsQualifiedName(k); len(errs) > 0 {
			return fmt.Errorf("metadata.labels: invalid key %q: %s", k, strings.Join(errs, "; "))
		}
		if errs := validation.IsValidLabelValue(v); len(errs) > 0 {
			return fmt.Errorf("metadata.labels[%q]: invalid value %q: %s", k, v, strings.Join(errs, "; "))
		}
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
	if a.Spec.LLM.Enabled() {
		if err := a.Spec.LLM.validate(); err != nil {
			return err
		}
	}
	if len(a.Spec.Connectors) != 0 {
		return fmt.Errorf("spec.connectors must stay empty in K2 — connectors arrive in K3")
	}
	return nil
}

func (l LLM) validate() error {
	if l.Kind != "anthropic" && l.Kind != "openai_compatible" {
		return fmt.Errorf("spec.llm.kind must be anthropic or openai_compatible, got %q", l.Kind)
	}
	if l.BaseURL == "" {
		return fmt.Errorf("spec.llm.base_url is required")
	}
	u, err := url.Parse(l.BaseURL)
	if err != nil {
		return fmt.Errorf("spec.llm.base_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("spec.llm.base_url must be an http(s) URL, got %q", l.BaseURL)
	}
	if u.User != nil {
		return fmt.Errorf("spec.llm.base_url must not carry userinfo")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("spec.llm.base_url has no host: %q", l.BaseURL)
	}
	if l.Model == "" {
		return fmt.Errorf("spec.llm.model is required")
	}
	if l.Local {
		if l.SecretRef != "" {
			return fmt.Errorf("spec.llm: local endpoints are keyless — drop secretRef or drop local")
		}
		return nil
	}
	if l.SecretRef == "" {
		return fmt.Errorf("spec.llm.secretRef is required (the Kubernetes Secret holding the API key; `tomtectl set-key` creates it) unless local: true")
	}
	if errs := validation.IsDNS1123Subdomain(l.SecretRef); len(errs) > 0 {
		return fmt.Errorf("spec.llm.secretRef %q is not a valid Secret name: %s", l.SecretRef, strings.Join(errs, "; "))
	}
	// A key must never travel in cleartext to the open internet. Plain
	// http stays legal only where cleartext cannot leave the machine or
	// the cluster: loopback, or a cluster-local service name.
	if u.Scheme == "http" && !clusterLocalHost(u.Hostname()) {
		return fmt.Errorf("spec.llm.base_url must use https when secretRef is set (plain http is allowed only for loopback or cluster-local hosts), got %q", l.BaseURL)
	}
	return nil
}

// clusterLocalHost reports whether cleartext to host cannot leave the
// machine or the cluster: loopback addresses, single-label service
// names, and *.svc / *.svc.cluster.local DNS names.
func clusterLocalHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	if host == "localhost" {
		return true
	}
	if !strings.Contains(host, ".") {
		return true // a single-label in-cluster Service name
	}
	host = strings.TrimSuffix(host, ".")
	return strings.HasSuffix(host, ".svc") || strings.HasSuffix(host, ".svc.cluster.local")
}

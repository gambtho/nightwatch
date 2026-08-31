// Package endpoint models the tenant's one configured LLM endpoint
// ("Endpoint agnosticism"): a preset or user-entered base URL, the wire
// kind, the credential connection, and the explicit zero-cost
// classification. It is a leaf package — proxy, httpapi, and the shell
// all speak this type.
package endpoint

import (
	"fmt"
	"net/url"
	"strings"
)

// Presets. anthropic/openai/openrouter/github pin fixed base URLs;
// azure/custom/local carry a user-entered one.
const (
	PresetAnthropic  = "anthropic"
	PresetOpenAI     = "openai"
	PresetOpenRouter = "openrouter"
	PresetGitHub     = "github"
	PresetAzure      = "azure"
	PresetCustom     = "custom"
	PresetLocal      = "local"
)

// Wire kinds: which SDK shape and allowed path the endpoint speaks.
const (
	KindAnthropic        = "anthropic"
	KindOpenAICompatible = "openai_compatible"
)

var fixedBase = map[string]string{
	PresetAnthropic:  "https://api.anthropic.com",
	PresetOpenAI:     "https://api.openai.com/v1",
	PresetOpenRouter: "https://openrouter.ai/api/v1",
	PresetGitHub:     "https://models.github.ai/inference",
}

type Endpoint struct {
	Preset  string
	Kind    string
	BaseURL string // canonical (Canonical's output)
	// Connection names the vault llm_api_key credential; empty only for
	// local, which carries no credential at all.
	Connection string
	RunModel   string
	// ZeroCost is explicit classification, never inference: local always;
	// github only when the user chose the free included quota. A loopback
	// base URL alone never implies it — a tunnel to a paid service can
	// sit on localhost.
	ZeroCost bool
}

// Provider is the name the permit allowlist, the compiled document, and
// the proxy route all key on — the harness names a provider, never a URL.
func (e Endpoint) Provider() string { return e.Preset }

// Route is the endpoint's entire blast radius: one upstream base and
// exactly one allowed (method, path) — the same allowlist as today
// (POST v1/messages | POST chat/completions; the Azure v1 API needs no
// api-version parameter).
func (e Endpoint) Route() (base, method, path string) {
	if e.Kind == KindAnthropic {
		return e.BaseURL, "POST", "v1/messages"
	}
	return e.BaseURL, "POST", "chat/completions"
}

// CredentialHeader names the header the proxy injects the credential in:
// "x-api-key" (anthropic), "api-key" (Azure — Bearer there is
// Entra-token-only), "authorization" (Bearer) for the rest, "" for local
// (nothing is injected).
func (e Endpoint) CredentialHeader() string {
	switch {
	case e.Preset == PresetLocal:
		return ""
	case e.Preset == PresetAzure:
		return "api-key"
	case e.Kind == KindAnthropic:
		return "x-api-key"
	default:
		return "authorization"
	}
}

func isLoopbackHost(hostname string) bool {
	return hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1"
}

// Canonical normalizes a base URL: lowercase scheme and host, no trailing
// slash. It rejects userinfo, query, and fragment outright — none of them
// belong in an upstream base.
func Canonical(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("endpoint: base URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" || u.Opaque != "" {
		return "", fmt.Errorf("endpoint: base URL %q must be absolute", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("endpoint: base URL must not carry userinfo")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("endpoint: base URL must not carry a query or fragment")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

// Validate normalizes and checks an endpoint record: fixed-base presets
// get their pinned URL (any submitted one is ignored), user-entered ones
// are canonicalized and checked (HTTPS except loopback; Azure's host
// suffix and /openai/v1 path), the zero-cost and connection rules are
// enforced, and the kind is derived from the preset.
func Validate(e Endpoint) (Endpoint, error) {
	if e.RunModel == "" {
		return e, fmt.Errorf("endpoint: run model required")
	}
	if e.Preset == PresetAnthropic {
		e.Kind = KindAnthropic
	} else {
		e.Kind = KindOpenAICompatible
	}

	if base, fixed := fixedBase[e.Preset]; fixed {
		e.BaseURL = base
	} else {
		canon, err := Canonical(e.BaseURL)
		if err != nil {
			return e, err
		}
		e.BaseURL = canon
	}
	u, err := url.Parse(e.BaseURL)
	if err != nil {
		return e, fmt.Errorf("endpoint: base URL: %w", err)
	}

	switch e.Preset {
	case PresetAnthropic, PresetOpenAI, PresetOpenRouter, PresetGitHub:
		// Fixed base; nothing further to check.
	case PresetAzure:
		if u.Scheme != "https" {
			return e, fmt.Errorf("endpoint: azure base URL must be https")
		}
		host := u.Hostname()
		if !strings.HasSuffix(host, ".openai.azure.com") && !strings.HasSuffix(host, ".services.ai.azure.com") {
			return e, fmt.Errorf("endpoint: azure base URL host must end in .openai.azure.com or .services.ai.azure.com")
		}
		if u.Path != "/openai/v1" {
			return e, fmt.Errorf("endpoint: azure base URL path must be /openai/v1 (the v1 API)")
		}
	case PresetCustom:
		if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
			return e, fmt.Errorf("endpoint: base URL must be https (http is allowed for loopback hosts only)")
		}
	case PresetLocal:
		// Loopback is necessary but never sufficient — the preset choice,
		// not the host, is what classifies the endpoint local.
		if !isLoopbackHost(u.Hostname()) {
			return e, fmt.Errorf("endpoint: a local endpoint must point at a loopback host")
		}
	default:
		return e, fmt.Errorf("endpoint: unknown preset %q", e.Preset)
	}

	switch e.Preset {
	case PresetLocal:
		if e.Connection != "" {
			return e, fmt.Errorf("endpoint: a local endpoint carries no credential connection")
		}
		e.ZeroCost = true
	case PresetGitHub:
		if e.Connection == "" {
			return e, fmt.Errorf("endpoint: connection required")
		}
		// ZeroCost is the user's free-vs-paid answer; both are valid.
	default:
		if e.Connection == "" {
			return e, fmt.Errorf("endpoint: connection required")
		}
		e.ZeroCost = false
	}
	return e, nil
}

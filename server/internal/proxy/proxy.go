// Package proxy is the egress gateway: the permit's enforcement point and
// the only place credentials are attached to outbound traffic. It depends
// only on the permit package — auth, storage, and crypto reach it through
// the narrow interfaces in Deps, which is what lets it become a standalone
// service later without a redesign.
package proxy

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/catalog"
	"github.com/gambtho/nightwatch/server/internal/permit"
)

type RunIdentity struct{ TenantID, RunID uuid.UUID }

// Secret is an injectable credential. MarkBroken, when set (oauth-kind
// connections), demotes the backing credential to needs_reauth after an
// upstream 401 — epoch-CAS inside, so a 401 earned by a stale token
// cannot demote a connection already refreshed to a newer one; it
// reports whether the demotion applied.
type Secret struct {
	Value      string
	MarkBroken func(ctx context.Context) (bool, error)
}

// HookRequest identifies the request the Hook is asked to admit.
// Provider is set on LLM routes; Connector/Op on connector routes — the
// additive widening that lets Plan 3 price tool calls without reopening
// the proxy.
type HookRequest struct {
	Identity  RunIdentity
	Provider  string
	Connector string
	Op        string
}

type AuthSource interface {
	VerifyRunToken(ctx context.Context, bearer string) (RunIdentity, error)
}

type PermitSource interface {
	PermitForRun(ctx context.Context, tenantID, runID uuid.UUID) (permit.Permit, error)
}

type CredentialSource interface {
	Credential(ctx context.Context, tenantID uuid.UUID, name, provider string) (Secret, error)
}

type EventSink interface {
	AppendEvent(ctx context.Context, tenantID, runID uuid.UUID, typ string, payload map[string]any) error
}

type Hook interface {
	Before(ctx context.Context, req HookRequest) error
}

type NopHook struct{}

func (NopHook) Before(ctx context.Context, req HookRequest) error { return nil }

// HookError lets a Hook (Plan 3 metering) choose the response status.
// Only 403 and 429 are honored; any other error maps to 403.
type HookError struct {
	Status int
	Msg    string
}

func (e HookError) Error() string { return e.Msg }

// ProviderRoute is a provider's entire v1 blast radius: one upstream base
// and exactly one allowed (method, path). Any other request to the origin
// is denied before credential injection.
type ProviderRoute struct {
	Base   string // upstream base URL, including any prefix the SDK folds into its base
	Method string
	Path   string // the forwarded remainder the SDK emits, no leading slash
}

type Config struct {
	Providers    map[string]ProviderRoute // DefaultConfig fills the three real providers
	InternalBase string                   // base URL of the internal API for the pass-through route

	// ConnectorUpstreams overrides the upstream base URL (scheme+host)
	// per connector id. Production leaves it empty — the catalog
	// binding's host is authoritative; tests point curated connectors at
	// fake upstreams with it.
	ConnectorUpstreams map[string]string
	// ConnectorTimeout bounds one compiled upstream request end to end;
	// zero or negative means the 60s default (the harness per-tool
	// budget).
	ConnectorTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{Providers: map[string]ProviderRoute{
		// The SDKs fold a version prefix into their base URL, so the
		// forwarded {path...} excludes it; each Base carries the prefix the
		// real host expects, and Path is exactly what the SDK emits. One
		// (method, path) per provider IS the v1 blast radius.
		"anthropic":  {Base: "https://api.anthropic.com", Method: "POST", Path: "v1/messages"},
		"openai":     {Base: "https://api.openai.com/v1", Method: "POST", Path: "chat/completions"},
		"openrouter": {Base: "https://openrouter.ai/api/v1", Method: "POST", Path: "chat/completions"},
	}}
}

type Deps struct {
	Auth        AuthSource
	Permits     PermitSource
	Credentials CredentialSource
	Events      EventSink
	Hook        Hook
	Config      Config
	// Catalog serves the connector routes. Like permit, it is
	// declarative data with no reach of its own — the proxy stays
	// standalone-capable. Nil disables the connector routes (404).
	Catalog *catalog.Catalog
}

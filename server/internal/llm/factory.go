package llm

import "fmt"

type Config struct {
	AnthropicBaseURL  string // "" means the SDK default
	OpenAIBaseURL     string
	OpenRouterBaseURL string
}

const defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"

// NewFactory returns a provider lookup. Supported: anthropic, openai,
// openrouter. API keys are per-call (CallOptions), not per-factory.
func NewFactory(cfg Config) func(name string) (Provider, error) {
	return func(name string) (Provider, error) {
		switch name {
		case "anthropic":
			return NewAnthropic(cfg.AnthropicBaseURL), nil
		case "openai":
			return NewOpenAI(cfg.OpenAIBaseURL), nil
		case "openrouter":
			base := cfg.OpenRouterBaseURL
			if base == "" {
				base = defaultOpenRouterBaseURL
			}
			return NewOpenAI(base), nil
		default:
			return nil, fmt.Errorf("llm: unknown provider %q (supported: anthropic, openai, openrouter)", name)
		}
	}
}

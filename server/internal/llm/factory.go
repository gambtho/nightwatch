package llm

// NewProxyFactory returns a provider lookup for the harness: every
// provider name resolves to an SDK client pointed at the egress proxy's
// per-provider route. anthropic speaks the Anthropic wire shape;
// everything else (openai, openrouter, github, azure, custom, local) is
// OpenAI-compatible. Unknown names are not an error here — the proxy is
// the enforcement point and 403s a provider with no route; the factory
// only chooses the wire shape.
func NewProxyFactory(proxyBase string) func(name string) (Provider, error) {
	return func(name string) (Provider, error) {
		base := proxyBase + "/proxy/llm/" + name
		if name == "anthropic" {
			return NewAnthropic(base), nil
		}
		return NewOpenAI(base), nil
	}
}

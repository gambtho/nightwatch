package llm

import "testing"

func TestNewProxyFactory(t *testing.T) {
	factory := NewProxyFactory("http://127.0.0.1:9999")
	for _, name := range []string{"anthropic", "openai", "openrouter", "github", "azure", "custom", "local"} {
		p, err := factory(name)
		if err != nil || p == nil {
			t.Fatalf("factory(%q): %v", name, err)
		}
	}
}

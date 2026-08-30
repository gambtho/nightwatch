package llm

import "testing"

func TestNewFactory(t *testing.T) {
	factory := NewFactory(Config{})
	for _, name := range []string{"anthropic", "openai", "openrouter"} {
		if _, err := factory(name); err != nil {
			t.Fatalf("factory(%q): %v", name, err)
		}
	}
	if _, err := factory("copilot-enterprise"); err == nil {
		t.Fatal("dropped provider should error")
	}
}

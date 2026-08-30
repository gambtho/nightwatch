package llm

import "testing"

func TestCostCentsKnownAndUnknown(t *testing.T) {
	u := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	if got := CostCents("anthropic", "no-such-model", u); got != 0 {
		t.Fatalf("unknown model: want 0, got %d", got)
	}
	// Any priced anthropic model must cost more than zero at 1M/1M tokens.
	// Pick the first entry in the table rather than hardcoding a model name.
	for model := range priceTable["anthropic"] {
		if got := CostCents("anthropic", model, u); got <= 0 {
			t.Fatalf("model %s: want > 0, got %d", model, got)
		}
		break
	}
}

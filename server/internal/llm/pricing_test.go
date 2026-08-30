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

// TestCostCentsFloorsCombinedNotSeparately guards against flooring input and
// output cents independently, which loses fractional cents that would round
// up when combined. gpt-4o-mini prices at in:15, out:60 cents per 1M tokens.
// 50,000 input tokens costs 50_000*15/1_000_000 = 0.75 cents; 5,000 output
// tokens costs 5_000*60/1_000_000 = 0.3 cents. Floored separately that's
// 0+0 = 0 cents. Summed first (0.75+0.3 = 1.05) and floored once, it's the
// correct 1 cent.
func TestCostCentsFloorsCombinedNotSeparately(t *testing.T) {
	u := Usage{InputTokens: 50_000, OutputTokens: 5_000}
	if got := CostCents("openai", "gpt-4o-mini", u); got != 1 {
		t.Fatalf("want 1 cent, got %d", got)
	}
}

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
	// Pin the default fixture model's public-list rate exactly ($2/$10 per
	// 1M as of 2026-08): 1M in + 1M out = 200 + 1000 cents.
	if got := CostCents("anthropic", "claude-sonnet-5", u); got != 1200 {
		t.Fatalf("claude-sonnet-5 at 1M/1M: want 1200 cents, got %d", got)
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

func TestPriced(t *testing.T) {
	if !Priced("anthropic", "claude-sonnet-5") {
		t.Fatal("claude-sonnet-5 must be priced — every fixture uses it")
	}
	if Priced("anthropic", "no-such-model") {
		t.Fatal("unknown model must not be priced")
	}
	if Priced("nope", "gpt-4o-mini") {
		t.Fatal("unknown provider must not be priced")
	}
}

func TestMaxTokensForBudget(t *testing.T) {
	// claude-haiku-4-5 out price is 500 cents/1M: 50 cents buys 100k tokens,
	// clamped to the 8192 ceiling.
	if got := MaxTokensForBudget("anthropic", "claude-haiku-4-5", 50); got != 8192 {
		t.Fatalf("clamped budget: got %d, want 8192", got)
	}
	// claude-opus-4-6 out price 7500: 1 cent buys 133 tokens.
	if got := MaxTokensForBudget("anthropic", "claude-opus-4-6", 1); got != 133 {
		t.Fatalf("small budget: got %d, want 133", got)
	}
	// No spend cap: platform default.
	if got := MaxTokensForBudget("anthropic", "claude-haiku-4-5", 0); got != 4096 {
		t.Fatalf("no cap: got %d, want 4096", got)
	}
	// Unpriced pair falls back to the default, never zero.
	if got := MaxTokensForBudget("anthropic", "unknown", 50); got != 4096 {
		t.Fatalf("unpriced: got %d, want 4096", got)
	}
}

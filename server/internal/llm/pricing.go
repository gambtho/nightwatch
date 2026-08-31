package llm

// pricePer1M is USD cents per 1,000,000 tokens, as fixed-point integers.
// 75 means $0.75. This bundled table covers the fixed-base presets only;
// azure and custom endpoints are priced by user-entered rows
// (store.ModelPrice), resolved at approval time.
type pricePer1M struct{ in, out int }

// priceTable is a minimal lookup for public-list pricing as of 2026-Q1.
// Unknown (provider, model) combinations return 0 rather than erroring —
// a run must not fail because we haven't catalogued a new model. Operators
// can PR new rows; stale rows are harmless (they just under/over-report).
// On the legacy env-mode path, approval fails closed on unpriced pairs, so
// CostCents returning 0 for an unknown model is safe there; endpoint-era
// approvals record their own prices in the compiled doc and never reach
// this table at run time.
var priceTable = map[string]map[string]pricePer1M{
	"openai": {
		"gpt-4o-mini": {in: 15, out: 60},
		"gpt-4o":      {in: 250, out: 1000},
		"gpt-5.1":     {in: 300, out: 600},
	},
	// OpenRouter passes through provider pricing; rows here are approximate
	// public-list prices as of 2026-Q1 for cost estimation only.
	"openrouter": {
		"openai/gpt-4o-mini":                {in: 15, out: 60},
		"openai/gpt-4o":                     {in: 250, out: 1000},
		"anthropic/claude-haiku-4.5":        {in: 100, out: 500},
		"anthropic/claude-sonnet-4.5":       {in: 300, out: 1500},
		"meta-llama/llama-3.3-70b-instruct": {in: 59, out: 79},
		"google/gemini-2.0-flash-001":       {in: 10, out: 40},
	},
	"anthropic": {
		"claude-haiku-4-5": {in: 100, out: 500},
		// Sonnet 5 launched at $2/$10 per 1M; the $3/$15 increase once
		// scheduled for 2026-09-01 was cancelled.
		"claude-sonnet-5":   {in: 200, out: 1000},
		"claude-sonnet-4-5": {in: 300, out: 1500},
		"claude-opus-4-6":   {in: 1500, out: 7500},
	},
}

// CostCents returns the run cost in whole cents (floored) for the given
// provider, model, and token usage. Returns 0 for unknown entries so the
// finalize path never emits a negative or arbitrary value.
func CostCents(provider, model string, u Usage) int {
	models, ok := priceTable[provider]
	if !ok {
		return 0
	}
	p, ok := models[model]
	if !ok {
		return 0
	}
	// Math: sum the input and output numerators before dividing, so we floor
	// once on the combined total instead of flooring input and output
	// separately (which can throw away up to 2 cents per run — e.g. 0.75 +
	// 0.75 floors to 0+0 instead of the correct 1). Use int64 to avoid
	// overflow at token counts in the hundreds of millions.
	numerator := int64(u.InputTokens)*int64(p.in) + int64(u.OutputTokens)*int64(p.out)
	return int(numerator / 1_000_000)
}

// Priced reports whether a (provider, model) pair has a bundled price
// row — the legacy env-mode approval gate (endpoint-era approvals resolve
// prices through resolvePrices instead: bundled row, then user-entered
// row, then a 400 that asks for one).
func Priced(provider, model string) bool {
	_, ok := priceTable[provider][model]
	return ok
}

// Price returns the bundled table row for a (provider, model) pair — the
// preset path of the reworked gate; azure/custom endpoints use
// user-entered rows instead (store.ModelPrice).
func Price(provider, model string) (inCentsPer1M, outCentsPer1M int, ok bool) {
	p, ok := priceTable[provider][model]
	return p.in, p.out, ok
}

// Token bounds for the compiled max_tokens: the default when no spend cap
// was approved, and a ceiling so a generous cap cannot compile a
// max_tokens beyond what the catalogued models' output limits accept.
const (
	defaultBudgetTokens = 4096
	maxBudgetTokens     = 8192
)

// MaxTokensForBudget derives the compiled max_tokens from the approved
// per-run spend cap against the model's output price: the largest output
// whose cost alone cannot exceed the cap, clamped to [1, 8192]. A version
// with no spend cap (perRunCents <= 0) gets the platform default. Callers
// must have checked Priced first; an unpriced pair falls back to the
// default rather than an arbitrary value.
func MaxTokensForBudget(provider, model string, perRunCents int) int {
	p := priceTable[provider][model]
	return MaxTokensForOutPrice(p.out, perRunCents)
}

// MaxTokensForOutPrice is the same derivation with the output price
// supplied directly — the endpoint path, where prices may be user-entered
// or zero (zero-cost endpoints fall back to the fixed default).
func MaxTokensForOutPrice(outCentsPer1M, perRunCents int) int {
	if perRunCents <= 0 || outCentsPer1M <= 0 {
		return defaultBudgetTokens
	}
	tokens := int64(perRunCents) * 1_000_000 / int64(outCentsPer1M)
	if tokens > maxBudgetTokens {
		return maxBudgetTokens
	}
	if tokens < 1 {
		return 1
	}
	return int(tokens)
}

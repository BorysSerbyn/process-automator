package agent

import "strings"

// price is a model's API list price, in USD per million tokens.
type price struct {
	in, out float64
	// cacheRead is the price of prompt-cache reads; 0 when not known, in which
	// case they're priced as input (an overestimate, the safe side for budgets).
	cacheRead float64
}

// cacheWriteFactor is what a 5-minute prompt-cache write costs relative to input.
const cacheWriteFactor = 1.25

// prices are Anthropic's list prices for the models the claude-api backend runs.
// Keep them current: a model missing here has no cost, so its runs don't
// count toward the LLM budgets (the run's log says so).
var prices = map[string]price{
	"claude-fable-5-2":  {in: 8, out: 40, cacheRead: 0.20},
	"claude-fable-5-1":  {in: 10, out: 50, cacheRead: 0.25},
	"claude-opus-5-5":   {in: 4, out: 20, cacheRead: 0.20},
	"claude-sonnet-5-5": {in: 2, out: 10, cacheRead: 0.10},
	// Haiku 5.5 costs more for prompts over 100K tokens; runs are priced at
	// the lower tier, since only their totals are known.
	"claude-haiku-5-5": {in: 0.10, out: 0.50},
}

// apiCost is a run's cost at list prices. input counts every prompt token,
// cacheRead and cacheWrite included (as the API backend reports them). ok is
// false when the model's price isn't known.
func apiCost(model string, input, output, cacheRead, cacheWrite int) (cost float64, ok bool) {
	p, ok := prices[strings.TrimPrefix(model, "anthropic/")]
	if !ok {
		return 0, false
	}
	readPrice := p.cacheRead
	if readPrice == 0 {
		readPrice = p.in
	}
	uncached := input - cacheRead - cacheWrite
	if uncached < 0 {
		uncached = 0
	}
	perToken := func(n int, usdPerM float64) float64 { return float64(n) * usdPerM / 1e6 }
	return perToken(uncached, p.in) + perToken(cacheRead, readPrice) +
		perToken(cacheWrite, p.in*cacheWriteFactor) + perToken(output, p.out), true
}

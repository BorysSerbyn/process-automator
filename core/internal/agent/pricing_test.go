package agent

import (
	"math"
	"testing"
)

func TestAPICost(t *testing.T) {
	// 1M uncached input + 1M cache reads + 1M cache writes + 1M output on Sonnet 5.5.
	got, ok := apiCost("anthropic/claude-sonnet-5-5", 3_000_000, 1_000_000, 1_000_000, 1_000_000)
	if want := 2 + 0.10 + 2*1.25 + 10; !ok || math.Abs(got-want) > 1e-9 {
		t.Errorf("sonnet: %v %v, want %v", got, ok, want)
	}
	// Unknown cache-read price: reads priced as input.
	got, _ = apiCost("claude-haiku-5-5", 1_000_000, 0, 1_000_000, 0)
	if math.Abs(got-0.10) > 1e-9 {
		t.Errorf("haiku cache reads: %v", got)
	}
	if _, ok := apiCost("anthropic/claude-unknown", 1, 1, 0, 0); ok {
		t.Error("an unknown model was priced")
	}
}

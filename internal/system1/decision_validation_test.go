package system1

import (
	"math"
	"testing"
)

func TestNonfiniteProbability(t *testing.T) {
	for _, p := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run("nonfinite", func(t *testing.T) {
			if (Decision{Verdict: Extract, ExtractProbability: &p}).Validate() == nil {
				t.Fatal("accepted nonfinite probability")
			}
		})
	}
}

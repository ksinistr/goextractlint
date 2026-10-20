package system1

import (
	"fmt"
	"math"
)

type Verdict string

const (
	Extract             Verdict = "extract"
	Keep                Verdict = "keep"
	InsufficientContext Verdict = "insufficient_context"
)

type Decision struct {
	Verdict            Verdict  `json:"verdict"`
	ExtractProbability *float64 `json:"extract_probability,omitempty"`
}

func (d Decision) Validate() error {
	switch d.Verdict {
	case Extract, Keep, InsufficientContext:
	default:
		return fmt.Errorf("decision has no valid verdict answer: %q", d.Verdict)
	}

	if p := d.ExtractProbability; p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0) || *p < 0 || *p > 1) {
		return fmt.Errorf("invalid extract probability: %v", *p)
	}

	return nil
}

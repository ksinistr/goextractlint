package analysis

import (
	"encoding/json"
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
		return fmt.Errorf("jev response has no valid verdict answer: %q", d.Verdict)
	}

	if p := d.ExtractProbability; p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0) || *p < 0 || *p > 1) {
		return fmt.Errorf("invalid extract probability: %v", *p)
	}

	return nil
}

func Decode(data []byte) (Decision, error) {
	var response struct {
		Answers struct {
			Verdict struct {
				Type          string  `json:"type"`
				Choice        Verdict `json:"choice"`
				Probabilities struct {
					Extract *float64 `json:"extract"`
				} `json:"probabilities"`
			} `json:"verdict"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return Decision{}, fmt.Errorf("decode jev response: %w", err)
	}

	answer := response.Answers.Verdict
	if answer.Type != "choice" {
		return Decision{}, fmt.Errorf("jev response has no valid verdict answer")
	}

	decision := Decision{Verdict: answer.Choice, ExtractProbability: answer.Probabilities.Extract}

	return decision, decision.Validate()
}

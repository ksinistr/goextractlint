package system1

import (
	"encoding/json"
	"fmt"
	"slices"

	"goextractlint/internal/goparse"
)

const estimatedBytesPerToken = 3

func prepareRequest(target Config, function goparse.Function) (preparedRequest, error) {
	fitted, err := fitContext(target, function)
	if err != nil {
		return preparedRequest{}, err
	}

	data, err := encodeRequest(target.Model, fitted)
	if err != nil {
		return preparedRequest{}, err
	}

	return preparedRequest{
		Endpoint: target.Endpoint,
		Model:    target.Model,
		Body:     data,
	}, nil
}

func fitContext(target Config, function goparse.Function) (goparse.Function, error) {
	candidates := function.Context
	function.Context = nil

	for _, item := range candidates {
		candidate := function
		candidate.Context = append(slices.Clone(function.Context), item)

		data, err := encodeRequest(target.Model, candidate)
		if err != nil {
			return goparse.Function{}, err
		}

		if estimatedTokens(data) > target.MaxTokens {
			function.ContextTruncated = true
			continue
		}

		function = candidate
	}

	return function, nil
}

func estimatedTokens(data []byte) int {
	return (len(data) + estimatedBytesPerToken - 1) / estimatedBytesPerToken
}

func encodeRequest(model string, function goparse.Function) ([]byte, error) {
	payload := map[string]any{
		"model": model,
		"state": map[string]any{"principle": principle(), "function": function},
		"questions": map[string]any{
			"verdict": map[string]any{
				"type": "choice",
				"instructions": "Using the principle in state, decide whether this function contains a fragment worth " +
					"extracting to reveal intention.",
				"criteria": map[string]string{
					"extract": "A specific fragment hides an intention behind implementation details; an " +
						"intention-revealing named function would improve understanding of the enclosing " +
						"function.",
					"keep": "The function is understandable at its current level; extraction would merely relocate " +
						"details or add indirection without clarifying intention.",
					"insufficient_context": "Missing domain or type information prevents a reliable judgment about whether " +
						"extraction would clarify intention.",
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	return data, nil
}

func decodeDecision(data []byte) (Decision, error) {
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

	if err := decision.Validate(); err != nil {
		return Decision{}, fmt.Errorf("validate decision: %w", err)
	}

	return decision, nil
}

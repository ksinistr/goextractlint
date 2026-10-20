package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"goextractlint/internal/domain"
	"goextractlint/internal/system1"
)

type (
	preparedRequest  = system1.PreparedRequest
	functionAnalyzer interface {
		Analyze(context.Context, preparedRequest) (domain.Decision, error)
	}
)

type system1Analyzer struct{ client *system1.Client }

func newSystem1Analyzer(client *system1.Client) *system1Analyzer {
	return &system1Analyzer{client: client}
}

func (a *system1Analyzer) Analyze(ctx context.Context, request preparedRequest) (domain.Decision, error) {
	body, err := a.client.Call(ctx, request)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("call system1: %w", err)
	}

	return decodeDecision(body)
}

const estimatedBytesPerToken = 3

func prepareAnalysis(target backend, function domain.Function) (preparedRequest, error) {
	fitted, err := fitContext(target, function)
	if err != nil {
		return preparedRequest{}, err
	}

	data, err := encodeAnalysis(target.model, fitted)
	if err != nil {
		return preparedRequest{}, err
	}

	return preparedRequest{
		Endpoint: target.endpoint,
		Model:    target.model,
		Body:     data,
	}, nil
}

func fitContext(target backend, function domain.Function) (domain.Function, error) {
	candidates := function.Context
	function.Context = nil

	for _, item := range candidates {
		candidate := function
		candidate.Context = append(slices.Clone(function.Context), item)

		data, err := encodeAnalysis(target.model, candidate)
		if err != nil {
			return domain.Function{}, err
		}

		if estimatedTokens(data) > target.maxTokens {
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

func encodeAnalysis(model string, function domain.Function) ([]byte, error) {
	payload := map[string]any{
		"model": model,
		"state": map[string]any{"principle": domain.Principle(), "function": function},
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

func decodeDecision(data []byte) (domain.Decision, error) {
	var response struct {
		Answers struct {
			Verdict struct {
				Type          string         `json:"type"`
				Choice        domain.Verdict `json:"choice"`
				Probabilities struct {
					Extract *float64 `json:"extract"`
				} `json:"probabilities"`
			} `json:"verdict"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return domain.Decision{}, fmt.Errorf("decode jev response: %w", err)
	}

	answer := response.Answers.Verdict
	if answer.Type != "choice" {
		return domain.Decision{}, fmt.Errorf("jev response has no valid verdict answer")
	}

	decision := domain.Decision{Verdict: answer.Choice, ExtractProbability: answer.Probabilities.Extract}

	if err := decision.Validate(); err != nil {
		return domain.Decision{}, fmt.Errorf("validate decision: %w", err)
	}

	return decision, nil
}

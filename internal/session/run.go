package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"goextractlint/internal/analysis"
	"goextractlint/internal/source"
)

type Result struct {
	File               string           `json:"file"`
	Function           string           `json:"function"`
	StartLine          int              `json:"start_line"`
	EndLine            int              `json:"end_line"`
	Verdict            analysis.Verdict `json:"verdict,omitempty"`
	ExtractProbability *float64         `json:"extract_probability,omitempty"`
	Error              string           `json:"error,omitempty"`
}

func Run(
	ctx context.Context,
	paths []string,
	paths0 io.Reader,
	stopRead func() error,
	loader *source.ContextLoader,
	analyzer analysis.Analyzer,
	endpoint, model string,
	output, diagnostics io.Writer,
) error {
	encoder := json.NewEncoder(output)
	failed := false

	err := source.Walk(ctx, paths, paths0, stopRead, loader, func(function source.Function) error {
		result, err := analyzeFunction(ctx, function, loader, analyzer, endpoint, model)
		if err != nil {
			return err
		}

		if result.Verdict == "" {
			failed = true

			fmt.Fprintf(
				diagnostics,
				"%s:%d %s: %s\n",
				result.File,
				result.StartLine,
				result.Function,
				result.Error,
			)
		}

		return encoder.Encode(result)
	})
	if failed {
		err = errors.Join(err, errors.New("function analysis failed"))
	}

	return err
}

func analyzeFunction(
	ctx context.Context,
	function source.Function,
	loader *source.ContextLoader,
	analyzer analysis.Analyzer,
	endpoint, model string,
) (Result, error) {
	function, err := loader.Prepare(ctx, function)
	if err != nil {
		return Result{}, fmt.Errorf("prepare source context: %w", err)
	}

	request, err := analysis.Prepare(endpoint, model, function)
	if err != nil {
		return Result{}, fmt.Errorf("prepare analysis request: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("context: %w", err)
	}

	decision, analysisErr := analyzer.Analyze(ctx, request)
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("context: %w", err)
	}

	if errors.Is(analysisErr, context.Canceled) ||
		errors.Is(analysisErr, context.DeadlineExceeded) && ctx.Err() != nil {
		return Result{}, fmt.Errorf("analyze function: %w", analysisErr)
	}

	if analysisErr == nil {
		analysisErr = decision.Validate()
	}

	result := Result{
		File:      function.File,
		Function:  function.Identity(),
		StartLine: function.StartLine,
		EndLine:   function.EndLine,
	}

	if analysisErr != nil {
		result.Error = analysisErr.Error()
	} else {
		result.Verdict, result.ExtractProbability = decision.Verdict, decision.ExtractProbability
	}

	return result, nil
}

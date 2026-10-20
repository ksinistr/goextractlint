package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"goextractlint/internal/domain"
	"goextractlint/internal/filewalk"
	"goextractlint/internal/goparse"
)

type Result struct {
	File               string         `json:"file"`
	Function           string         `json:"function"`
	StartLine          int            `json:"start_line"`
	EndLine            int            `json:"end_line"`
	Verdict            domain.Verdict `json:"verdict,omitempty"`
	ExtractProbability *float64       `json:"extract_probability,omitempty"`
	Error              string         `json:"error,omitempty"`
}

func runAnalysis(
	ctx context.Context,
	paths []string,
	paths0 io.Reader,
	stopRead func() error,
	include func(domain.Function) bool,
	loader *goparse.ContextLoader,
	analyzer functionAnalyzer,
	target backend,
	output, diagnostics io.Writer,
) error {
	encoder := json.NewEncoder(output)
	failed := false

	err := walkFunctions(ctx, paths, paths0, stopRead, loader, func(function domain.Function) error {
		if !include(function) {
			return nil
		}

		result, err := analyzeFunction(ctx, function, loader, analyzer, target)
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

func includeAll(domain.Function) bool {
	return true
}

func analyzeFunction(
	ctx context.Context,
	function domain.Function,
	loader *goparse.ContextLoader,
	analyzer functionAnalyzer,
	target backend,
) (Result, error) {
	function, err := loader.Prepare(ctx, function)
	if err != nil {
		return Result{}, fmt.Errorf("prepare source context: %w", err)
	}

	request, err := prepareAnalysis(target, function)
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

func walkFunctions(
	ctx context.Context,
	paths []string,
	paths0 io.Reader,
	stopRead func() error,
	loader *goparse.ContextLoader,
	visit func(domain.Function) error,
) error {
	err := filewalk.Walk(ctx, paths, paths0, stopRead, func(path string) error {
		functions, err := loader.Functions(path)
		if err != nil {
			return fmt.Errorf("load functions: %w", err)
		}

		for _, function := range functions {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("context: %w", err)
			}

			if err := visit(function); err != nil {
				return fmt.Errorf("visit function: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("walk source files: %w", err)
	}

	return nil
}

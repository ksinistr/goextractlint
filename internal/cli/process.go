package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"goextractlint/internal/filewalk"
	"goextractlint/internal/goparse"
	"goextractlint/internal/system1"
)

type functionEvaluator interface {
	Evaluate(context.Context, goparse.Function) (system1.Decision, error)
}

type Result struct {
	File               string          `json:"file"`
	Function           string          `json:"function"`
	StartLine          int             `json:"start_line"`
	EndLine            int             `json:"end_line"`
	Verdict            system1.Verdict `json:"verdict,omitempty"`
	ExtractProbability *float64        `json:"extract_probability,omitempty"`
	Error              string          `json:"error,omitempty"`
}

func processFunctions(
	ctx context.Context,
	paths []string,
	paths0 io.Reader,
	stopRead func() error,
	include func(goparse.Function) bool,
	loader *goparse.ContextLoader,
	evaluator functionEvaluator,
	output, diagnostics io.Writer,
) error {
	encoder := json.NewEncoder(output)
	failed := false

	err := walkFunctions(ctx, paths, paths0, stopRead, loader, func(function goparse.Function) error {
		if !include(function) {
			return nil
		}

		result, err := evaluateFunction(ctx, function, loader, evaluator)
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

func includeAll(goparse.Function) bool {
	return true
}

func evaluateFunction(
	ctx context.Context,
	function goparse.Function,
	loader *goparse.ContextLoader,
	evaluator functionEvaluator,
) (Result, error) {
	function, err := loader.Prepare(ctx, function)
	if err != nil {
		return Result{}, fmt.Errorf("prepare source context: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("context: %w", err)
	}

	decision, evaluationErr := evaluator.Evaluate(ctx, function)
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("context: %w", err)
	}

	if errors.Is(evaluationErr, context.Canceled) ||
		errors.Is(evaluationErr, context.DeadlineExceeded) && ctx.Err() != nil {
		return Result{}, fmt.Errorf("analyze function: %w", evaluationErr)
	}

	if evaluationErr == nil {
		evaluationErr = decision.Validate()
	}

	result := Result{
		File:      function.File,
		Function:  function.Identity(),
		StartLine: function.StartLine,
		EndLine:   function.EndLine,
	}

	if evaluationErr != nil {
		result.Error = evaluationErr.Error()
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
	visit func(goparse.Function) error,
) error {
	err := filewalk.Walk(ctx, paths, paths0, stopRead, func(path string) error {
		functions, err := loader.Functions(path)
		if err != nil {
			return fmt.Errorf("load functions: %w", err)
		}

		return visitFunctions(ctx, functions, visit)
	})
	if err != nil {
		return fmt.Errorf("walk source files: %w", err)
	}

	return nil
}

func visitFunctions(ctx context.Context, functions []goparse.Function, visit func(goparse.Function) error) error {
	for _, function := range functions {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context: %w", err)
		}

		if err := visit(function); err != nil {
			return fmt.Errorf("visit function: %w", err)
		}
	}

	return nil
}

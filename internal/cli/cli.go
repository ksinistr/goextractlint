package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"goextractlint/internal/cache"
	"goextractlint/internal/goparse"
	"goextractlint/internal/linerange"
	"goextractlint/internal/system1"
)

func Run(
	ctx context.Context,
	args []string,
	key string,
	httpClient *http.Client,
	userCacheDir func() (string, error),
	input io.ReadCloser,
	output, status io.Writer,
) error {
	options, err := parseOptions(args, key, status)
	if err != nil {
		return err
	}

	if options.help {
		return nil
	}

	transport := *httpClient
	transport.Timeout = options.timeout

	var (
		paths0   io.Reader
		stopRead func() error
	)

	if options.explicitFiles0 {
		stream, openErr := openPaths(options.files0, input)
		if openErr != nil {
			return openErr
		}

		defer func() {
			if closeErr := stream.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
				fmt.Fprintf(status, "close input: %v\n", closeErr)
			}
		}()

		paths0, stopRead = stream, stream.Close
	}

	selection, err := selectFunctions(options, input)
	if err != nil {
		return err
	}

	var store system1.DecisionStore
	if !options.noCache {
		store, err = openCache(userCacheDir, status)
		if err != nil {
			return err
		}
	}

	evaluator := system1.NewClient(&transport, system1.Config{
		Endpoint:     options.backend.endpoint,
		Model:        options.backend.model,
		APIKey:       options.backend.apiKey,
		MaxTokens:    options.backend.maxTokens,
		RefreshCache: options.refreshCache,
	}, store, status)

	err = processFunctions(
		ctx,
		selection.paths,
		paths0,
		stopRead,
		selection.include,
		goparse.NewContextLoader(),
		evaluator,
		output,
		status,
	)
	if err != nil {
		return fmt.Errorf("run analysis: %w", err)
	}

	return nil
}

type functionSelection struct {
	paths   []string
	include func(goparse.Function) bool
}

func selectFunctions(options cliOptions, input io.ReadCloser) (functionSelection, error) {
	if !options.selectsRanges() {
		return functionSelection{paths: options.paths, include: includeAll}, nil
	}

	ranges, err := loadRanges(options, input)
	if err != nil {
		return functionSelection{}, err
	}

	selection := linerange.NewSelection(ranges)
	include := func(function goparse.Function) bool {
		return selection.Overlaps(function.File, function.StartLine, function.EndLine)
	}

	return functionSelection{paths: selection.Files(), include: include}, nil
}

func loadRanges(options cliOptions, input io.ReadCloser) ([]linerange.Range, error) {
	ranges := append([]linerange.Range(nil), options.ranges...)

	if options.explicitRangesFrom {
		read, err := readRanges(options.rangesFrom, input)
		if err != nil {
			return nil, err
		}

		ranges = append(ranges, read...)
	}

	for i := range ranges {
		absolute, err := filepath.Abs(ranges[i].File)
		if err != nil {
			return nil, fmt.Errorf("resolve range path: %w", err)
		}

		ranges[i].File = absolute
	}

	return ranges, nil
}

func readRanges(path string, input io.ReadCloser) ([]linerange.Range, error) {
	stream, err := openPaths(path, input)
	if err != nil {
		return nil, err
	}

	ranges, err := linerange.Read(stream)
	if closeErr := stream.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
		err = errors.Join(err, fmt.Errorf("close ranges: %w", closeErr))
	}

	if err != nil {
		return nil, err
	}

	return ranges, nil
}

func openCache(
	userCacheDir func() (string, error),
	status io.Writer,
) (system1.DecisionStore, error) {
	directory, err := userCacheDir()
	if err != nil {
		fmt.Fprintf(status, "cache: %v\n", err)
		return nil, nil
	}

	root, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}

	projectHash := fmt.Sprintf("%x", sha256.Sum256([]byte(root)))
	store := cache.New(filepath.Join(directory, "goextractlint", "v1", projectHash))

	return store, nil
}

func openPaths(path string, input io.ReadCloser) (io.ReadCloser, error) {
	if path == "-" {
		if input == nil {
			return nil, fmt.Errorf("stdin is unavailable")
		}

		return input, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open path stream: %w", err)
	}

	return file, nil
}

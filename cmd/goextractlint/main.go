package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"goextractlint/internal/cache"
	"goextractlint/internal/domain"
	"goextractlint/internal/goparse"
	"goextractlint/internal/linerange"
	"goextractlint/internal/system1"
)

func main() {
	logger := log.New(os.Stderr, "", 0)
	if err := run(); err != nil {
		logger.Print(err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return runCLI(
		ctx,
		os.Args[1:],
		os.Getenv("OPENROUTER_API_KEY"),
		&http.Client{},
		os.UserCacheDir,
		os.Stdin,
		os.Stdout,
		os.Stderr,
	)
}

func runCLI(
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

	var analyzer functionAnalyzer = newSystem1Analyzer(system1.NewClient(&transport, options.backend.apiKey))

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

	if !options.noCache {
		analyzer, err = cachedAnalyzer(analyzer, options.refreshCache, userCacheDir, status)
		if err != nil {
			return err
		}
	}

	err = runAnalysis(
		ctx,
		selection.paths,
		paths0,
		stopRead,
		selection.include,
		goparse.NewContextLoader(),
		analyzer,
		options.backend,
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
	include func(domain.Function) bool
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
	include := func(function domain.Function) bool {
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

func cachedAnalyzer(
	analyzer functionAnalyzer,
	refresh bool,
	userCacheDir func() (string, error),
	status io.Writer,
) (functionAnalyzer, error) {
	directory, err := userCacheDir()
	if err != nil {
		fmt.Fprintf(status, "cache: %v\n", err)
		return analyzer, nil
	}

	root, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}

	projectHash := fmt.Sprintf("%x", sha256.Sum256([]byte(root)))
	store := cache.New(filepath.Join(directory, "goextractlint", "v1", projectHash))

	return cache.NewCachedAnalyzer(analyzer, store, refresh, status), nil
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

type cliOptions struct {
	paths                                       []string
	files0                                      string
	explicitFiles0, noCache, refreshCache, help bool
	ranges                                      []linerange.Range
	rangesFrom                                  string
	explicitRangesFrom                          bool
	backend                                     backend
	timeout                                     time.Duration
}

func (o cliOptions) selectsRanges() bool {
	return len(o.ranges) > 0 || o.explicitRangesFrom
}

func parseOptions(args []string, key string, status io.Writer) (cliOptions, error) {
	flags := flag.NewFlagSet("goextractlint", flag.ContinueOnError)
	flags.SetOutput(status)
	files0 := flags.String("files0-from", "", "NUL-separated paths from a file or - for stdin")
	rangesFrom := flags.String("ranges-from", "", "line ranges, one per line, from a file or - for stdin")
	ranges := registerRangeFlag(flags)
	noCache := flags.Bool("no-cache", false, "disable the cache")
	refreshCache := flags.Bool("refresh-cache", false, "refresh the cache without reading it")
	provider := flags.String("provider", openRouterProvider, "decision provider: openrouter or ollama")
	jevModel := flags.String(
		"jev-model",
		"",
		"decision model (default typesafe/jev-1.13 for openrouter, tev1:4b-mxfp8 for ollama)",
	)
	endpoint := flags.String("endpoint", "", "decision endpoint URL overriding the provider default")
	maxTokens := flags.Int(
		"max-tokens",
		0,
		"estimated request token limit; context that does not fit is dropped (default 8192 for openrouter, 2048 for ollama)",
	)
	timeout := flags.Duration("timeout", 2*time.Minute, "timeout for each HTTP request")

	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), `Highlight Go functions worth reviewing for Extract Function.
Look for opportunities to separate intent from implementation, not just long functions.
Source files remain unchanged.

Usage: goextractlint [flags] [path ...]

Flags:`)
		flags.PrintDefaults()
		fmt.Fprintln(flags.Output(), `
Without paths, analyze the current directory. Put flags before paths.
Use -- before paths starting with a hyphen.
-files0-from reads NUL-terminated paths relative to the working directory;
use - for stdin. It cannot be combined with positional paths.
-range and -ranges-from analyze only functions overlapping the given lines;
-ranges-from reads one path:line or path:start-end per line, use - for stdin.
They cannot be combined with positional paths or -files0-from.
-no-cache and -refresh-cache are mutually exclusive.
The model must be nonempty and the timeout and -max-tokens must be positive.
-max-tokens should not exceed the model context; tokens are estimated as 3 bytes each.
OPENROUTER_API_KEY is required for the openrouter provider.
The ollama provider uses a local Ollama server at localhost:11434.
-endpoint points either provider at any compatible decision API;
the provider still selects the API key and the default model.

Examples:
  goextractlint ./internal
  goextractlint ./service.go
  goextractlint -provider ollama ./internal
  goextractlint -provider ollama -endpoint http://gpu-box:11434/v1/systemone ./internal
  find ./internal -type f -name '*.go' -print0 | goextractlint -files0-from -
  goextractlint -range ./service.go:40-72
  printf 'service.go:40-72\nrepo.go:15\n' | goextractlint -ranges-from -

Output: JSONL on stdout, diagnostics on stderr.
A nonzero exit code means a technical error; output may be partial.`)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cliOptions{help: true}, nil
		}

		return cliOptions{}, fmt.Errorf("parse arguments: %w", err)
	}

	explicit := map[string]bool{}

	flags.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	options := cliOptions{
		files0:             *files0,
		explicitFiles0:     explicit["files0-from"],
		ranges:             *ranges,
		rangesFrom:         *rangesFrom,
		explicitRangesFrom: explicit["ranges-from"],
	}

	if err := validateInputs(options, flags.NArg()); err != nil {
		return cliOptions{}, err
	}

	if *noCache && *refreshCache {
		return cliOptions{}, fmt.Errorf("-no-cache and -refresh-cache are mutually exclusive")
	}

	if (explicit["jev-model"] && strings.TrimSpace(*jevModel) == "") || *timeout <= 0 {
		return cliOptions{}, fmt.Errorf("jev model must not be empty and timeout must be positive")
	}

	if explicit["endpoint"] && strings.TrimSpace(*endpoint) == "" {
		return cliOptions{}, fmt.Errorf("endpoint must not be empty")
	}

	if explicit["max-tokens"] && *maxTokens <= 0 {
		return cliOptions{}, fmt.Errorf("-max-tokens must be positive")
	}

	selected, err := resolveBackend(backendOptions{
		provider:  *provider,
		endpoint:  strings.TrimSpace(*endpoint),
		model:     strings.TrimSpace(*jevModel),
		key:       key,
		maxTokens: *maxTokens,
	})
	if err != nil {
		return cliOptions{}, err
	}

	options.paths = flags.Args()
	if len(options.paths) == 0 && !options.explicitFiles0 && !options.selectsRanges() {
		options.paths = []string{"."}
	}

	options.noCache, options.refreshCache = *noCache, *refreshCache
	options.backend, options.timeout = selected, *timeout

	return options, nil
}

func registerRangeFlag(flags *flag.FlagSet) *[]linerange.Range {
	var ranges []linerange.Range

	flags.Func("range", "analyze functions overlapping path:line or path:start-end; repeatable", func(spec string) error {
		parsed, err := linerange.Parse(spec)
		if err != nil {
			return fmt.Errorf("parse range: %w", err)
		}

		ranges = append(ranges, parsed)

		return nil
	})

	return &ranges
}

func validateInputs(options cliOptions, positional int) error {
	if options.explicitFiles0 && (options.files0 == "" || positional != 0) {
		return fmt.Errorf("-files0-from requires a file or - and excludes positional paths")
	}

	if options.explicitRangesFrom && options.rangesFrom == "" {
		return fmt.Errorf("-ranges-from requires a file or -")
	}

	if options.selectsRanges() && (options.explicitFiles0 || positional != 0) {
		return fmt.Errorf("-range and -ranges-from exclude positional paths and -files0-from")
	}

	return nil
}

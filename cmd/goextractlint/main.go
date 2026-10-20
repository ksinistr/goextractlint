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

	"goextractlint/internal/analysis"
	"goextractlint/internal/cache"
	"goextractlint/internal/session"
	"goextractlint/internal/source"
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
		"https://openrouter.ai",
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
	endpoint string,
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

	key = strings.TrimSpace(key)

	transport := *httpClient
	transport.Timeout = options.timeout

	var analyzer analysis.Analyzer = analysis.NewClient(&transport, key)

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

	if !options.noCache {
		analyzer, err = cachedAnalyzer(analyzer, options.refreshCache, userCacheDir, status)
		if err != nil {
			return err
		}
	}

	err = session.Run(
		ctx,
		options.paths,
		paths0,
		stopRead,
		source.NewContextLoader(),
		analyzer,
		endpoint,
		options.model,
		output,
		status,
	)
	if err != nil {
		return fmt.Errorf("run analysis: %w", err)
	}

	return nil
}

func cachedAnalyzer(
	analyzer analysis.Analyzer,
	refresh bool,
	userCacheDir func() (string, error),
	status io.Writer,
) (analysis.Analyzer, error) {
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

	return analysis.NewCachedAnalyzer(analyzer, store, refresh, status), nil
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
	model                                       string
	timeout                                     time.Duration
}

func parseOptions(args []string, key string, status io.Writer) (cliOptions, error) {
	flags := flag.NewFlagSet("goextractlint", flag.ContinueOnError)
	flags.SetOutput(status)
	files0 := flags.String("files0-from", "", "NUL-separated paths from a file or - for stdin")
	noCache := flags.Bool("no-cache", false, "disable the cache")
	refreshCache := flags.Bool("refresh-cache", false, "refresh the cache without reading it")
	jevModel := flags.String("jev-model", "typesafe/jev-1.13", "Jev model")
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
-no-cache and -refresh-cache are mutually exclusive.
The model must be nonempty and the timeout must be positive.
OPENROUTER_API_KEY is required for analysis.

Examples:
  goextractlint ./internal
  goextractlint ./service.go
  find ./internal -type f -name '*.go' -print0 | goextractlint -files0-from -

Output: JSONL on stdout, diagnostics on stderr.
A nonzero exit code means a technical error; output may be partial.`)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cliOptions{help: true}, nil
		}

		return cliOptions{}, fmt.Errorf("parse arguments: %w", err)
	}

	explicitFiles0 := false

	flags.Visit(func(f *flag.Flag) {
		if f.Name == "files0-from" {
			explicitFiles0 = true
		}
	})

	if *noCache && *refreshCache {
		return cliOptions{}, fmt.Errorf("-no-cache and -refresh-cache are mutually exclusive")
	}

	if explicitFiles0 && (*files0 == "" || flags.NArg() != 0) {
		return cliOptions{}, fmt.Errorf("-files0-from requires a file or - and excludes positional paths")
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return cliOptions{}, fmt.Errorf("set OPENROUTER_API_KEY")
	}

	if strings.TrimSpace(*jevModel) == "" || *timeout <= 0 {
		return cliOptions{}, fmt.Errorf("Jev model must not be empty and timeout must be positive")
	}

	paths := flags.Args()
	if len(paths) == 0 && !explicitFiles0 {
		paths = []string{"."}
	}

	return cliOptions{
		paths:          paths,
		files0:         *files0,
		explicitFiles0: explicitFiles0,
		noCache:        *noCache,
		refreshCache:   *refreshCache,
		model:          *jevModel,
		timeout:        *timeout,
	}, nil
}

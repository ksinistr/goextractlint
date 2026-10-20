package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"goextractlint/internal/linerange"
)

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

	flags.Func(
		"range",
		"analyze functions overlapping path:line or path:start-end; repeatable",
		func(spec string) error {
			parsed, err := linerange.Parse(spec)
			if err != nil {
				return fmt.Errorf("parse range: %w", err)
			}

			ranges = append(ranges, parsed)

			return nil
		},
	)

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

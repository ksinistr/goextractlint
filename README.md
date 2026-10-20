# goextractlint

A small Go CLI that uses TypeSafe Jev to highlight functions worth reviewing for
Extract Function, following [Martin Fowler's principle](https://martinfowler.com/bliki/FunctionLength.html).
The criterion is separating intent from implementation, not function length.
Findings support human judgment; source files remain unchanged.

## Installation

Requires Go 1.25 or later and an OpenRouter API key or a local Ollama server.

```sh
make install
export OPENROUTER_API_KEY='your-key'
```

Add `GOBIN` (or `$(go env GOPATH)/bin` if unset) to PATH.
Requests use your OpenRouter balance; `.env` is not loaded automatically.

To run locally instead, use Ollama 0.40+ with a decision model (default `tev1:4b-mxfp8`)
and pass `-provider ollama`; no API key is needed:

```sh
goextractlint -provider ollama ./internal
```

Each request is kept under an estimated token limit (`-max-tokens`, default 2048 for ollama
and 8192 for openrouter); context that does not fit is dropped and `context_truncated` is set.

Override the model with `-jev-model` and the URL with `-endpoint` to use any compatible
decision API; the provider still selects the API key and the default model:

```sh
goextractlint -provider ollama -endpoint http://gpu-box:11434/v1/systemone ./internal
```

## Usage

Analyze a directory and display extraction candidates with `jq`:

```sh
set -o pipefail
goextractlint ./internal | jq -c 'select(.verdict == "extract")'
```

Analyze only functions that overlap given lines, for example the lines changed on a branch:

```sh
git diff -U0 origin/main...HEAD -- '*.go' |
  awk '/^\+\+\+ b\//{f=substr($0,7)} /^@@/{split($3,a,/[+,]/); n=(a[3]=="")?1:a[3]; if(n>0) print f":"a[2]"-"a[2]+n-1}' |
  goextractlint -ranges-from -
```

`-ranges-from` reads one `path:line` or `path:start-end` per line; `-range` takes the same value and can be repeated.

Run `goextractlint -help` for flags, defaults, and more examples.

## Output

stdout emits one JSONL record per completed analysis attempt; diagnostics go to stderr.

```jsonl
{
    "file":"/work/project/service.go",
    "function":"(*Service).Process",
    "start_line":42,
    "end_line":85,
    "verdict":"extract",
    "extract_probability":0.87
}
```

Verdicts are `extract`, `keep`, or `insufficient_context`.
`extract_probability` is optional. API failures produce an `error` instead of a verdict.
Exit code `0` means no technical errors; a nonzero code means the output may be partial.

## Development

All changes must follow the [architecture principles](ARCHITECTURE.md).
Run `make test` and `make check` to verify changes; tests make no paid requests.

## Project structure

- `cmd/goextractlint`: CLI setup, analysis workflow, JSONL output, and the adapter
  between domain concepts and System1 requests.
- `internal/filewalk`: incremental discovery of Go files, exclusions, and path
  deduplication; does not parse file contents.
- `internal/linerange`: parsing of `path:start-end` line ranges and matching
  functions that overlap them.
- `internal/goparse`: Go parsing, function extraction, and bounded package context;
  keeps AST snapshots and package indexes in memory.
- `internal/domain`: functions, context, decisions, validation, and the Extract
  Function criterion.
- `internal/system1`: HTTP transport for System1 endpoints, independent of domain
  concepts.
- `internal/cache`: decision caching and atomic filesystem storage, keyed by the
  exact request, endpoint, and model.

# goextractlint

A small Go CLI that uses TypeSafe Jev to highlight functions worth reviewing for
Extract Function, following [Martin Fowler's principle](https://martinfowler.com/bliki/FunctionLength.html).
The criterion is separating intent from implementation, not function length.
Findings support human judgment; source files remain unchanged.

## Installation

Requires Go 1.25 or later and an OpenRouter API key.

```sh
make install
export OPENROUTER_API_KEY='your-key'
```

Add `GOBIN` (or `$(go env GOPATH)/bin` if unset) to PATH.
Requests use your OpenRouter balance; `.env` is not loaded automatically.

## Usage

Analyze a directory and display extraction candidates with `jq`:

```sh
set -o pipefail
goextractlint ./internal | jq -c 'select(.verdict == "extract")'
```

Run `goextractlint -help` for flags, defaults, and more examples.

## Output

stdout emits one JSONL record per completed analysis attempt; diagnostics go to stderr.

```jsonl
{"file":"/work/project/service.go","function":"(*Service).Process","start_line":42,"end_line":85,"verdict":"extract","extract_probability":0.87}
```

Verdicts are `extract`, `keep`, or `insufficient_context`.
`extract_probability` is optional. API failures produce an `error` instead of a verdict.
Exit code `0` means no technical errors; a nonzero code means the output may be partial.

## Development

All changes must follow the [architecture principles](ARCHITECTURE.md).
Run `make test` and `make check` to verify changes; tests make no paid requests.

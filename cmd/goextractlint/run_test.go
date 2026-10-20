package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goextractlint/internal/domain"
	"goextractlint/internal/goparse"
)

type analyzerFunc func(context.Context, preparedRequest) (domain.Decision, error)

const (
	extractAnswer = "extract"
	errorAnswer   = "error"
	brokenSibling = "broken sibling"
)

const (
	keepAnswer   = "keep"
	cancelBefore = "cancel before"
)

const (
	cancelRequest    = "cancel request"
	writerStopsInput = "writer stops input"
	writerMode       = "writer"
)

func (f analyzerFunc) Analyze(ctx context.Context, r preparedRequest) (domain.Decision, error) {
	return f(ctx, r)
}

type runWriterFunc func([]byte) (int, error)

func (f runWriterFunc) Write(b []byte) (int, error) { return f(b) }

func TestRun(t *testing.T) {
	for _, tc := range []struct {
		name                string
		answers             []string
		probability, method bool
	}{
		{name: "all verdicts", answers: []string{extractAnswer, keepAnswer, insufficientContextAnswer}},
		{name: "error continues", answers: []string{errorAnswer, keepAnswer, extractAnswer}},
		{name: "probability and receiver", answers: []string{extractAnswer, keepAnswer, insufficientContextAnswer}, probability: true, method: true},
		{name: "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "a.go")
			code := "package p\ntype Box int\n"

			for _, name := range []string{"First", "Second", "Third"} {
				if tc.method {
					code += "// documentation\nfunc (Box) " + name + "() {}\n"
				} else {
					code += "func " + name + "() {}\n"
				}
			}

			if len(tc.answers) > 0 {
				if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var output, status bytes.Buffer

			calls := 0
			analyzer := analyzerFunc(func(_ context.Context, _ preparedRequest) (domain.Decision, error) {
				if calls > 0 && bytes.Count(output.Bytes(), []byte("\n")) != calls {
					t.Fatal("previous result not written")
				}

				answer := tc.answers[calls]
				calls++

				if answer == errorAnswer {
					return domain.Decision{}, errors.New("HTTP 429")
				}

				d := domain.Decision{Verdict: domain.Verdict(answer)}

				if tc.probability {
					p := 0.75
					d.ExtractProbability = &p
				}

				return d, nil
			})
			err := runAnalysis(
				context.Background(),
				[]string{root},
				nil,
				nil,
				includeAll,
				goparse.NewContextLoader(),
				analyzer,
				backend{endpoint: "http://unused", model: "model", maxTokens: openRouterMaxTokens},
				&output,
				&status,
			)

			wantErr := tc.name == "error continues"
			if (err != nil) != wantErr || calls != len(tc.answers) || (status.Len() > 0) != wantErr {
				t.Fatalf("err=%v calls=%d stderr=%s", err, calls, &status)
			}

			decoder := json.NewDecoder(&output)
			decoder.DisallowUnknownFields()

			for i, answer := range tc.answers {
				var got Result
				if err := decoder.Decode(&got); err != nil {
					t.Fatal(err)
				}

				name := []string{"First", "Second", "Third"}[i]
				if tc.method {
					name = "(Box)." + name
				}

				if got.File != path || got.Function != name || got.EndLine < got.StartLine {
					t.Fatalf("location=%+v", got)
				}

				if answer == errorAnswer {
					if got.Error == "" || got.Verdict != "" || got.ExtractProbability != nil {
						t.Fatalf("error=%+v", got)
					}
				} else if string(got.Verdict) != answer || got.Error != "" || (got.ExtractProbability != nil) != tc.probability {
					t.Fatalf("result=%+v", got)
				}
			}

			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatalf("extra output: %v", err)
			}
		})
	}
}

func TestFatalStops(t *testing.T) {
	for _, mode := range []string{writerMode, cancelRequest, cancelBefore, "late input", "late source", brokenSibling, writerStopsInput} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			first := filepath.Join(root, "a.go")

			code := "package p\nfunc First() {}\nfunc Second() {}\nfunc Third() {}"
			if strings.Contains(mode, "late") || mode == writerStopsInput {
				code = "package p\nfunc First() {}"
			}

			if err := os.WriteFile(first, []byte(code), 0o600); err != nil {
				t.Fatal(err)
			}

			paths := []string{first}

			var input io.Reader
			if mode == "late input" {
				input = strings.NewReader(first + "\x00missing\x00")
			}

			if mode == "late source" {
				bad := filepath.Join(root, "bad")
				if err := os.Mkdir(bad, 0o700); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(filepath.Join(bad, "b.go"), []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}

				paths = append(paths, bad)
			}

			if mode == brokenSibling {
				if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if mode == writerStopsInput {
				input = &singleRead{data: []byte(first + "\x00"), t: t}
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var (
				output, status bytes.Buffer
				out            io.Writer = &output
			)

			calls := 0

			if mode == writerMode || mode == writerStopsInput {
				out = runWriterFunc(func([]byte) (int, error) { return 0, errors.New("closed pipe") })
			}

			if mode == cancelBefore {
				cancel()
			}

			analyzer := analyzerFunc(func(context.Context, preparedRequest) (domain.Decision, error) {
				calls++
				if mode == cancelRequest && calls == 2 {
					cancel()
					return domain.Decision{}, context.Canceled
				}

				return domain.Decision{Verdict: domain.Keep}, nil
			})
			err := runAnalysis(
				ctx,
				paths,
				input,
				nil,
				includeAll,
				goparse.NewContextLoader(),
				analyzer,
				backend{endpoint: "http://unused", model: "m", maxTokens: openRouterMaxTokens},
				out,
				&status,
			)

			wantCalls := 1
			if mode == cancelRequest {
				wantCalls = 2
			}

			if mode == cancelBefore || mode == brokenSibling {
				wantCalls = 0
			}

			wantLines := 1
			if mode == writerMode || mode == writerStopsInput || wantCalls == 0 {
				wantLines = 0
			}

			if err == nil || calls != wantCalls || bytes.Count(output.Bytes(), []byte("\n")) != wantLines {
				t.Fatalf("err=%v calls=%d output=%s", err, calls, &output)
			}
		})
	}
}

type singleRead struct {
	data []byte
	t    *testing.T
}

func (r *singleRead) Read(b []byte) (int, error) {
	if len(r.data) == 0 {
		r.t.Fatal("consumed next target after writer failure")
	}

	n := copy(b, r.data)
	r.data = r.data[n:]

	return n, nil
}

const (
	typeContextKind           = "type"
	missingPath               = "missing"
	insufficientContextAnswer = "insufficient_context"
)

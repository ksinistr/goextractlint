package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goextractlint/internal/domain"
)

type transportFunc func(*http.Request) (*http.Response, error)

const (
	testAPIKey     = "key"
	noCacheFlag    = "-no-cache"
	files0Flag     = "-files0-from"
	testSourcePath = "a.go"
	writerStops    = "writer stops"
)

const (
	siblingSourcePath = "b.go"
)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func answer(verdict string) string {
	return `{"answers":{"verdict":{"type":"choice","choice":"` + verdict + `"}}}`
}

func write(t *testing.T, root, path, data string) {
	t.Helper()

	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) { r.t.Fatal("unexpected stdin read"); return 0, io.EOF }
func (failReader) Close() error               { return nil }

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

func TestArgumentValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		key  string
	}{
		{"eval", []string{"-eval", "x"}, testAPIKey},
		{"changed", []string{"-changed"}, testAPIKey},
		{"minimum", []string{"-min-file-bytes", "0"}, testAPIKey},
		{"cache conflict", []string{noCacheFlag, "-refresh-cache"}, testAPIKey},
		{"input conflict", []string{files0Flag, "-", "."}, testAPIKey},
		{"empty input flag", []string{"-files0-from="}, testAPIKey},
		{"empty model", []string{"-jev-model", " "}, testAPIKey},
		{"timeout", []string{"-timeout", "0"}, testAPIKey},
		{testAPIKey, nil, " "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{
				Transport: transportFunc(
					func(*http.Request) (*http.Response, error) { t.Fatal("unexpected HTTP"); return nil, nil },
				),
			}

			var out, status bytes.Buffer

			err := runCLI(
				context.Background(),
				tc.args,
				tc.key,
				client,
				"http://test",
				func() (string, error) { t.Fatal("unexpected cache"); return "", nil },
				failReader{t},
				&out,
				&status,
			)
			if err == nil || out.Len() != 0 {
				t.Fatalf("err=%v out=%s", err, &out)
			}
		})
	}
}

func TestCLI(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		list      string
		wantCalls int
		bad       bool
		lines     int
		cacheBad  bool
	}{
		{name: "default", wantCalls: 3, lines: 3},
		{name: "mixed paths", args: []string{siblingSourcePath, "sub", testSourcePath}, wantCalls: 3, lines: 3},
		{name: "dash delimiter", args: []string{"--", "-target.go"}, wantCalls: 1, lines: 1},
		{name: "stdin", args: []string{files0Flag, "-"}, list: "b.go\x00", wantCalls: 1, lines: 1},
		{name: "external list", args: []string{files0Flag, "LIST"}, list: "a.go\x00", wantCalls: 1, lines: 1},
		{name: "empty", args: []string{files0Flag, "-"}},
		{name: "api error", args: []string{testSourcePath, siblingSourcePath}, wantCalls: 2, lines: 2, bad: true},
		{name: "source error", args: []string{"sub"}, bad: true},
		{name: missingPath, args: []string{testSourcePath, missingPath}, wantCalls: 1, lines: 1, bad: true},
		{name: "malformed input", args: []string{files0Flag, "-"}, list: "a.go\x00tail", wantCalls: 1, lines: 1, bad: true},
		{name: "cancel", args: []string{testSourcePath, siblingSourcePath}, wantCalls: 1, bad: true},
		{name: "output", args: []string{testSourcePath, siblingSourcePath}, wantCalls: 1, bad: true},
		{name: "cache failure", args: []string{testSourcePath}, wantCalls: 1, lines: 1, cacheBad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			write(t, root, testSourcePath, "package p\nfunc A(){}")
			write(t, root, siblingSourcePath, "package p\nfunc B(){}")
			write(t, root, "sub/c.go", "package p\nfunc C(){}")

			if tc.name == "dash delimiter" {
				write(t, root, "-target.go", "package p\nfunc Dash(){}")
			}

			if tc.name == "source error" {
				write(t, root, "sub/c.go", "invalid")
			}

			args := append([]string(nil), tc.args...)
			for i, s := range args {
				if s == "LIST" {
					list := filepath.Join(t.TempDir(), "list")
					if err := os.WriteFile(list, []byte(tc.list), 0o600); err != nil {
						t.Fatal(err)
					}

					args[i] = list
				}
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			calls := 0

			var (
				out, status bytes.Buffer
				output      io.Writer = &out
			)
			if tc.name == "output" {
				output = writerFunc(func([]byte) (int, error) { return 0, errors.New("writer failed") })
			}

			client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if calls > 1 && bytes.Count(out.Bytes(), []byte("\n")) != calls-1 {
					t.Error("result was not immediate")
				}

				if tc.name == "cancel" {
					cancel()
					return nil, context.Canceled
				}

				if tc.name == "api error" && calls == 1 {
					return response(429, "busy"), nil
				}

				return response(
					200,
					answer([]string{"extract", keepAnswer, insufficientContextAnswer}[(calls-1)%3]),
				), nil
			})}

			var input io.ReadCloser = failReader{t}

			for _, arg := range args {
				if arg == files0Flag {
					input = io.NopCloser(strings.NewReader(tc.list))
				}
			}

			cacheDir := t.TempDir()
			cacheHome := func() (string, error) {
				if tc.cacheBad {
					return "", errors.New("unavailable")
				}

				return cacheDir, nil
			}

			err := runCLI(ctx, args, testAPIKey, client, "http://test", cacheHome, input, output, &status)
			if (err != nil) != tc.bad || calls != tc.wantCalls || bytes.Count(out.Bytes(), []byte("\n")) != tc.lines {
				t.Fatalf("err=%v calls=%d out=%s", err, calls, &out)
			}

			if !tc.bad && !tc.cacheBad && status.Len() != 0 {
				t.Fatalf("stderr=%s", &status)
			}

			if tc.cacheBad && !strings.Contains(status.String(), "cache:") {
				t.Fatalf("stderr=%s", &status)
			}

			if strings.Contains(status.String(), "Processed") || strings.Contains(status.String(), "Jev request") {
				t.Fatal("progress output")
			}
		})
	}
}

func TestStreamingContextCache(t *testing.T) {
	for _, mode := range []string{"stream and cache", writerStops} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			write(
				t,
				root,
				"target.go",
				"package p\nimport (\"fmt\"; _ \"unknown.example/pkg\"; . \"dot.example/pkg\")\n// Target docs\nfunc (b *Box) Target() int {return b.Value}",
			)
			write(t, root, "types.go", "package p\ntype Box struct{Value int}")
			cacheDir := t.TempDir()
			calls := 0

			var inputs []domain.Function

			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++

				var payload struct {
					State struct {
						Function domain.Function `json:"function"`
					} `json:"state"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}

				inputs = append(inputs, payload.State.Function)

				return response(200, answer(keepAnswer)), nil
			})}
			run := func(flags []string, input io.ReadCloser, output io.Writer) error {
				var status bytes.Buffer

				err := runCLI(context.Background(), flags, testAPIKey, client, "http://test", func() (string, error) {
					if len(flags) > 0 && flags[0] == noCacheFlag {
						t.Error("cache initialized during bypass")
					}

					return cacheDir, nil
				}, input, output, &status)
				if status.Len() != 0 {
					t.Errorf("stderr=%s", &status)
				}

				return err
			}

			reader, writer := io.Pipe()
			defer func() {
				if err := reader.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
					t.Error(err)
				}
			}()
			defer func() {
				if err := writer.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
					t.Error(err)
				}
			}()

			written := make(chan []byte, 1)

			var first bytes.Buffer

			output := writerFunc(func(b []byte) (int, error) {
				if mode == writerStops {
					return 0, errors.New("closed pipe")
				}

				first.Write(b)

				written <- append([]byte(nil), b...)

				return len(b), nil
			})

			done := make(chan error, 1)
			go func() { done <- run([]string{files0Flag, "-"}, reader, output) }()

			if _, err := io.WriteString(writer, "target.go\x00"); err != nil {
				t.Fatal(err)
			}

			if mode == writerStops {
				if err := <-done; err == nil || calls != 1 {
					t.Fatalf("err=%v calls=%d", err, calls)
				}

				return
			}

			record := <-written

			var result Result
			if err := json.Unmarshal(record, &result); err != nil || result.Function != "(*Box).Target" ||
				result.Verdict != keepAnswer {
				t.Fatalf("record=%s err=%v", record, err)
			}

			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}

			if err := <-done; err != nil {
				t.Fatal(err)
			}

			if calls != 1 || len(inputs[0].Context) != 4 {
				t.Fatalf("calls=%d context=%+v", calls, inputs)
			}

			for _, step := range []struct {
				name  string
				flags []string
				calls int
			}{{"hit", nil, 1}, {"context miss", nil, 2}, {"refresh", []string{"-refresh-cache"}, 3}, {"bypass", []string{noCacheFlag}, 4}, {"hit after bypass", nil, 4}} {
				if step.name == "context miss" {
					write(t, root, "types.go", "package p\ntype Box struct{Value int64}")
				}

				var out bytes.Buffer

				args := append(append([]string{}, step.flags...), "target.go")
				if err := run(args, failReader{t}, &out); err != nil || calls != step.calls ||
					out.String() != first.String() {
					t.Fatalf("%s err=%v calls=%d output=%s", step.name, err, calls, &out)
				}
			}

			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}

			namespace := filepath.Join(
				cacheDir,
				"goextractlint",
				"v1",
				fmt.Sprintf("%x", sha256.Sum256([]byte(filepath.Clean(cwd)))),
			)
			if entries, err := os.ReadDir(namespace); err != nil || len(entries) != 2 {
				t.Fatalf("namespace entries=%v err=%v", entries, err)
			}

			if !strings.Contains(fmt.Sprint(inputs[1].Context), "int64") {
				t.Fatal("context did not change")
			}
		})
	}
}

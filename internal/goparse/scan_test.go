package goparse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"goextractlint/internal/domain"
	"goextractlint/internal/filewalk"
)

const spacedSourcePath = "space \n.go"

func TestWalk(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		stop    bool
		want    []string
		wantErr bool
	}{
		{
			name: "ordered declarations and exclusions",
			files: map[string]string{
				testSourcePath:   "package p\nfunc First() {}\nfunc Second() { _ = func() {} }\n",
				nestedSourcePath: "package p\ntype Box[T any] struct{}\nfunc (b *Box[T]) Method() {}\n",
				"a_test.go":      invalidGo,
				vendorSourcePath: invalidGo,
				".git/g.go":      invalidGo,
				"notes.txt":      invalidGo,
			},
			want: []string{"a.go:First", "a.go:Second", "sub/b.go:Method"},
		},
		{
			name:  "stopping does not parse later files",
			files: map[string]string{testSourcePath: "package p\nfunc First() {}", siblingSourcePath: invalidGo},
			stop:  true, want: []string{"a.go:First"},
		},
		{
			name:    "parse failure",
			files:   map[string]string{testSourcePath: invalidGo},
			wantErr: true,
		},
		{name: "empty directory", files: map[string]string{}},
		{
			name: "equal sizes sorted by path",
			files: map[string]string{
				siblingSourcePath: "package p\nfunc B() {}",
				testSourcePath:    "package p\nfunc A() {}",
			},
			want: []string{"a.go:A", "b.go:B"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, content := range tc.files {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var got []string

			err := Walk(
				context.Background(),
				[]string{root},
				nil,
				nil,
				NewContextLoader(),
				func(function domain.Function) error {
					path, err := filepath.Rel(root, function.File)
					if err != nil {
						return fmt.Errorf("path/filepath.Rel: %w", err)
					}

					got = append(got, filepath.ToSlash(path)+":"+function.Name)

					if tc.stop {
						return errors.New("stop")
					}

					return nil
				},
			)
			if (err != nil) != (tc.wantErr || tc.stop) {
				t.Fatalf("error = %v", err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("functions = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParsePreservesSource(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		want     string
		receiver string
		start    int
		end      int
	}{
		{
			name:     "comments and generic receiver",
			content:  "package p\ntype Box[T any] struct{}\n// Documentation.\nfunc (b *Box[T]) Value() int {\n\t// Inside.\n\treturn 1\n}\n",
			want:     "// Documentation.\nfunc (b *Box[T]) Value() int {\n\t// Inside.\n\treturn 1\n}",
			receiver: "*Box[T]", start: 3, end: 7,
		},
		{
			name:    "bodyless declaration skipped",
			content: "package p\nfunc External()\nfunc Local() {}\n",
			want:    "func Local() {}", start: 3, end: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.go")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := NewContextLoader().Functions(path)
			if err != nil || len(got) != 1 {
				t.Fatalf("Parse = %v, %v", got, err)
			}

			function := got[0]
			if function.Source != tc.want || function.Receiver != tc.receiver || function.StartLine != tc.start ||
				function.EndLine != tc.end {
				t.Fatalf("function = %+v", function)
			}

			if strings.Count(function.Source, "\n")+1 != function.EndLine-function.StartLine+1 {
				t.Fatal("source line numbers do not match")
			}
		})
	}
}

func TestPaths(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		list  string
		from0 bool
		want  []string
		bad   bool
	}{
		{name: "mixed overlapping", args: []string{nestedSourcePath, ".", testSourcePath, "sub/../a.go"}, want: []string{"B", "A", secondFunction}},
		{name: "lexical", args: []string{"."}, want: []string{"A", secondFunction, "B"}},
		{name: "direct exclusions", args: []string{vendorSourcePath, ".git", "x_test.go", "notes", "linked.go", "linked-dir", "linked-dir/b.go"}},
		{name: "NUL spaces and newline", list: "space \n.go\x00a.go\x00", from0: true, want: []string{"Space", "A", secondFunction}},
		{name: "empty list", from0: true},
		{name: "empty record", list: "a.go\x00\x00", from0: true, want: []string{"A", secondFunction}, bad: true},
		{name: "unterminated", list: "a.go\x00sub/b.go", from0: true, want: []string{"A", secondFunction}, bad: true},
		{name: "late missing", list: "a.go\x00missing\x00", from0: true, want: []string{"A", secondFunction}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(
				t,
				root,
				map[string]string{
					testSourcePath:   "package p\nfunc A(){}\nfunc Second(){}",
					nestedSourcePath: "package p\nfunc B(){}",
					vendorSourcePath: invalidSource,
					".git/g.go":      invalidSource,
					"x_test.go":      invalidSource,
					"notes":          invalidSource,
					spacedSourcePath: "package p\nfunc Space(){}",
				},
			)

			if err := os.Symlink(filepath.Join(root, testSourcePath), filepath.Join(root, "linked.go")); err != nil {
				t.Fatal(err)
			}

			if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "linked-dir")); err != nil {
				t.Fatal(err)
			}

			if tc.name == "mixed overlapping" {
				tc.args = append(tc.args, filepath.Join(root, testSourcePath))
			}

			if !tc.from0 {
				if err := os.Remove(filepath.Join(root, spacedSourcePath)); err != nil {
					t.Fatal(err)
				}
			}

			t.Chdir(root)

			var input io.Reader
			if tc.from0 {
				input = strings.NewReader(tc.list)
			}

			var got []string

			err := Walk(context.Background(), tc.args, input, nil, NewContextLoader(), func(f domain.Function) error {
				got = append(got, f.Name)
				if !filepath.IsAbs(f.File) {
					t.Fatal("relative identity")
				}

				return nil
			})
			if (err != nil) != tc.bad || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got=%v err=%v", got, err)
			}
		})
	}
}

func TestPathPipe(t *testing.T) {
	for _, cancelWaiting := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelWaiting), func(t *testing.T) {
			root := t.TempDir()
			writeFiles(
				t,
				root,
				map[string]string{testSourcePath: "package p\nfunc A(){}", siblingSourcePath: "package p\nfunc B(){}"},
			)

			reader, writer := io.Pipe()
			waiting := make(chan struct{})

			input := &waitingReader{reader: reader, waiting: waiting}
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

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			visited := make(chan string)

			done := make(chan error, 1)
			go func() {
				done <- Walk(ctx, nil, input, reader.Close, NewContextLoader(), func(f domain.Function) error { visited <- f.Name; return nil })
			}()

			if _, err := io.WriteString(writer, filepath.Join(root, testSourcePath)+"\x00"); err != nil {
				t.Fatal(err)
			}

			if got := <-visited; got != "A" {
				t.Fatal(got)
			}

			if cancelWaiting {
				<-waiting
				cancel()

				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else {
				if _, err := io.WriteString(writer, filepath.Join(root, siblingSourcePath)+"\x00"); err != nil {
					t.Fatal(err)
				}

				if got := <-visited; got != "B" {
					t.Fatal(got)
				}

				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}

				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

type waitingReader struct {
	reader  io.Reader
	reads   int
	waiting chan struct{}
}

func (r *waitingReader) Read(b []byte) (int, error) {
	r.reads++
	if r.reads == 2 {
		close(r.waiting)
	}

	n, err := r.reader.Read(b)
	if err != nil {
		return n, fmt.Errorf("read input: %w", err)
	}

	return n, nil
}

func Walk(
	ctx context.Context,
	paths []string,
	input io.Reader,
	closeInput func() error,
	loader *ContextLoader,
	visit func(domain.Function) error,
) error {
	err := filewalk.Walk(ctx, paths, input, closeInput, func(path string) error {
		functions, err := loader.Functions(path)
		if err != nil {
			return err
		}

		for _, f := range functions {
			if err := visit(f); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("walk source files: %w", err)
	}

	return nil
}

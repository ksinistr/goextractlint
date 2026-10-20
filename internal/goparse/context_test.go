package goparse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	valueDeclaration    = "type Value struct{}"
	helperDeclaration   = "func Helper() {}"
	targetCallingHelper = "func Target() { Helper() }"
	invalidGo           = "invalid Go"
	fmtImport           = "\"fmt\""
	testSourcePath      = "a.go"
	nestedSourcePath    = "sub/b.go"
	vendorSourcePath    = "vendor/v.go"
	siblingSourcePath   = "b.go"
	secondFunction      = "Second"
	invalidSource       = "invalid"
)

const (
	typesSourcePath = "types.go"
)

func TestContext(t *testing.T) {
	cases := []struct {
		name, code, neighbor string
		want                 []string
	}{
		{
			"receiver and nested signature",
			"func (b *Box[T]) Target(v map[string][]*Value, f func(Value) *Value) *Value { return nil }",
			"type Box[T any] struct{}\ntype Value struct{}",
			[]string{"type Box[T any] struct{}", valueDeclaration},
		},
		{"type parameter shadows package type", "func Target[Value any](v Value) {}", valueDeclaration, nil},
		{
			"receiver parameter shadows package type",
			"func (b *Box[Value]) Target(v Value) {}",
			"type Box[T any] struct{}\ntype Value struct{}",
			[]string{"type Box[T any] struct{}"},
		},
		{
			"direct generic call",
			"func Target() { Helper[int](1) }",
			"func Helper[T any](v T) {}",
			[]string{"func Helper[T any](v T) {}"},
		},
		{"parameter shadow", "func Target(Helper func()) { Helper() }", helperDeclaration, nil},
		{"local shadow", "func Target() { Helper := func(){}; Helper() }", helperDeclaration, nil},
		{
			"scope ends",
			"func Target() { { Helper := func(){}; Helper() }; Helper() }",
			helperDeclaration,
			[]string{helperDeclaration},
		},
		{"recursive", "func Target() { Target() }", "", nil},
		{
			"method call",
			"func Target(v Value) { v.Helper() }",
			"type Value struct{}\nfunc (Value) Helper() {}",
			[]string{valueDeclaration},
		},
		{"function value", "func Target() { f := Helper; f() }", helperDeclaration, nil},
		{
			"one level",
			targetCallingHelper,
			"func Helper() { Deeper() }\nfunc Deeper() {}",
			[]string{"func Helper() { Deeper() }"},
		},
		{"ambiguous helper", targetCallingHelper, "func Helper() {}\nfunc Helper() {}", nil},
		{"other package", targetCallingHelper, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "target.go")
			files := map[string]string{
				"target.go":       "package p\n" + tc.code,
				typesSourcePath:   "package p\n" + tc.neighbor,
				"ignored_test.go": invalidGo,
				vendorSourcePath:  invalidGo,
				"other/x.go":      "package other\nfunc Helper() {}",
			}
			writeFiles(t, root, files)

			if err := os.Symlink(filepath.Join(root, "ignored_test.go"), filepath.Join(root, "linked.go")); err != nil {
				t.Fatal(err)
			}

			loader := NewContextLoader()

			functions, err := loader.Functions(path)
			if err != nil {
				t.Fatal(err)
			}

			got, err := loader.Prepare(context.Background(), functions[0])
			if err != nil {
				t.Fatal(err)
			}

			var sources []string
			for _, item := range got.Context {
				sources = append(sources, item.Source)
			}

			if !reflect.DeepEqual(sources, tc.want) {
				t.Fatalf("sources=%q want=%q", sources, tc.want)
			}

			again, err := loader.Prepare(context.Background(), functions[0])
			if err != nil {
				t.Fatal(err)
			}

			first, err := json.Marshal(got)
			if err != nil {
				t.Error(err)
				return
			}

			second, err := json.Marshal(again)
			if err != nil {
				t.Error(err)
				return
			}

			if string(first) != string(second) {
				t.Fatal("unstable context")
			}
		})
	}
}

func TestImports(t *testing.T) {
	cases := []struct {
		name, code string
		want       []string
	}{
		{
			"all import forms",
			`import ("fmt"; alias "unknown.example/odd"; _ "blank.example/p"; . "dot.example/p")
func Target(){fmt.Println(1)}`,
			nil,
		},
		{
			"imports with type",
			`import "fmt"
func Target(v Value){fmt.Println(v)}
type Value int`,
			[]string{"type Value int"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, map[string]string{testSourcePath: "package p\n" + tc.code})

			loader := NewContextLoader()

			functions, err := loader.Functions(filepath.Join(root, testSourcePath))
			if err != nil {
				t.Fatal(err)
			}

			f, err := loader.Prepare(context.Background(), functions[0])
			if err != nil {
				t.Fatal(err)
			}

			var got []string
			for _, item := range f.Context {
				got = append(got, item.Source)
			}

			if !reflect.DeepEqual(got, tc.want) || f.ContextTruncated {
				t.Fatalf("got=%q truncated=%v want=%q", got, f.ContextTruncated, tc.want)
			}
		})
	}
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for name, data := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTargetFiltersAndLazyContext(t *testing.T) {
	cases := []struct {
		name   string
		broken bool
	}{{name: "small unchanged sibling"}, {name: "broken sibling", broken: true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()

			neighbor := "package p\ntype Value int"
			if tc.broken {
				neighbor = invalidGo
			}

			code := "package p\nfunc Target(v Value) { _ = v; " + strings.Repeat(" ", 100) + "}"
			writeFiles(t, root, map[string]string{testSourcePath: code, typesSourcePath: neighbor})

			loader := NewContextLoader()

			var got []Function

			err := Walk(
				context.Background(),
				[]string{filepath.Join(root, testSourcePath)},
				nil,
				nil,
				loader,
				func(f Function) error {
					f, err := loader.Prepare(context.Background(), f)
					if err == nil {
						got = append(got, f)
					}

					return err
				},
			)
			if (err != nil) != tc.broken {
				t.Fatalf("error=%v", err)
			}

			if !tc.broken &&
				(len(got) != 1 || len(got[0].Context) != 1 || got[0].Context[0].Source != "type Value int") {
				t.Fatalf("functions=%+v", got)
			}
		})
	}
}

func TestSiblingReadFailure(t *testing.T) {
	for _, name := range []string{"unreadable sibling"} {
		t.Run(name, func(t *testing.T) {
			if os.Geteuid() == 0 {
				t.Skip("root can read files without read permission")
			}

			root := t.TempDir()
			writeFiles(
				t,
				root,
				map[string]string{
					testSourcePath:  "package p\nfunc Target(v Value){}",
					typesSourcePath: "package p\ntype Value int",
				},
			)

			path := filepath.Join(root, typesSourcePath)
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrClosed) {
					t.Error(err)
				}
			}()

			loader := NewContextLoader()

			functions, err := loader.Functions(filepath.Join(root, testSourcePath))
			if err != nil {
				t.Fatal(err)
			}

			if _, err := loader.Prepare(context.Background(), functions[0]); err == nil {
				t.Fatal("ignored read error")
			}
		})
	}
}

func TestSameLineMethods(t *testing.T) {
	for _, name := range []string{"exact receiver identity"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(
				t,
				root,
				map[string]string{
					testSourcePath: "package p\ntype A int; type B int; func (A) Target() {}; func (B) Target() {}",
				},
			)

			loader := NewContextLoader()

			functions, err := loader.Functions(filepath.Join(root, testSourcePath))
			if err != nil {
				t.Fatal(err)
			}

			for i, want := range []string{"type A int", "type B int"} {
				got, err := loader.Prepare(context.Background(), functions[i])
				if err != nil || len(got.Context) != 1 || got.Context[0].Source != want {
					t.Fatalf("method %d: %+v %v", i, got, err)
				}
			}
		})
	}
}

func TestSnapshot(t *testing.T) {
	for _, changeBeforePrepare := range []bool{false, true} {
		t.Run(fmt.Sprint(changeBeforePrepare), func(t *testing.T) {
			root := t.TempDir()
			original := "package p\ntype A int; type B int; func (A) Target() {}; func (B) Target() {}"
			writeFiles(t, root, map[string]string{testSourcePath: original})

			loader := NewContextLoader()

			functions, err := loader.Functions(filepath.Join(root, testSourcePath))
			if err != nil {
				t.Fatal(err)
			}

			for i, want := range []string{"type A int", "type B int"} {
				if changeBeforePrepare || i == 1 {
					writeFiles(t, root, map[string]string{testSourcePath: invalidGo})
				}

				got, err := loader.Prepare(context.Background(), functions[i])
				if err != nil || len(got.Context) != 1 || got.Context[0].Source != want ||
					got.Source != functions[i].Source ||
					got.File != filepath.Join(root, testSourcePath) {
					t.Fatalf("snapshot: %+v %v", got, err)
				}
			}
		})
	}
}

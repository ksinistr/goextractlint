package filewalk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWalk(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop bool
		want []string
	}{
		{name: "paths without parsing", want: []string{testSourcePath, "sub/b.go"}},
		{name: "visitor stops traversal", stop: true, want: []string{testSourcePath}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{testSourcePath, "sub/b.go", "a_test.go", "vendor/c.go", ".git/d.go", "notes.txt"} {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(path, []byte("not valid Go"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var got []string

			stopped := errors.New("stop")

			err := Walk(
				context.Background(),
				[]string{root, filepath.Join(root, testSourcePath)},
				nil,
				nil,
				func(path string) error {
					relative, err := filepath.Rel(root, path)
					if err != nil {
						return fmt.Errorf("relative path: %w", err)
					}

					got = append(got, filepath.ToSlash(relative))

					if tc.stop {
						return stopped
					}

					return nil
				},
			)
			if !reflect.DeepEqual(got, tc.want) || tc.stop && !errors.Is(err, stopped) || !tc.stop && err != nil {
				t.Fatalf("paths=%v error=%v", got, err)
			}
		})
	}
}

const testSourcePath = "a.go"

func Test_hasSymlinkAncestor(t *testing.T) {
	for _, tc := range []struct {
		name, path, workingDir string
		want                   bool
	}{
		{name: "plain path", path: "real/a.go", workingDir: "elsewhere"},
		{name: "symlink inside tree", path: linkedSourcePath, workingDir: "elsewhere", want: true},
		{name: "symlink is working directory", path: linkedSourcePath, workingDir: "link"},
		{name: "symlink above working directory", path: linkedSourcePath, workingDir: "link/sub"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "real", "sub"), 0o700); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(root, "real", "a.go"), nil, 0o600); err != nil {
				t.Fatal(err)
			}

			if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}

			got, err := hasSymlinkAncestor(filepath.Join(root, tc.path), filepath.Join(root, tc.workingDir))
			if err != nil || got != tc.want {
				t.Fatalf("got=%v err=%v", got, err)
			}
		})
	}
}

const linkedSourcePath = "link/a.go"

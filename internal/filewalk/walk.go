package filewalk

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func Walk(
	ctx context.Context,
	paths []string,
	paths0 io.Reader,
	stopRead func() error,
	visit func(string) error,
) error {
	seen := make(map[string]bool)

	process := func(path string) error {
		return walkPath(ctx, path, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("walk source: %w", err)
			}

			return visitSource(ctx, path, entry, seen, visit)
		})
	}

	if paths0 == nil {
		for _, path := range paths {
			if err := process(path); err != nil {
				return fmt.Errorf("process source path: %w", err)
			}
		}

		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context: %w", err)
		}

		return nil
	}

	return walkStream(ctx, paths0, stopRead, process)
}

func excluded(path string) bool {
	for part := range strings.SplitSeq(filepath.Clean(path), string(filepath.Separator)) {
		if part == ".git" || part == "vendor" {
			return true
		}
	}

	return false
}

func walkPath(ctx context.Context, path string, visit fs.WalkDirFunc) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context: %w", err)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	hasSymlink, err := hasSymlinkAncestor(absolute, workingDir)
	if err != nil {
		return err
	}

	if hasSymlink {
		return nil
	}

	if err := filepath.WalkDir(absolute, visit); err != nil {
		return fmt.Errorf("walk source path: %w", err)
	}

	return nil
}

func hasSymlinkAncestor(path, workingDir string) (bool, error) {
	for parent := path; !trustedAncestor(parent, workingDir); parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil {
			return false, fmt.Errorf("inspect source ancestor: %w", err)
		}

		if info.Mode()&fs.ModeSymlink != 0 {
			return true, nil
		}
	}

	return false, nil
}

func trustedAncestor(path, workingDir string) bool {
	root := filepath.Dir(filepath.Dir(path)) == filepath.Dir(path)

	return root || containsDir(path, workingDir)
}

func visitSource(
	ctx context.Context,
	path string,
	entry fs.DirEntry,
	seen map[string]bool,
	visit func(string) error,
) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return fmt.Errorf("context: %w", contextErr)
	}

	if excluded(path) || entry.Type()&fs.ModeSymlink != 0 {
		if entry.IsDir() {
			return filepath.SkipDir
		}

		return nil
	}

	if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || seen[path] {
		return nil
	}

	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("inspect source file: %w", err)
	}

	if !info.Mode().IsRegular() {
		return nil
	}

	seen[path] = true

	return visit(path)
}

func walkStream(ctx context.Context, paths0 io.Reader, stopRead func() error, process func(string) error) error {
	done := make(chan struct{})
	defer close(done)

	if stopRead != nil {
		go func() {
			select {
			case <-ctx.Done():
				if err := stopRead(); err != nil {
					return
				}
			case <-done:
			}
		}()
	}

	reader := bufio.NewReader(paths0)

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context: %w", err)
		}

		path, err := readPath(ctx, reader)
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}

		if err := process(path); err != nil {
			return fmt.Errorf("process stream path: %w", err)
		}
	}
}

func readPath(ctx context.Context, reader *bufio.Reader) (string, error) {
	path, err := reader.ReadString(0)

	if contextErr := ctx.Err(); contextErr != nil {
		return "", fmt.Errorf("context: %w", contextErr)
	}

	if errors.Is(err, io.EOF) {
		if path == "" {
			return "", io.EOF
		}

		return "", fmt.Errorf("unterminated NUL path")
	}

	if err != nil {
		return "", fmt.Errorf("read path stream: %w", err)
	}

	if len(path) == 1 {
		return "", fmt.Errorf("empty NUL path")
	}

	return path[:len(path)-1], nil
}

func containsDir(parent, path string) bool {
	relative, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}

	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

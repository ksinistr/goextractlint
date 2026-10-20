package source

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Function struct {
	snapshot         *parsedFile
	declaration      *ast.FuncDecl
	File             string        `json:"file"`
	Package          string        `json:"package"`
	Name             string        `json:"name"`
	Receiver         string        `json:"receiver,omitempty"`
	StartLine        int           `json:"start_line"`
	EndLine          int           `json:"end_line"`
	Source           string        `json:"source"`
	Context          []ContextItem `json:"context,omitempty"`
	ContextTruncated bool          `json:"context_truncated"`
}

func Walk(
	ctx context.Context,
	paths []string,
	paths0 io.Reader,
	stopRead func() error,
	loader *ContextLoader,
	visit func(Function) error,
) error {
	seen := make(map[string]bool)

	process := func(path string) error {
		return walkPath(ctx, path, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("walk source: %w", err)
			}

			return visitSource(ctx, path, entry, seen, loader, visit)
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
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == ".git" || part == "vendor" {
			return true
		}
	}

	return false
}

func (l *ContextLoader) Functions(path string) ([]Function, error) {
	file, err := l.readAST(path)
	if err != nil {
		return nil, err
	}

	var functions []Function

	for _, declaration := range file.syntax.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}

		metadata, err := functionFromDeclaration(file, function)
		if err != nil {
			return nil, err
		}

		functions = append(functions, metadata)
	}

	return functions, nil
}

func functionFromDeclaration(file *parsedFile, function *ast.FuncDecl) (Function, error) {
	start := function.Pos()
	if function.Doc != nil {
		start = function.Doc.Pos()
	}

	var receiver bytes.Buffer
	if function.Recv != nil {
		if err := format.Node(&receiver, file.positions, function.Recv.List[0].Type); err != nil {
			return Function{}, fmt.Errorf("go/format.Node: %w", err)
		}
	}

	startPosition, endPosition := file.positions.Position(start), file.positions.Position(function.End())

	return Function{
		snapshot: file, declaration: function,
		File: file.path, Package: file.syntax.Name.Name, Name: function.Name.Name,
		Receiver: receiver.String(), StartLine: startPosition.Line,
		EndLine: endPosition.Line,
		Source:  string(file.data[startPosition.Offset:endPosition.Offset]),
	}, nil
}

func (f Function) Identity() string {
	if f.Receiver != "" {
		return "(" + f.Receiver + ")." + f.Name
	}

	return f.Name
}

func walkPath(ctx context.Context, path string, visit fs.WalkDirFunc) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context: %w", err)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}

	hasSymlink, err := hasSymlinkAncestor(absolute)
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

func hasSymlinkAncestor(path string) (bool, error) {
	for parent := path; ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil {
			return false, fmt.Errorf("inspect source ancestor: %w", err)
		}

		if info.Mode()&fs.ModeSymlink != 0 {
			return true, nil
		}

		if filepath.Dir(parent) == parent {
			break
		}
	}

	return false, nil
}

func visitSource(
	ctx context.Context,
	path string,
	entry fs.DirEntry,
	seen map[string]bool,
	loader *ContextLoader,
	visit func(Function) error,
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

	functions, err := loader.Functions(path)
	if err != nil {
		return fmt.Errorf("load functions: %w", err)
	}

	for _, function := range functions {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context: %w", err)
		}

		if err := visit(function); err != nil {
			return fmt.Errorf("visit function: %w", err)
		}
	}

	return nil
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

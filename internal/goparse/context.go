package goparse

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const typeKind = "type"

type parsedFile struct {
	path      string
	data      []byte
	positions *token.FileSet
	syntax    *ast.File
	functions map[functionLocation]*ast.FuncDecl
}
type definition struct {
	file  *parsedFile
	node  ast.Node
	start token.Pos
	kind  string
}
type packageIndex struct {
	names map[string][]definition
}
type ContextLoader struct {
	files    map[string]*parsedFile
	errors   map[string]error
	packages map[string]*packageIndex
}

func NewContextLoader() *ContextLoader {
	return &ContextLoader{
		files:    make(map[string]*parsedFile),
		errors:   make(map[string]error),
		packages: make(map[string]*packageIndex),
	}
}

func (l *ContextLoader) readAST(path string) (*parsedFile, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve source path: %w", err)
	}

	if f, ok := l.files[path]; ok {
		return f, l.errors[path]
	}

	f, err := readAST(path)
	l.files[path], l.errors[path] = f, err

	return f, err
}

func readAST(path string) (*parsedFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read source: %w", err)
	}

	positions := token.NewFileSet()

	syntax, err := parser.ParseFile(positions, path, data, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return &parsedFile{
		path:      path,
		data:      data,
		positions: positions,
		syntax:    syntax,
		functions: make(map[functionLocation]*ast.FuncDecl),
	}, nil
}

func (l *ContextLoader) load(
	ctx context.Context,
	dir, name string,
) (*packageIndex, error) {
	key := dir + "\x00" + name
	if index := l.packages[key]; index != nil {
		return index, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read package directory: %w", err)
	}

	index := &packageIndex{names: make(map[string][]definition)}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context: %w", err)
		}

		eligible, err := isPackageSource(entry)
		if err != nil {
			return nil, err
		}

		if !eligible {
			continue
		}

		path := filepath.Join(dir, entry.Name())

		file, err := l.readAST(path)
		if err != nil {
			return nil, fmt.Errorf("read package goparse: %w", err)
		}

		if file.syntax.Name.Name != name {
			continue
		}

		index.addFile(file)
	}

	l.packages[key] = index

	return index, nil
}

func isPackageSource(entry fs.DirEntry) (bool, error) {
	if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
		!strings.HasSuffix(entry.Name(), ".go") ||
		strings.HasSuffix(entry.Name(), "_test.go") {
		return false, nil
	}

	info, err := entry.Info()
	if err != nil {
		return false, fmt.Errorf("inspect package entry: %w", err)
	}

	return info.Mode().IsRegular(), nil
}

func (l *ContextLoader) Prepare(
	ctx context.Context,
	function Function,
) (Function, error) {
	index, err := l.load(ctx, filepath.Dir(function.File), function.Package)
	if err != nil {
		return function, err
	}

	file := l.files[function.File]

	var target *ast.FuncDecl
	if file != nil {
		target = file.functions[locateFunction(function)]
	}

	if file == nil || target == nil {
		return function, fmt.Errorf("function has no source snapshot: %s", function.Identity())
	}

	selected := make(map[string]definition)
	add := func(d definition) {
		if d.file == file && d.node == target {
			return
		}

		selected[d.file.path+":"+strconv.Itoa(int(d.start))] = d
	}
	resolve := func(name, kind string) {
		if defs := index.names[name]; len(defs) == 1 && defs[0].kind == kind {
			add(defs[0])
		}
	}
	parameters := collectSignatureTypes(target, resolve)

	ast.Inspect(target, func(node ast.Node) bool {
		if n, ok := node.(*ast.CallExpr); ok {
			fun := directCallee(n.Fun)
			if id, ok := fun.(*ast.Ident); ok && !parameters[id.Name] && packageReference(id, "function") {
				resolve(id.Name, "function")
			}
		}

		return true
	})

	function.Context = contextItems(selected)

	return function, nil
}

func contextItems(selected map[string]definition) []ContextItem {
	definitions := make([]definition, 0, len(selected))
	for _, d := range selected {
		definitions = append(definitions, d)
	}

	sortContextDefinitions(definitions)

	items := make([]ContextItem, 0, len(definitions))
	for _, d := range definitions {
		items = append(items, contextItem(d))
	}

	return items
}

func sortContextDefinitions(definitions []definition) {
	rank := func(kind string) int {
		switch kind {
		case typeKind:
			return 0
		default:
			return 1
		}
	}

	sort.Slice(definitions, func(i, j int) bool {
		a, b := definitions[i], definitions[j]
		if rank(a.kind) != rank(b.kind) {
			return rank(a.kind) < rank(b.kind)
		}

		if a.file.path != b.file.path {
			return a.file.path < b.file.path
		}

		return a.start < b.start
	})
}

func contextItem(d definition) ContextItem {
	start, end := d.file.positions.Position(d.start), d.file.positions.Position(d.node.End())

	return ContextItem{
		Kind:   d.kind,
		Source: string(d.file.data[start.Offset:end.Offset]),
	}
}

func collectSignatureTypes(
	target *ast.FuncDecl,
	resolve func(string, string),
) map[string]bool {
	parameters := make(map[string]bool)

	if target.Type.TypeParams != nil {
		for _, field := range target.Type.TypeParams.List {
			for _, n := range field.Names {
				parameters[n.Name] = true
			}
		}
	}

	collectReceiverTypes(target.Recv, parameters, resolve)

	var inspectType func(ast.Node) bool

	inspectType = func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SelectorExpr:
			return false
		case *ast.Field:
			ast.Inspect(n.Type, inspectType)
			return false
		case *ast.Ident:
			if !parameters[n.Name] && packageReference(n, typeKind) {
				resolve(n.Name, typeKind)
			}
		}

		return true
	}

	for _, fields := range []*ast.FieldList{target.Type.Params, target.Type.Results} {
		if fields == nil {
			continue
		}

		for _, field := range fields.List {
			ast.Inspect(field.Type, inspectType)
		}
	}

	return parameters
}

func directCallee(expression ast.Expr) ast.Expr {
	switch e := expression.(type) {
	case *ast.ParenExpr:
		return directCallee(e.X)
	case *ast.IndexExpr:
		return directCallee(e.X)
	case *ast.IndexListExpr:
		return directCallee(e.X)
	default:
		return expression
	}
}

func packageReference(id *ast.Ident, kind string) bool {
	if id.Obj == nil {
		return true
	}

	switch decl := id.Obj.Decl.(type) {
	case *ast.FuncDecl:
		return kind == "function" && decl.Recv == nil
	case *ast.TypeSpec:
		return kind == typeKind && id.Obj.Kind == ast.Typ
	}

	return false
}

func (index *packageIndex) addFile(file *parsedFile) {
	for _, decl := range file.syntax.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil {
				continue
			}

			start := d.Pos()
			if d.Doc != nil {
				start = d.Doc.Pos()
			}

			index.names[d.Name.Name] = append(index.names[d.Name.Name], definition{file, d, start, "function"})
		case *ast.GenDecl:
			index.addDeclaration(file, d)
		}
	}
}

func (index *packageIndex) addDeclaration(file *parsedFile, declaration *ast.GenDecl) {
	for _, spec := range declaration.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			node, start := typeDefinition(declaration, s)
			index.names[s.Name.Name] = append(index.names[s.Name.Name], definition{file, node, start, typeKind})
		case *ast.ValueSpec:
			for _, name := range s.Names {
				index.names[name.Name] = append(index.names[name.Name], definition{file, s, s.Pos(), "value"})
			}
		}
	}
}

func typeDefinition(declaration *ast.GenDecl, spec *ast.TypeSpec) (ast.Node, token.Pos) {
	if len(declaration.Specs) == 1 && !declaration.Lparen.IsValid() {
		start := declaration.Pos()
		if declaration.Doc != nil {
			start = declaration.Doc.Pos()
		}

		return declaration, start
	}

	start := spec.Pos()
	if spec.Doc != nil {
		start = spec.Doc.Pos()
	} else if len(declaration.Specs) == 1 && declaration.Doc != nil {
		start = declaration.Doc.Pos()
	}

	return spec, start
}

func collectReceiverTypes(receiverFields *ast.FieldList, parameters map[string]bool, resolve func(string, string)) {
	if receiverFields == nil {
		return
	}

	receiver := receiverFields.List[0].Type
	if star, ok := receiver.(*ast.StarExpr); ok {
		receiver = star.X
	}

	switch r := receiver.(type) {
	case *ast.Ident:
		resolve(r.Name, typeKind)
	case *ast.IndexExpr:
		if id, ok := r.X.(*ast.Ident); ok {
			resolve(id.Name, typeKind)
		}

		if id, ok := r.Index.(*ast.Ident); ok {
			parameters[id.Name] = true
		}
	case *ast.IndexListExpr:
		if id, ok := r.X.(*ast.Ident); ok {
			resolve(id.Name, typeKind)
		}

		for _, arg := range r.Indices {
			if id, ok := arg.(*ast.Ident); ok {
				parameters[id.Name] = true
			}
		}
	}
}

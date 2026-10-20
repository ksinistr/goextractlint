package goparse

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"

	"goextractlint/internal/domain"
)

type functionLocation struct {
	name      string
	receiver  string
	startLine int
}

func locateFunction(function domain.Function) functionLocation {
	return functionLocation{function.Name, function.Receiver, function.StartLine}
}

func (l *ContextLoader) Functions(path string) ([]domain.Function, error) {
	file, err := l.readAST(path)
	if err != nil {
		return nil, err
	}

	var functions []domain.Function

	for _, declaration := range file.syntax.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}

		metadata, err := functionFromDeclaration(file, function)
		if err != nil {
			return nil, err
		}

		file.functions[locateFunction(metadata)] = function

		functions = append(functions, metadata)
	}

	return functions, nil
}

func functionFromDeclaration(file *parsedFile, function *ast.FuncDecl) (domain.Function, error) {
	start := function.Pos()
	if function.Doc != nil {
		start = function.Doc.Pos()
	}

	var receiver bytes.Buffer
	if function.Recv != nil {
		if err := format.Node(&receiver, file.positions, function.Recv.List[0].Type); err != nil {
			return domain.Function{}, fmt.Errorf("go/format.Node: %w", err)
		}
	}

	startPosition, endPosition := file.positions.Position(start), file.positions.Position(function.End())

	return domain.Function{
		File: file.path, Package: file.syntax.Name.Name, Name: function.Name.Name,
		Receiver: receiver.String(), StartLine: startPosition.Line,
		EndLine: endPosition.Line,
		Source:  string(file.data[startPosition.Offset:endPosition.Offset]),
	}, nil
}

package checkpolicy

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ResolveIntegration derives top-level test names from Go syntax, rather than
// silently accepting a stale hand-written -run expression matching zero tests.
func (p *Plan) ResolveIntegration(ctx context.Context, root string) error {
	companions, err := companionTests(ctx, root, p.GoFiles)
	if err != nil {
		return err
	}
	p.integration = unique(append(p.integration, companions...))
	for _, file := range p.integration {
		names, err := testNames(ctx, filepath.Join(root, file))
		if os.IsNotExist(err) {
			p.requireFull(file, "selected integration file deleted")
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect integration file %s: %w", file, err)
		}
		if len(names) == 0 {
			p.requireFull(file, "integration helper changed without declared tests")
			continue
		}
		pkg := "./" + filepath.ToSlash(filepath.Dir(file))
		p.Integration[pkg] = unique(append(p.Integration[pkg], names...))
	}
	return nil
}

func companionTests(ctx context.Context, root string, files []string) ([]string, error) {
	result := make([]string, 0)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(file, "internal/postgres/") || strings.HasSuffix(file, "_test.go") {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, filepath.Dir(file)))
		if err != nil {
			return nil, fmt.Errorf("inspect storage test companions: %w", err)
		}
		stem := strings.TrimSuffix(filepath.Base(file), ".go")
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), "_integration_test.go") {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), "_integration_test.go")
			if stem == name || strings.HasPrefix(stem, name+"_") {
				result = append(result, filepath.ToSlash(filepath.Join(filepath.Dir(file), entry.Name())))
			}
		}
	}
	return unique(result), nil
}

func testNames(ctx context.Context, file string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0)
	for _, declaration := range source.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || !isTestName(function.Name.Name) {
			continue
		}
		// go test validates signatures, including aliases and dot imports. Do not
		// narrow by a guessed import spelling and silently omit valid tests.
		names = append(names, function.Name.Name)
	}
	return unique(names), nil
}

func isTestName(name string) bool {
	if !strings.HasPrefix(name, "Test") {
		return false
	}
	if name == "Test" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len("Test"):])
	return !unicode.IsLower(r)
}

func TestPattern(names []string) string {
	escaped := make([]string, 0, len(names))
	for _, name := range unique(names) {
		escaped = append(escaped, regexp.QuoteMeta(name))
	}
	return "^(" + strings.Join(escaped, "|") + ")$"
}

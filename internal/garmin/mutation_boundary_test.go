package garmin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// This targeted source gate supplements visibility; it does not claim that the
// compiler enforces every architectural convention or forbid unsafe reflection.
func TestRawDeviceAccessStaysAtBoundary(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	owners := map[string]bool{"filesystem.go": true, "file_cache.go": true, "diagnostics.go": true, "lazy.go": true}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}

			if !owners[name] && (name != "service.go" || fn.Name.Name != "Close") {
				assertNoRawDeviceSelector(t, name, fn)
			}

			assertNoExportedTransportResult(t, name, fn)
		}
	}
}

func assertNoRawDeviceSelector(t *testing.T, file string, fn *ast.FuncDecl) {
	t.Helper()

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "dev" {
			t.Errorf("%s:%s bypasses the file/capability boundary with .dev", file, fn.Name.Name)
		}

		return true
	})
}

func assertNoExportedTransportResult(t *testing.T, file string, fn *ast.FuncDecl) {
	t.Helper()

	if !fn.Name.IsExported() || fn.Type.Results == nil {
		return
	}

	rawTypes := map[string]bool{
		"Device": true, "HiddenFS": true, "MTPDiagnostics": true, "RawMTPReader": true, "StorageReporter": true,
	}

	ast.Inspect(fn.Type.Results, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		pkg, ok := selector.X.(*ast.Ident)
		if ok && pkg.Name == "device" && rawTypes[selector.Sel.Name] {
			t.Errorf("%s:%s exports a raw transport capability", file, fn.Name.Name)
		}

		return true
	})
}

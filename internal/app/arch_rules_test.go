package app

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"
)

const architectureSettings = architectureModule + "/internal/containers/settings"

func TestArchitectureImportRules(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, from, to string
		forbidden      bool
	}{
		{name: "entry point builds app", from: architectureModule + "/cmd/garminctl", to: architectureModule + "/internal/app"},
		{name: "shell cannot import app", from: architectureModule + "/internal/web", to: architectureModule + "/internal/app", forbidden: true},
		{name: "infrastructure cannot import feature", from: architectureModule + "/internal/ship/watchsession", to: architectureSettings, forbidden: true},
		{name: "client cannot import facade", from: architectureModule + "/internal/gc", to: architectureModule + "/internal/garmin", forbidden: true},
		{name: "core cannot import HTTP helper", from: architectureSettings, to: architectureModule + "/internal/ship/httpx", forbidden: true},
		{name: "feature cannot import legacy", from: architectureSettings, to: architectureModule + "/internal/garmin", forbidden: true},
		{name: "own UI uses core", from: architectureSettings + "/ui/http", to: architectureSettings},
		{name: "UI cannot import other feature", from: architectureSettings + "/ui/http", to: architectureModule + "/internal/containers/apps", forbidden: true},
		{name: "UI cannot import legacy facade", from: architectureSettings + "/ui/http", to: architectureModule + "/internal/garmin", forbidden: true},
		{name: "core cannot import own UI", from: architectureSettings, to: architectureSettings + "/ui/http", forbidden: true},
		{name: "legacy facade may delegate to core", from: architectureModule + "/internal/garmin", to: architectureSettings},
		{name: "HTTP clients allowed", from: architectureSettings, to: "net/http"},
		{name: "MCP SDK forbidden in core", from: architectureSettings, to: "github.com/modelcontextprotocol/go-sdk/mcp", forbidden: true},
		{name: "Wails forbidden in core", from: architectureSettings, to: "github.com/wailsapp/wails/v3/pkg/application", forbidden: true},
		{name: "MCP helper allowed", from: architectureModule + "/internal/ship/mcpx", to: "github.com/modelcontextprotocol/go-sdk/mcp"},
		{name: "reviewed HTML parser", from: architectureModule + "/internal/ciqstore", to: "golang.org/x/net/html"},
		{name: "reviewed USB library", from: architectureModule + "/internal/device/mtp", to: "github.com/google/gousb"},
		{name: "unreviewed feature library", from: architectureSettings, to: "example.com/new/library", forbidden: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if reason := architectureImportViolation(tt.from, tt.to); (reason != "") != tt.forbidden {
				t.Fatalf("%s -> %s reason=%q forbidden=%t", tt.from, tt.to, reason, tt.forbidden)
			}
		})
	}
}

type architectureFixtureImporter map[string]*types.Package

func (imports architectureFixtureImporter) Import(path string) (*types.Package, error) {
	if pkg := imports[path]; pkg != nil {
		return pkg, nil
	}

	return nil, fmt.Errorf("fixture did not load %s", path)
}

func TestArchitectureTypedRules(t *testing.T) {
	t.Parallel()

	imports := architectureFixtureImporter{}
	for _, pkg := range loadArchitecture(t.Context(), t, architectureView{name: "typed fixtures"}, packages.LoadTypes, "net/http", architectureModule+"/internal/garmin") {
		imports[pkg.PkgPath] = pkg.Types
	}

	for _, tt := range []struct {
		name, owner, code string
		forbidden         bool
	}{
		{name: "upstream client and request APIs", code: `import wire "net/http"; var _ = &wire.Client{}; var _ = wire.MethodGet; var _ = wire.NewRequestWithContext`},
		{name: "domain type may be named Server", code: `type Server struct{}; var _ Server`},
		{name: "aliased ResponseWriter", code: `import wire "net/http"; type Writer = wire.ResponseWriter; var _ Writer`, forbidden: true},
		{name: "handler function", code: `import wire "net/http"; var _ wire.HandlerFunc`, forbidden: true},
		{name: "handler interface", code: `import wire "net/http"; var _ wire.Handler`, forbidden: true},
		{name: "mux constructor value", code: `import wire "net/http"; var _ = wire.NewServeMux`, forbidden: true},
		{name: "server struct", code: `import wire "net/http"; var _ = &wire.Server{}`, forbidden: true},
		{name: "serve function value", code: `import wire "net/http"; var _ = wire.ListenAndServe`, forbidden: true},
		{name: "server APIs belong to UI", owner: architectureSettings + "/ui/http", code: `import wire "net/http"; var _ = &wire.Server{}`},
		{name: "runtime factory alias outside app", owner: architectureModule + "/internal/web", code: `import legacy "github.com/DevWolk/garminctl/internal/garmin"; var factory = legacy.NewRuntime`, forbidden: true},
		{name: "runtime factory belongs to app", owner: architectureModule + "/internal/app", code: `import legacy "github.com/DevWolk/garminctl/internal/garmin"; var factory = legacy.NewRuntime`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			owner := tt.owner
			if owner == "" {
				owner = architectureSettings
			}

			info, positions := architectureTypeFixture(t, owner, tt.code, imports)

			violations := architectureTypedViolations(owner, info, positions)
			if (len(violations) > 0) != tt.forbidden {
				t.Fatalf("violations=%v forbidden=%t", violations, tt.forbidden)
			}
		})
	}
}

func architectureTypeFixture(t *testing.T, owner, code string, imports types.Importer) (*types.Info, *token.FileSet) {
	t.Helper()

	positions := token.NewFileSet()

	file, err := parser.ParseFile(positions, "fixture.go", "package fixture\n"+code, 0)
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}

	config := &types.Config{Importer: imports}
	if _, err := config.Check(owner, positions, []*ast.File{file}, info); err != nil {
		t.Fatal(err)
	}

	return info, positions
}

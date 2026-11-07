package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"
)

const (
	architectureSettings = architectureModule + "/internal/containers/settings"
	architectureApps     = architectureModule + "/internal/containers/apps"
	architectureProcess  = architectureModule + "/internal/app/process"
)

func TestArchitectureImportRules(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, from, to string
		forbidden      bool
	}{
		{name: "entry point builds app", from: architectureModule + "/cmd/garminctl", to: architectureModule + "/internal/app"},
		{name: "shell cannot import app", from: architectureModule + "/internal/web", to: architectureModule + "/internal/app", forbidden: true},
		{name: "infrastructure cannot import feature", from: architectureModule + "/internal/ship/watchsession", to: architectureSettings, forbidden: true},
		{name: "core cannot import HTTP helper", from: architectureSettings, to: architectureModule + "/internal/ship/httpx", forbidden: true},
		{name: "own UI uses core", from: architectureSettings + "/ui/http", to: architectureSettings},
		{name: "UI cannot import other feature", from: architectureSettings + "/ui/http", to: architectureApps, forbidden: true},
		{name: "core cannot import own UI", from: architectureSettings, to: architectureSettings + "/ui/http", forbidden: true},
		{name: "process runtime builds feature owners", from: architectureProcess, to: architectureModule + "/internal/containers/connect"},
		{name: "shell cannot import process runtime", from: architectureModule + "/internal/web", to: architectureProcess, forbidden: true},
		{name: "feature cannot import process runtime", from: architectureSettings, to: architectureProcess, forbidden: true},
		{name: "HTTP clients allowed", from: architectureSettings, to: "net/http"},
		{name: "MCP SDK forbidden in core", from: architectureSettings, to: "github.com/modelcontextprotocol/go-sdk/mcp", forbidden: true},
		{name: "Wails forbidden in core", from: architectureSettings, to: "github.com/wailsapp/wails/v3/pkg/application", forbidden: true},
		{name: "MCP helper allowed", from: architectureModule + "/internal/ship/mcpx", to: "github.com/modelcontextprotocol/go-sdk/mcp"},
		{name: "reviewed HTML parser", from: architectureModule + "/internal/ciqstore", to: "golang.org/x/net/html"},
		{name: "reviewed USB library", from: architectureModule + "/internal/device/mtp", to: "github.com/google/gousb"},
		{name: "unreviewed feature library", from: architectureSettings, to: "example.com/new/library", forbidden: true},
		{name: "application SemVer allowance", from: architectureModule + "/internal/containers/application", to: "golang.org/x/mod/semver"},
		{name: "application allowance is package specific", from: architectureModule + "/internal/containers/application", to: "golang.org/x/mod/module", forbidden: true},
		{name: "SemVer allowance is feature specific", from: architectureSettings, to: "golang.org/x/mod/semver", forbidden: true},
		{name: "application cannot import Wails updater", from: architectureModule + "/internal/containers/application", to: "github.com/wailsapp/wails/v3/pkg/updater", forbidden: true},
		{name: "update transport cannot import application policy", from: architectureModule + "/internal/ship/updatetransport", to: architectureModule + "/internal/containers/application", forbidden: true},
		{name: "update transport cannot import app wiring", from: architectureModule + "/internal/ship/updatetransport", to: architectureModule + "/internal/app", forbidden: true},
		{name: "metadata transport has no Wails allowance", from: architectureModule + "/internal/ship/updatetransport", to: "github.com/wailsapp/wails/v3/pkg/updater", forbidden: true},
		{name: "app wires release transport", from: architectureModule + "/internal/app", to: architectureModule + "/internal/ship/updatetransport"},
		{name: "app wires release policy", from: architectureModule + "/internal/app", to: architectureModule + "/internal/containers/application"},
		{name: "application has no watch feature dependency", from: architectureModule + "/internal/containers/application", to: architectureApps, forbidden: true},
		{name: "shell cannot import feature adapter", from: architectureModule + "/internal/web", to: architectureSettings + "/ui/http", forbidden: true},
		{name: "shell cannot import feature core", from: architectureModule + "/internal/mcpserver", to: architectureApps, forbidden: true},
		{name: "desktop cannot import web shell", from: architectureModule + "/internal/desktop", to: architectureModule + "/internal/web", forbidden: true},
		{name: "desktop cannot import MCP shell", from: architectureModule + "/internal/desktop", to: architectureModule + "/internal/mcpserver", forbidden: true},
		{name: "web mounts MCP shell", from: architectureModule + "/internal/web", to: architectureModule + "/internal/mcpserver"},
		{name: "UI cannot import web shell", from: architectureSettings + "/ui/http", to: architectureModule + "/internal/web", forbidden: true},
		{name: "UI cannot import MCP shell", from: architectureSettings + "/ui/mcp", to: architectureModule + "/internal/mcpserver", forbidden: true},
		{name: "declared feature edge", from: architectureModule + "/internal/containers/workouts", to: architectureModule + "/internal/containers/connect"},
		{name: "undeclared feature edge", from: architectureModule + "/internal/containers/music", to: architectureModule + "/internal/containers/health", forbidden: true},
		{name: "declared edge is one-way", from: architectureApps, to: architectureModule + "/internal/containers/connect", forbidden: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if reason := architectureImportViolation(tt.from, tt.to); (reason != "") != tt.forbidden {
				t.Fatalf("%s -> %s reason=%q forbidden=%t", tt.from, tt.to, reason, tt.forbidden)
			}
		})
	}
}

func TestArchitectureTestImportRules(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, from, to string
		forbidden      bool
	}{
		{name: "feature test uses own core", from: architectureSettings + "_test", to: architectureSettings},
		{name: "feature test uses fixtures", from: architectureSettings, to: architectureModule + "/internal/testdump"},
		{name: "feature test cannot import app", from: architectureSettings + "_test", to: architectureModule + "/internal/app", forbidden: true},
		{name: "UI test may use declared feature edge", from: architectureModule + "/internal/containers/updates/ui/cli", to: architectureApps},
		{name: "feature test cannot add feature edge", from: architectureSettings + "/ui/http_test", to: architectureApps, forbidden: true},
		{name: "shell test assembles features", from: architectureModule + "/internal/web", to: architectureSettings + "/ui/http"},
		{name: "shell test builds the process runtime", from: architectureModule + "/internal/web", to: architectureProcess},
		{name: "feature test cannot import process runtime", from: architectureSettings + "_test", to: architectureProcess, forbidden: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if reason := architectureTestImportViolation(tt.from, tt.to); (reason != "") != tt.forbidden {
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
	for _, pkg := range loadArchitecture(t.Context(), t, architectureView{name: "typed fixtures"}, packages.LoadTypes, "net/http",
		architectureProcess, architectureModule+"/internal/ship/watchsession", architectureModule+"/internal/containers/connect", architectureModule+"/internal/gc") {
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
		{name: "runtime factory alias outside app", owner: architectureModule + "/internal/web", code: `import rt "github.com/DevWolk/garminctl/internal/app/process"; var factory = rt.New`, forbidden: true},
		{name: "runtime factory belongs to app", owner: architectureModule + "/internal/app", code: `import rt "github.com/DevWolk/garminctl/internal/app/process"; var factory = rt.New`},
		{name: "watch owner outside app", owner: architectureModule + "/internal/web", code: `import ws "github.com/DevWolk/garminctl/internal/ship/watchsession"; var _ = ws.NewManager`, forbidden: true},
		{name: "process runtime owns watch manager", owner: architectureProcess, code: `import ws "github.com/DevWolk/garminctl/internal/ship/watchsession"; var _ = ws.NewManagerWithGate`},
		{name: "sync coordinator outside app", owner: architectureModule + "/internal/mcpserver", code: `import c "github.com/DevWolk/garminctl/internal/containers/connect"; var _ = c.NewSyncCoordinator`, forbidden: true},
		{name: "account built by feature UI", owner: architectureSettings + "/ui/http", code: `import "github.com/DevWolk/garminctl/internal/gc"; var _ = gc.New`, forbidden: true},
		{name: "entry point builds account", owner: architectureModule + "/cmd/garminctl", code: `import "github.com/DevWolk/garminctl/internal/gc"; var _ = gc.New`},
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

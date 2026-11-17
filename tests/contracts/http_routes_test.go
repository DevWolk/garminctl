package contracts

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// httpRoutes is every pattern the web shell and the feature HTTP adapters
// register. A route added, removed or renamed must change this list too.
var httpRoutes = []string{
	"/",
	"DELETE /api/apps/pending",
	"DELETE /api/apps/{ref}",
	"DELETE /api/file",
	"DELETE /api/music",
	"DELETE /api/workouts/{id}",
	"DELETE /mcp",
	"GET /api/apps",
	"GET /api/apps/pending",
	"GET /api/apps/{ref}/schema",
	"GET /api/apps/{ref}/settings",
	"GET /api/cloud/messages",
	"GET /api/cloud/settings",
	"GET /api/cloud/status",
	"GET /api/cloud/sync",
	"GET /api/decode",
	"GET /api/file",
	"GET /api/files",
	"GET /api/health/activities",
	"GET /api/health/activities/{name}",
	"GET /api/health/daily",
	"GET /api/health/day",
	"GET /api/health/extras",
	"GET /api/mcp",
	"GET /api/music",
	"GET /api/schema",
	"GET /api/server",
	"GET /api/settings",
	"GET /api/software",
	"GET /api/status",
	"GET /api/store",
	"GET /api/store/apps/{id}",
	"GET /api/updates/apps",
	"GET /api/updates/software",
	"GET /api/workouts",
	"GET /mcp",
	"GET /{tab}",
	"GET /{tab}/{rest...}",
	"POST /api/apps/install",
	"POST /api/cloud/login",
	"POST /api/cloud/logout",
	"POST /api/cloud/mfa",
	"POST /api/cloud/sync",
	"POST /api/connect",
	"POST /api/disconnect",
	"POST /api/file",
	"POST /api/health/weight",
	"POST /api/music",
	"POST /api/refresh",
	"POST /api/settings/apply",
	"POST /api/updates/software/install",
	"POST /api/workouts",
	"POST /api/workouts/preview",
	"POST /api/workouts/{id}/send",
	"POST /mcp",
	"PUT /api/apps/{ref}/hidden",
	"PUT /api/apps/{ref}/settings",
	"PUT /api/cloud/settings",
	"PUT /api/music/playlists/{name}",
}

func TestHTTPRouteSetIsPinned(t *testing.T) {
	t.Parallel()

	var registered []string

	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
				return err
			}

			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}

			ast.Inspect(parsed, func(node ast.Node) bool {
				if pattern, found := registeredRoute(t, file, node); found {
					registered = append(registered, pattern)
				}

				return true
			})

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	slices.Sort(registered)

	for _, pattern := range registered {
		if !slices.Contains(httpRoutes, pattern) {
			t.Errorf("route %q is registered but not pinned", pattern)
		}
	}

	for _, pattern := range httpRoutes {
		if !slices.Contains(registered, pattern) {
			t.Errorf("pinned route %q is no longer registered", pattern)
		}
	}

	if len(registered) != len(httpRoutes) {
		t.Errorf("%d registrations for %d pinned routes: %q", len(registered), len(httpRoutes), registered)
	}
}

// registeredRoute returns the pattern of a mux.Handle or mux.HandleFunc call.
// A pattern that is not a string literal cannot be pinned and fails the test.
func registeredRoute(t *testing.T, file string, node ast.Node) (string, bool) {
	t.Helper()

	call, ok := node.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 {
		return "", false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc") {
		return "", false
	}

	if receiver, ok := selector.X.(*ast.Ident); !ok || receiver.Name != "mux" {
		return "", false
	}

	literal, ok := call.Args[0].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		t.Errorf("%s: route pattern must be a string literal", file)
		return "", false
	}

	pattern, err := strconv.Unquote(literal.Value)
	if err != nil {
		t.Errorf("%s: %v", file, err)
		return "", false
	}

	return pattern, true
}

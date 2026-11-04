package app

import (
	"context"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"
)

const architectureModule = "github.com/DevWolk/garminctl"

type architectureView struct {
	name string
	tags string
	env  []string
}

func architectureRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate architecture test source")
	}

	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root: %v", err)
	}

	return root
}

func loadArchitecture(ctx context.Context, t *testing.T, view architectureView, mode packages.LoadMode, patterns ...string) []*packages.Package {
	t.Helper()

	cfg := &packages.Config{Context: ctx, Dir: architectureRoot(t), Mode: mode, Env: append(os.Environ(), view.env...), BuildFlags: []string{"-mod=readonly"}}
	if view.tags != "" {
		cfg.BuildFlags = append(cfg.BuildFlags, "-tags="+view.tags)
	}

	loaded, err := packages.Load(cfg, patterns...)
	if err != nil {
		t.Fatalf("load %s architecture: %v", view.name, err)
	}

	if len(loaded) == 0 {
		t.Fatalf("%s architecture matched no packages", view.name)
	}

	for _, pkg := range loaded {
		for _, issue := range pkg.Errors {
			t.Errorf("%s: %s", view.name, issue)
		}
	}

	if t.Failed() {
		t.FailNow()
	}

	t.Logf("%s: %d production packages", view.name, len(loaded))

	return loaded
}

func TestArchitecture(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)

	patterns := []string{"./cmd/...", "./internal/...", "./tools/..."}

	active := architectureView{name: "active", tags: architectureBuildTags}
	for _, pkg := range loadArchitecture(ctx, t, active, packages.LoadSyntax, patterns...) {
		checkArchitecturePackage(t, pkg, true)
	}

	for _, view := range []architectureView{
		{name: "linux portable", env: []string{"GOOS=linux", "CGO_ENABLED=0"}},
		{name: "linux desktop", tags: "desktop,production,gtk3", env: []string{"GOOS=linux", "CGO_ENABLED=1"}},
		{name: "windows desktop", tags: "desktop,production", env: []string{"GOOS=windows", "CGO_ENABLED=0"}},
	} {
		t.Run(view.name, func(t *testing.T) {
			t.Parallel()
			// Import metadata excludes generated cgo files and never executes another OS.
			// Typed checks use the active build above; native CI repeats them there.
			for _, pkg := range loadArchitecture(ctx, t, view, packages.NeedName|packages.NeedFiles|packages.NeedImports, patterns...) {
				checkArchitecturePackage(t, pkg, false)
			}
		})
	}
}

func checkArchitecturePackage(t *testing.T, pkg *packages.Package, typed bool) {
	t.Helper()

	var violations []string

	for imported := range pkg.Imports {
		if reason := architectureImportViolation(pkg.PkgPath, imported); reason != "" {
			violations = append(violations, pkg.PkgPath+" -> "+imported+": "+reason)
		}
	}

	if typed {
		violations = append(violations, architectureTypedViolations(pkg.PkgPath, pkg.TypesInfo, pkg.Fset)...)
	}

	sort.Strings(violations)

	for _, issue := range violations {
		t.Error(issue)
	}
}

func architectureWithin(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func architectureInternal(path string) string {
	return strings.TrimPrefix(path, architectureModule+"/internal/")
}

func architectureFeature(path string) string {
	rel := architectureInternal(path)
	if !strings.HasPrefix(rel, "containers/") {
		return ""
	}

	feature, _, _ := strings.Cut(strings.TrimPrefix(rel, "containers/"), "/")

	return feature
}

func architectureFeatureUI(path string) bool {
	return architectureFeature(path) != "" && (strings.Contains(path, "/ui/") || strings.HasSuffix(path, "/ui"))
}

func architectureTransport(path string) bool {
	rel := architectureInternal(path)
	for _, prefix := range []string{"web", "mcpserver", "desktop", "ship/httpx", "ship/mcpx", "ship/clix"} {
		if architectureWithin(rel, prefix) {
			return true
		}
	}

	return architectureFeatureUI(path)
}

func architectureInfrastructure(path string) bool {
	rel := architectureInternal(path)
	for _, prefix := range []string{"ship", "device", "devicehost", "deviceinfo", "fit", "ciq", "ciqstore", "prg", "gc", "hostfile", "hostexec", "paths", "notify"} {
		if architectureWithin(rel, prefix) {
			return true
		}
	}

	return false
}

func architectureCore(path string) bool {
	return !architectureTransport(path) && (architectureInfrastructure(path) || architectureFeature(path) != "" || architectureInternal(path) == "garmin")
}

func architectureImportViolation(from, to string) string {
	if architectureWithin(to, architectureModule+"/internal/app") && !architectureWithin(from, architectureModule+"/cmd") && !architectureWithin(from, architectureModule+"/internal/app") {
		return "only entry points and app wiring may import app"
	}

	if architectureInfrastructure(from) && (architectureFeature(to) != "" || architectureInternal(to) == "garmin" || architectureTransport(to) && !architectureWithin(architectureInternal(to), "ship")) {
		return "infrastructure cannot depend on features or server/native shells"
	}

	if architectureCore(from) {
		return architectureCoreImportViolation(from, to)
	}

	if architectureFeatureUI(from) && architectureFeature(to) != "" && architectureFeature(from) != architectureFeature(to) {
		return "a feature UI may import only its own feature"
	}

	return ""
}

func architectureCoreImportViolation(from, to string) string {
	if architectureTransport(to) {
		return "core cannot import transport adapters"
	}

	if architectureFeature(from) != "" && architectureInternal(to) == "garmin" {
		return "new feature core cannot import the legacy facade"
	}

	if !strings.HasPrefix(to, architectureModule+"/") && strings.Contains(strings.Split(to, "/")[0], ".") && !architectureApprovedLibrary(from, to) {
		return "third-party core dependency needs an explicit reviewed allowance"
	}

	return ""
}

func architectureApprovedLibrary(from, to string) bool {
	allow := map[string][]string{
		"device/mtp": {"github.com/google/gousb"},
		"ciqstore":   {"golang.org/x/net/html"},
		"gc":         {"golang.org/x/net/publicsuffix"},
		"hostfile":   {"golang.org/x/sys/windows"},
		"hostexec":   {"golang.org/x/sys/windows"},
	}
	for owner, libraries := range allow {
		if !architectureWithin(architectureInternal(from), owner) {
			continue
		}

		for _, prefix := range libraries {
			if architectureWithin(to, prefix) {
				return true
			}
		}
	}

	return false
}

func architectureHTTPRole(name string) bool {
	switch name {
	case "Handler", "HandlerFunc", "ResponseWriter", "ServeMux", "Server", "NewServeMux",
		"Handle", "HandleFunc", "ListenAndServe", "ListenAndServeTLS", "Serve", "ServeTLS", "ServeHTTP",
		"FileServer", "FileServerFS", "ServeFile", "ServeFileFS", "ServeContent", "Redirect", "Error",
		"NotFound", "NotFoundHandler", "StripPrefix", "TimeoutHandler", "MaxBytesHandler", "MaxBytesReader",
		"CrossOriginProtection", "NewCrossOriginProtection", "Shutdown", "RegisterOnShutdown":
		return true
	default:
		return false
	}
}

func architectureTypedViolations(owner string, info *types.Info, positions *token.FileSet) []string {
	var violations []string

	for id, object := range info.Uses {
		pkg := object.Pkg()
		if pkg == nil {
			continue
		}

		if pkg.Path() == architectureModule+"/internal/garmin" && object.Name() == "NewRuntime" && !architectureWithin(owner, architectureModule+"/internal/app") {
			violations = append(violations, fmt.Sprintf("%s: production runtime construction belongs to app", positions.Position(id.Pos())))
		}

		if architectureCore(owner) && pkg.Path() == "net/http" && architectureHTTPRole(object.Name()) {
			violations = append(violations, fmt.Sprintf("%s: HTTP server role %s in core", positions.Position(id.Pos()), object.Name()))
		}
	}

	return violations
}

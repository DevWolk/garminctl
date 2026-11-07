package architecture

import (
	"context"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"
)

const (
	architectureModule      = "github.com/DevWolk/garminctl"
	architectureCGODisabled = "CGO_ENABLED=0"
)

type architectureView struct {
	name  string
	tags  string
	env   []string
	tests bool
}

// architectureFeatureEdges declares the only feature-to-feature imports. A new
// edge needs review here; UIs still import only their own feature.
var architectureFeatureEdges = map[string][]string{
	"connect":  {"apps"},    // delivers queued app packages and invalidates the app registry
	"store":    {"apps"},    // marks installed store apps
	"updates":  {"apps"},    // reports installed app versions
	"workouts": {"connect"}, // returns the pulled workout file
}

// architectureOwner lists constructors of process-wide owners and the packages,
// beside the defining one, that may call them. Any other caller would create a
// second owner of the watch, its jobs or the account store.
type architectureOwner struct {
	pkg     string
	names   []string
	callers []string
}

const ownerApp = "internal/app"

var architectureOwners = []architectureOwner{
	{pkg: ownerApp + "/process", names: []string{"New"}, callers: []string{ownerApp}},
	{pkg: "internal/ship/watchsession", names: []string{"NewManager", "NewManagerWithGate"}, callers: []string{ownerApp}},
	{pkg: "internal/containers/connect", names: []string{"NewSyncCoordinator"}, callers: []string{ownerApp}},
	{pkg: "internal/containers/health", names: []string{"NewActivityCoordinator"}, callers: []string{ownerApp}},
	{pkg: "internal/gc", names: []string{"New"}, callers: []string{"cmd", ownerApp, "internal/testdump"}},
}

// architectureRoot finds the module above the working directory, which go test
// and the Windows runner set to the package directory; the source path recorded
// at build time does not exist where a cross-compiled test runs. Loading
// packages needs the go command, so a host without it skips the gate.
func architectureRoot(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("architecture gate needs the go command: %v", err)
	}

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("module root: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("module root: no go.mod above the working directory")
		}

		dir = parent
	}
}

func loadArchitecture(ctx context.Context, t *testing.T, view architectureView, mode packages.LoadMode, patterns ...string) []*packages.Package {
	t.Helper()

	cfg := &packages.Config{Context: ctx, Dir: architectureRoot(t), Mode: mode, Env: append(os.Environ(), view.env...), BuildFlags: []string{"-mod=readonly"}, Tests: view.tests}
	// Active views must inspect the same build as the running suite. Reusing its
	// export data also avoids compiling an uninstrumented desktop dependency tree.
	if architectureRaceEnabled && len(view.env) == 0 {
		cfg.BuildFlags = append(cfg.BuildFlags, "-race")
	}

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

	t.Logf("%s: %d packages", view.name, len(loaded))

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

	t.Run("tests", func(t *testing.T) {
		t.Parallel()

		// Test-only packages under tests/ have no production view.
		view := architectureView{name: "tests", tags: architectureBuildTags, tests: true}
		for _, pkg := range loadArchitecture(ctx, t, view, packages.NeedName|packages.NeedImports, append(patterns, "./tests/...")...) {
			checkArchitectureTestPackage(t, pkg)
		}
	})

	for _, view := range []architectureView{
		{name: "linux portable", env: []string{"GOOS=linux", architectureCGODisabled}},
		{name: "linux desktop", tags: "desktop,production,gtk3", env: []string{"GOOS=linux", "CGO_ENABLED=1"}},
		{name: "windows desktop", tags: "desktop,production", env: []string{"GOOS=windows", architectureCGODisabled}},
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

// checkArchitectureTestPackage applies the test rules to a package compiled
// with its test files; the generated test main is skipped.
func checkArchitectureTestPackage(t *testing.T, pkg *packages.Package) {
	t.Helper()

	if strings.HasSuffix(pkg.PkgPath, ".test") {
		return
	}

	var violations []string

	for imported := range pkg.Imports {
		if reason := architectureTestImportViolation(pkg.PkgPath, imported); reason != "" {
			violations = append(violations, pkg.ID+" -> "+imported+": "+reason)
		}
	}

	sort.Strings(violations)

	for _, issue := range violations {
		t.Error(issue)
	}
}

// architectureTestImportViolation keeps feature tests inside the production
// feature boundaries. Shell tests assemble their own fixture composition and
// are not checked here.
func architectureTestImportViolation(from, to string) string {
	feature := architectureFeature(strings.TrimSuffix(from, "_test"))
	if feature == "" {
		return ""
	}

	if architectureWithin(to, architectureModule+"/"+ownerApp) {
		return "feature tests cannot import app"
	}

	if other := architectureFeature(to); other != "" && other != feature && !architectureFeatureEdge(feature, other) {
		return "feature tests may import another feature only through a declared edge"
	}

	return ""
}

func architectureFeatureEdge(from, to string) bool {
	return slices.Contains(architectureFeatureEdges[from], to)
}

func architectureShell(path string) bool {
	rel := architectureInternal(path)

	return architectureWithin(rel, "web") || architectureWithin(rel, "mcpserver") || architectureWithin(rel, "desktop")
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
	return !architectureTransport(path) && (architectureInfrastructure(path) || architectureFeature(path) != "")
}

func architectureImportViolation(from, to string) string {
	if architectureWithin(to, architectureModule+"/"+ownerApp) && !architectureWithin(from, architectureModule+"/cmd") && !architectureWithin(from, architectureModule+"/"+ownerApp) {
		return "only entry points and app wiring may import app"
	}

	if architectureInfrastructure(from) && (architectureFeature(to) != "" || architectureTransport(to) && !architectureWithin(architectureInternal(to), "ship")) {
		return "infrastructure cannot depend on features or server/native shells"
	}

	if architectureShell(from) {
		return architectureShellImportViolation(from, to)
	}

	if architectureCore(from) {
		return architectureCoreImportViolation(from, to)
	}

	if architectureFeatureUI(from) {
		return architectureFeatureUIImportViolation(from, to)
	}

	return ""
}

// architectureShellImportViolation keeps features behind the registrars app
// supplies. The native shell reaches the server through its local backend.
func architectureShellImportViolation(from, to string) string {
	if architectureFeature(to) != "" {
		return "shells receive feature registrations from app"
	}

	if architectureWithin(architectureInternal(from), "desktop") && architectureShell(to) && !architectureWithin(architectureInternal(to), "desktop") {
		return "the native shell imports neither the web nor the MCP shell"
	}

	return ""
}

func architectureFeatureUIImportViolation(from, to string) string {
	if architectureShell(to) {
		return "a feature UI cannot import server or native shells"
	}

	if architectureFeature(to) != "" && architectureFeature(from) != architectureFeature(to) {
		return "a feature UI may import only its own feature"
	}

	return ""
}

func architectureCoreImportViolation(from, to string) string {
	if architectureTransport(to) {
		return "core cannot import transport adapters"
	}

	if feature, other := architectureFeature(from), architectureFeature(to); feature != "" && other != "" && other != feature && !architectureFeatureEdge(feature, other) {
		return "feature core may import another feature only through a declared edge"
	}

	if !strings.HasPrefix(to, architectureModule+"/") && strings.Contains(strings.Split(to, "/")[0], ".") && !architectureApprovedLibrary(from, to) {
		return "third-party core dependency needs an explicit reviewed allowance"
	}

	return ""
}

func architectureApprovedLibrary(from, to string) bool {
	allow := map[string][]string{
		"device/mtp":             {"github.com/google/gousb"},
		"ciqstore":               {"golang.org/x/net/html"},
		"gc":                     {"golang.org/x/net/publicsuffix"},
		"hostfile":               {"golang.org/x/sys/windows"},
		"hostexec":               {"golang.org/x/sys/windows"},
		"containers/application": {"golang.org/x/mod/semver"}, // pinned SemVer policy without the Wails runtime
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

// architectureOwnerViolation reports a call to an owner constructor outside
// the packages allowed to build that owner.
func architectureOwnerViolation(caller, pkg, name string) string {
	for _, owner := range architectureOwners {
		if pkg != architectureModule+"/"+owner.pkg || !slices.Contains(owner.names, name) || caller == pkg {
			continue
		}

		for _, allowed := range owner.callers {
			if architectureWithin(caller, architectureModule+"/"+allowed) {
				return ""
			}
		}

		return fmt.Sprintf("%s.%s builds a process-wide owner; only %s may call it", path.Base(owner.pkg), name, strings.Join(owner.callers, ", "))
	}

	return ""
}

func architectureTypedViolations(owner string, info *types.Info, positions *token.FileSet) []string {
	var violations []string

	for id, object := range info.Uses {
		pkg := object.Pkg()
		if pkg == nil {
			continue
		}

		if reason := architectureOwnerViolation(owner, pkg.Path(), object.Name()); reason != "" {
			violations = append(violations, fmt.Sprintf("%s: %s", positions.Position(id.Pos()), reason))
		}

		if architectureCore(owner) && pkg.Path() == "net/http" && architectureHTTPRole(object.Name()) {
			violations = append(violations, fmt.Sprintf("%s: HTTP server role %s in core", positions.Position(id.Pos()), object.Name()))
		}
	}

	return violations
}

package updatetransport

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRestartValidatesRealHeadlessChildAndMCP(t *testing.T) {
	for _, name := range []string{"ready", "mixed CLI", "mixed desktop", "mixed backend", "MCP failure"} {
		t.Run(name, func(t *testing.T) {
			cache, _, ticket := startupFixture(t)

			backendVersion, mcpVersion := startupTestVersion, startupTestVersion
			if name == "mixed backend" {
				backendVersion = "v0.9.0"
			}

			if name == "MCP failure" {
				mcpVersion = "v0.9.0"
			}

			protocol := mcp.NewServer(&mcp.Implementation{Name: "garminctl", Version: mcpVersion}, nil)
			mux := http.NewServeMux()
			mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return protocol }, &mcp.StreamableHTTPOptions{JSONResponse: true}))
			mux.HandleFunc("/api/server", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"application":"garminctl","desktopAPI":"1","version":"` + backendVersion + `"}`))
			})
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			t.Setenv("GARMINCTL_TEST_STARTUP_PROCESS", "1")
			t.Setenv("GARMINCTL_TEST_STARTUP_CACHE", cache)
			t.Setenv("GARMINCTL_TEST_STARTUP_BACKEND", server.URL)
			t.Setenv("GARMINCTL_TEST_STARTUP_VERSION", startupTestVersion)
			t.Setenv("GARMINCTL_TEST_STARTUP_CLI_VERSION", startupTestVersion)

			if name == "mixed CLI" {
				t.Setenv("GARMINCTL_TEST_STARTUP_CLI_VERSION", "v0.9.0")
			}

			if name == "mixed desktop" {
				t.Setenv("GARMINCTL_TEST_STARTUP_VERSION", "v0.9.0")
			}

			source, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}

			root := t.TempDir()

			options := InstallOptions{CLI: filepath.Join(root, "garminctl"), Desktop: filepath.Join(root, "garmin-desktop")}

			if runtime.GOOS == platformWindows {
				suffix := filepath.Ext(source)
				options.CLI += suffix
				options.Desktop += suffix
			}

			for _, target := range []string{options.CLI, options.Desktop} {
				if err := copyStageCLI(t.Context(), source, target); err != nil {
					t.Fatal(err)
				}
			}

			err = restartInstalledDesktop(t.Context(), options, startupTestVersion, filepath.Join(cache, ticket.Directory), mcpx.ProbeServer)
			if (err == nil) != (name == "ready") {
				t.Fatalf("restart validation=%v", err)
			}
		})
	}
}

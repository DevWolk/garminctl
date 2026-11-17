package updatetransport

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// Relative restart arguments such as -root DIR keep their meaning only in the
// directory the application was started from.
func TestRestartedDesktopKeepsTheOriginalWorkingDirectory(t *testing.T) {
	t.Parallel()

	installation, original := t.TempDir(), t.TempDir()
	options := InstallOptions{Desktop: filepath.Join(installation, "garmin-desktop"), Args: []string{"-root", "dump"}, Dir: original}

	command := desktopCommand(options, "ticket")
	if command.Dir != original || command.Path != options.Desktop || len(command.Args) != 3 || command.Args[2] != "dump" {
		t.Fatalf("restart command dir=%q path=%q args=%v", command.Dir, command.Path, command.Args)
	}

	for _, unusable := range []string{"", filepath.Join(original, "removed"), options.Desktop} {
		options.Dir = unusable
		if got := desktopCommand(options, "ticket").Dir; got != installation {
			t.Fatalf("working directory for %q=%q, want the installation directory", unusable, got)
		}
	}
}

func TestRestartedDesktopReceivesOnlyItsOwnStartupTicket(t *testing.T) {
	t.Setenv(StartupEnvironment, "ticket of an earlier restart")

	command := desktopCommand(InstallOptions{Desktop: filepath.Join(t.TempDir(), "garmin-desktop")}, "ticket")

	var tickets []string

	for _, entry := range command.Env {
		if value, ok := strings.CutPrefix(entry, StartupEnvironment+"="); ok {
			tickets = append(tickets, value)
		}
	}

	if len(tickets) != 1 || tickets[0] != "ticket" {
		t.Fatalf("startup tickets=%v", tickets)
	}
}

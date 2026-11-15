package app

import (
	"os"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func TestApplicationStartupTicketCannotLeakToChildProcesses(t *testing.T) {
	t.Setenv(updatetransport.StartupEnvironment, "invalid ticket")

	if _, err := ApplicationStartupNotice("v1.2.3"); err == nil {
		t.Fatal("invalid startup ticket accepted")
	}

	if _, ok := os.LookupEnv(updatetransport.StartupEnvironment); ok {
		t.Fatal("startup ticket remained inheritable")
	}

	notice, err := ApplicationStartupNotice("v1.2.3")
	if err != nil || notice != nil {
		t.Fatalf("normal startup created a notice: %v", err)
	}
}

func TestApplicationStartupMCPProbeUsesExistingRuntimeWithoutWatch(t *testing.T) { //nolint:paralleltest // The fixture owns config/cache environment and the real HTTP/MCP runtime.
	f := serveApplicationBackend(t)
	if err := mcpx.ProbeServer(t.Context(), f.address+"/mcp", "v1.2.3"); err != nil {
		t.Fatal(err)
	}

	if f.runtime.Manager().Current() != nil {
		t.Fatal("startup MCP probe opened a watch")
	}
}

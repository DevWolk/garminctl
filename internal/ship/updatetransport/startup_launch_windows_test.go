package updatetransport

import (
	"path/filepath"
	"testing"
)

// A hidden-window start request overrides the first ShowWindow call of the
// started program, so the restarted application would stay invisible.
func TestRestartedDesktopIsNotStartedHidden(t *testing.T) {
	t.Parallel()

	command := desktopCommand(InstallOptions{Desktop: filepath.Join(t.TempDir(), "garmin-desktop.exe")}, "ticket")
	if attributes := command.SysProcAttr; attributes != nil && (attributes.HideWindow || attributes.CreationFlags != 0) {
		t.Fatalf("desktop start attributes=%+v", attributes)
	}
}

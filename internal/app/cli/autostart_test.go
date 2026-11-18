package cli

import (
	"strings"
	"testing"
)

func TestAutostartScriptStopsAtTheFirstFailure(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"status", "off", "on"} {
		script := autostartScript(mode, `C:\Program Files\garminctl\garminctl.exe`)
		if !strings.HasPrefix(script, "$ErrorActionPreference = 'Stop'; ") {
			t.Errorf("%s: a failed step would not stop the script: %s", mode, script)
		}
	}

	on := autostartScript("on", `C:\it's\garminctl.exe`)
	if !strings.Contains(on, `Copy-Item -Force 'C:\it''s\garminctl.exe'`) {
		t.Errorf("source path is not quoted for PowerShell: %s", on)
	}

	if copyAt, shortcutAt := strings.Index(on, "Copy-Item"), strings.Index(on, "CreateShortcut"); copyAt < 0 || shortcutAt < copyAt {
		t.Errorf("the shortcut must be written only after the copy: %s", on)
	}
}

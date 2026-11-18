package cli

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDesktopPassesRootAndDeviceToTheApp(t *testing.T) {
	t.Parallel()

	c := &cli{root: "dump", devID: `\\?\usb#vid_091e&pid_4cda#1#{6ac27878}`}

	args, err := c.desktopArgs(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	abs, err := filepath.Abs("dump")
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{rootFlag, abs, "-device", c.devID}; !slices.Equal(args, want) {
		t.Fatalf("args = %q, want %q", args, want)
	}

	args, err = c.desktopArgs(t.Context(), func(_ context.Context, path string) (string, error) {
		return `\\wsl.localhost\Ubuntu` + strings.ReplaceAll(path, "/", `\`), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(args[1], `\\wsl.localhost\Ubuntu`) {
		t.Fatalf("root was not converted for Windows: %q", args[1])
	}

	if args, err := new(cli).desktopArgs(t.Context(), nil); err != nil || len(args) != 0 {
		t.Fatalf("no options gave %q, %v", args, err)
	}
}

func TestDesktopLaunchScriptQuotesEachArgument(t *testing.T) {
	t.Parallel()

	if script := desktopLaunchScript(nil); !strings.HasSuffix(script, "Start-Process -FilePath $app") {
		t.Fatalf("plain launch changed: %s", script)
	}

	script := desktopLaunchScript([]string{rootFlag, `C:\Users\O'Neil\watch dump\`})
	if want := ` -ArgumentList @('"-root"', '"C:\Users\O''Neil\watch dump\\"')`; !strings.HasSuffix(script, want) {
		t.Fatalf("script %s\nwant suffix %s", script, want)
	}

	for arg, want := range map[string]string{
		`C:\`:                    `"C:\\"`,
		`a"b`:                    `"a\"b"`,
		`a\"b`:                   `"a\\\"b"`,
		`\\wsl.localhost\Ubuntu`: `"\\wsl.localhost\Ubuntu"`,
		``:                       `""`,
	} {
		if got := windowsCommandLineArg(arg); got != want {
			t.Errorf("windowsCommandLineArg(%q) = %s, want %s", arg, got, want)
		}
	}
}

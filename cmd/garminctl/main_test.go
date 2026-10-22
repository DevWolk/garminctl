package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

const rootFlag = "-root"

func TestRun_ListsADump(t *testing.T) {
	t.Parallel()

	root := testdump.Root(t)

	var out, errOut bytes.Buffer
	if err := run([]string{rootFlag, root, "-json", "ls", "GARMIN"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v (stderr %q)", err, errOut.String())
	}

	var entries []device.Entry
	if err := json.Unmarshal(out.Bytes(), &entries); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}

	if !strings.Contains(strings.Join(names, ","), "GarminDevice.xml") {
		t.Fatalf("ls GARMIN = %v", names)
	}

	if errOut.Len() != 0 {
		t.Fatalf("unexpected stderr output: %q", errOut.String())
	}
}

func TestRun_InfoPrintsTheWatch(t *testing.T) {
	t.Parallel()

	root := testdump.Root(t)

	var out, errOut bytes.Buffer
	if err := run([]string{rootFlag, root, "info"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "Unit ID:") {
		t.Fatalf("info output: %q", out.String())
	}
}

func TestRun_Version(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run([]string{"version"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(out.String(), "garminctl ") {
		t.Fatalf("version output: %q", out.String())
	}
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	root := testdump.Root(t)

	tests := []struct {
		name     string
		args     []string
		expected int
	}{
		{name: "no command", args: nil, expected: 2},
		{name: "unknown command", args: []string{"bogus"}, expected: 2},
		{name: "missing argument", args: []string{rootFlag, root, "cat"}, expected: 2},
		{name: "bad flag", args: []string{rootFlag, root, "activities", "-bogus"}, expected: 2},
		{name: "help", args: []string{"-h"}, expected: 0},
		{name: "subcommand help", args: []string{rootFlag, root, "cat", "-h"}, expected: 0},
		{name: "missing file", args: []string{rootFlag, root, "cat", "GARMIN/nope.fit"}, expected: 1},
		{name: "ok", args: []string{rootFlag, root, "ls"}, expected: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out, errOut bytes.Buffer

			err := run(tt.args, &out, &errOut)

			if got := exitCode(err); got != tt.expected {
				t.Fatalf("exitCode(%v) = %d, want %d (stderr %q)", err, got, tt.expected, errOut.String())
			}
		})
	}

	if exitCode(context.Canceled) != 130 {
		t.Fatal("an interrupted run should exit with 130")
	}

	if !errors.Is(parseFlags(flag.NewFlagSet("x", flag.ContinueOnError), []string{"-h"}), flag.ErrHelp) {
		t.Fatal("-h must surface flag.ErrHelp")
	}
}

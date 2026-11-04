package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/testdump"
)

const infoCommand = "info"

func TestStoreCLIContracts(t *testing.T) { //nolint:paralleltest // fixture replaces the process store client
	for _, test := range []struct { //nolint:paralleltest // cases replace the process store client
		name string
		args []string
		json bool
		fail string
		hash string
	}{
		{name: "list text", args: []string{"list"}, hash: "11c8a4234943ac40bdc430bd78fad50fd9158231937378346dbcae68791c8f43"},
		{name: "list JSON", args: []string{"list"}, json: true, hash: "9471fba5d0a829822300e62ba865694cee01db262783e34677fe6dda606150e2"},
		{name: "search text", args: []string{"search", "world", "face", "-page", "2"}, hash: "1fcb6ebd77c7e80cda7da6770d2b5555d11a4e35af66e66fe5a512af6eea86e4"},
		{name: "filtered search JSON", args: []string{"search", "face", "-type", "watchface", "-page", "2"}, json: true, hash: "c31f825c3fa176bf66a6cd24fe32de4bb1e29cf57191ec46e8bc561b3ea21e24"},
		{name: "info text", args: []string{infoCommand, testdump.StoreAppID}, hash: "b315bde6361af9b927f0b02e4b8fe9e55f858390e519751f2d04525ab9d687a1"},
		{name: "info JSON", args: []string{infoCommand, testdump.StoreAppID}, json: true, hash: "9cd5231e8736c18c99a2f9f4811ad2bf9123b854c2eee0824372c4d2fc681366"},
		{name: "reviews unavailable", args: []string{infoCommand, testdump.StoreAppID}, json: true, fail: "/apps/" + testdump.StoreAppID + "/reviews", hash: "09339850b2fe8c587a074d20d56fb3e5be1ebacabb0207ce875428341826a6b3"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := testdump.StoreEnvironment(t, test.fail)
			root := testdump.Root(t)

			argv := []string{rootFlag, root}
			if test.json {
				argv = append(argv, "-json")
			}

			argv = append(append(argv, "store"), test.args...)

			var out, diagnostics bytes.Buffer

			if err := run(argv, &out, &diagnostics); err != nil || diagnostics.Len() != 0 {
				t.Fatalf("error=%v diagnostics=%s", err, &diagnostics)
			}

			if test.args[0] != infoCommand {
				offset := 0
				if test.args[0] == "search" {
					offset = 48
				}

				fixture.CheckPage(t, 24, offset)
			}

			testdump.CheckDigest(t, out.Bytes(), test.hash)
		})
	}
}

func TestStoreCLIRequiresDeviceXML(t *testing.T) { //nolint:paralleltest // fixture replaces the process store client
	fixture := testdump.StoreEnvironment(t, "")

	root := testdump.Root(t)
	if err := os.Remove(filepath.Join(root, "GARMIN", "GarminDevice.xml")); err != nil {
		t.Fatal(err)
	}

	var out, diagnostics bytes.Buffer

	err := run([]string{rootFlag, root, "store", "list"}, &out, &diagnostics)
	if err == nil || !strings.Contains(err.Error(), "GarminDevice.xml") || out.Len() != 0 || fixture.Count() != 0 {
		t.Fatalf("error=%v output=%s requests=%d", err, &out, fixture.Count())
	}
}

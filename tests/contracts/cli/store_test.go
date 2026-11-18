package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/testdump"
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
		{name: "list text", args: []string{"list"}, hash: "4c6d0cd3339da318a4967e0296a81189dea76e2d931898a9e7bda2aa723c2dfc"},
		{name: "list JSON", args: []string{"list"}, json: true, hash: "c251be60081287c65c9aa4ced047f73a86575900b769012daecca841f3273145"},
		{name: "search text", args: []string{"search", "world", "face", "-page", "2"}, hash: "3098018aa21501eb8c611b22f0f34c5f19ff54bff545c8c57aca9347eb1f8da9"},
		{name: "filtered search JSON", args: []string{"search", "face", "-type", "watchface", "-page", "2"}, json: true, hash: "08243b9512b86a106aa94c90fa09398263aa890a2ecd375d05930f0776061721"},
		{name: "info text", args: []string{infoCommand, testdump.StoreAppID}, hash: "a912632ba504c057bd78c536621a7e633a551a0e6ebe91be307354e0322ae990"},
		{name: "info JSON", args: []string{infoCommand, testdump.StoreAppID}, json: true, hash: "6f409d9ee4953331416db450f344af877770f0abb999031108b5dc000cb35b0d"},
		{name: "reviews unavailable", args: []string{infoCommand, testdump.StoreAppID}, json: true, fail: "/apps/" + testdump.StoreAppID + "/reviews", hash: "748938747c5a7d3be4ee3a036646b699b3027a7e26840957084d2cdda0ffad10"},
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

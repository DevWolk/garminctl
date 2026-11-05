package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/updates"
	"github.com/DevWolk/garminctl/internal/testdump"
)

const updatesCommand = "updates"

func TestUpdatesCLIContracts(t *testing.T) { //nolint:paralleltest // replaces process HTTP clients
	for _, test := range []struct { //nolint:paralleltest // cases replace process HTTP clients
		name, command, mode string
		args                []string
		json                bool
		exit                int
		hash                string
	}{
		{name: "check text", command: updatesCommand, hash: "8bf96d123c6428b1d1ad7b5ca8cdc2be3aa2393a2c8f8dde529f0903224b7b42"},
		{name: "check JSON", command: updatesCommand, json: true, hash: "eaf351390f59a70f49bafd761c7b3643829a89a67f698de4abc90a4b0ee81e8f"},
		{name: "software failure JSON", command: updatesCommand, mode: "software", json: true, exit: 1, hash: "eb01ea645625ba03e372cbfb129f7071688941024f673eeda8b7b58f183a29e9"},
		{name: "app failure JSON", command: updatesCommand, mode: "app", json: true, exit: 1, hash: "de10dc0124bf18aec26c1d7586889def963a4cc81000dabbb3d035bab6a25c49"},
		{name: "install text", command: updatesCommand, args: []string{"install", testdump.UpdatePart}, hash: "e1347be7f701f67cf7266aba24c7cd6c4cb109507bc15fd687e584cefea6ae45"},
		{name: "install JSON", command: updatesCommand, args: []string{"install", testdump.UpdatePart}, json: true, hash: "f0307348f6689e3f3d352df59e45bdc3ec021b45a746b415c07b413656953de8"},
		{name: "inventory text", command: "software", hash: "a239846cf50c52f54bd966f3bc5e84ab580276ab53f2f7043421909ee1b8dd45"},
		{name: "inventory JSON", command: "software", json: true, hash: "1ec62acbde9549bb40340db8662bd6d4cb88506313f26c17f2b1fde1c2fde666"},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := updates.HTTPClient
			updates.HTTPClient = testdump.UpdateEnvironment(t, test.mode)
			t.Cleanup(func() { updates.HTTPClient = previous })
			root := testdump.UpdateRoot(t)

			args := []string{rootFlag, root}
			if test.json {
				args = append(args, "-json")
			}

			args = append(append(args, test.command), test.args...)

			var output, diagnostics bytes.Buffer

			err := run(args, &output, &diagnostics)
			if exitCode(err) != test.exit || diagnostics.Len() != 0 {
				t.Fatalf("exit=%d error=%v diagnostics=%s", exitCode(err), err, &diagnostics)
			}

			if len(test.args) != 0 {
				data, err := os.ReadFile(filepath.Join(root, "GARMIN", "RemoteSW", "GUPDATE.GCD")) //nolint:gosec // fixed path under testdump.Root's temporary directory
				if err != nil || string(data) != testdump.UpdatePayload {
					t.Fatalf("staged data=%q error=%v", data, err)
				}
			}

			testdump.CheckDigest(t, output.Bytes(), test.hash)
		})
	}
}

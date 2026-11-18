package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/updates"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
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
		{name: "check text", command: updatesCommand, hash: "045c1cd7db8a7b48ec62a2658363a7b9429d058180b02a9c7b5272b8bb27a515"},
		{name: "check JSON", command: updatesCommand, json: true, hash: "5c19662e94af69c1d5053e2df450d98ee41f5af300fd93a1ac446bda4e180c0b"},
		{name: "software failure JSON", command: updatesCommand, mode: "software", json: true, exit: 1, hash: "8bf96dfef9f5edc69e49ca7de4083dccd7011fc387f2f44079aba4c9cac85825"},
		{name: "app failure JSON", command: updatesCommand, mode: "app", json: true, exit: 1, hash: "27593a0a26134125b5771e1f39431cabd4ae5a6ee7ef704631fe605dd7d66d83"},
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

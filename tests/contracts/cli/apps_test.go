package cli

import (
	"bytes"
	"testing"

	"github.com/DevWolk/garminctl/internal/testdump"
)

const appsSettingsCommand = "app-settings"

func TestAppsCLIContracts(t *testing.T) { //nolint:paralleltest // isolates process schema client and cache environment
	testdump.AppEnvironment(t)

	for _, test := range []struct { //nolint:paralleltest // cases share the process schema client and cache environment
		name, cmd string
		args      []string
		json      bool
		hash      string
	}{
		{name: "list text", cmd: "apps", hash: "2e9b23f6d6a1911c385b7f97c07b8c41f5ebacf0b24cbb2da141fc9706902635"},
		{name: "list JSON", cmd: "apps", json: true, hash: "dff319c7bb522ac24e1ff4a4c82a718e92b421cc124e7cfc828a608fefb0d242"},
		{name: "pending text", cmd: "app-pending", hash: "7d12575ee0d9c266658e7b48f8538764f35e6eb4318caab02c113dcaaa410f84"},
		{name: "pending JSON", cmd: "app-pending", json: true, hash: "37517e5f3dc66819f61f5a7bb8ace1921282415f10551d2defa5c3eb0985b570"},
		{name: "settings text", cmd: appsSettingsCommand, args: []string{testdump.AppRef}, hash: "688c4b910cd92e9eb6b051eea183c8ea575255d816430ef3ca5ad1e36c9fa099"},
		{name: "settings JSON", cmd: appsSettingsCommand, args: []string{testdump.AppRef}, json: true, hash: "2de1d2dc716317dd1f3a299950d9e8bad2669b601eee4545c31aed4515599e46"},
		{name: "schema text", cmd: "app-schema", args: []string{testdump.AppRef}, hash: "04b9f959168e9a8bc588e9222f4afc7ce03c0028da84b3269e96fa65b9e51b10"},
		{name: "schema JSON", cmd: "app-schema", args: []string{testdump.AppRef}, json: true, hash: "467b5594597f3a3735e58a62c1dc56e7cb7e6239653be452c29e6b8aab9f96eb"},
		{name: "set text", cmd: appsSettingsCommand, args: []string{"set", testdump.AppRef, "Mode=Night"}, hash: "f853dbbc140fb9a782101b688619c195a208cd0e3b0055f604ae697c248d8cee"},
		{name: "set JSON", cmd: appsSettingsCommand, args: []string{"set", testdump.AppRef, "Mode=Night"}, json: true, hash: "808f85081e709c00494a3dde9c1d23196770e5a2b11ae047018d0d6a465942e6"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dev, root := testdump.Apps(t)
			if err := dev.Close(); err != nil {
				t.Fatal(err)
			}

			var out, diagnostics bytes.Buffer

			argv := []string{rootFlag, root}
			if test.json {
				argv = append(argv, "-json")
			}

			argv = append(argv, test.cmd)

			argv = append(argv, test.args...)
			if err := run(argv, &out, &diagnostics); err != nil || diagnostics.Len() != 0 {
				t.Fatalf("command error=%v diagnostics=%s", err, &diagnostics)
			}

			testdump.CheckDigest(t, out.Bytes(), test.hash)
		})
	}
}

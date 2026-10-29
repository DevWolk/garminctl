package main

import (
	"bytes"
	"testing"

	"github.com/DevWolk/garminctl/internal/garmin"
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
		{name: "list text", cmd: "apps", hash: "3e9c82a2680c5ec3c9a62feb561e6b1b444ac402aa2b4df0921d00cf85f0dbc0"},
		{name: "list JSON", cmd: "apps", json: true, hash: "162c967e588dd15d4a005a43e35ee404ca62e1364f90d6ddfed6bb5fd119523d"},
		{name: "pending text", cmd: "app-pending", hash: "7d12575ee0d9c266658e7b48f8538764f35e6eb4318caab02c113dcaaa410f84"},
		{name: "pending JSON", cmd: "app-pending", json: true, hash: "37517e5f3dc66819f61f5a7bb8ace1921282415f10551d2defa5c3eb0985b570"},
		{name: "settings text", cmd: appsSettingsCommand, args: []string{testdump.AppRef}, hash: "0a71399f22fb5ac06dcf2ebc9d7a9c3d92463833e2b37d17f5e94cc19e0e1f5b"},
		{name: "settings JSON", cmd: appsSettingsCommand, args: []string{testdump.AppRef}, json: true, hash: "aff10c42a3b5357ee655c382a56e16c3a0e6cf56b2a525dc7a9b1ff47561b5fe"},
		{name: "schema text", cmd: "app-schema", args: []string{testdump.AppRef}, hash: "04b9f959168e9a8bc588e9222f4afc7ce03c0028da84b3269e96fa65b9e51b10"},
		{name: "schema JSON", cmd: "app-schema", args: []string{testdump.AppRef}, json: true, hash: "4e98620ab6c3b86ae1e998e0031249b58c58d2581ffd0ca8a8b4581f72c57259"},
		{name: "set text", cmd: appsSettingsCommand, args: []string{"set", testdump.AppRef, "Mode=Night"}, hash: "06b5e37152d4c20e4fd2233bcbf0f356b577f655c295210ded80f672689f8d18"},
		{name: "set JSON", cmd: appsSettingsCommand, args: []string{"set", testdump.AppRef, "Mode=Night"}, json: true, hash: "424037d3339e9372188b5ab87b8781d36aaccb09344d784b927d4e831850e831"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dev, _ := testdump.Apps(t)
			svc := garmin.New(dev)

			t.Cleanup(func() { _ = svc.Close() })

			var out, diagnostics bytes.Buffer

			c := &cli{asJSON: test.json, out: newPrinter(&out), errOut: newPrinter(&diagnostics)}
			if err := c.deviceCommands()[test.cmd](t.Context(), svc, test.args); err != nil || diagnostics.Len() != 0 {
				t.Fatalf("command error=%v diagnostics=%s", err, &diagnostics)
			}

			testdump.CheckDigest(t, out.Bytes(), test.hash)
		})
	}
}

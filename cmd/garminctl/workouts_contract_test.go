package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/testdump"
)

const (
	workoutCreateCommand = "create"
	workoutSendCommand   = "send"
)

func TestWorkoutsCLIContracts(t *testing.T) { //nolint:paralleltest // isolates the account path and process HTTP transport
	for _, test := range []struct { //nolint:paralleltest // subtests replace process account paths and HTTP transport
		name                  string
		args                  []string
		mode                  string
		json, file, normalize bool
		code                  int
		hash                  string
	}{
		{name: "default workout text", hash: "a20ad7ab1bebb618e9e730764f1dc39475da814ef82a8fd5a6e303d1d0fc51ba"},
		{name: "workout JSON", json: true, hash: "95f225a733dddeddda12f2401cbabf7f2a3f17173800911f56c14693ad1d8d03"},
		{name: "empty JSON", json: true, mode: "empty", hash: "37517e5f3dc66819f61f5a7bb8ace1921282415f10551d2defa5c3eb0985b570"},
		{name: workoutCreateCommand, args: []string{workoutCreateCommand}, file: true, hash: "3de95b6ccf6782b11ed6b2d5655f267a023f99d380638bc4af8698884cd21b24"},
		{name: "create JSON flag", args: []string{workoutCreateCommand}, file: true, json: true, hash: "3de95b6ccf6782b11ed6b2d5655f267a023f99d380638bc4af8698884cd21b24"},
		{name: "local", args: []string{workoutCreateCommand, "-local", "-send"}, file: true, normalize: true, hash: "fa63f4701601d9a237d56809f3cfc3e5b1fc3db5c7a9ce4b8a2f09065480cfa5"},
		{name: "create and send", args: []string{workoutCreateCommand, "-send"}, file: true, normalize: true, hash: "ad8c9e800c0f8511847ab98f1a533b51aa30d1171b4375eb57ed9d229a1d8605"},
		{name: "partial create", args: []string{workoutCreateCommand, "-send"}, file: true, mode: "queue failure", code: 1, hash: "e6605ba52ee0061d4464f7665ff751ba73dcd1fa33160ed384364179eafc536f"},
		{name: workoutSendCommand, args: []string{workoutSendCommand, "42"}, normalize: true, hash: "0b94d4ec570b2e28299a91798415d6ba46138738d97b1c9853c2186bbfe0fdda"},
		{name: "missing send id", args: []string{workoutSendCommand}, code: 2, hash: "85836e234740b2d056e2c411517e8cd4fd3501ba70e6cf2c37621586bc9f3ae9"},
		{name: "invalid id", args: []string{workoutSendCommand, "x"}, code: 1, hash: "d1e6fece5b7a110343fb38259666069bdd0a7ae41695ba96128c5f3c6d1e0995"},
		{name: "unknown", args: []string{"other"}, code: 2, hash: "c24c718b0cb6e40824b1ab9e68c06aa16fb1d1e88ac1321e06c0ba0e77d16758"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testdump.WorkoutCLIEnvironment(t, test.mode)
			root := testdump.WorkoutRoot(t)

			args := append([]string(nil), test.args...)
			if test.file {
				p := filepath.Join(t.TempDir(), "workout.json")
				if err := os.WriteFile(p, []byte(testdump.WorkoutSpecJSON), 0o600); err != nil {
					t.Fatal(err)
				}

				args = append(args, p)
			}

			argv := []string{rootFlag, root}
			if test.json {
				argv = append(argv, "-json")
			}

			argv = append(append(argv, "workouts"), args...)

			var output, diagnostics bytes.Buffer

			err := run(argv, &output, &diagnostics)
			if exitCode(err) != test.code || diagnostics.Len() != 0 {
				t.Fatalf("code=%d error=%v diagnostics=%s", exitCode(err), err, &diagnostics)
			}

			data := output.Bytes()
			if err != nil {
				data = append(data, []byte("error: "+err.Error())...)
			}

			if test.normalize {
				data = testdump.NormalizePendingPath(t, data)
			}

			data = []byte(strings.ReplaceAll(string(data), root, "<root>"))
			testdump.CheckDigest(t, data, test.hash)
		})
	}
}

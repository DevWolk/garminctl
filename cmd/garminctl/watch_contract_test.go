package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
)

const (
	watchStorageDir = "GARMIN"
	watchCatCommand = "cat"
)

func TestWatchCLIReadContracts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		cmd  string
		args []string
		json bool
		hash string
	}{
		{name: "info text", cmd: infoCommand, hash: "7033653ffdcc3a3f4cefb05c2768dec281e4e40faedbff7d83043e72c0c25a10"},
		{name: "info JSON", cmd: infoCommand, json: true, hash: "98bfabe17c291b0667cd3b7c8ea5416cd9ea5ba5f422e358ec40a13a19bb724e"},
		{name: "list text", cmd: "ls", args: []string{watchStorageDir}, hash: "0b3dac477c24cf48be105d6d439ee37637a984434b7b6019b327cb6854a5d951"},
		{name: "list JSON", cmd: "ls", args: []string{watchStorageDir}, json: true, hash: "76d8f64ba5adf0ad11de9029d1ba41718d7a850d27e4690e0d13329b477a405c"},
		{name: "decode text", cmd: "decode", args: []string{watchsession.PathSettings}, hash: "20612928fa469d61e7ea236d38bb93903ce2bcea68f8bb21a457ed32c37d54ab"},
		{name: "decode JSON", cmd: "decode", args: []string{watchsession.PathSettings}, json: true, hash: "12428aa2bd441bc4296e0f9253d04241069955c77886f9e8d9762cd6ac83d32e"},
		{name: "cat text", cmd: watchCatCommand, args: []string{"GARMIN/notes.txt"}, hash: "52f000e01e486d4b4caca23e88c4a003305ba77b294fd563237f1012a3451ad3"},
		{name: "cat binary", cmd: watchCatCommand, args: []string{"GARMIN/sample.bin"}, hash: "068a9db2aec30c1c6f182c6c9e6ed8f34b8b6e1d303e9918a29534d481bef126"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dev, _ := testdump.Watch(t)
			sess := watchsession.New(dev)

			t.Cleanup(func() { _ = sess.Close() })

			var out, diagnostics bytes.Buffer

			c := &cli{asJSON: test.json, out: newPrinter(&out), errOut: newPrinter(&diagnostics)}
			if err := c.sessionCommands()[test.cmd](t.Context(), sess, test.args); err != nil || diagnostics.Len() != 0 {
				t.Fatalf("command error=%v diagnostics=%s", err, &diagnostics)
			}

			testdump.CheckDigest(t, out.Bytes(), test.hash)
		})
	}
}

func TestWatchCLIBackupContracts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		json bool
		hash string
	}{
		{name: "text", hash: "66b7c272d2939d9a8d387ff97baacb16a98f4c40568904c9b1bb91500aaffb8e"},
		{name: "JSON flag", json: true, hash: "1aa7b1639fd5fa858e9388546b841711b24318a593bb79270751df669fe97f33"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dev, _ := testdump.Watch(t)
			runtime := app.NewRuntime(t.Context(), func(context.Context) (device.Device, error) { return dev, nil })
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 3*time.Second)
				defer cancel()

				if err := runtime.Close(ctx); err != nil {
					t.Error(err)
				}
			})

			lease, err := runtime.Watch.Manager().Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			epoch := lease.Epoch()
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}

			dest := t.TempDir()

			var out, diagnostics bytes.Buffer

			c := &cli{runtime: runtime, epoch: epoch, asJSON: test.json, out: newPrinter(&out), errOut: newPrinter(&diagnostics)}
			if err := c.runDeviceJob(t.Context(), "backup", c.backup, []string{dest}); err != nil || diagnostics.Len() != 0 {
				t.Fatalf("backup error=%v diagnostics=%s", err, &diagnostics)
			}

			data := []byte(strings.ReplaceAll(out.String(), dest, "<backup>"))
			testdump.CheckDigest(t, data, test.hash)

			for _, path := range []string{"Music/song.mp3", "Podcasts/episode.mp3", "Audiobooks/chapter.mp3"} {
				if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(path))); !os.IsNotExist(err) {
					t.Fatalf("default skip changed for %s: %v", path, err)
				}
			}

			backup, err := os.OpenRoot(dest)
			if err != nil {
				t.Fatal(err)
			}
			defer backup.Close()

			backupData, err := backup.ReadFile("GARMIN/notes.txt")
			if err != nil || string(backupData) != "synthetic text\n" {
				t.Fatalf("backup text=%q error=%v", backupData, err)
			}
		})
	}
}

func TestWatchCLIMutationsAndExportPreserveBytes(t *testing.T) {
	t.Parallel()

	dev, _ := testdump.Watch(t)
	sess := watchsession.New(dev)

	t.Cleanup(func() { _ = sess.Close() })

	var out, diagnostics bytes.Buffer

	c := &cli{out: newPrinter(&out), errOut: newPrinter(&diagnostics)}
	input, exported := filepath.Join(t.TempDir(), "input"), filepath.Join(t.TempDir(), "exported")

	data := []byte{0, 0xff, 0x7f, 1}
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		cmd  string
		args []string
	}{
		{cmd: "mkdir", args: []string{"GARMIN/export"}},
		{cmd: "put", args: []string{input, "GARMIN/export/file.bin"}},
		{cmd: watchCatCommand, args: []string{"-o", exported, "GARMIN/export/file.bin"}},
		{cmd: "rm", args: []string{"GARMIN/export/file.bin"}},
	} {
		if err := c.sessionCommands()[test.cmd](context.Background(), sess, test.args); err != nil {
			t.Fatalf("%s: %v", test.cmd, err)
		}
	}

	export, err := os.OpenRoot(filepath.Dir(exported))
	if err != nil {
		t.Fatal(err)
	}
	defer export.Close()

	back, err := export.ReadFile("exported")
	if err != nil || !bytes.Equal(back, data) {
		t.Fatalf("export changed bytes=%x error=%v", back, err)
	}

	if out.String() != "wrote 4 bytes to GARMIN/export/file.bin\n" || diagnostics.Len() != 0 {
		t.Fatalf("mutation output=%q diagnostics=%s", &out, &diagnostics)
	}
}

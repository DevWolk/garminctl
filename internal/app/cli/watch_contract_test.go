package cli

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
	"github.com/DevWolk/garminctl/internal/ship/clix"
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
		{name: "info text", cmd: infoCommand, hash: "a45243bedada3754fd4008c80fd8c38e6b84a821b0f35b17b1d6bef262e60612"},
		{name: "info JSON", cmd: infoCommand, json: true, hash: "918027973413c7e47f03fd95185965d53962ddaa40b1611df83f7555fe583c29"},
		{name: "list text", cmd: "ls", args: []string{watchStorageDir}, hash: "3c9fda9c6036f1cf1e381855c971c8c3f8a87404d2c0cadb2e61c63789954fd6"},
		{name: "list JSON", cmd: "ls", args: []string{watchStorageDir}, json: true, hash: "f68106f20d987560fcc0269b013fc907aca8ff790a2a6b97a8f002a14132b1f8"},
		{name: "decode text", cmd: "decode", args: []string{watchsession.PathSettings}, hash: "775cafea06677caec6e73fd15356ae14694071f34d9e9341b1cfb1b92e157317"},
		{name: "decode JSON", cmd: "decode", args: []string{watchsession.PathSettings}, json: true, hash: "e3394a14f78098c189cca55179db9886834fbe51ac380873eec382ab494c8da0"},
		{name: "cat text", cmd: watchCatCommand, args: []string{"GARMIN/notes.txt"}, hash: "52f000e01e486d4b4caca23e88c4a003305ba77b294fd563237f1012a3451ad3"},
		{name: "cat binary", cmd: watchCatCommand, args: []string{"GARMIN/sample.bin"}, hash: "068a9db2aec30c1c6f182c6c9e6ed8f34b8b6e1d303e9918a29534d481bef126"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dev, _ := testdump.Watch(t)
			sess := watchsession.New(dev)

			t.Cleanup(func() { _ = sess.Close() })

			var out, diagnostics bytes.Buffer

			c := &cli{asJSON: test.json, out: clix.NewPrinter(&out), errOut: clix.NewPrinter(&diagnostics)}
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
		{name: "text", hash: "5cd9fcab25da9b3e3071fd81cb84fbdbd1ae831c194015c433435f81a5ec48ec"},
		{name: "JSON flag", json: true, hash: "c403ee77f3da5cc10ecd7798dfc51d2af45e9a347bae3ec7007498b01b3f92fc"},
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

			lease, err := runtime.Manager().Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			epoch := lease.Epoch()
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}

			dest := t.TempDir()

			var out, diagnostics bytes.Buffer

			c := &cli{runtime: runtime, epoch: epoch, asJSON: test.json, out: clix.NewPrinter(&out), errOut: clix.NewPrinter(&diagnostics)}
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

	c := &cli{out: clix.NewPrinter(&out), errOut: clix.NewPrinter(&diagnostics)}
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

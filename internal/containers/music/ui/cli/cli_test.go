package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func TestAddReadsHostFilesBeforeEachLease(t *testing.T) {
	t.Parallel()
	input, root := t.TempDir(), t.TempDir()
	files := []string{filepath.Join(input, "one.mp3"), filepath.Join(input, "two.mp3")}

	payload := []byte("synthetic audio")
	for _, file := range files {
		if err := os.WriteFile(file, payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	dev, err := device.NewDirFS(root)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = dev.Close() })

	sess := watchsession.New(dev)
	command := &Command{Out: clix.NewPrinter(io.Discard), ErrOut: clix.NewPrinter(io.Discard)}
	calls := 0

	err = command.Add(t.Context(), files, func(ctx context.Context, body func(context.Context, *watchsession.Session) error) error {
		if calls >= len(files) {
			t.Fatal("too many operation leases")
		}
		// Removing the input at lease entry proves its bytes were read before
		// device I/O. Windows also requires the host handle to have been closed.
		if err := os.Remove(files[calls]); err != nil {
			t.Fatal(err)
		}

		calls++

		return body(ctx, sess)
	})
	if err != nil || calls != 2 {
		t.Fatalf("leases=%d error=%v", calls, err)
	}

	for _, name := range []string{"Music/one.mp3", "Music/two.mp3"} {
		data, err := dev.Read(t.Context(), name)
		if err != nil || !bytes.Equal(data, payload) {
			t.Fatalf("staged %s=%q error=%v", name, data, err)
		}
	}
}

func TestMissingHostFileDoesNotEnterOperation(t *testing.T) {
	t.Parallel()

	command := &Command{Out: clix.NewPrinter(io.Discard), ErrOut: clix.NewPrinter(io.Discard)}

	err := command.Add(t.Context(), []string{filepath.Join(t.TempDir(), "missing.mp3")}, func(context.Context, func(context.Context, *watchsession.Session) error) error {
		t.Fatal("missing host file acquired a device lease")
		return nil
	})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("host read error=%v", err)
	}
}

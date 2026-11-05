package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
	"github.com/DevWolk/garminctl/internal/testdump"
)

const musicAddSubcommand = "add"

type musicJobDevice struct {
	device.Device
	writes *atomic.Int32
	after  func()
}

func (d musicJobDevice) Write(ctx context.Context, path string, data []byte) error {
	d.writes.Add(1)

	err := d.Device.Write(ctx, path, data)
	if err == nil {
		d.after()
	}

	return err
}

type musicJobFixture struct {
	cli           *cli
	opens, writes atomic.Int32
}

func newMusicJobFixture(t *testing.T, dropAfterWrite bool) *musicJobFixture {
	t.Helper()
	root := testdump.MusicRoot(t)
	f := new(musicJobFixture)
	runtime := app.NewRuntime(t.Context(), func(context.Context) (device.Device, error) {
		f.opens.Add(1)

		dev, err := device.NewDirFS(root)

		return musicJobDevice{Device: dev, writes: &f.writes, after: func() {
			if dropAfterWrite {
				f.cli.runtime.Manager().Drop()
			}
		}}, err
	})
	f.cli = &cli{runtime: runtime, out: newPrinter(io.Discard), errOut: newPrinter(io.Discard)}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
		defer cancel()

		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	return f
}

func musicJobInputs(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()

	files := []string{filepath.Join(root, "one.mp3"), filepath.Join(root, "two.mp3")}
	for _, file := range files {
		if err := os.WriteFile(file, []byte("synthetic audio"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return files
}

type musicProgressProbe struct {
	t          *testing.T
	gate       *lifecycle.Gate
	operations int
	messages   int
}

func (p *musicProgressProbe) Write(data []byte) (int, error) {
	p.messages++

	snapshot := p.gate.Snapshot()
	if snapshot.Operations != p.operations || snapshot.Jobs != 1 {
		p.t.Errorf("progress=%q gate=%+v want operations=%d jobs=1", data, snapshot, p.operations)
	}

	return len(data), nil
}

func TestMusicJobsKeepPerFileLeaseAndProgressTiming(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		command    string
		operations int
	}{
		{command: musicAddSubcommand, operations: 1},
		{command: "rm", operations: 0},
	} {
		t.Run(test.command, func(t *testing.T) {
			t.Parallel()
			f := newMusicJobFixture(t, false)

			args := []string{test.command, testdump.MusicTrack, "Podcasts/episode.aac"}
			if test.command == musicAddSubcommand {
				args = append([]string{test.command}, musicJobInputs(t)...)
			}

			probe := &musicProgressProbe{t: t, gate: f.cli.runtime.Manager().Gate(), operations: test.operations}

			f.cli.out = newPrinter(probe)
			if err := f.cli.runSessionJob(t.Context(), f.cli.musicJob, args); err != nil {
				t.Fatal(err)
			}

			state := probe.gate.Snapshot()
			if probe.messages != 2 || f.opens.Load() != 1 || state.Operations != 0 || state.Jobs != 0 {
				t.Fatalf("progress=%d opens=%d gate=%+v", probe.messages, f.opens.Load(), state)
			}
		})
	}
}

func TestMusicJobDoesNotContinueAfterEpochDrop(t *testing.T) {
	t.Parallel()
	f := newMusicJobFixture(t, true)
	args := append([]string{musicAddSubcommand}, musicJobInputs(t)...)
	err := f.cli.runSessionJob(t.Context(), f.cli.musicJob, args)

	state := f.cli.runtime.Manager().Gate().Snapshot()
	if err == nil || f.writes.Load() != 1 || f.opens.Load() != 1 || state.Operations != 0 || state.Jobs != 0 {
		t.Fatalf("error=%v writes=%d opens=%d gate=%+v", err, f.writes.Load(), f.opens.Load(), state)
	}
}

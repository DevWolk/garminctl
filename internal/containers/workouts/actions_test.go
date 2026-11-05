package workouts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
)

// writeCountDevice counts write attempts before delegating them.
type writeCountDevice struct {
	device.Device
	writes int
}

func (d *writeCountDevice) Write(ctx context.Context, path string, data []byte) error {
	d.writes++
	return d.Device.Write(ctx, path, data)
}

func TestWriteWorkoutRejectsOverflowBeforeDeviceWrite(t *testing.T) {
	t.Parallel()

	fs, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = fs.Close() })

	d := &writeCountDevice{Device: fs}
	w := &WorkoutSpec{Name: "overflow", Steps: []StepSpec{{Duration: "5000000s"}}}

	path, err := (WriteLocalAction{}).Run(t.Context(), watchsession.New(d), w)
	if err == nil || path != "" || d.writes != 0 {
		t.Fatalf("overflow workout staged: path=%q error=%v writes=%d", path, err, d.writes)
	}
}

func TestLocalWorkoutsQueueSeparateFiles(t *testing.T) {
	t.Parallel()
	root := testdump.WorkoutRoot(t)

	fs, err := device.NewDirFS(root)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = fs.Close() })

	sess := watchsession.New(fs)
	paths := map[string]bool{}

	for range 2 {
		spec := &WorkoutSpec{Name: "Local workout", Steps: []StepSpec{{Duration: "90s"}}}

		p, err := (WriteLocalAction{}).Run(t.Context(), sess, spec)
		if err != nil || paths[p] {
			t.Fatalf("queued path=%q error=%v", p, err)
		}

		paths[p] = true
		testdump.NormalizePendingPath(t, []byte(p))

		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p))) //nolint:gosec // generated NewFiles path under the temporary watch
		if err != nil {
			t.Fatal(err)
		}

		f, err := fit.Decode(data)
		if err != nil || len(f.MessagesByNum(26)) != 1 || len(f.MessagesByNum(27)) != 1 {
			t.Fatalf("workout FIT error=%v", err)
		}

		if f.MessagesByNum(0)[0].Value(0) != uint64(5) {
			t.Fatal("workout file type changed")
		}
	}
}

package garmin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestWorkoutCodecContracts(t *testing.T) {
	t.Parallel()

	var spec WorkoutSpec
	if err := json.Unmarshal([]byte(testdump.WorkoutSpecJSON), &spec); err != nil {
		t.Fatal(err)
	}

	doc, err := spec.ConnectJSON()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("connect", func(t *testing.T) {
		t.Parallel()
		testdump.CheckJSON(t, doc, "26cd7b8b2dedfda928a1aa7c4d8bff051b1f3fb689466c1ad5a6ed4140c7d96d")
	})
	t.Run("normalized spec", func(t *testing.T) {
		t.Parallel()
		testdump.CheckJSON(t, spec, "8e8c57e7fb1524744eae8a2a01c25b4c3518b94c33b5bc77dd536b510c5f263c")
	})

	data, err := spec.FIT(0xfedcba9876543210, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("FIT", func(t *testing.T) {
		t.Parallel()
		testdump.CheckDigest(t, data, "4cab94c37c951368f404157ef620824f265ee3a40729c40ec71885fb0758db02")
	})
}

func TestLocalWorkoutsQueueSeparateFiles(t *testing.T) {
	t.Parallel()
	root := testdump.WorkoutRoot(t)

	fs, err := device.NewDirFS(root)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = fs.Close() })

	svc := New(fs)
	paths := map[string]bool{}

	for range 2 {
		spec := &WorkoutSpec{Name: "Local workout", Steps: []StepSpec{{Duration: "90s"}}}

		p, err := svc.WriteWorkoutToWatch(t.Context(), spec)
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

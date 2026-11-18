package watchsession

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
)

type directoryDevice struct {
	device.Device
	statErrors map[string]error
	mkdirError error
	stats      []string
	mkdirs     []string
}

func (d *directoryDevice) Stat(_ context.Context, p string) (device.Entry, error) {
	d.stats = append(d.stats, p)
	return device.Entry{}, d.statErrors[p]
}

func (d *directoryDevice) Mkdir(_ context.Context, p string) error {
	d.mkdirs = append(d.mkdirs, p)
	return d.mkdirError
}

func TestEnsureDirPreservesTraversalAndErrors(t *testing.T) {
	t.Parallel()

	const directoryRoot = "GARMIN"

	failure := errors.New("transport failure")
	for _, test := range []struct {
		name       string
		statErrors map[string]error
		mkdirError error
		wantDirs   []string
		wantStats  []string
		wantErr    error
		wantText   string
	}{
		{name: "existing", wantStats: []string{directoryRoot, PathNewFiles}},
		{name: "missing", statErrors: map[string]error{PathNewFiles: device.ErrNotFound}, wantDirs: []string{PathNewFiles}, wantStats: []string{directoryRoot, PathNewFiles}},
		{name: "stat failure", statErrors: map[string]error{directoryRoot: failure}, wantStats: []string{directoryRoot}, wantErr: failure, wantText: "stat GARMIN: transport failure"},
		{name: "mkdir failure", statErrors: map[string]error{directoryRoot: device.ErrNotFound}, mkdirError: failure, wantStats: []string{directoryRoot}, wantDirs: []string{directoryRoot}, wantErr: failure, wantText: "mkdir GARMIN: transport failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			d := &directoryDevice{statErrors: test.statErrors, mkdirError: test.mkdirError}

			err := New(d).EnsureDir(t.Context(), "/GARMIN//NewFiles/")
			if !errors.Is(err, test.wantErr) || err != nil && err.Error() != test.wantText {
				t.Fatalf("EnsureDir error = %v", err)
			}

			if !slices.Equal(d.stats, test.wantStats) || !slices.Equal(d.mkdirs, test.wantDirs) {
				t.Fatalf("stats=%v mkdirs=%v", d.stats, d.mkdirs)
			}
		})
	}
}

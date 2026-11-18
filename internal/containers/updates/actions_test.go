package updates

import (
	"context"
	"errors"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

type updateDevice struct {
	device.Device
	writes, mkdirs int
}

func (d *updateDevice) Write(ctx context.Context, path string, data []byte) error {
	d.writes++
	return d.Device.Write(ctx, path, data)
}

func (d *updateDevice) Mkdir(ctx context.Context, path string) error {
	d.mkdirs++
	return d.Device.Mkdir(ctx, path)
}

func TestInstallValidatesBeforeDeviceMutation(t *testing.T) { //nolint:paralleltest // replaces the store client for the fixture
	for _, mode := range []string{"checksum", "size", "software", ""} { //nolint:paralleltest // cases replace the process store client
		t.Run("mode="+mode, func(t *testing.T) {
			config := Config{URL: DefaultURL, Client: testdump.UpdateEnvironment(t, mode)}

			dev, err := device.NewDirFS(testdump.UpdateRoot(t))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = dev.Close() })

			tracked := &updateDevice{Device: dev}
			sess := watchsession.New(tracked)

			var invalidation uint64

			result, err := NewInstallSoftwareAction(config, func(gen uint64) { invalidation = gen }).Run(t.Context(), sess, testdump.UpdatePart)

			generation, _ := sess.CacheGeneration()
			if mode != "" {
				if err == nil || tracked.writes != 0 || tracked.mkdirs != 0 || generation != 0 || invalidation != 0 {
					t.Fatalf("verification failure mutated device: err=%v writes=%d mkdirs=%d generation=%d", err, tracked.writes, tracked.mkdirs, generation)
				}

				return
			}

			if err != nil || result.Path != "Garmin/RemoteSW/GUPDATE.GCD" || !result.Restart || tracked.writes != 1 || invalidation != generation || invalidation == 0 {
				t.Fatalf("staging=%+v error=%v writes=%d invalidation=%d generation=%d", result, err, tracked.writes, invalidation, generation)
			}
		})
	}
}

func TestFindUpdateRetainsLastDuplicateAndMissingLocationError(t *testing.T) {
	t.Parallel()

	list := []SoftwareUpdate{{PartNumber: "part", DownloadURL: "one", InstallPath: "Garmin", FileName: "one"}, {PartNumber: "part", ProductName: "last"}}
	if _, err := findUpdate(list, "part"); err == nil || err.Error() != "last: Garmin did not provide a download or install location" {
		t.Fatalf("last duplicate selection error=%v", err)
	}

	if _, err := findUpdate(list, "missing"); err == nil {
		t.Fatal("missing part was accepted")
	}
}

func TestAppRegistryFailureIsReturned(t *testing.T) {
	t.Parallel()

	want := errors.New("registry unavailable")

	_, err := NewCheckAppsAction(func(context.Context) ([]apps.App, error) { return nil, want }).Run(t.Context())
	if !errors.Is(err, want) {
		t.Fatalf("registry error=%v", err)
	}
}

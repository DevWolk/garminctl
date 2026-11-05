package web

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	watchhttp "github.com/DevWolk/garminctl/internal/containers/watch/ui/http"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
)

type uncertainWriteDevice struct{ device.Device }

func (d uncertainWriteDevice) Write(ctx context.Context, path string, data []byte) error {
	if err := d.Device.Write(ctx, path, data); err != nil {
		return err
	}

	return errors.New("verification failed after possible storage change")
}

func TestFilePutFailureInvalidatesSharedServiceCache(t *testing.T) {
	t.Parallel()

	root := testdump.Root(t)
	opens := 0
	manager := watchsession.NewManager(func(context.Context) (device.Device, error) {
		opens++

		d, err := device.NewDirFS(root)

		return uncertainWriteDevice{Device: d}, err
	})
	t.Cleanup(manager.Drop)

	lease, err := manager.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	epoch := lease.Epoch()

	original, readErr := lease.Session().DeviceXML(lease.Context())
	if err := errors.Join(readErr, lease.Release()); err != nil {
		t.Fatal(err)
	}

	xml, err := os.ReadFile(filepath.Join(root, "GARMIN", "GarminDevice.xml")) //nolint:gosec // fixed fixture path under testdump
	if err != nil {
		t.Fatal(err)
	}

	const changedDescription = "changed despite verification failure"

	modified := bytes.Replace(xml,
		[]byte("<Description>"+original.Description+"</Description>"),
		[]byte("<Description>"+changedDescription+"</Description>"), 1)
	if bytes.Equal(xml, modified) {
		t.Fatal("fixture description was not replaced")
	}

	mux := http.NewServeMux()
	(&watchhttp.Handler{Manager: manager}).RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/file?path=GARMIN/GarminDevice.xml", bytes.NewReader(modified))
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("failed verification status=%d body=%s", w.Code, w.Body)
	}

	lease, err = manager.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	nextEpoch := lease.Epoch()

	updated, readErr := lease.Session().DeviceXML(lease.Context())
	if err := errors.Join(readErr, lease.Release()); err != nil {
		t.Fatal(err)
	}

	if nextEpoch != epoch || opens != 1 || updated.Description != changedDescription {
		t.Fatalf("failed HTTP write retained stale cache or reopened: opens=%d description=%q", opens, updated.Description)
	}
}

package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/hostfile"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestCloudLogoutOverHTTPRemovesSessionCopiesFromApplicationBackups(t *testing.T) { //nolint:paralleltest // The fixture redirects process config/cache environment.
	testdump.HealthEnvironment(t)
	testdump.ConnectCLIEnvironment(t, "")

	account := gc.DefaultStorePath()
	config := filepath.Dir(account)

	store := hostfile.NewSnapshotStore(filepath.Join(config, "application-backups"), []hostfile.SnapshotSource{{Name: "config", Directory: config}}, func(_, path string) hostfile.SnapshotKind {
		if path == filepath.Base(account) {
			return hostfile.SnapshotFile
		}

		return hostfile.SnapshotIgnore
	})

	snapshot, err := store.Create(t.Context(), json.RawMessage(`{}`))
	if err != nil || len(snapshot.Files) != 1 {
		t.Fatalf("fixture backup=%+v error=%v", snapshot, err)
	}

	root := testdump.ConnectRoot(t)
	ts := serveContractRoot(t, func(context.Context) (device.Device, error) { return device.NewDirFS(root) })

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/api/cloud/logout", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout status=%d", resp.StatusCode)
	}

	for _, name := range []string{account, filepath.Join(snapshot.Directory, "config", filepath.Base(account))} {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived sign-out: %v", name, err)
		}
	}
}

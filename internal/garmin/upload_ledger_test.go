package garmin

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
)

const ledgerXML = `<Device><Id>42</Id><MassStorageMode><DataType><Name>FIT_TYPE_4</Name><File><Location><Path>GARMIN/Activity</Path><FileExtension>fit</FileExtension></Location><TransferDirection>OutputFromUnit</TransferDirection></File></DataType></MassStorageMode></Device>`

func TestUploadLedgerMissingRoundTripAndPrivacy(t *testing.T) {
	t.Parallel()

	c := &Cloud{cacheDir: filepath.Join(t.TempDir(), "new-cache")}

	l, err := c.loadLedger(42)
	if err != nil || len(l) != 0 {
		t.Fatalf("first-run ledger=%v error=%v", l, err)
	}

	want := ledger{"GARMIN/Activity/example.fit": "7|2026-01-02"}
	if err := c.saveLedger(42, want); err != nil {
		t.Fatal(err)
	}

	l, err = c.loadLedger(42)
	if err != nil || !reflect.DeepEqual(l, want) {
		t.Fatalf("saved ledger=%v error=%v", l, err)
	}

	if runtime.GOOS != "windows" {
		for path, mode := range map[string]os.FileMode{c.cacheDir: 0o700, c.ledgerPath(42): 0o600} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			if info.Mode().Perm() != mode {
				t.Errorf("%s mode=%o, want %o", path, info.Mode().Perm(), mode)
			}
		}
	}
}

func TestUploadLedgerRejectsInvalidExistingState(t *testing.T) {
	t.Parallel()

	for _, data := range []string{"{", "null", "[]", `{"path":12}`, ""} {
		t.Run(data, func(t *testing.T) {
			t.Parallel()

			c := &Cloud{cacheDir: t.TempDir()}
			if err := os.WriteFile(c.ledgerPath(42), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := c.loadLedger(42); err == nil {
				t.Fatal("invalid existing ledger silently became empty")
			}

			got, err := os.ReadFile(c.ledgerPath(42))
			if err != nil || string(got) != data {
				t.Fatalf("invalid ledger changed: %q error=%v", got, err)
			}
		})
	}
}

func TestUploadLedgerReplaceFailurePreservesPreviousFile(t *testing.T) {
	t.Parallel()

	c := &Cloud{cacheDir: t.TempDir()}
	if err := c.saveLedger(42, ledger{"previous": "stamp"}); err != nil {
		t.Fatal(err)
	}

	previous, err := os.ReadFile(c.ledgerPath(42))
	if err != nil {
		t.Fatal(err)
	}

	var calls int

	err = writeUploadLedger(c.ledgerPath(42), []byte(`{"new":"stamp"}`), func(from, to string) error {
		calls++

		data, readErr := os.ReadFile(from) //nolint:gosec // this call's generated temporary ledger path
		if readErr != nil || string(data) != `{"new":"stamp"}` || to != c.ledgerPath(42) {
			t.Errorf("replacement arguments/data: %q %q %q %v", from, to, data, readErr)
		}

		return os.ErrPermission
	})
	if !errors.Is(err, os.ErrPermission) || calls != 1 {
		t.Fatalf("replace error=%v calls=%d", err, calls)
	}

	got, err := os.ReadFile(c.ledgerPath(42))
	if err != nil || !bytes.Equal(got, previous) {
		t.Fatalf("previous ledger changed: %q error=%v", got, err)
	}

	entries, err := os.ReadDir(c.cacheDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "uploaded-42.json" {
		t.Fatalf("temporary file cleanup: %v error=%v", entries, err)
	}
}

type ledgerWatch struct{ device.Device }

func (*ledgerWatch) Read(_ context.Context, path string) ([]byte, error) {
	if path == PathDeviceXML {
		return []byte(ledgerXML), nil
	}

	return []byte("payload"), nil
}

func (*ledgerWatch) List(_ context.Context, _ string) ([]device.Entry, error) {
	return []device.Entry{{Name: "sample.fit", Size: 7, Modified: "synthetic-stamp"}}, nil
}

func ledgerTestCloud(t *testing.T, response int, blockSave bool) (*Cloud, *atomic.Int32) {
	t.Helper()

	var requests atomic.Int32

	cacheDir := t.TempDir()
	client := syncTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/upload-service/upload/activity" {
			t.Errorf("unexpected ledger request: %s", req.URL.Path)
		}

		requests.Add(1)

		if blockSave {
			if err := os.Mkdir(filepath.Join(cacheDir, "uploaded-42.json"), 0o700); err != nil {
				return nil, err
			}
		}

		resp := syncResponse(req, []byte(`{}`))
		resp.StatusCode = response

		return resp, nil
	})
	c := NewCloud(New(&ledgerWatch{}), client)
	c.cacheDir = cacheDir

	return c, &requests
}

func TestUploadLedgerSaveFailureReturnsAcceptedResultWithoutReplay(t *testing.T) {
	t.Parallel()

	c, requests := ledgerTestCloud(t, http.StatusCreated, true)

	up, err := c.Upload(t.Context(), nil)
	if err == nil || !strings.Contains(err.Error(), "save upload ledger") || up == nil {
		t.Fatalf("upload=%+v error=%v", up, err)
	}

	if up.Uploaded != 1 || len(up.Files) != 1 || up.Files[0].Status != uploadStatusUploaded || requests.Load() != 1 {
		t.Fatalf("accepted upload lost/replayed: %+v requests=%d", up, requests.Load())
	}

	if _, err := c.Upload(t.Context(), nil); err == nil || requests.Load() != 1 {
		t.Fatalf("invalid ledger caused replay: error=%v requests=%d", err, requests.Load())
	}
}

func TestSyncKeepsUploadAndAggregateLedgerError(t *testing.T) {
	t.Parallel()

	c, requests := ledgerTestCloud(t, http.StatusCreated, true)

	res, err := c.Sync(t.Context(), true, false, nil)
	if err != nil || res == nil || res.Upload == nil || res.Upload.Uploaded != 1 {
		t.Fatalf("sync lost partial upload: result=%+v error=%v", res, err)
	}

	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "upload: save upload ledger") || requests.Load() != 1 {
		t.Fatalf("sync ledger error missing/replayed: result=%+v requests=%d", res, requests.Load())
	}
}

func TestUploadCorruptLedgerStopsBeforeNetwork(t *testing.T) {
	t.Parallel()

	c, requests := ledgerTestCloud(t, http.StatusCreated, false)
	if err := os.WriteFile(c.ledgerPath(42), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Upload(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "load upload ledger") || requests.Load() != 0 {
		t.Fatalf("corrupt ledger was ignored: error=%v requests=%d", err, requests.Load())
	}
}

func TestUploadLedgerRemembersAcceptedAndDuplicateFiles(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusCreated, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			c, requests := ledgerTestCloud(t, status, false)
			if _, err := c.Upload(t.Context(), nil); err != nil {
				t.Fatal(err)
			}

			res, err := c.Upload(t.Context(), nil)
			if err != nil || res.Skipped != 1 || requests.Load() != 1 {
				t.Fatalf("saved upload not skipped: result=%+v error=%v requests=%d", res, err, requests.Load())
			}
		})
	}
}

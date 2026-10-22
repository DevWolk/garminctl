package garmin

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"garminctl/internal/device"
)

const updatesFixture = `{"updates":[{"partNumber":"006-DA094-00","productName":"Americas Golf Courses DB","version":"1.10","isPrimaryFirmware":false,"dataType":"Firmware","severity":"Recommended","releaseNotes":[{"version":"1.10","changeLog":["Course updates"]}],"deliverable":{"downloadUrl":"BASE/file","md5":"MD5","sizeInBytes":20},"installation":{"isProductInstalled":false,"path":"Garmin/RemoteSW","fileName":"AmericasCourses.db","isRestartRequired":false},"displayVersion":"1.10"}],"accessLevel":"Public"}`

func TestSoftwareUpdates(t *testing.T) {
	xmlBytes, err := os.ReadFile("testdata/GarminDevice.xml")
	if err != nil {
		t.Skip("no sample")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "GARMIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "GARMIN", "GarminDevice.xml"), xmlBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	payload := []byte("golf course database")
	sum := md5.Sum(payload)

	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/updates":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotReq)
			w.Header().Set("Content-Type", "application/json")
			resp := strings.NewReplacer("BASE", "http://"+r.Host, "MD5", hex.EncodeToString(sum[:])).Replace(updatesFixture)
			_, _ = io.WriteString(w, resp)
		case "/file":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := UpdatesURL
	UpdatesURL = srv.URL + "/updates"
	defer func() { UpdatesURL = old }()

	dfs, err := device.NewDirFS(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(dfs)
	ctx := context.Background()
	ups, err := svc.CheckSoftwareUpdates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := gotReq["GarminDeviceXml"].(string); !strings.Contains(s, "<Device") {
		t.Fatalf("request did not carry GarminDevice.xml: %v", gotReq)
	}
	if len(ups) != 1 || ups[0].PartNumber != "006-DA094-00" || ups[0].ReleaseNotes[0] != "Course updates" || ups[0].IsProductInstalled {
		t.Fatalf("unexpected updates: %+v", ups)
	}
	res, err := svc.InstallSoftwareUpdate(ctx, "006-DA094-00")
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != "Garmin/RemoteSW/AmericasCourses.db" || res.Bytes != len(payload) {
		t.Fatalf("unexpected result: %+v", res)
	}
	written, err := dfs.Read(ctx, "Garmin/RemoteSW/AmericasCourses.db") // the dump is case-insensitive like the watch
	if err != nil || string(written) != string(payload) {
		t.Fatalf("file not written: %v %q", err, written)
	}
	if _, err := svc.InstallSoftwareUpdate(ctx, "006-XXXXX-00"); err == nil {
		t.Fatal("expected an error for an unknown part number")
	}
}

func TestInstallPRGRejectsGarbage(t *testing.T) {
	dfs, err := device.NewDirFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := New(dfs)
	if _, err := svc.InstallPRG(context.Background(), []byte("not a prg")); err == nil {
		t.Fatal("expected rejection")
	}
}

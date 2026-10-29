package garmin

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

type unavailableAppFolder struct {
	device.Device
	calls []string
}

func (d *unavailableAppFolder) Stat(ctx context.Context, path string) (device.Entry, error) {
	d.calls = append(d.calls, "stat "+path)
	if strings.HasPrefix(strings.ToUpper(path), AppFolder) {
		return device.Entry{}, device.ErrNotFound
	}

	return d.Device.Stat(ctx, path)
}

func (d *unavailableAppFolder) List(ctx context.Context, path string) ([]device.Entry, error) {
	d.calls = append(d.calls, "list "+path)
	if strings.EqualFold(path, AppFolder) {
		return nil, device.ErrNotFound
	}

	return d.Device.List(ctx, path)
}

type hiddenAppDevice struct {
	*unavailableAppFolder
	deleteErr error
}

func (d *hiddenAppDevice) WriteHidden(ctx context.Context, dir, name string, data []byte) error {
	d.calls = append(d.calls, "hidden write "+dir+"/"+name)
	return d.Write(ctx, dir+"/"+name, data)
}

func (d *hiddenAppDevice) ListHidden(_ context.Context, dir string) ([]device.Entry, error) {
	d.calls = append(d.calls, "hidden list "+dir)
	return []device.Entry{{Name: "fixture.PRG", Size: 4}}, nil
}

func (d *hiddenAppDevice) DeleteHidden(ctx context.Context, dir, name string) error {
	d.calls = append(d.calls, "hidden delete "+dir+"/"+name)
	if d.deleteErr != nil {
		return d.deleteErr
	}

	return d.Delete(ctx, dir+"/"+name)
}

func TestAppHiddenWriteAndListContracts(t *testing.T) {
	t.Parallel()

	base, _ := testdump.Apps(t)
	d := &hiddenAppDevice{unavailableAppFolder: &unavailableAppFolder{Device: base}}
	svc := New(d)

	t.Cleanup(func() { _ = svc.Close() })

	data := []byte{0, 1, 2, 0xff}

	path, err := svc.WriteAppFile(t.Context(), "fixture.PRG", data)
	if err != nil || path != AppFolder+"/fixture.PRG" {
		t.Fatalf("hidden write path=%q error=%v", path, err)
	}

	back, err := base.Read(t.Context(), path)
	if err != nil || !bytes.Equal(back, data) {
		t.Fatalf("hidden write bytes=%x error=%v", back, err)
	}

	entries, err := svc.AppFiles(t.Context())
	if err != nil || !reflect.DeepEqual(entries, []device.Entry{{Name: "fixture.PRG", Size: 4}}) {
		t.Fatalf("hidden list=%v error=%v", entries, err)
	}

	want := []string{"stat " + AppFolder, "hidden write " + path, "list " + AppFolder, "hidden list " + AppFolder}
	if !reflect.DeepEqual(d.calls, want) {
		t.Fatalf("capability call order=%v want=%v", d.calls, want)
	}
}

func TestAppHiddenRemovalDoesNotRetryFailures(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		err   error
		files int
	}{
		{name: "present", files: 5},
		{name: "missing binary", err: device.ErrNotFound, files: 4},
		{name: "disconnect", err: device.ErrDisconnected},
		{name: "other failure", err: errors.New("synthetic hidden deletion failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			base, _ := testdump.Apps(t)
			d := &hiddenAppDevice{unavailableAppFolder: &unavailableAppFolder{Device: base}, deleteErr: test.err}
			svc := New(d)

			t.Cleanup(func() { _ = svc.Close() })

			removed, err := svc.RemoveApp(t.Context(), testdump.AppRef)
			if len(removed) != test.files {
				t.Fatalf("removed=%v want %d files", removed, test.files)
			}

			if test.files > 0 {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, test.err) || len(d.calls) != 2 {
				t.Fatalf("failure=%v calls=%v", err, d.calls)
			}

			want := []string{"stat " + AppFolder + "/" + testdump.AppRef + ".PRG", "hidden delete " + AppFolder + "/" + testdump.AppRef + ".PRG"}
			if len(d.calls) < 2 || !reflect.DeepEqual(d.calls[:2], want) {
				t.Fatalf("hidden delete call order=%v want prefix=%v", d.calls, want)
			}
		})
	}
}

func TestAppFolderWithoutHiddenCapabilityContracts(t *testing.T) {
	t.Parallel()

	base, _ := testdump.Apps(t)
	d := &unavailableAppFolder{Device: base}
	svc := New(d)

	t.Cleanup(func() { _ = svc.Close() })

	if _, err := svc.WriteAppFile(t.Context(), "fixture.PRG", nil); err == nil || err.Error() != "this watch does not expose its app folder (GARMIN/APPS/MEDIA) over USB, so apps can only be installed through Garmin Connect on the phone" {
		t.Fatalf("unsupported write error=%v", err)
	}

	if _, err := svc.AppFiles(t.Context()); !errors.Is(err, device.ErrNotFound) {
		t.Fatalf("unsupported list error=%v", err)
	}

	if _, err := svc.RemoveApp(t.Context(), testdump.AppRef); err == nil || err.Error() != "this watch does not expose its app folder over USB from here" {
		t.Fatalf("unsupported removal error=%v", err)
	}
}

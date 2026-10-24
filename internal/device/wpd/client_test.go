package wpd

import (
	"slices"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
)

func TestPick(t *testing.T) {
	t.Parallel()

	const iface = "{6ac27878-a6fa-4155-ba85-f98f491d4f33}"

	watch := device.Info{ID: `\\?\usb#vid_091e&pid_514a#0000e4f9adae#` + iface, Name: "vívoactive 5", Manufacturer: "Garmin"}
	// A USB drive: Windows lists mass storage through the same API.
	ssd := device.Info{ID: `\\?\swd#wpdbusenum#{4f2da7fb-bd09-11f1-bd01-34fd70d7c8b8}#0000000000100000#` + iface, Name: "Ubuntu", Manufacturer: "TUF"}
	phone := device.Info{ID: `\\?\usb#vid_18d1&pid_4ee1#1234#` + iface, Name: "Pixel", Manufacturer: "Google"}
	unnamed := device.Info{ID: `\\?\usb#VID_091E&PID_514A#0000#` + iface, Name: "MTP Device"}

	tests := []struct {
		name    string
		devices []device.Info
		id      string
		want    string // ID of the chosen device
		wantErr string // part of the error text
	}{
		{name: "nothing connected", wantErr: "no portable device connected"},
		{name: "nothing connected, explicit id", id: watch.ID, wantErr: "no portable device connected"},
		{name: "watch alone", devices: []device.Info{watch}, want: watch.ID},
		{name: "usb drive alone is not the watch", devices: []device.Info{ssd}, wantErr: "no Garmin device among 1 portable device(s): Ubuntu (TUF)"},
		{name: "phone and drive, no watch", devices: []device.Info{phone, ssd}, wantErr: "among 2 portable device(s): Pixel (Google), Ubuntu (TUF)"},
		{name: "watch after other devices", devices: []device.Info{ssd, phone, watch}, want: watch.ID},
		{name: "garmin by usb vendor id only", devices: []device.Info{ssd, unnamed}, want: unnamed.ID},
		{name: "explicit id wins over the watch", devices: []device.Info{watch, ssd}, id: ssd.ID, want: ssd.ID},
		{name: "explicit id missing", devices: []device.Info{watch}, id: "nope", wantErr: `device "nope" not found among 1 portable devices`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := pick(tt.devices, tt.id)
			if err != nil {
				if tt.wantErr == "" || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("pick() error = %v, want %q", err, tt.wantErr)
				}

				return
			}

			if tt.wantErr != "" {
				t.Fatalf("pick() = %q, want error %q", got.ID, tt.wantErr)
			}

			if got.ID != tt.want {
				t.Errorf("pick() = %q, want %q", got.ID, tt.want)
			}
		})
	}
}

func TestTrimInfo(t *testing.T) {
	t.Parallel()

	got := trimInfo([]device.Info{
		{ID: ` \\?\swd#wpdbusenum#x `, Name: "Ubuntu ", Manufacturer: "TUF     ", Description: " Ubuntu\t"},
		{ID: "watch", Name: "vívoactive 5", Manufacturer: "Garmin"},
	})

	want := []device.Info{
		{ID: ` \\?\swd#wpdbusenum#x `, Name: "Ubuntu", Manufacturer: "TUF", Description: "Ubuntu"},
		{ID: "watch", Name: "vívoactive 5", Manufacturer: "Garmin"},
	}

	if !slices.Equal(got, want) {
		t.Errorf("trimInfo() = %q, want %q", got, want)
	}
}

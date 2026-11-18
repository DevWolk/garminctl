package hostfile

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestSnapshotInspectCurrentReadsRetainedBytesWithoutReplacingLiveData(t *testing.T) {
	t.Parallel()
	f := newRestoreFileFixture(t)
	seen := map[string]string{}

	err := f.restorer.InspectCurrent(t.Context(), f.safety, func(info SnapshotFileInfo, reader io.Reader) error {
		data, err := io.ReadAll(reader)
		seen[info.Path] = string(data)

		return err
	})
	if err != nil || seen["account.json"] != "new account" || seen["mfa.json"] != "new pending MFA" {
		t.Fatalf("inspection=%v error=%v", seen, err)
	}

	for name, want := range seen {
		got, err := f.live.ReadFile(name)
		if err != nil || !bytes.Equal(got, []byte(want)) {
			t.Fatalf("live changed=%q %v", got, err)
		}
	}

	assertRestoreCopiesAndCleanup(t, f)
}

func TestSnapshotInspectCurrentRefusesLiveDriftCancellationAndRejectedReader(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"before", "during", "canceled", "reader"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newRestoreFileFixture(t)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if name == "before" {
				if err := f.live.WriteFile("account.json", []byte("drift"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if name == "canceled" {
				cancel()
			}

			err := f.restorer.InspectCurrent(ctx, f.safety, func(SnapshotFileInfo, io.Reader) error {
				if name == "during" {
					return f.live.WriteFile("account.json", []byte("drift"), 0o600)
				}

				if name == "reader" {
					return errors.New("unsupported format")
				}

				return nil
			})
			if err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}

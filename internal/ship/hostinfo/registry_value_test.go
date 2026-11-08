package hostinfo

import (
	"errors"
	"testing"
)

type directoryKeyFixture struct {
	value             string
	kind              uint32
	readErr, closeErr error
	closes            int
}

func (k *directoryKeyFixture) GetStringValue(name string) (string, uint32, error) {
	if name != "InstallDir" {
		return "", 0, errors.New("unexpected value name")
	}

	return k.value, k.kind, k.readErr
}

func (k *directoryKeyFixture) Close() error { k.closes++; return k.closeErr }

func TestInstallDirectoryClosesKeyAndPreservesReadAndCloseErrors(t *testing.T) {
	t.Parallel()

	readErr, closeErr := errors.New("registry read failed"), errors.New("registry close failed")
	for _, tc := range []struct {
		name    string
		key     directoryKeyFixture
		invalid bool
	}{
		{"success", directoryKeyFixture{value: "fixture directory", kind: 1}, false},
		{"read failure", directoryKeyFixture{readErr: readErr}, true},
		{"close failure", directoryKeyFixture{value: "fixture directory", kind: 1, closeErr: closeErr}, true},
		{"both failures", directoryKeyFixture{readErr: readErr, closeErr: closeErr}, true},
		{"wrong type", directoryKeyFixture{value: "fixture directory", kind: 2}, true},
		{"empty path", directoryKeyFixture{kind: 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key := tc.key

			value, err := readInstallDirectory(&key, 1)
			if key.closes != 1 || (err != nil) != tc.invalid {
				t.Fatalf("value=%q error=%v closes=%d", value, err, key.closes)
			}

			if key.readErr != nil && !errors.Is(err, readErr) {
				t.Fatalf("read cause lost: %v", err)
			}

			if key.closeErr != nil && !errors.Is(err, closeErr) {
				t.Fatalf("close cause lost: %v", err)
			}

			if !tc.invalid && value != key.value {
				t.Fatalf("directory=%q", value)
			}
		})
	}
}

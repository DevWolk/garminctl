package apps

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func TestRestoreRegistryPreservesBytesAndRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	d, original, _ := registryFixture(t)
	sess := watchsession.New(d)
	action := RestoreRegistryAction{}

	if err := action.Run(t.Context(), sess, "saved.bin", original); err != nil {
		t.Fatal(err)
	}

	pending, err := sess.ReadFile(t.Context(), PathAppsIn)
	if err != nil || !bytes.Equal(pending, original) {
		t.Fatalf("restored registry differs: %v", err)
	}

	if err := action.Run(t.Context(), sess, "broken.bin", []byte{0xff}); err == nil || !strings.HasPrefix(err.Error(), "broken.bin is not an app registry: ") {
		t.Fatalf("invalid registry error=%v", err)
	}

	if d.writes != 1 {
		t.Fatalf("invalid input reached the device: writes=%d", d.writes)
	}

	pending, err = sess.ReadFile(t.Context(), PathAppsIn)
	if err != nil || !bytes.Equal(pending, original) {
		t.Fatalf("invalid input replaced the pending registry: %v", err)
	}

	d.fail = errors.New("synthetic registry write failure")
	if err := action.Run(t.Context(), sess, "saved.bin", original); !errors.Is(err, d.fail) {
		t.Fatalf("restore lost the transport error: %v", err)
	}
}

func TestCancelRegistryPreservesCurrentAndMissingError(t *testing.T) {
	t.Parallel()

	d, original, _ := registryFixture(t)

	sess := watchsession.New(d)
	if err := d.Device.Write(t.Context(), PathAppsIn, original); err != nil {
		t.Fatal(err)
	}

	action := CancelRegistryAction{}
	if err := action.Run(t.Context(), sess); err != nil {
		t.Fatal(err)
	}

	if _, err := sess.ReadFile(t.Context(), PathAppsIn); !errors.Is(err, device.ErrNotFound) {
		t.Fatalf("pending registry survived cancellation: %v", err)
	}

	if err := action.Run(t.Context(), sess); !errors.Is(err, device.ErrNotFound) {
		t.Fatalf("cancel changed the missing-file error: %v", err)
	}

	current, err := sess.ReadFile(t.Context(), PathAppsConfig)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("cancel modified the current registry: %v", err)
	}
}

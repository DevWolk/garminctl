package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
)

type actionDevice struct {
	device.Device
	reads map[string]int
}

func (d *actionDevice) Read(ctx context.Context, path string) ([]byte, error) {
	d.reads[path]++
	return d.Device.Read(ctx, path)
}

func actionFixture(t *testing.T) (*watchsession.Session, *actionDevice) {
	t.Helper()

	d, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = d.Close() })

	if err := d.Mkdir(t.Context(), PathNewFiles); err != nil {
		t.Fatal(err)
	}

	counter := &actionDevice{Device: d, reads: map[string]int{}}

	return watchsession.New(counter), counter
}

func TestActionsShareSessionCacheAndInvalidateAfterApply(t *testing.T) {
	t.Parallel()
	sess, d := actionFixture(t)
	profile := fit.MustProfile()

	read := NewReadAction(profile)
	for range 2 {
		if view, err := read.Run(t.Context(), sess); err != nil || len(view.Rows()) == 0 {
			t.Fatalf("read=%+v error=%v", view, err)
		}
	}

	if d.reads[PathSettings] != 1 {
		t.Fatalf("repeated read missed shared cache: %v", d.reads)
	}

	fields, err := NewSchemaAction(profile).Run("deviceSettings")
	if err != nil || len(fields) == 0 {
		t.Fatalf("schema=%v error=%v", fields, err)
	}

	result, err := NewApplyAction(profile).Run(t.Context(), sess, []Change{{Mesg: "2", Field: "58", Value: 1500}})
	if err != nil || len(result.Changes) != 1 {
		t.Fatalf("apply=%+v error=%v", result, err)
	}

	if d.reads[PathSettings] != 1 {
		t.Fatalf("apply bypassed shared cache: %v", d.reads)
	}

	if _, err := read.Run(t.Context(), sess); err != nil {
		t.Fatal(err)
	}

	if d.reads[PathSettings] != 2 {
		t.Fatalf("write failed to invalidate read cache: %v", d.reads)
	}
}

func TestApplyPreservesRequiredXMLAndExistingNewFiles(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ name, missing string }{
		{name: "metadata", missing: watchsession.PathDeviceXML},
		{name: "queue directory", missing: PathNewFiles},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			sess, d := actionFixture(t)
			if err := d.Delete(t.Context(), test.missing); err != nil {
				t.Fatal(err)
			}

			result, err := NewApplyAction(fit.MustProfile()).Run(t.Context(), sess, []Change{{Mesg: "2", Field: "58", Value: 1500}})
			if result != nil || !errors.Is(err, device.ErrNotFound) {
				t.Fatalf("missing %s changed policy: result=%+v error=%v", test.missing, result, err)
			}
		})
	}
}

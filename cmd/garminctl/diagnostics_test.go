package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/garmin"
)

type diagnosticDevice struct {
	device.Device
	trace  []string
	data   []byte
	opcode uint32
	params []uint32
}

func (d *diagnosticDevice) Handles(_ context.Context, path string) (uint32, uint32, error) {
	d.trace = append(d.trace, "handles:"+path)
	return 0x11, 0x22, nil
}

func (d *diagnosticDevice) FindChild(_ context.Context, _, parent uint32, name string) (device.MTPObject, bool, error) {
	d.trace = append(d.trace, fmt.Sprintf("find:%x:%s", parent, name))
	if name == "GCTLTEST.TMP" {
		return device.MTPObject{Handle: 0x99, Name: name, Size: uint32(len(d.data))}, d.data != nil, nil //nolint:gosec // synthetic test payload is 1000 bytes
	}

	return device.MTPObject{Handle: parent + 0x11, Name: name, Format: 0x3001}, true, nil
}

func (d *diagnosticDevice) AllChildren(_ context.Context, _, parent uint32) ([]device.MTPObject, error) {
	d.trace = append(d.trace, fmt.Sprintf("children:%x", parent))
	return []device.MTPObject{{Handle: 0x55, Name: "hidden.PRG", Format: 0x3000, Size: 9}}, nil
}

func (d *diagnosticDevice) SendFile(_ context.Context, _, parent uint32, name string, data []byte) (uint32, error) {
	d.trace = append(d.trace, fmt.Sprintf("send:%x:%s", parent, name))
	d.data = bytes.Clone(data)

	return 0x99, nil
}

func (d *diagnosticDevice) ReadHandle(_ context.Context, handle uint32) ([]byte, error) {
	d.trace = append(d.trace, fmt.Sprintf("read:%x", handle))
	return bytes.Clone(d.data), nil
}

func (d *diagnosticDevice) DeleteHandle(_ context.Context, handle uint32) error {
	d.trace = append(d.trace, fmt.Sprintf("delete:%x", handle))
	d.data = nil

	return nil
}

func (d *diagnosticDevice) MtpRead(_ context.Context, opcode uint32, params []uint32) ([]byte, uint32, []uint32, error) {
	d.opcode, d.params = opcode, params
	return []byte{0xaa}, 0x2001, []uint32{7}, nil
}

func TestHiddenUsesOptionalDiagnostics(t *testing.T) {
	t.Parallel()

	d := &diagnosticDevice{}

	var out bytes.Buffer

	c := &cli{out: newPrinter(&out), errOut: newPrinter(&bytes.Buffer{})}
	if err := c.hidden(t.Context(), garmin.New(d), []string{"GARMIN/APPS/MEDIA"}); err != nil {
		t.Fatal(err)
	}

	const want = "GARMIN/APPS/MEDIA: storage 0x00000011 handle 0x00000044\n  0x00000055  hidden.PRG               file 9\n"
	if out.String() != want {
		t.Fatalf("hidden output = %q", out.String())
	}

	wantTrace := []string{"handles:GARMIN", "find:22:APPS", "find:33:MEDIA", "children:44"}
	if !reflect.DeepEqual(d.trace, wantTrace) {
		t.Fatalf("hidden trace = %q", d.trace)
	}
}

func TestHiddenWriteDiagnosticsUseTheSameHandles(t *testing.T) {
	t.Parallel()

	d := &diagnosticDevice{}

	c := &cli{out: newPrinter(&bytes.Buffer{})}

	if err := garmin.New(d).WithDiagnostics(t.Context(), func(scope *garmin.Diagnostics) error {
		return c.hiddenWriteTest(scope, 0x11, 0x44)
	}); err != nil {
		t.Fatal(err)
	}

	want := []string{"find:44:GCTLTEST.TMP", "send:44:GCTLTEST.TMP", "find:44:GCTLTEST.TMP", "read:99", "delete:99", "find:44:GCTLTEST.TMP"}
	if !reflect.DeepEqual(d.trace, want) || d.data != nil {
		t.Fatalf("write/read/delete diagnostics trace=%q remaining=%q", d.trace, d.data)
	}
}

func TestMTPUsesOptionalReader(t *testing.T) {
	t.Parallel()

	d := &diagnosticDevice{}

	var out bytes.Buffer

	c := &cli{out: newPrinter(&out)}
	if err := c.mtp(t.Context(), garmin.New(d), []string{"0x1009", "AB"}); err != nil {
		t.Fatal(err)
	}

	want := "response 0x2001 params [7], 1 bytes\n" + hex.Dump([]byte{0xaa})
	if d.opcode != 0x1009 || !reflect.DeepEqual(d.params, []uint32{0xab}) || out.String() != want {
		t.Fatalf("raw read opcode=%x params=%x output=%q", d.opcode, d.params, out.String())
	}
}

type hiddenOnlyDevice struct{ device.Device }

func (*hiddenOnlyDevice) ListHidden(context.Context, string) ([]device.Entry, error) {
	return nil, errors.New("ordinary hidden listing is not handle diagnostics")
}

func (*hiddenOnlyDevice) WriteHidden(context.Context, string, string, []byte) error {
	return errors.New("unsupported test write")
}

func (*hiddenOnlyDevice) DeleteHidden(context.Context, string, string) error {
	return errors.New("unsupported test delete")
}

var _ device.HiddenFS = (*hiddenOnlyDevice)(nil)

func TestHiddenFSDoesNotAdvertiseRawDiagnostics(t *testing.T) {
	t.Parallel()

	c := &cli{out: newPrinter(&bytes.Buffer{}), errOut: newPrinter(&bytes.Buffer{})}

	svc := garmin.New(&hiddenOnlyDevice{})
	if err := c.hidden(t.Context(), svc, []string{"GARMIN"}); err == nil || exitCode(err) != 1 {
		t.Fatalf("unsupported handle diagnostics = %v", err)
	}

	for _, args := range [][]string{{"1009"}, {"not-hex"}, nil} {
		if err := c.mtp(t.Context(), svc, args); err == nil || exitCode(err) != 2 {
			t.Fatalf("unsupported raw operation %q = %v", args, err)
		}
	}
}

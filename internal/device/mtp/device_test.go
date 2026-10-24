package mtp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
)

type (
	modelObject struct {
		name        string
		parent      uint32
		dir, hidden bool
		data        []byte
	}
	modelWire struct {
		testWire
		objects map[uint32]modelObject
		request []byte
		sending uint32
	}
)

func (w *modelWire) WriteContext(ctx context.Context, b []byte) (int, error) {
	if _, err := w.testWire.WriteContext(ctx, b); err != nil {
		return 0, err
	}

	if len(b) < 12 {
		return len(b), nil
	}

	kind := binary.LittleEndian.Uint16(b[4:])
	code := binary.LittleEndian.Uint16(b[6:])

	transaction := binary.LittleEndian.Uint32(b[8:])
	if kind == containerCommand && (code == 0x100c || code == 0x100d) {
		w.request = bytes.Clone(b)
		return len(b), nil
	}

	data, params, err := w.respond(code, b[12:])
	if err != nil {
		return 0, err
	}

	if data != nil {
		w.packets = append(w.packets, packet(containerData, code, transaction, data))
	}

	reply := []byte{}
	for _, v := range params {
		reply = binary.LittleEndian.AppendUint32(reply, v)
	}

	w.packets = append(w.packets, packet(containerResponse, responseOK, transaction, reply))

	return len(b), nil
}

func (w *modelWire) respond(code uint16, body []byte) ([]byte, []uint32, error) {
	switch code {
	case 0x1002, 0x1003:
		return nil, nil, nil
	case 0x1004:
		return words(1), nil, nil
	case 0x1007, 0x9002:
		parentOffset := 4
		if code == 0x1007 {
			parentOffset = 8
		}

		parent := binary.LittleEndian.Uint32(body[parentOffset:])
		ids := []uint32{}

		for handle, obj := range w.objects {
			if obj.parent == parent && (!obj.hidden || code == 0x9002) {
				ids = append(ids, handle)
			}
		}

		return words(ids...), nil, nil
	case 0x1008:
		obj := w.objects[binary.LittleEndian.Uint32(body)]
		data, err := encodeObject(1, obj.parent, obj.name, len(obj.data), obj.dir)

		return data, nil, err
	case 0x1009:
		return w.objects[binary.LittleEndian.Uint32(body)].data, nil, nil
	case 0x100b:
		delete(w.objects, binary.LittleEndian.Uint32(body))
		return nil, nil, nil
	case 0x100c:
		obj, err := parseObject(99, body)
		if err != nil {
			return nil, nil, err
		}

		w.sending = 99
		w.objects[99] = modelObject{name: obj.entry.Name, parent: obj.parent, dir: obj.entry.Dir, hidden: true}

		return nil, []uint32{1, obj.parent, 99}, nil
	case 0x100d:
		obj := w.objects[w.sending]
		obj.data = bytes.Clone(body)
		w.objects[w.sending] = obj

		return nil, nil, nil
	default:
		return nil, nil, fmt.Errorf("unexpected operation: %04x", code)
	}
}

func words(values ...uint32) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(values))) //nolint:gosec // small test fixtures.
	for _, v := range values {
		b = binary.LittleEndian.AppendUint32(b, v)
	}

	return b
}

func modelDevice(t *testing.T) (*Device, *modelWire) {
	t.Helper()

	w := &modelWire{objects: map[uint32]modelObject{
		1: {name: "GARMIN", parent: 0xffffffff, dir: true},
		2: {name: "Apps", parent: 1, dir: true},
		3: {name: "Media", parent: 2, dir: true, hidden: true},
		4: {name: "OLD.PRG", parent: 3, hidden: true, data: []byte("old app")},
	}}

	d, err := newDevice(t.Context(), w, device.Info{Name: "Garmin test"})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})

	return d, w
}

func TestHiddenFiles(t *testing.T) {
	t.Parallel()
	d, _ := modelDevice(t)

	entries, err := d.List(t.Context(), "garmin/apps/media")
	if err != nil || len(entries) != 0 {
		t.Fatalf("ordinary listing: %v %v", entries, err)
	}

	entries, err = d.ListHidden(t.Context(), "GARMIN/Apps/Media")
	if err != nil || len(entries) != 1 || entries[0].Name != "OLD.PRG" {
		t.Fatalf("hidden listing: %v %v", entries, err)
	}

	data := []byte("new PRG contents")
	if err := d.WriteHidden(t.Context(), "GARMIN/Apps/Media", "OLD.PRG", data); err != nil {
		t.Fatal(err)
	}

	got, err := d.Read(t.Context(), "GARMIN/Apps/Media/old.prg")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read after replace: %q %v", got, err)
	}

	if err := d.DeleteHidden(t.Context(), "GARMIN/Apps/Media", "OLD.PRG"); err != nil {
		t.Fatal(err)
	}

	_, err = d.Stat(t.Context(), "GARMIN/Apps/Media/OLD.PRG")
	if !errors.Is(err, device.ErrNotFound) {
		t.Fatalf("deleted file: %v", err)
	}
}

func TestUnsafePathsNeverWrite(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/", "GARMIN/../bad", "GARMIN/a\\b", "GARMIN/./bad", "GARMIN/Apps/Media/" + string(bytes.Repeat([]byte("x"), 255))} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			d, w := modelDevice(t)
			if err := d.Write(t.Context(), path, []byte("bad")); err == nil {
				t.Fatal("accepted unsafe path")
			}

			if len(w.objects) != 4 {
				t.Fatal("validation changed watch files")
			}
		})
	}
}

func TestMTPSelection(t *testing.T) {
	t.Parallel()

	candidates := []device.Info{{ID: "usb:001:002"}, {ID: "usb:001:003"}}
	if _, err := selectDevice(candidates, ""); err == nil {
		t.Fatal("ambiguous watch selection")
	}

	if _, err := selectDevice(nil, ""); err == nil {
		t.Fatal("selected absent watch")
	}

	got, err := selectDevice(candidates, "usb:001:003")
	if err != nil || got.ID != "usb:001:003" {
		t.Fatalf("explicit ID: %v %v", got, err)
	}
}

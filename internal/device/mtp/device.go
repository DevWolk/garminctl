package mtp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
)

// Device serializes filesystem operations over one native MTP session.
type Device struct {
	mu       sync.Mutex
	protocol protocol
	info     device.Info
	storage  uint32
	closed   bool
	abort    context.CancelFunc
}

var _ device.Aborter = (*Device)(nil)

func newDevice(ctx context.Context, w wire, info device.Info) (*Device, error) {
	lifetime, abort := context.WithCancel(context.Background())

	d := &Device{protocol: protocol{wire: w, lifetime: lifetime}, info: info, abort: abort}
	if _, _, err := d.protocol.exchange(ctx, 0x1002, []uint32{1}, nil); err != nil {
		abort()

		_ = w.Close()

		return nil, fmt.Errorf("open MTP session (close the file manager's watch connection first): %w", err)
	}

	data, _, err := d.protocol.exchange(ctx, 0x1004, nil, nil)
	if err != nil {
		_ = d.Close()
		return nil, err
	}

	ids, err := uint32Array(data)
	if err != nil {
		_ = d.Close()
		return nil, err
	}

	if len(ids) != 1 {
		_ = d.Close()
		return nil, fmt.Errorf("expected one Garmin storage, found %d", len(ids))
	}

	d.storage = ids[0]

	return d, nil
}

// Info identifies the selected Garmin USB device.
func (d *Device) Info() device.Info { return d.info }

func (d *Device) objectInfo(ctx context.Context, handle uint32) (object, error) {
	data, _, err := d.protocol.exchange(ctx, 0x1008, []uint32{handle}, nil)
	if err != nil {
		return object{}, err
	}

	return parseObject(handle, data)
}

func (d *Device) children(ctx context.Context, parent uint32, hidden bool) ([]object, error) {
	data, _, err := d.protocol.exchange(ctx, 0x1007, []uint32{d.storage, 0, parent}, nil)
	if err != nil {
		return nil, err
	}

	handles, err := uint32Array(data)
	if err != nil {
		return nil, err
	}

	if hidden {
		extra, err := d.hiddenHandles(ctx, parent)
		if err != nil {
			return nil, err
		}

		handles = append(handles, extra...)
	}

	slices.Sort(handles)
	handles = slices.Compact(handles)

	out := make([]object, 0, len(handles))
	for _, handle := range handles {
		obj, err := d.objectInfo(ctx, handle)
		if err != nil {
			return nil, err
		}

		if !validName(obj.entry.Name) {
			return nil, errors.New("watch returned an unsafe object name")
		}

		out = append(out, obj)
	}

	return out, nil
}

func (d *Device) hiddenHandles(ctx context.Context, parent uint32) ([]uint32, error) {
	extra, _, err := d.protocol.exchange(ctx, 0x9002, []uint32{d.storage, parent}, nil)
	if err != nil {
		if errors.Is(err, device.ErrDisconnected) || errors.Is(err, device.ErrBusy) {
			return nil, err
		}

		if _, ok := errors.AsType[responseError](err); ok {
			return nil, nil
		}

		return nil, err
	}

	return uint32Array(extra)
}

func (d *Device) resolve(ctx context.Context, path string) (object, error) {
	parts, err := segments(path)
	if err != nil {
		return object{}, err
	}

	current := object{handle: 0xffffffff, entry: device.Entry{Name: "/", Dir: true}}
	for _, part := range parts {
		if !current.entry.Dir {
			return object{}, fmt.Errorf("%s: parent is not a directory", path)
		}

		kids, err := d.children(ctx, current.handle, true)
		if err != nil {
			return object{}, err
		}

		index := slices.IndexFunc(kids, func(o object) bool { return strings.EqualFold(o.entry.Name, part) })
		if index < 0 {
			return object{}, fmt.Errorf("%s: %w", path, device.ErrNotFound)
		}

		current = kids[index]
	}

	return current, nil
}

func (d *Device) list(ctx context.Context, path string, hidden bool) ([]device.Entry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	obj, err := d.resolve(ctx, path)
	if err != nil {
		return nil, err
	}

	if !obj.entry.Dir {
		return nil, errors.New("not a directory")
	}

	kids, err := d.children(ctx, obj.handle, hidden)
	if err != nil {
		return nil, err
	}

	out := make([]device.Entry, 0, len(kids))
	for _, kid := range kids {
		out = append(out, kid.entry)
	}

	return out, nil
}

// List returns the ordinary visible directory listing.
func (d *Device) List(ctx context.Context, path string) ([]device.Entry, error) {
	return d.list(ctx, path, false)
}

// ListHidden also includes Garmin's vendor-specific hidden objects.
func (d *Device) ListHidden(ctx context.Context, path string) ([]device.Entry, error) {
	return d.list(ctx, path, true)
}

// Stat returns metadata for a watch path.
func (d *Device) Stat(ctx context.Context, path string) (device.Entry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	obj, err := d.resolve(ctx, path)

	return obj.entry, err
}

// Read downloads a file from the watch.
func (d *Device) Read(ctx context.Context, path string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	obj, err := d.resolve(ctx, path)
	if err != nil {
		return nil, err
	}

	if obj.entry.Dir {
		return nil, errors.New("cannot read a directory")
	}

	data, _, err := d.protocol.exchange(ctx, 0x1009, []uint32{obj.handle}, nil)

	return data, err
}

// Write creates or replaces a file, using SendObjectInfo followed by SendObject.
func (d *Device) Write(ctx context.Context, path string, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.write(ctx, path, data, false)
}

func (d *Device) write(ctx context.Context, path string, data []byte, dir bool) error {
	parts, err := segments(path)
	if err != nil {
		return err
	}

	if len(parts) == 0 {
		return errors.New("cannot replace storage root")
	}

	name := parts[len(parts)-1]

	parent, err := d.resolve(ctx, strings.Join(parts[:len(parts)-1], "/"))
	if err != nil {
		return err
	}

	if !parent.entry.Dir {
		return errors.New("parent is not a directory")
	}

	dataset, err := encodeObject(d.storage, parent.handle, name, len(data), dir)
	if err != nil {
		return err
	} // Validate before removing an existing file.

	if err := d.prepareWrite(ctx, parent.handle, name, dir); err != nil {
		return err
	}

	_, params, err := d.protocol.exchange(ctx, 0x100c, []uint32{d.storage, parent.handle}, dataset)
	if err != nil {
		return err
	}

	if len(params) != 3 {
		return errors.New("SendObjectInfo returned no object handle")
	}

	handle := params[2]

	if !dir {
		if data == nil {
			data = []byte{}
		} // An empty file still has a data phase.

		if _, _, err := d.protocol.exchange(ctx, 0x100d, nil, data); err != nil {
			return err
		}
	}

	return d.verifyWrite(ctx, handle, len(data), dir)
}

func (d *Device) verifyWrite(ctx context.Context, handle uint32, size int, dir bool) error {
	created, err := d.objectInfo(ctx, handle)
	if err != nil {
		return err
	}

	if created.entry.Dir != dir || (!dir && created.entry.Size != int64(size)) {
		return errors.New("watch reported a different size after writing")
	}

	return nil
}

func (d *Device) prepareWrite(ctx context.Context, parent uint32, name string, dir bool) error {
	kids, err := d.children(ctx, parent, true)
	if err != nil {
		return err
	}

	for _, kid := range kids {
		if !strings.EqualFold(kid.entry.Name, name) {
			continue
		}

		if kid.entry.Dir || dir {
			return errors.New("directory already exists or conflicts with a file")
		}

		_, _, err := d.protocol.exchange(ctx, 0x100b, []uint32{kid.handle, 0}, nil)

		return err
	}

	return nil
}

// WriteHidden writes into a Garmin hidden directory.
func (d *Device) WriteHidden(ctx context.Context, dir, name string, data []byte) error {
	if !validName(name) {
		return errors.New("invalid file name")
	}

	return d.Write(ctx, dir+"/"+name, data)
}

// Mkdir creates a directory. Existing directories are accepted.
func (d *Device) Mkdir(ctx context.Context, path string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	obj, err := d.resolve(ctx, path)
	if err == nil && obj.entry.Dir {
		return nil
	}

	if err != nil && !errors.Is(err, device.ErrNotFound) {
		return err
	}

	return d.write(ctx, path, nil, true)
}

// Delete removes one file or directory from the watch.
func (d *Device) Delete(ctx context.Context, path string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	obj, err := d.resolve(ctx, path)
	if err != nil {
		return err
	}

	if obj.handle == 0xffffffff {
		return errors.New("cannot delete storage root")
	}

	_, _, err = d.protocol.exchange(ctx, 0x100b, []uint32{obj.handle, 0}, nil)

	return err
}

// DeleteHidden removes an object in a Garmin hidden directory.
func (d *Device) DeleteHidden(ctx context.Context, dir, name string) error {
	if !validName(name) {
		return errors.New("invalid file name")
	}

	return d.Delete(ctx, dir+"/"+name)
}

// Storage reports capacity and free space from GetStorageInfo.
func (d *Device) Storage(ctx context.Context) (device.Storage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	data, _, err := d.protocol.exchange(ctx, 0x1005, []uint32{d.storage}, nil)
	if err != nil {
		return device.Storage{}, err
	}

	if len(data) < 27 {
		return device.Storage{}, errors.New("short MTP StorageInfo")
	}

	name, err := stringAt(data, 26)
	if err != nil {
		return device.Storage{}, err
	}

	return device.Storage{CapacityBytes: binary.LittleEndian.Uint64(data[6:]), FreeBytes: binary.LittleEndian.Uint64(data[14:]), Description: name}, nil
}

// Close ends the MTP session and releases USB without resetting the device.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.abort != nil {
		defer d.abort()
	}

	if d.closed {
		return nil
	}

	d.closed = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if !d.protocol.broken {
		_, _, _ = d.protocol.exchange(ctx, 0x1003, nil, nil)
	}

	d.protocol.broken = true

	return d.protocol.wire.Close()
}

// Abort cancels active libusb transfers without taking the operation mutex or
// closing endpoint/config handles concurrently with their transfer cleanup.
// The owner calls Close once the operation has unwound.
func (d *Device) Abort() {
	if d.abort != nil {
		d.abort()
	}
}

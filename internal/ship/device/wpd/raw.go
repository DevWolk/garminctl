package wpd

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/DevWolk/garminctl/internal/ship/device"
)

// Raw PTP/MTP operations reach hidden app folders through Garmin operation
// 0x9002. Objects unavailable through WPD enumeration are written directly
// with SendObjectInfo/SendObject.

const (
	opGetStorageInfo   = 0x1005
	opGetObjectHandles = 0x1007
	opGetObjectInfo    = 0x1008
	opDeleteObject     = 0x100B
	opSendObjectInfo   = 0x100C
	opSendObject       = 0x100D
	opGarminObjectIDs  = 0x9002 // Garmin: object handles including hidden ones

	ptpOK          = 0x2001
	ptpFormatUndef = 0x3000
)

func ptpStatusError(code uint32) error {
	var kind error

	switch code {
	case 0x2003, 0x2004:
		kind = device.ErrDisconnected
	case 0x2009:
		kind = device.ErrNotFound
	case 0x2019, 0x201e:
		kind = device.ErrBusy
	case 0x201d:
		kind = device.ErrInvalidArgument
	}

	if kind == nil {
		return nil
	}

	return fmt.Errorf("%w: PTP response 0x%04X", kind, code)
}

// RawObject retains the WPD API name for the shared diagnostic object model.
type RawObject = device.MTPObject

var (
	_ device.MTPDiagnostics = (*Device)(nil)
	_ device.RawMTPReader   = (*Device)(nil)
)

// Handles returns the PTP storage id and the object handle behind a visible path.
func (d *Device) Handles(ctx context.Context, path string) (storage, handle uint32, err error) {
	obj, stor, err := d.ObjectID(ctx, path)
	if err != nil {
		return 0, 0, err
	}

	h, err := strconv.ParseUint(strings.TrimPrefix(obj, "o"), 16, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("object id %q is not o<hex>: %w", obj, err)
	}

	s, err := strconv.ParseUint(strings.TrimPrefix(stor, "s"), 16, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("storage id %q is not s<hex>: %w", stor, err)
	}

	return uint32(s), uint32(h), nil
}

// ObjectInfo fetches one object's description.
func (d *Device) ObjectInfo(ctx context.Context, handle uint32) (RawObject, error) {
	data, code, _, err := d.MtpRead(ctx, opGetObjectInfo, []uint32{handle})
	if err != nil {
		return RawObject{}, err
	}

	if code != ptpOK {
		return RawObject{}, fmt.Errorf("GetObjectInfo(0x%08X): PTP response 0x%04X", handle, code)
	}

	return parseObjectInfo(handle, data)
}

// AllChildren lists a folder including the objects the ordinary listing hides.
// Both the standard GetObjectHandles and Garmin's 0x9002 are asked; the union is returned.
func (d *Device) AllChildren(ctx context.Context, storage, parent uint32) ([]RawObject, error) {
	seen := map[uint32]bool{}

	var out []RawObject

	for _, op := range []struct {
		code uint32
		prms []uint32
	}{{opGetObjectHandles, []uint32{storage, 0, parent}}, {opGarminObjectIDs, []uint32{storage, parent}}} {
		data, code, _, err := d.MtpRead(ctx, op.code, op.prms)
		if err != nil {
			if errors.Is(err, device.ErrDisconnected) || errors.Is(err, device.ErrBusy) ||
				errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}

			if op.code == opGarminObjectIDs {
				continue // a device without the vendor operation
			}

			return nil, err
		}

		if code != ptpOK || len(data) < 4 {
			continue
		}

		n := binary.LittleEndian.Uint32(data)
		for i := uint32(0); i < n && 8+4*int(i) <= len(data); i++ {
			h := binary.LittleEndian.Uint32(data[4+4*i:])
			if seen[h] {
				continue
			}

			seen[h] = true

			o, err := d.ObjectInfo(ctx, h)
			if err != nil {
				return nil, err
			}

			out = append(out, o)
		}
	}

	return out, nil
}

// FindChild returns the child of parent with the given name, hidden ones included.
func (d *Device) FindChild(ctx context.Context, storage, parent uint32, name string) (RawObject, bool, error) {
	kids, err := d.AllChildren(ctx, storage, parent)
	if err != nil {
		return RawObject{}, false, err
	}

	for _, k := range kids {
		if strings.EqualFold(k.Name, name) {
			return k, true, nil
		}
	}

	return RawObject{}, false, nil
}

// SendFile writes a file into a folder addressed by handle and returns the new handle.
func (d *Device) SendFile(ctx context.Context, storage, parent uint32, name string, data []byte) (uint32, error) {
	size, err := objectSize(data)
	if err != nil {
		return 0, err
	}

	info, err := objectInfoDataset(storage, parent, name, size)
	if err != nil {
		return 0, err
	}

	code, rp, err := d.MtpWrite(ctx, opSendObjectInfo, []uint32{storage, parent}, info)
	if err != nil {
		return 0, fmt.Errorf("SendObjectInfo: %w", err)
	}

	if code != ptpOK || len(rp) < 3 {
		return 0, fmt.Errorf("SendObjectInfo: PTP response 0x%04X params %v", code, rp)
	}

	handle := rp[2]

	code, _, err = d.MtpWrite(ctx, opSendObject, []uint32{}, data)
	if err != nil {
		return handle, fmt.Errorf("SendObject after SendObjectInfo %v: %w", rp, err)
	}

	if code != ptpOK {
		return handle, fmt.Errorf("SendObject: PTP response 0x%04X", code)
	}

	return handle, nil
}

// objectSize is the PTP ObjectCompressedSize of data; files beyond 4 GiB
// cannot be described in an ObjectInfo dataset.
func objectSize(data []byte) (uint32, error) {
	if len(data) > math.MaxUint32 {
		return 0, errors.New("file is larger than MTP can describe (4 GiB)")
	}

	return uint32(len(data)), nil //nolint:gosec // bounds checked above
}

// DeleteHandle deletes one object by handle.
func (d *Device) DeleteHandle(ctx context.Context, handle uint32) error {
	code, _, err := d.MtpNoData(ctx, opDeleteObject, []uint32{handle, 0})
	if err != nil {
		return err
	}

	if code != ptpOK {
		return fmt.Errorf("DeleteObject(0x%08X): PTP response 0x%04X", handle, code)
	}

	return nil
}

// ReadHandle reads an object's bytes by handle (GetObject).
func (d *Device) ReadHandle(ctx context.Context, handle uint32) ([]byte, error) {
	data, code, _, err := d.MtpRead(ctx, 0x1009, []uint32{handle})
	if err != nil {
		return nil, err
	}

	if code != ptpOK {
		return nil, fmt.Errorf("GetObject(0x%08X): PTP response 0x%04X", handle, code)
	}

	return data, nil
}

func parseObjectInfo(handle uint32, b []byte) (RawObject, error) {
	if len(b) < 53 {
		return RawObject{}, fmt.Errorf("GetObjectInfo(0x%08X): short dataset (%d bytes)", handle, len(b))
	}

	o := RawObject{Handle: handle}
	o.Format = binary.LittleEndian.Uint16(b[4:])
	o.Size = binary.LittleEndian.Uint32(b[8:])
	o.Parent = binary.LittleEndian.Uint32(b[38:])

	n := int(b[52])
	if 53+2*n <= len(b) {
		u := make([]uint16, n)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(b[53+2*i:])
		}

		o.Name = strings.TrimRight(string(utf16.Decode(u)), "\x00")
	}

	return o, nil
}

// objectInfoDataset builds the SendObjectInfo dataset with undefined object
// and thumbnail formats (0x3000).
func objectInfoDataset(storage, parent uint32, name string, size uint32) ([]byte, error) {
	// A PTP string carries its length in one byte, terminator included.
	const maxPTPString = 254

	if n := len(utf16.Encode([]rune(name))); n > maxPTPString {
		return nil, fmt.Errorf("name %q is %d UTF-16 units long; PTP allows %d", name, n, maxPTPString)
	}

	b := make([]byte, 0, 64+2*len(name))
	u32 := func(v uint32) { b = binary.LittleEndian.AppendUint32(b, v) }
	u16 := func(v uint16) { b = binary.LittleEndian.AppendUint16(b, v) }
	str := func(s string) {
		if s == "" {
			b = append(b, 0)
			return
		}

		u := utf16.Encode([]rune(s))

		b = append(b, byte(len(u)+1)) //nolint:gosec // bounded by the maxPTPString check above
		for _, c := range u {
			u16(c)
		}

		u16(0)
	}

	u32(storage)
	u16(ptpFormatUndef) // ObjectFormat
	u16(0)              // ProtectionStatus
	u32(size)           // ObjectCompressedSize
	u16(ptpFormatUndef) // ThumbFormat

	for range 6 {
		u32(0) // ThumbCompressedSize, ThumbPixWidth, ThumbPixHeight, ImagePixWidth, ImagePixHeight, ImageBitDepth
	}

	u32(parent)
	u16(0) // AssociationType
	u32(0) // AssociationDesc
	u32(0) // SequenceNumber
	str(name)
	str("") // DateCreated
	str("") // DateModified
	str("") // Keywords

	return b, nil
}

// resolveHidden walks a path through hidden folders and returns its handles.
// The first segment must be visible to the ordinary listing (it always is:
// only sub-folders are hidden).
func (d *Device) resolveHidden(ctx context.Context, dir string) (storage, handle uint32, err error) {
	parts := strings.Split(strings.Trim(dir, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return 0, 0, fmt.Errorf("hidden path %q: empty", dir)
	}

	storage, handle, err = d.Handles(ctx, parts[0])
	if err != nil {
		return 0, 0, err
	}

	for _, p := range parts[1:] {
		o, found, err := d.FindChild(ctx, storage, handle, p)
		if err != nil {
			return 0, 0, err
		}

		if !found {
			return 0, 0, fmt.Errorf("%s: no %q on the watch: %w", dir, p, device.ErrNotFound)
		}

		handle = o.Handle
	}

	return storage, handle, nil
}

// ListHidden lists a folder including the objects the ordinary listing hides.
func (d *Device) ListHidden(ctx context.Context, dir string) ([]device.Entry, error) {
	storage, handle, err := d.resolveHidden(ctx, dir)
	if err != nil {
		return nil, err
	}

	kids, err := d.AllChildren(ctx, storage, handle)
	if err != nil {
		return nil, err
	}

	out := make([]device.Entry, 0, len(kids))
	for _, k := range kids {
		out = append(out, device.Entry{Name: k.Name, Dir: k.IsDir(), Size: int64(k.Size)})
	}

	return out, nil
}

// WriteHidden writes a file into a possibly hidden folder, replacing a file of
// the same name, and verifies the size the watch reports afterwards.
func (d *Device) WriteHidden(ctx context.Context, dir, name string, data []byte) error {
	storage, handle, err := d.resolveHidden(ctx, dir)
	if err != nil {
		return err
	}

	old, found, err := d.FindChild(ctx, storage, handle, name)
	if err != nil {
		return err
	}

	// From the delete of the old file on, the caller's cancellation would
	// leave the watch with neither file; cleanup of a half-sent object must run
	// after it too. Abort still interrupts.
	detached, cancel := device.Detached(ctx, len(data))
	defer cancel()

	if found {
		ctx = detached
		if err := d.DeleteHandle(ctx, old.Handle); err != nil {
			return fmt.Errorf("replace %s/%s: %w", dir, name, err)
		}
	}

	h, err := d.SendFile(ctx, storage, handle, name, data)
	if err != nil {
		if h != 0 {
			_ = d.DeleteHandle(detached, h)
		}

		return fmt.Errorf("write %s/%s: %w", dir, name, err)
	}

	o, found, err := d.FindChild(detached, storage, handle, name)
	if err != nil {
		return err
	}

	if !found || uint64(o.Size) != uint64(len(data)) {
		_ = d.DeleteHandle(detached, h)
		return fmt.Errorf("write %s/%s: the watch lists %d bytes after writing %d", dir, name, o.Size, len(data))
	}

	return nil
}

// DeleteHidden deletes a file from a possibly hidden folder.
func (d *Device) DeleteHidden(ctx context.Context, dir, name string) error {
	storage, handle, err := d.resolveHidden(ctx, dir)
	if err != nil {
		return err
	}

	o, found, err := d.FindChild(ctx, storage, handle, name)
	if err != nil {
		return err
	}

	if !found {
		return device.ErrNotFound
	}

	return d.DeleteHandle(ctx, o.Handle)
}

// Storage reports capacity and free space through PTP GetStorageInfo.
func (d *Device) Storage(ctx context.Context) (device.Storage, error) {
	storage, _, err := d.Handles(ctx, "GARMIN")
	if err != nil {
		return device.Storage{}, err
	}

	data, code, _, err := d.MtpRead(ctx, opGetStorageInfo, []uint32{storage})
	if err != nil {
		return device.Storage{}, err
	}

	if code != ptpOK || len(data) < 27 {
		return device.Storage{}, fmt.Errorf("GetStorageInfo: PTP response 0x%04X, %d bytes", code, len(data))
	}
	// StorageType u16, FilesystemType u16, AccessCapability u16, MaxCapacity u64, FreeSpaceInBytes u64, FreeSpaceInObjects u32, StorageDescription string, VolumeLabel string
	st := device.Storage{CapacityBytes: binary.LittleEndian.Uint64(data[6:]), FreeBytes: binary.LittleEndian.Uint64(data[14:])}
	if n := int(data[26]); 27+2*n <= len(data) && n > 0 {
		u := make([]uint16, n)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(data[27+2*i:])
		}

		st.Description = strings.TrimRight(string(utf16.Decode(u)), "\x00")
	}

	return st, nil
}

// Battery reads MTP BatteryLevel (0x5001). Devices may return a fixed default;
// this value is not exposed as a reliable charge indicator.
func (d *Device) Battery(ctx context.Context) (int, bool, error) {
	data, code, _, err := d.MtpRead(ctx, 0x1015, []uint32{0x5001}) // GetDevicePropValue
	if err != nil {
		return 0, false, err
	}

	if code != ptpOK || len(data) == 0 {
		return 0, false, nil
	}

	v := int(data[0])
	if len(data) >= 2 && data[1] != 0 { // u16 form
		v = int(binary.LittleEndian.Uint16(data))
	}

	if v < 0 || v > 100 {
		return 0, false, nil
	}

	return v, true, nil
}

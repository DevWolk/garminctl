// Package device abstracts watch filesystems through WPD, native Linux MTP
// or DirFS for mass-storage mounts and offline dumps.
package device

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a path does not exist on the device.
var ErrNotFound = errors.New("not found")

// ErrDisconnected means that the transport session can no longer be used.
// It may wrap cancellation after a transaction was admitted to the transport.
var ErrDisconnected = errors.New("watch disconnected")

// ErrBusy means that the device or its interface is in use by another operation.
var ErrBusy = errors.New("watch is busy")

// ErrInvalidArgument identifies rejected device paths or operation parameters.
var ErrInvalidArgument = errors.New("invalid device argument")

// Entry describes a file or directory on the device.
type Entry struct {
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	Modified string `json:"modified,omitempty"`
}

// Storage is the watch's storage capacity as it reports it.
type Storage struct {
	CapacityBytes uint64 `json:"capacityBytes"`
	FreeBytes     uint64 `json:"freeBytes"`
	Description   string `json:"description,omitempty"`
}

// StorageReporter is implemented by devices that can report their storage.
type StorageReporter interface {
	Storage(ctx context.Context) (Storage, error)
}

// Aborter interrupts in-flight I/O without acquiring its transaction lock.
// The connection owner still calls Close after the operation unwinds.
type Aborter interface {
	Abort()
}

// ReconnectPolicy allows a connection owner to forbid transparent reopen inside
// dependent operation groups. Transports without automatic retry need no policy.
type ReconnectPolicy interface {
	SetAutoReconnect(enabled bool)
}

// HiddenFS provides access to folders hidden from ordinary MTP listings.
// Paths use the same slash-separated format as Device.
type HiddenFS interface {
	ListHidden(ctx context.Context, dir string) ([]Entry, error)
	WriteHidden(ctx context.Context, dir, name string, data []byte) error
	DeleteHidden(ctx context.Context, dir, name string) error
}

// MTPObject is the subset of GetObjectInfo used by handle diagnostics.
type MTPObject struct {
	Handle uint32
	Parent uint32
	Format uint16
	Size   uint32
	Name   string
}

// IsDir reports whether the object is a folder (PTP association).
func (o MTPObject) IsDir() bool { return o.Format == 0x3001 }

// RawMTPReader is an optional diagnostic capability. A data-in opcode is not
// necessarily read-only; callers must not add automatic retries for unknown ops.
type RawMTPReader interface {
	MtpRead(ctx context.Context, opcode uint32, params []uint32) ([]byte, uint32, []uint32, error)
}

// MTPDiagnostics provides optional handle-based diagnostics. It is distinct
// from HiddenFS: ordinary app-file operations should use HiddenFS. The current
// WPD implementation supports it; a native MTP transport need not implement it.
// Callers use it only inside the connection owner's active lease.
type MTPDiagnostics interface {
	Handles(ctx context.Context, path string) (uint32, uint32, error)
	AllChildren(ctx context.Context, storage, parent uint32) ([]MTPObject, error)
	FindChild(ctx context.Context, storage, parent uint32, name string) (MTPObject, bool, error)
	SendFile(ctx context.Context, storage, parent uint32, name string, data []byte) (uint32, error)
	ReadHandle(ctx context.Context, handle uint32) ([]byte, error)
	DeleteHandle(ctx context.Context, handle uint32) error
}

// Info identifies a device.
type Info struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Description  string `json:"description,omitempty"`
}

// Device is a file store on the watch. Paths are slash separated and
// relative to the storage root, e.g. "GARMIN/Settings/Settings.fit".
// Name matching is case-insensitive.
type Device interface {
	Info() Info
	List(ctx context.Context, path string) ([]Entry, error)
	Stat(ctx context.Context, path string) (Entry, error)
	Read(ctx context.Context, path string) ([]byte, error)
	Write(ctx context.Context, path string, data []byte) error
	Delete(ctx context.Context, path string) error
	Mkdir(ctx context.Context, path string) error
	Close() error
}

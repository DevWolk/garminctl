// Package device abstracts the watch's file system. Garmin watches in MTP
// mode are reached through the Windows Portable Devices bridge (see wpd);
// a plain directory (a mass-storage mount or a dump) is served by DirFS.
package device

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a path does not exist on the device.
var ErrNotFound = errors.New("not found")

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

// HiddenFS is implemented by devices that can reach folders the ordinary
// listing hides, such as the Connect IQ app folder on the vívoactive 5 and the
// fēnix 8. Paths are the same slash-separated watch paths as elsewhere.
type HiddenFS interface {
	ListHidden(ctx context.Context, dir string) ([]Entry, error)
	WriteHidden(ctx context.Context, dir, name string, data []byte) error
	DeleteHidden(ctx context.Context, dir, name string) error
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

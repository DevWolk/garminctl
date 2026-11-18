package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/device"
)

// BackupSource supplies bounded file operations. The caller owns job admission
// and connection epochs; each call may acquire/release its own Session lease.
type BackupSource interface {
	List(context.Context, string) ([]device.Entry, error)
	ReadFile(context.Context, string) ([]byte, error)
}

// BackupRequest names the destination and top-level folders to skip.
type BackupRequest struct {
	Destination string
	Skip        []string
}

// BackupResult counts successfully copied files and bytes.
type BackupResult struct {
	Files int
	Bytes int
}

// BackupObserver receives synchronous progress outside device operation leases.
type BackupObserver struct {
	Copied  func(path string, bytes int)
	Skipped func(path string, err error)
}

// BackupAction copies device storage through a caller-owned file capability.
type BackupAction struct {
	source   BackupSource
	observer BackupObserver
}

// NewBackupAction uses the supplied file source and optional progress callbacks.
func NewBackupAction(source BackupSource, observer BackupObserver) *BackupAction {
	return &BackupAction{source: source, observer: observer}
}

// Run copies files beneath an os.Root, preserving the existing best-effort read
// policy. It starts no goroutines and never opens or closes a device.
func (a *BackupAction) Run(ctx context.Context, request BackupRequest) (*BackupResult, error) {
	b := &backupRun{source: a.source, observer: a.observer, skip: map[string]bool{}}

	for _, s := range request.Skip {
		if s = strings.TrimSpace(s); s != "" {
			b.skip[strings.ToLower(s)] = true
		}
	}

	if err := os.MkdirAll(request.Destination, 0o750); err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(request.Destination)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	b.root = root
	if err := b.walk(ctx, ""); err != nil {
		return nil, err
	}

	return &b.result, nil
}

type backupRun struct {
	source   BackupSource
	observer BackupObserver
	root     *os.Root
	skip     map[string]bool
	result   BackupResult
}

func (b *backupRun) walk(ctx context.Context, dir string) error {
	entries, err := b.source.List(ctx, dir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		p := e.Name
		if dir != "" {
			p = dir + "/" + e.Name
		}

		if dir == "" && b.skip[strings.ToLower(e.Name)] {
			continue
		}

		if e.Dir {
			if err := b.walk(ctx, p); err != nil {
				return err
			}

			continue
		}

		if err := b.copyFile(ctx, p); err != nil {
			return err
		}
	}

	return nil
}

func (b *backupRun) copyFile(ctx context.Context, p string) error {
	data, err := b.source.ReadFile(ctx, p)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, device.ErrDisconnected) {
			return err
		}

		if b.observer.Skipped != nil {
			b.observer.Skipped(p, err)
		}

		return nil
	}

	rel := filepath.FromSlash(p)
	if dir := filepath.Dir(rel); dir != "." {
		if err := b.root.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}

	if err := b.root.WriteFile(rel, data, 0o600); err != nil {
		return err
	}

	b.result.Files++

	b.result.Bytes += len(data)
	if b.observer.Copied != nil {
		b.observer.Copied(p, len(data))
	}

	return nil
}

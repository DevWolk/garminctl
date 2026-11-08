package updatetransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/DevWolk/garminctl/internal/hostfile"
)

const (
	historyFile       = "history.json"
	maxHistoryBytes   = maxPages * maxPageBytes
	maxHistoryRecords = maxPages * pageSize
)

// HistorySnapshot contains display metadata, never package/signature authority.
type HistorySnapshot struct {
	CheckedAt time.Time
	Releases  []Release
}

// HistoryCache persists complete snapshots in one dedicated private directory.
// Concurrent processes replace whole files; the last successful writer wins.
type HistoryCache struct{ directory string }

type historyEnvelope struct {
	Version   int             `json:"version"`
	Source    string          `json:"source"`
	CheckedAt time.Time       `json:"checkedAt"`
	Releases  historyReleases `json:"releases"`
}

// NewHistoryCache validates a caller-selected cache directory without IO.
func NewHistoryCache(directory string) (*HistoryCache, error) {
	if directory == "" {
		return nil, errors.New("release history requires a dedicated cache directory")
	}

	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}

	return &HistoryCache{directory: absolute}, nil
}

// Load reads one bounded, versioned snapshot; missing or invalid files are errors.
func (c *HistoryCache) Load(ctx context.Context) (*HistorySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if c == nil || c.directory == "" {
		return nil, errors.New("release history cache is not configured")
	}

	if err := hostfile.Protect(c.directory, true); err != nil {
		return nil, err
	}

	if err := hostfile.Protect(filepath.Join(c.directory, historyFile), false); err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(c.directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	data, err := readHistoryFile(root)
	if err != nil {
		return nil, err
	}

	snapshot, err := decodeHistory(data)

	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}

	return snapshot, err
}

func readHistoryFile(root *os.Root) ([]byte, error) {
	file, err := root.Open(historyFile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() || info.Size() > maxHistoryBytes {
		return nil, errors.New("invalid release history file size or type")
	}

	data, err := io.ReadAll(io.LimitReader(file, maxHistoryBytes+1))
	if err != nil {
		return nil, err
	}

	if len(data) > maxHistoryBytes {
		return nil, errors.New("release history exceeds the byte limit")
	}

	return data, nil
}

// Save publishes a complete owner-only file using same-directory replacement.
// A cancellation before replacement leaves the old snapshot in place.
func (c *HistoryCache) Save(ctx context.Context, snapshot HistorySnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if c == nil || c.directory == "" {
		return errors.New("release history cache is not configured")
	}

	data, err := encodeHistory(snapshot)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(c.directory, 0o700); err != nil {
		return err
	} // #nosec G703 -- dedicated local composition path, never request metadata.

	if err := hostfile.Protect(c.directory, true); err != nil {
		return err
	}

	return writeHistoryFile(ctx, c.directory, data, hostfile.Replace)
}

func encodeHistory(snapshot HistorySnapshot) ([]byte, error) {
	if snapshot.CheckedAt.IsZero() || snapshot.Releases == nil || len(snapshot.Releases) > maxHistoryRecords {
		return nil, errors.New("incomplete or oversized release history snapshot")
	}

	remaining := maxHistoryBytes

	for _, release := range snapshot.Releases {
		for _, text := range []string{release.Tag, release.Name, release.Notes} {
			if len(text) > remaining {
				return nil, errors.New("release history exceeds the byte limit")
			}

			remaining -= len(text)
		}
	}

	data, err := json.Marshal(historyEnvelope{Version: 1, Source: releasesEndpoint, CheckedAt: snapshot.CheckedAt, Releases: historyReleases(snapshot.Releases)})
	if err != nil {
		return nil, err
	}

	if len(data) > maxHistoryBytes {
		return nil, errors.New("release history exceeds the byte limit")
	}

	return data, nil
}

func decodeHistory(data []byte) (*HistorySnapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var wire historyEnvelope
	if err := decoder.Decode(&wire); err != nil {
		return nil, err
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("unexpected trailing release history data")
	}

	if wire.Version != 1 || wire.Source != releasesEndpoint || wire.CheckedAt.IsZero() || wire.Releases == nil {
		return nil, errors.New("invalid release history schema, source or timestamp")
	}

	return &HistorySnapshot{CheckedAt: wire.CheckedAt, Releases: []Release(wire.Releases)}, nil
}

type historyReleases []Release

func (r *historyReleases) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	token, err := decoder.Token()
	if err != nil {
		return err
	}

	if token != json.Delim('[') {
		return errors.New("release history requires an array")
	}

	items := make(historyReleases, 0)
	for decoder.More() {
		if len(items) == maxHistoryRecords {
			return errors.New("release history exceeds the record limit")
		}

		var item Release
		if err := decoder.Decode(&item); err != nil {
			return err
		}

		items = append(items, item)
	}

	if _, err := decoder.Token(); err != nil {
		return err
	}

	*r = items

	return nil
}

func writeHistoryFile(ctx context.Context, directory string, data []byte, replace func(string, string) error) (err error) {
	file, err := os.CreateTemp(directory, ".history-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		if cleanup := os.Remove(file.Name()); cleanup != nil && !errors.Is(cleanup, os.ErrNotExist) {
			err = errors.Join(err, cleanup)
		}
	}()

	if err := hostfile.Protect(file.Name(), false); err != nil {
		return err
	}

	if _, err := file.Write(data); err != nil {
		return err
	}

	if err := file.Sync(); err != nil {
		return err
	}

	if err := file.Close(); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	return replace(file.Name(), filepath.Join(directory, historyFile))
}

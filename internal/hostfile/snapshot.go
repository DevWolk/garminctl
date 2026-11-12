package hostfile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const (
	snapshotFileLimit     = 64 << 20
	snapshotTotalLimit    = 512 << 20
	snapshotEntryLimit    = 4096
	snapshotMetadataLimit = 64 << 10
)

// SnapshotKind constrains selected paths to regular files or real directories.
type SnapshotKind int

// Selection never follows an ignored directory.
const (
	SnapshotIgnore SnapshotKind = iota
	SnapshotFile
	SnapshotDirectory
)

// SnapshotSource names a trusted local root; no source path is stored in the manifest.
type SnapshotSource struct{ Name, Directory string }

// SnapshotFileInfo describes the bytes retained under Area/Path.
type SnapshotFileInfo struct {
	Area   string `json:"area"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Digest string `json:"sha256"`
}

// SnapshotManifest versions this copy format, not the source data schema.
type SnapshotManifest struct {
	Version        int                `json:"version"`
	ID             string             `json:"id"`
	CreatedAt      time.Time          `json:"createdAt"`
	Metadata       json.RawMessage    `json:"metadata"`
	MissingSources []string           `json:"missingSources"`
	Files          []SnapshotFileInfo `json:"files"`
}

// Snapshot is one whole-directory publication.
type Snapshot struct {
	SnapshotManifest
	Directory string
}

// SnapshotStore copies selected local files without interpreting their contents.
type SnapshotStore struct {
	destination string
	sources     []SnapshotSource
	selectPath  func(string, string) SnapshotKind
}

// NewSnapshotStore performs no IO; selection and roots come from trusted composition.
func NewSnapshotStore(destination string, sources []SnapshotSource, selectPath func(string, string) SnapshotKind) *SnapshotStore {
	return &SnapshotStore{destination: destination, sources: append([]SnapshotSource(nil), sources...), selectPath: selectPath}
}

// Create publishes a complete protected copy or removes only its own pending directory.
func (s *SnapshotStore) Create(ctx context.Context, metadata json.RawMessage) (result *Snapshot, err error) {
	if err := s.validate(ctx, metadata); err != nil {
		return nil, err
	}

	parent, err := s.openDestination()
	if err != nil {
		return nil, err
	}
	defer parent.Close()

	original, err := s.capture(ctx)
	if err != nil {
		return nil, err
	}
	defer original.close()

	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}

	name := hex.EncodeToString(id)

	pending := ".pending-" + name
	if err := parent.Mkdir(pending, 0o700); err != nil {
		return nil, err
	}

	defer func() {
		if result == nil {
			err = errors.Join(err, parent.RemoveAll(pending))
		}
	}()

	if err := Protect(filepath.Join(s.destination, pending), true); err != nil {
		return nil, err
	}

	destination, err := parent.OpenRoot(pending)
	if err != nil {
		return nil, err
	}
	defer destination.Close()

	manifest := SnapshotManifest{Version: 1, ID: name, CreatedAt: time.Now().UTC(), Metadata: slices.Clone(metadata), MissingSources: original.missing(), Files: []SnapshotFileInfo{}}
	for _, file := range original.files {
		value, err := copySnapshotFile(ctx, destination, original.roots[file.source].root, file, filepath.Join(s.destination, pending))
		if err != nil {
			return nil, err
		}

		manifest.Files = append(manifest.Files, value)
	}

	if err := s.verifySource(ctx, original); err != nil {
		return nil, err
	}

	if err := writeSnapshotManifest(ctx, destination, manifest, filepath.Join(s.destination, pending)); err != nil {
		return nil, err
	}

	if err := checkSnapshotDestination(parent, s.destination); err != nil {
		return nil, err
	}

	if err := publishSnapshot(ctx, parent, destination, pending, name); err != nil {
		return nil, err
	}

	return &Snapshot{SnapshotManifest: manifest, Directory: filepath.Join(s.destination, name)}, nil
}

func checkSnapshotDestination(root *os.Root, path string) error {
	opened, err := root.Stat(".")
	if err != nil {
		return err
	}

	current, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if !current.IsDir() || !os.SameFile(opened, current) {
		return errors.New("snapshot destination directory changed")
	}

	return nil
}

func (s *SnapshotStore) validate(ctx context.Context, metadata json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if s.selectPath == nil || !filepath.IsAbs(s.destination) || len(s.sources) == 0 || len(metadata) > snapshotMetadataLimit || !json.Valid(metadata) {
		return errors.New("invalid local snapshot configuration or metadata")
	}

	seen := make(map[string]bool)
	for _, source := range s.sources {
		if !validSnapshotSource(source) || seen[source.Name] {
			return errors.New("snapshot sources require unique local names and absolute roots")
		}

		seen[source.Name] = true

		relative, err := filepath.Rel(source.Directory, s.destination)
		if err == nil && (relative == "." || s.selectPath(source.Name, filepath.ToSlash(relative)) != SnapshotIgnore) {
			return errors.New("snapshot destination overlaps selected source data")
		}
	}

	return nil
}

func validSnapshotSource(source SnapshotSource) bool {
	return source.Name != "" && source.Name != "." && filepath.Base(source.Name) == source.Name && filepath.IsLocal(source.Name) && filepath.IsAbs(source.Directory)
}

func publishSnapshot(ctx context.Context, parent, destination *os.Root, pending, name string) error {
	if err := destination.Close(); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if _, err := parent.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return errors.New("snapshot destination already exists or is unreadable")
	}

	return parent.Rename(pending, name)
}

func (s *SnapshotStore) openDestination() (*os.Root, error) {
	if err := os.MkdirAll(s.destination, 0o700); err != nil {
		return nil, err
	}

	if err := Protect(s.destination, true); err != nil {
		return nil, err
	}

	return os.OpenRoot(s.destination)
}

func (s *SnapshotStore) verifySource(ctx context.Context, original *snapshotCapture) error {
	again, err := s.capture(ctx)
	if err != nil {
		return err
	}
	defer again.close()

	if !original.equal(again) {
		return errors.New("local state changed while preparing its snapshot")
	}

	return ctx.Err()
}

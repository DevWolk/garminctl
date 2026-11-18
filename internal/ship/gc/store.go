package gc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func prepareStoreDir(path string) error {
	if path == "" {
		return errors.New("garmin connect config path is unavailable; configure the user config directory")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil { // #nosec G703 -- local caller-selected account directory, never a watch or HTTP path
		return fmt.Errorf("create Garmin Connect config directory: %w", err)
	}

	if err := protectStorePath(dir, true); err != nil {
		return fmt.Errorf("protect Garmin Connect config directory: %w", err)
	}

	return nil
}

func readStore(path string) ([]byte, error) {
	if err := prepareStoreDir(path); err != nil {
		return nil, err
	}

	if err := protectStorePath(path, false); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("protect Garmin Connect session: %w", err)
	}

	b, err := os.ReadFile(path) //nolint:gosec // the path is the caller-selected, protected account store
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	return b, err
}

func loadSession(path string) (Session, error) {
	b, err := readStore(path)
	if err != nil {
		return Session{}, err
	}

	if b == nil {
		return Session{}, nil
	}

	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return Session{}, fmt.Errorf("read Garmin Connect session: %w", err)
	}

	return s, nil
}

// writeStore replaces a complete protected file in the same directory. No
// caller may publish new in-memory credentials until this succeeds.
func writeStore(path string, data []byte) (err error) {
	if err := prepareStoreDir(path); err != nil {
		return err
	}

	f, err := os.CreateTemp(filepath.Dir(path), ".garminctl-*.tmp")
	if err != nil {
		return fmt.Errorf("create Garmin Connect session temporary file: %w", err)
	}

	defer func() {
		_ = f.Close() // closed below on success; also release the descriptor on errors

		if cleanupErr := os.Remove(f.Name()); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) { // #nosec G703 -- remove only our own CreateTemp output
			err = errors.Join(err, fmt.Errorf("remove session temporary file: %w", cleanupErr))
		}
	}()

	if err := writeStoreTemp(f, data); err != nil {
		return fmt.Errorf("write Garmin Connect session temporary file: %w", err)
	}

	if err := replaceStore(f.Name(), path); err != nil {
		return fmt.Errorf("replace Garmin Connect session: %w", err)
	}

	return nil
}

func writeStoreTemp(f *os.File, data []byte) error {
	if err := protectStorePath(f.Name(), false); err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		return err
	}

	if err := f.Sync(); err != nil {
		return err
	}

	return f.Close()
}

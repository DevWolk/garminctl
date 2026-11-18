package connect

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DevWolk/garminctl/internal/ship/hostfile"
)

type ledger map[string]string // watch path -> "size|modified"

func (c *Cloud) ledgerPath(unit uint64) string {
	return filepath.Join(c.cacheDir, fmt.Sprintf("uploaded-%d.json", unit))
}

func (c *Cloud) loadLedger(unit uint64) (ledger, error) {
	for _, item := range []struct {
		path      string
		directory bool
	}{{c.cacheDir, true}, {c.ledgerPath(unit), false}} {
		if err := hostfile.Protect(item.path, item.directory); errors.Is(err, os.ErrNotExist) {
			return ledger{}, nil
		} else if err != nil {
			return nil, err
		}
	}

	data, err := os.ReadFile(c.ledgerPath(unit))
	if err != nil {
		return nil, err
	}

	var l ledger
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}

	if l == nil {
		return nil, errors.New("upload ledger must be a JSON object")
	}

	return l, nil
}

func (c *Cloud) saveLedger(unit uint64, l ledger) error {
	data, err := json.MarshalIndent(l, "", " ")
	if err != nil {
		return err
	}

	return writeUploadLedger(c.ledgerPath(unit), data, hostfile.Replace)
}

func prepareUploadLedger(target string) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	if err := hostfile.Protect(dir, true); err != nil {
		return err
	}

	if err := hostfile.Protect(target, false); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

// writeUploadLedger preserves the previous entry until a complete protected
// temporary file has been written, synced and closed. Replace is injectable for
// local filesystem fault tests; production uses the platform hostfile primitive.
func writeUploadLedger(target string, data []byte, replace func(string, string) error) (err error) {
	if err := prepareUploadLedger(target); err != nil {
		return err
	}

	f, err := os.CreateTemp(filepath.Dir(target), ".uploaded-*.tmp")
	if err != nil {
		return err
	}

	defer func() {
		_ = f.Close()
		if cleanup := os.Remove(f.Name()); cleanup != nil && !errors.Is(cleanup, os.ErrNotExist) {
			err = errors.Join(err, cleanup)
		}
	}()

	if err := hostfile.Protect(f.Name(), false); err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		return err
	}

	if err := f.Sync(); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return replace(f.Name(), target)
}

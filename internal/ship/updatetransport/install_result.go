package updatetransport

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	installResultName = "install-result.json"
	maxInstallResult  = 64 << 10
	maxInstallRecords = 4096
)

// Installation outcomes recorded by the helper after an accepted Commit.
const (
	InstallSucceeded = "installed"
	InstallFailed    = "failed"
	InstallUnknown   = "unknown"
)

type installResultRecord struct {
	Operation string `json:"operation"`
	Tag       string `json:"tag"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

// InstallResult is what the helper recorded for one committed installation.
// Directory holds its logs; Finished is when the record was written.
type InstallResult struct {
	Tag, Status, Error, Directory string
	Finished                      time.Time
}

// LastInstallResult returns the most recent record under the installer cache,
// or nil when no committed installation has left one. It reads local metadata
// only and verifies no package.
func LastInstallResult(cache string) (*InstallResult, error) {
	root, err := os.OpenRoot(cache)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	defer root.Close()

	listing, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = listing.Close() }()

	entries, err := listing.ReadDir(maxInstallRecords)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	var (
		latest *InstallResult
		failed error
	)

	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "stage-") {
			continue
		}

		result, err := readInstallResult(root, entry.Name())
		if err != nil {
			failed = errors.Join(failed, err)
			continue
		}

		if result != nil && (latest == nil || result.Finished.After(latest.Finished)) {
			result.Directory = filepath.Join(cache, entry.Name())
			latest = result
		}
	}

	if latest == nil {
		return nil, failed
	}

	return latest, nil
}

func readInstallResult(root *os.Root, operation string) (*InstallResult, error) {
	directory, err := root.OpenRoot(operation)
	if err != nil {
		return nil, err
	}
	defer directory.Close()

	file, err := directory.Open(installResultName)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() || info.Size() > maxInstallResult {
		return nil, errors.New("invalid installation result file")
	}

	data, err := io.ReadAll(io.LimitReader(file, maxInstallResult+1))
	if err != nil {
		return nil, err
	}

	var record installResultRecord
	if err := decodeStageJSON(data, &record); err != nil {
		return nil, err
	}

	known := record.Status == InstallSucceeded || record.Status == InstallFailed || record.Status == InstallUnknown
	if !known || !publisherVersion.MatchString(record.Tag) {
		return nil, errors.New("invalid installation result record")
	}

	return &InstallResult{Tag: record.Tag, Status: record.Status, Error: record.Error, Finished: info.ModTime()}, nil
}

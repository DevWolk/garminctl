package updatetransport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installResultFixture(t *testing.T, cache, operation, data string, finished time.Time) {
	t.Helper()

	directory := filepath.Join(cache, operation)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}

	if data == "" {
		return
	}

	path := filepath.Join(directory, installResultName)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(path, finished, finished); err != nil {
		t.Fatal(err)
	}
}

func TestLastInstallResultReadsTheNewestRecord(t *testing.T) {
	t.Parallel()

	cache := filepath.Join(t.TempDir(), "application-installing")
	if result, err := LastInstallResult(cache); result != nil || err != nil {
		t.Fatalf("missing cache=%+v %v", result, err)
	}

	now := time.Now().Truncate(time.Second)
	installResultFixture(t, cache, "stage-1", `{"operation":"a","tag":"v1.2.3","status":"installed"}`, now.Add(-2*time.Hour))
	installResultFixture(t, cache, "stage-2", `{"operation":"b","tag":"v2.0.1","status":"failed","error":"exit status 126"}`, now.Add(-time.Hour))
	installResultFixture(t, cache, "stage-3", "", now)
	installResultFixture(t, cache, "other", `{"operation":"c","tag":"v9.9.9","status":"installed"}`, now)

	result, err := LastInstallResult(cache)
	if err != nil || result == nil {
		t.Fatalf("result=%+v %v", result, err)
	}

	want := InstallResult{Tag: "v2.0.1", Status: InstallFailed, Error: "exit status 126", Directory: filepath.Join(cache, "stage-2"), Finished: now.Add(-time.Hour)}
	if result.Tag != want.Tag || result.Status != want.Status || result.Error != want.Error || result.Directory != want.Directory || !result.Finished.Equal(want.Finished) {
		t.Fatalf("result=%+v, want %+v", *result, want)
	}
}

func TestLastInstallResultReadsWhatTheHelperWrites(t *testing.T) {
	t.Parallel()

	cache := t.TempDir()
	directory := filepath.Join(cache, "stage-written")

	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	request := installRequest{Operation: strings.Repeat("a", 64), Target: MatchRequest{ReleaseTag: installTestTag}}
	if err := writeInstallResult(root, directory, request, errInstallerOutcomeUnknown); err != nil {
		t.Fatal(err)
	}

	result, err := LastInstallResult(cache)
	if err != nil || result == nil || result.Tag != installTestTag || result.Status != InstallUnknown || result.Error != errInstallerOutcomeUnknown.Error() {
		t.Fatalf("result=%+v %v", result, err)
	}
}

func TestLastInstallResultSkipsUnreadableRecords(t *testing.T) {
	t.Parallel()

	cache := t.TempDir()
	now := time.Now()

	for name, data := range map[string]string{
		"stage-unknown-field": `{"operation":"a","tag":"v1.2.3","status":"installed","extra":1}`,
		"stage-status":        `{"operation":"a","tag":"v1.2.3","status":"done"}`,
		"stage-tag":           `{"operation":"a","tag":"../x","status":"installed"}`,
		"stage-oversize":      `{"operation":"a","tag":"v1.2.3","status":"failed","error":"` + strings.Repeat("x", maxInstallResult) + `"}`,
	} {
		installResultFixture(t, cache, name, data, now)
	}

	if result, err := LastInstallResult(cache); result != nil || err == nil {
		t.Fatalf("only unreadable records=%+v %v", result, err)
	}

	installResultFixture(t, cache, "stage-good", `{"operation":"b","tag":"v2.0.2","status":"installed"}`, now.Add(-time.Hour))

	result, err := LastInstallResult(cache)
	if err != nil || result == nil || result.Tag != "v2.0.2" {
		t.Fatalf("readable record hidden by unreadable ones: %+v %v", result, err)
	}
}

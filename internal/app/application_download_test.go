package app

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

const (
	applicationDownloadTag = "v1.2.0"
	downloadTestBuild      = "v1.0.0"
	downloadTestKeyEnv     = "GARMINCTL_SYNTHETIC_DOWNLOAD_TEST_KEY"
)

var (
	downloadTestBytes  = []byte("synthetic application package; never executable")
	downloadPrivateKey *rsa.PrivateKey
	downloadPublicKey  []byte
)

func initializeDownloadTestKey() error {
	encoded := os.Getenv(downloadTestKeyEnv)
	if encoded == "" {
		key, err := rsa.GenerateKey(rand.Reader, 3072)
		if err != nil {
			return err
		}

		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return err
		}

		encoded = base64.StdEncoding.EncodeToString(der)
		if err := os.Setenv(downloadTestKeyEnv, encoded); err != nil {
			return err
		}
	}

	der, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return err
	}

	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return err
	}

	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return errors.New("synthetic application key is not RSA")
	}

	downloadPrivateKey = key
	downloadPublicKey, err = x509.MarshalPKIXPublicKey(&key.PublicKey)

	return err
}

type downloadTestHTTP func(*http.Request) (*http.Response, error)

func (f downloadTestHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func downloadTestResponse(body []byte) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/octet-stream"}}, Body: io.NopCloser(bytes.NewReader(body))}
}

func TestMain(m *testing.M) {
	if err := initializeDownloadTestKey(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if len(os.Args) == 2 && os.Args[1] == updatetransport.InstallCommand {
		if err := runApplicationInstall(context.Background(), downloadTestBuild, base64.StdEncoding.EncodeToString(downloadPublicKey), os.Stdin, os.Stdout, installTestHostCheck()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		os.Exit(0)
	}

	if len(os.Args) != 2 || (os.Args[1] != updatetransport.StageCommand && os.Args[1] != updatetransport.VerifyCommand) {
		os.Exit(m.Run())
	}

	if err := downloadTestWorker(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	os.Exit(0)
}

func downloadTestWorker() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	digest := sha256.Sum256(downloadTestBytes)

	signature, err := rsa.SignPKCS1v15(nil, downloadPrivateKey, crypto.SHA256, digest[:])
	if err != nil {
		return err
	}

	input, err := io.ReadAll(io.LimitReader(os.Stdin, (64<<10)+1))
	if err != nil {
		return err
	}

	var request struct {
		Target updatetransport.MatchRequest `json:"target"`
	}
	if err := json.Unmarshal(input, &request); err != nil {
		return err
	}

	if strings.HasSuffix(request.Target.ReleaseTag, "-bad") {
		signature[0] ^= 0xff
	}

	client := &http.Client{Transport: downloadTestHTTP(func(r *http.Request) (*http.Response, error) {
		if os.Args[1] == updatetransport.VerifyCommand {
			return nil, errors.New("offline verifier attempted HTTP")
		}

		switch r.URL.Path {
		case "/repos/DevWolk/garminctl/releases/assets/101":
			return downloadTestResponse(downloadTestBytes), nil
		case "/repos/DevWolk/garminctl/releases/assets/102":
			return downloadTestResponse(signature), nil
		}

		return nil, fmt.Errorf("unexpected fixture URL %s", r.URL)
	})}

	config := updatetransport.WorkerConfig{Directory: filepath.Dir(executable), BuildVersion: downloadTestBuild, PublicKey: bytes.Clone(downloadPublicKey), HTTPClient: client}
	if os.Args[1] == updatetransport.VerifyCommand {
		return updatetransport.RunVerifyWorker(context.Background(), config, bytes.NewReader(input), os.Stdout)
	}

	return updatetransport.RunStageWorker(context.Background(), config, bytes.NewReader(input), os.Stdout)
}

func downloadPipeline(t *testing.T, tag string, draft bool) (applicationPackageDownload, application.DownloadTarget) {
	t.Helper()

	return downloadSeriesPipeline(t, draft, tag)
}

// downloadSeriesPipeline publishes every given tag, so that one cache can
// hold several releases.
func downloadSeriesPipeline(t *testing.T, draft bool, tags ...string) (applicationPackageDownload, application.DownloadTarget) {
	t.Helper()

	if runtime.GOARCH != "amd64" || (runtime.GOOS != "linux" && runtime.GOOS != "windows") {
		t.Skip("synthetic native Setup/DEB fixture requires an amd64 publishing platform")
	}

	cli, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	kind, setup := "deb", runtime.GOOS == "windows"
	if setup {
		kind = "setup"
	}

	const firstReleaseID = 11

	client := &http.Client{Transport: downloadTestHTTP(func(r *http.Request) (*http.Response, error) {
		for index, tag := range tags {
			id := firstReleaseID + index

			var value any

			switch r.URL.Path {
			case "/repos/DevWolk/garminctl/releases/tags/" + tag:
				value = map[string]any{"id": id, "tag_name": tag, "draft": draft}
			case fmt.Sprintf("/repos/DevWolk/garminctl/releases/%d/assets", id):
				name := "garminctl_" + strings.ReplaceAll(strings.TrimPrefix(tag, "v"), "-", ".") + "_linux_amd64.deb"
				if setup {
					name = "garminctl_" + tag + "_windows_amd64_setup.exe"
				}

				value = []updatetransport.AssetRef{{ID: 101, Name: name, Size: int64(len(downloadTestBytes)), URL: "https://api.github.com/repos/DevWolk/garminctl/releases/assets/101"}, {ID: 102, Name: name + ".sig", Size: 384, URL: "https://api.github.com/repos/DevWolk/garminctl/releases/assets/102"}}
			default:
				continue
			}

			body, err := json.Marshal(value)

			return downloadTestResponse(body), err
		}

		return nil, fmt.Errorf("unexpected parent HTTP %s", r.URL)
	})}
	directory := t.TempDir()

	return applicationPackageDownload{source: updatetransport.NewGitHub(client), key: base64.StdEncoding.EncodeToString(downloadPublicKey), stageDirectory: filepath.Join(directory, "stages"), packageDirectory: filepath.Join(directory, "packages")}, application.DownloadTarget{OS: runtime.GOOS, Arch: runtime.GOARCH, Installation: kind, CLIPath: cli, CLIVersion: downloadTestBuild, IncludePrerelease: true}
}

func TestApplicationDownloadUsesRealCachedCLIAndReverifiesRetainedPackage(t *testing.T) {
	t.Parallel()
	pipeline, target := downloadPipeline(t, applicationDownloadTag, false)

	var phases []string

	result, err := pipeline.Download(t.Context(), application.Release{Tag: applicationDownloadTag}, target, func(p application.DownloadProgress) { phases = append(phases, p.Phase) })
	if err != nil || result == nil || result.CacheID == "" || result.Bytes != int64(len(downloadTestBytes)) {
		t.Fatalf("result=%+v %v phases=%v", result, err, phases)
	}

	again, err := pipeline.Download(t.Context(), application.Release{Tag: applicationDownloadTag}, target, nil)
	if err != nil || again == nil || *again != *result {
		t.Fatalf("retained=%+v %v", again, err)
	}

	assertDownloadOperationsClean(t, pipeline)
}

type downloadTestInventory struct{ value *application.InventoryResult }

func (f downloadTestInventory) Info(context.Context) (*application.InventoryResult, error) {
	return f.value, nil
}

type downloadTestFeed []application.Release

func (f downloadTestFeed) Releases(context.Context) ([]application.Release, error) { return f, nil }

// A selection that is already saved is only verified again, so nothing marks
// it as recently saved. Saving the running version beside it then triggers
// retention, which must not remove the selection the download reports.
func TestApplicationDownloadKeepsAnOlderSavedSelectionBesideTheRunningVersion(t *testing.T) {
	t.Parallel()

	saved := []string{"v2.1.0", "v2.2.0", "v2.3.0", "v2.4.0", "v2.5.0"}
	if len(saved) != application.RetainedPackages {
		t.Fatalf("the fixture must fill the retention limit of %d", application.RetainedPackages)
	}

	pipeline, target := downloadSeriesPipeline(t, false, append(slices.Clone(saved), downloadTestBuild)...)
	releases := downloadTestFeed{{Tag: downloadTestBuild}}
	oldest := time.Now().Add(-time.Hour)

	for age, tag := range saved {
		releases = append(releases, application.Release{Tag: tag})

		result, err := pipeline.Download(t.Context(), application.Release{Tag: tag}, target, nil)
		if err != nil {
			t.Fatal(err)
		}

		when := oldest.Add(time.Duration(age) * time.Minute)
		if err := os.Chtimes(filepath.Join(pipeline.packageDirectory, result.CacheID), when, when); err != nil {
			t.Fatal(err)
		}
	}

	info := &application.InventoryResult{OS: target.OS, Arch: target.Arch, Desktop: application.BinaryVersion{Version: downloadTestBuild}, CLI: application.BinaryVersion{Path: target.CLIPath, Version: downloadTestBuild}, Installation: application.InstallationInfo{Kind: target.Installation}}
	action := application.NewDownloadReleaseAction(downloadTestInventory{info}, releases, pipeline)

	result, err := action.Run(t.Context(), application.DownloadRequest{Tag: saved[0]}, nil)
	if err != nil || result == nil || result.Tag != saved[0] {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	if _, err := os.Stat(filepath.Join(pipeline.packageDirectory, result.CacheID)); err != nil {
		t.Fatalf("the download reported %s as saved, but it is not in the cache: %v", saved[0], err)
	}

	var phases []string

	running, err := pipeline.Download(t.Context(), application.Release{Tag: downloadTestBuild}, target, func(p application.DownloadProgress) { phases = append(phases, p.Phase) })
	if err != nil || running == nil || slices.Contains(phases, "downloading") {
		t.Fatalf("the running version was not saved by the download: result=%+v err=%v phases=%q", running, err, phases)
	}

	assertDownloadOperationsClean(t, pipeline)
}

// savedPackageFile names the package inside a saved catalog entry.
func savedPackageFile(t *testing.T, pipeline applicationPackageDownload, id string) string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(pipeline.packageDirectory, id))
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".deb") || strings.HasSuffix(entry.Name(), ".exe") {
			return filepath.Join(pipeline.packageDirectory, id, entry.Name())
		}
	}

	t.Fatalf("no package file in %v", entries)

	return ""
}

func TestApplicationDownloadReplacesASavedPackageThatNoLongerVerifies(t *testing.T) {
	t.Parallel()
	pipeline, target := downloadPipeline(t, applicationDownloadTag, false)

	result, err := pipeline.Download(t.Context(), application.Release{Tag: applicationDownloadTag}, target, nil)
	if err != nil {
		t.Fatal(err)
	}

	saved := savedPackageFile(t, pipeline, result.CacheID)
	if err := os.WriteFile(saved, bytes.Repeat([]byte{0}, len(downloadTestBytes)), 0o600); err != nil {
		t.Fatal(err)
	}

	// The damaged copy is never accepted: the release is downloaded again and
	// the verified bytes replace the entry under the same identity.
	repaired, err := pipeline.Download(t.Context(), application.Release{Tag: applicationDownloadTag}, target, nil)
	if err != nil || repaired == nil || *repaired != *result {
		t.Fatalf("repair=%+v %v", repaired, err)
	}

	entry, err := os.OpenRoot(filepath.Dir(saved))
	if err != nil {
		t.Fatal(err)
	}
	defer entry.Close()

	if data, err := entry.ReadFile(filepath.Base(saved)); err != nil || !bytes.Equal(data, downloadTestBytes) {
		t.Fatalf("saved bytes after repair differ from the signed package: %v", err)
	}

	assertDownloadOperationsClean(t, pipeline)

	if entries, err := os.ReadDir(pipeline.packageDirectory); err != nil || len(entries) != 1 {
		t.Fatalf("repair left entries behind: %v %v", entries, err)
	}
}

func assertDownloadOperationsClean(t *testing.T, pipeline applicationPackageDownload) {
	t.Helper()

	operations, err := filepath.Glob(filepath.Join(pipeline.stageDirectory, "stage-*"))
	if err != nil || len(operations) != 0 {
		t.Fatalf("operations=%v err=%v", operations, err)
	}
}

func TestApplicationDownloadRejectsChangedPolicyAndBadSignature(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, tag string
		draft     bool
	}{{name: "changed draft", tag: applicationDownloadTag, draft: true}, {name: "bad signature", tag: "v1.2.0-bad"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pipeline, target := downloadPipeline(t, tc.tag, tc.draft)
			if result, err := pipeline.Download(t.Context(), application.Release{Tag: tc.tag}, target, nil); err == nil || result != nil {
				t.Fatalf("result=%+v %v", result, err)
			}

			assertDownloadOperationsClean(t, pipeline)
		})
	}
}

func TestApplicationDownloadCancellationDoesNotRetainPartialPackage(t *testing.T) {
	t.Parallel()
	pipeline, target := downloadPipeline(t, applicationDownloadTag, false)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	result, err := pipeline.Download(ctx, application.Release{Tag: applicationDownloadTag}, target, func(p application.DownloadProgress) {
		if p.Written > 0 {
			cancel()
		}
	})
	if err == nil || result != nil {
		t.Fatalf("canceled=%+v %v", result, err)
	}

	assertDownloadOperationsClean(t, pipeline)

	entries, err := os.ReadDir(pipeline.packageDirectory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}

	if len(entries) != 0 {
		t.Fatalf("partial retained=%v", entries)
	}
}

func TestNativeApplicationReaderMissingKeyDisablesOnlyDownloads(t *testing.T) {
	t.Parallel()

	reader, err := newApplicationNativeReader(ApplicationReadConfig{Inventory: ApplicationInventoryConfig{DesktopVersion: downloadTestBuild}, HistoryDirectory: filepath.Join(t.TempDir(), "history")}, "")
	if err != nil || reader == nil || reader.DownloadDisabledReason() == "" {
		t.Fatalf("reader=%+v %v", reader, err)
	}

	if value, err := reader.Download(t.Context(), application.DownloadRequest{Tag: applicationDownloadTag}, nil); err == nil || value != nil {
		t.Fatalf("missing key started package preparation=%+v %v", value, err)
	}

	if value, err := reader.History(t.Context(), false, false); err != nil || value != nil {
		t.Fatalf("read-only first use changed=%+v %v", value, err)
	}
}

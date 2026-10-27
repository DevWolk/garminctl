package garmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/testdump"
)

type syncTransport func(*http.Request) (*http.Response, error)

func (f syncTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func syncResponse(req *http.Request, body []byte) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: req}
}

func syncMessages() []byte {
	return []byte(`{"messages":[{"messageId":42,"messageType":"settings-change","messageStatus":"new","metaData":{"fileType":"FIT","messageUrl":"https://connectapi.garmin.com/synthetic.fit","messageName":"synthetic change"}}]}`)
}

func syncTestClient(t *testing.T, transport syncTransport) *gc.Client {
	t.Helper()
	store := filepath.Join(t.TempDir(), "account.json")

	data, err := json.Marshal(gc.Session{Email: "sync@example.invalid", OAuth1: &gc.OAuth1Token{Token: "synthetic", Domain: gc.DefaultDomain}, OAuth2: &gc.OAuth2Token{AccessToken: "synthetic-access", ExpiresAt: time.Now().Add(time.Hour).Unix()}})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(store, data, 0o600); err != nil {
		t.Fatal(err)
	}

	client := gc.New(store)
	if err := client.StoreError(); err != nil {
		t.Fatal(err)
	}

	client.HTTP = &http.Client{Transport: transport}

	return client
}

func managedTestCloud(t *testing.T, root string, transport syncTransport) (*Cloud, *Lazy, ConnectionEpoch) {
	t.Helper()

	l := NewLazy(func(context.Context) (device.Device, error) { return device.NewDirFS(root) })

	t.Cleanup(func() { closeOwner(t, l) })
	lease := acquireLease(t, l)

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	c := NewJobCloud(l, epoch, syncTestClient(t, transport))
	c.cacheDir = t.TempDir()

	return c, l, epoch
}

type deliveryTransport struct {
	owner    *Lazy
	root     string
	payload  []byte
	entered  chan struct{}
	download chan struct{}
	acks     atomic.Int32
}

func (tr *deliveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if tr.owner.Gate().Snapshot().Operations != 0 {
		return nil, errors.New("network request retained USB lease")
	}

	switch req.URL.Path {
	case "/device-service/devicemessage/messages":
		return syncResponse(req, syncMessages()), nil
	case "/synthetic.fit":
		close(tr.entered)

		select {
		case <-tr.download:
			return syncResponse(req, tr.payload), nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	case "/device-service/devicemessage/message/42":
		if req.Method != http.MethodPut || req.URL.Query().Get("status") != "received" {
			return nil, errors.New("unexpected acknowledgement")
		}

		if err := verifyDeliveredBytes(tr.root, tr.payload); err != nil {
			return nil, err
		}

		tr.acks.Add(1)

		return syncResponse(req, nil), nil
	default:
		return nil, fmt.Errorf("unexpected fake request %s", req.URL.Path)
	}
}

func TestManagedSyncReleasesLeaseForNetworkAndWritesBeforeAck(t *testing.T) {
	t.Parallel()
	root := testdump.Root(t)
	payload := []byte{0x00, 0x01, 0xff, 0x42}
	entered, download := make(chan struct{}), make(chan struct{})

	transport := &deliveryTransport{root: root, payload: payload, entered: entered, download: download}
	c, l, epoch := managedTestCloud(t, root, transport.RoundTrip)
	transport.owner = l
	coordinator := NewSyncCoordinator(t.Context(), l.Gate())
	t.Cleanup(func() {
		if err := coordinator.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})

	job, _, err := coordinator.Start(t.Context(), SyncOptions{Download: true, Epoch: epoch}, func(ctx context.Context, progress Progress) (*SyncResult, error) {
		return c.Sync(ctx, false, true, progress)
	})
	if err != nil {
		t.Fatal(err)
	}

	awaitSyncStage(t, entered, job)

	lease, err := l.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := lease.Service().Stat(lease.Context(), "GARMIN"); err != nil {
		t.Fatal(err)
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	if l.Gate().Snapshot().Jobs != 1 {
		t.Fatal("network stage lost job ownership")
	}

	close(download)

	result, err := job.Wait(t.Context())
	if err != nil || len(result.Pulled) != 1 || result.Pulled[0].Error != "" || transport.acks.Load() != 1 {
		t.Fatalf("managed delivery: result=%+v err=%v acks=%d", result, err, transport.acks.Load())
	}
}

func verifyDeliveredBytes(root string, payload []byte) error {
	dir := filepath.Join(root, "GARMIN", "NewFiles")

	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	if len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".FIT") {
		return errors.New("ack preceded unique FIT delivery")
	}

	confined, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer confined.Close()

	data, err := confined.ReadFile(files[0].Name())
	if err != nil {
		return err
	}

	if !bytes.Equal(data, payload) {
		return errors.New("delivered bytes changed before ack")
	}

	return nil
}

func TestManagedSyncDropCancelsNetworkBeforeAnyDeliveryOrAck(t *testing.T) {
	t.Parallel()
	root := testdump.Root(t)
	entered := make(chan struct{})

	var acks atomic.Int32

	c, l, epoch := managedTestCloud(t, root, syncTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/device-service/devicemessage/messages":
			return syncResponse(req, syncMessages()), nil
		case "/synthetic.fit":
			close(entered)
			<-req.Context().Done()

			return nil, req.Context().Err()
		default:
			acks.Add(1)
			return nil, fmt.Errorf("unexpected fake request %s", req.URL.Path)
		}
	}))
	done := make(chan error, 1)

	go func() { _, err := c.Sync(t.Context(), false, true, nil); done <- err }()

	<-entered

	if err := l.DropAt(epoch); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrSessionChanged) || !errors.Is(err, device.ErrDisconnected) {
			t.Fatalf("dropped job cause: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Drop did not cancel network stage")
	}

	if acks.Load() != 0 {
		t.Fatal("ack sent without device delivery")
	}

	if _, err := os.Stat(filepath.Join(root, "GARMIN", "NewFiles")); !os.IsNotExist(err) {
		t.Fatalf("cancelled job wrote pending data: %v", err)
	}

	lease := acquireLease(t, l)
	if lease.Epoch() == epoch {
		t.Fatal("reopened connection reused old epoch")
	}
}

type uploadTransport struct {
	owner    *Lazy
	payload  []byte
	entered  chan struct{}
	finish   chan struct{}
	requests atomic.Int32
}

func (tr *uploadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.URL.Path != "/upload-service/upload/activity" {
		return nil, fmt.Errorf("unexpected upload request %s %s", req.Method, req.URL.Path)
	}

	if tr.owner.Gate().Snapshot().Operations != 0 {
		return nil, errors.New("upload retained watch lease")
	}

	file, header, err := req.FormFile("file")
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(file)

	err = errors.Join(err, file.Close(), req.MultipartForm.RemoveAll())
	if err != nil {
		return nil, err
	}

	if header.Filename != "sample.fit" || !bytes.Equal(data, tr.payload) {
		return nil, errors.New("multipart changed watch file bytes/name")
	}

	tr.requests.Add(1)
	close(tr.entered)

	select {
	case <-tr.finish:
		return syncResponse(req, []byte(`{}`)), nil
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

func TestManagedUploadReleasesReadLeaseBeforeNetwork(t *testing.T) {
	t.Parallel()
	root := testdump.Root(t)

	data := []byte("synthetic activity bytes")
	if err := os.WriteFile(filepath.Join(root, "GARMIN", "Activity", "sample.fit"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	transport := &uploadTransport{payload: data, entered: make(chan struct{}), finish: make(chan struct{})}
	c, l, epoch := managedTestCloud(t, root, transport.RoundTrip)
	transport.owner = l
	job := startManagedTestSync(t, c, l, SyncOptions{Upload: true, Epoch: epoch})

	awaitSyncStage(t, transport.entered, job)

	lease, err := l.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	close(transport.finish)

	result, err := job.Wait(t.Context())
	if err != nil || result.Upload == nil || result.Upload.Uploaded != 1 || result.Upload.Failed != 0 || transport.requests.Load() != 1 {
		t.Fatalf("managed upload: %+v %v", result, err)
	}
}

func startManagedTestSync(t *testing.T, c *Cloud, l *Lazy, options SyncOptions) *SyncJob {
	t.Helper()
	coordinator := NewSyncCoordinator(t.Context(), l.Gate())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := coordinator.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	job, _, err := coordinator.Start(t.Context(), options, func(ctx context.Context, progress Progress) (*SyncResult, error) {
		return c.Sync(ctx, options.Upload, options.Download, progress)
	})
	if err != nil {
		t.Fatal(err)
	}

	return job
}

func awaitSyncStage(t *testing.T, entered <-chan struct{}, job *SyncJob) {
	t.Helper()

	select {
	case <-entered:
	case <-job.Done():
		result, err := job.Wait(t.Context())
		t.Fatalf("job ended before controlled network stage: result=%+v err=%v", result, err)
	case <-time.After(5 * time.Second):
		t.Fatal("job did not reach controlled network stage")
	}
}

type deniedWriteDevice struct {
	device.Device
	writes atomic.Int32
}

func (d *deniedWriteDevice) Write(context.Context, string, []byte) error {
	d.writes.Add(1)
	return errors.New("synthetic write refused")
}

func TestManagedPullDoesNotAckFailedWrite(t *testing.T) {
	t.Parallel()
	root := testdump.Root(t)

	var failed *deniedWriteDevice

	l := NewLazy(func(context.Context) (device.Device, error) {
		dev, err := device.NewDirFS(root)
		if err != nil {
			return nil, err
		}

		failed = &deniedWriteDevice{Device: dev}

		return failed, nil
	})

	t.Cleanup(func() { closeOwner(t, l) })
	lease := acquireLease(t, l)

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	var acks atomic.Int32

	client := syncTestClient(t, syncTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/device-service/devicemessage/messages":
			return syncResponse(req, syncMessages()), nil
		case "/synthetic.fit":
			return syncResponse(req, []byte("synthetic payload")), nil
		case "/device-service/devicemessage/message/42":
			acks.Add(1)
			return syncResponse(req, nil), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
		}
	}))
	c := NewJobCloud(l, epoch, client)
	c.cacheDir = t.TempDir()
	job := startManagedTestSync(t, c, l, SyncOptions{Download: true, Epoch: epoch})

	result, err := job.Wait(t.Context())
	if err != nil || len(result.Pulled) != 1 || !strings.Contains(result.Pulled[0].Error, "synthetic write refused") {
		t.Fatalf("failed delivery result: %+v %v", result, err)
	}

	if acks.Load() != 0 || failed.writes.Load() != 1 {
		t.Fatalf("failed write acked/replayed: acks=%d writes=%d", acks.Load(), failed.writes.Load())
	}
}

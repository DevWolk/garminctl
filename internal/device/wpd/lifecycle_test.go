package wpd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/hostexec"
)

func TestBridgeProcessHelper(t *testing.T) {
	t.Parallel()

	mode := os.Getenv("GARMINCTL_TEST_BRIDGE_PROCESS")
	if mode == "" {
		return
	}

	_, _ = fmt.Fprintln(os.Stdout, `{"ok":true,"ready":true}`)

	if mode == "no-read" {
		time.Sleep(24 * time.Hour)
		os.Exit(0)
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if mode == "block-requests" {
			time.Sleep(24 * time.Hour)
			os.Exit(0)
		}

		var req struct {
			ID int    `json:"id"`
			Op string `json:"op"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			os.Exit(90)
		}

		runBridgeHelperRequest(req.ID, req.Op)
	}

	os.Exit(0) // Do not append the testing runner's PASS to the JSON stream.
}

func runBridgeHelperRequest(id int, op string) {
	switch op {
	case "crash":
		_, _ = fmt.Fprintln(os.Stderr, "COMException 0x802A0002 secret-file-content")

		os.Exit(23)
	case "invalid-json":
		_, _ = fmt.Fprintln(os.Stderr, "ParserError secret-file-content")
		_, _ = fmt.Fprintln(os.Stdout, `{"b64":"secret-file-content"`)

		os.Exit(24)
	case "partial":
		_, _ = fmt.Fprintf(os.Stdout, `{"id":%d,"ok":true}`, id)

		os.Exit(25)
	case "mismatch":
		_, _ = fmt.Fprintf(os.Stdout, "{\"id\":%d,\"ok\":true}\n", id+100)
	case "block":
		time.Sleep(24 * time.Hour)
	case "quit":
		_, _ = fmt.Fprintf(os.Stdout, "{\"id\":%d,\"ok\":true}\n", id)

		os.Exit(0)
	default:
		_, _ = fmt.Fprintf(os.Stdout, "{\"id\":%d,\"ok\":true}\n", id)
	}
}

func testProcessBridge(t *testing.T, mode string) *Bridge {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestBridgeProcessHelper$") //nolint:gosec // own test executable, fixed arguments

	cmd.Env = append(os.Environ(), "GARMINCTL_TEST_BRIDGE_PROCESS="+mode, "GORACE=atexit_sleep_ms=0")
	hostexec.HideConsole(cmd)

	b, err := startBridge(cmd)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}

		assertBridgeStopped(t, b)
	})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	ready, err := b.readResponse(ctx)
	if err != nil || !ready.Ready {
		t.Fatalf("test child startup: response=%v, err=%v", ready, err)
	}

	return b
}

func assertBridgeStopped(t *testing.T, b *Bridge) {
	t.Helper()

	for _, done := range []<-chan struct{}{b.readerDone, b.processDone} {
		select {
		case <-done:
		default:
			t.Error("bridge worker still running after cleanup")
		}
	}

	if b.cmd.ProcessState == nil {
		t.Error("bridge child was not reaped")
	}
}

func TestBridgeProcessFailuresJoinWorkersAndOmitPayload(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		op   string
		want string
	}{
		{op: "crash", want: "0x802A0002"},
		{op: "invalid-json", want: "invalid JSON"},
		{op: "partial", want: "unexpected EOF"},
		{op: "mismatch", want: "ID does not match"},
	} {
		t.Run(tt.op, func(t *testing.T) {
			t.Parallel()
			b := testProcessBridge(t, "normal")

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			_, err := b.call(ctx, map[string]any{"op": tt.op})
			if !errors.Is(err, device.ErrDisconnected) || !strings.Contains(err.Error(), tt.want) || strings.Contains(err.Error(), "secret-file-content") {
				t.Fatalf("child failure = %v", err)
			}

			assertBridgeStopped(t, b)

			if _, err := b.call(t.Context(), map[string]any{"op": "read"}); !errors.Is(err, device.ErrDisconnected) {
				t.Fatalf("broken child accepted another request: %v", err)
			}
		})
	}
}

type admittedInput struct {
	io.WriteCloser
	entered chan struct{}
	once    sync.Once
}

func (w *admittedInput) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	return w.WriteCloser.Write(p)
}

func TestBridgeCancellationInterruptsReadAndBlockedWrite(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"normal", "no-read"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			b := testProcessBridge(t, mode)
			input := &admittedInput{WriteCloser: b.stdin, entered: make(chan struct{})}
			b.stdin = input

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			req := map[string]any{"op": "block"}
			if mode == "no-read" {
				req["b64"] = strings.Repeat("x", 8<<20)
			}

			done := make(chan error, 1)

			go func() { _, err := b.call(ctx, req); done <- err }()

			select {
			case <-input.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("call never reached pipe write")
			}
			// The child either reads and withholds its response, or never reads
			// stdin at all. Both operations must remain pending before cancel.
			select {
			case err := <-done:
				t.Fatalf("call completed before cancellation: %v", err)
			case <-time.After(30 * time.Millisecond):
			}

			cancel()

			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || !errors.Is(err, device.ErrDisconnected) {
					t.Fatalf("in-flight cancellation: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancel did not interrupt pipe operation")
			}

			assertBridgeStopped(t, b)
		})
	}
}

func TestBridgeCloseInterruptsCallAndIsConcurrentSafe(t *testing.T) {
	t.Parallel()
	b := testProcessBridge(t, "normal")
	input := &admittedInput{WriteCloser: b.stdin, entered: make(chan struct{})}
	b.stdin = input
	callDone := make(chan error, 1)

	go func() { _, err := b.call(t.Context(), map[string]any{"op": "block"}); callDone <- err }()

	<-input.entered

	var closers sync.WaitGroup
	for range 8 {
		closers.Go(func() {
			if err := b.Close(); err != nil {
				t.Error(err)
			}
		})
	}

	closers.Wait()

	select {
	case err := <-callDone:
		if !errors.Is(err, device.ErrDisconnected) {
			t.Fatalf("closed call: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close left call blocked")
	}

	assertBridgeStopped(t, b)
}

func TestBridgeSingleReaderHandlesSuccessiveIDsAndGracefulEOF(t *testing.T) {
	t.Parallel()

	b := testProcessBridge(t, "normal")
	for range 100 {
		if _, err := b.call(t.Context(), map[string]any{"op": "read"}); err != nil {
			t.Fatal(err)
		}
	}

	if err := b.Close(); err != nil {
		t.Fatal(err)
	}

	assertBridgeStopped(t, b)

	if !b.cmd.ProcessState.Success() {
		t.Fatalf("idle child was killed instead of receiving EOF: %v", b.waitErr)
	}
}

func TestBridgeCloseKillsIdleUnresponsiveProcess(t *testing.T) {
	t.Parallel()
	b := testProcessBridge(t, "no-read")

	done := make(chan error, 1)
	go func() { done <- b.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle unresponsive child blocked Close")
	}

	assertBridgeStopped(t, b)
}

func TestBridgeDiagnosticsAreBounded(t *testing.T) {
	t.Parallel()

	d := &bridgeDiagnostics{}
	_, _ = d.Write([]byte(strings.Repeat("secret", bridgeDiagnosticLimit)))

	_, _ = d.Write([]byte(" COMException 0x802A0002"))
	if len(d.tail) > bridgeDiagnosticLimit || d.summary() != "bridge stderr: COMException, 0x802A0002" {
		t.Fatalf("unsafe diagnostics: %q", d.summary())
	}
}

func TestDeviceAbortInterruptsUncancelledReadWithoutReopening(t *testing.T) {
	t.Parallel()
	b := testProcessBridge(t, "block-requests")
	input := &admittedInput{WriteCloser: b.stdin, entered: make(chan struct{})}
	b.stdin = input
	opens := 0
	d := &Device{bridge: b, open: func(context.Context, string) (*Device, error) { opens++; return nil, errors.New("unexpected reopen") }}
	done := make(chan error, 1)

	go func() { _, err := d.Read(t.Context(), "GARMIN/test"); done <- err }()

	<-input.entered
	d.Abort()

	select {
	case err := <-done:
		if !errors.Is(err, device.ErrDisconnected) || opens != 0 {
			t.Fatalf("aborted read reopened: err=%v opens=%d", err, opens)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Abort did not interrupt read")
	}

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	assertBridgeStopped(t, b)
}

func TestDeviceCloseCancelsReopenAndClosesUnpublishedBridge(t *testing.T) {
	t.Parallel()

	old, _ := scriptedBridge("{\"id\":1,\"ok\":false,\"error\":\"0x802A0002\"}\n")
	fresh := testProcessBridge(t, "normal")
	entered := make(chan struct{})
	d := &Device{bridge: old, open: func(ctx context.Context, _ string) (*Device, error) {
		close(entered)
		<-ctx.Done()
		// An opener may complete just as Close cancels its startup. The new
		// bridge must be closed instead of published to the closed Device.
		return &Device{bridge: fresh}, nil
	}}
	done := make(chan error, 1)

	go func() { _, err := d.Read(t.Context(), "GARMIN/test.txt"); done <- err }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reopen did not start")
	}

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, device.ErrDisconnected) || !errors.Is(err, context.Canceled) {
			t.Fatalf("closed reopen: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close left reopen blocked")
	}

	if d.Reconnects() != 0 {
		t.Fatal("closed Device published replacement")
	}

	assertBridgeStopped(t, fresh)
}

func TestReopenWaitCanBeCancelledWithoutCancellingOwner(t *testing.T) {
	t.Parallel()

	old, _ := scriptedBridge("")
	fresh := testProcessBridge(t, "normal")
	entered, release := make(chan struct{}), make(chan struct{})
	d := &Device{bridge: old, open: func(context.Context, string) (*Device, error) {
		close(entered)
		<-release

		return &Device{bridge: fresh}, nil
	}}
	owner := make(chan error, 1)

	go func() { _, err := d.reopen(t.Context(), old); owner <- err }()

	<-entered

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := d.reopen(ctx, old); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting caller: %v", err)
	}

	close(release)

	if err := <-owner; err != nil {
		t.Fatal(err)
	}

	if d.Reconnects() != 1 {
		t.Fatal("cancelled waiter affected successful owner")
	}

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

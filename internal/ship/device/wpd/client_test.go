package wpd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/device"
)

type bridgeInput struct {
	bytes.Buffer
	writes int
}

func (w *bridgeInput) Write(data []byte) (int, error) {
	w.writes++
	return w.Buffer.Write(data)
}

func (*bridgeInput) Close() error { return nil }

func scriptedBridge(reply string) (*Bridge, *bridgeInput) {
	input := &bridgeInput{}
	b := &Bridge{cmd: &exec.Cmd{}, stdin: input, out: bufio.NewReader(strings.NewReader(reply)), stderr: &bridgeDiagnostics{}}

	return b, input
}

func TestDeviceDoesNotReplayMutationsOrRawDiagnostics(t *testing.T) {
	t.Parallel()

	for _, op := range []string{"write", "delete", "mkdir", "mtpwrite", "mtpnodata", "mtpread", "listid"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()

			b, input := scriptedBridge("{\"id\":1,\"ok\":false,\"error\":\"0x802A0002: Shutdown was already called\"}\n")
			reopens := 0
			d := &Device{bridge: b, open: func(context.Context, string) (*Device, error) {
				reopens++
				return nil, errors.New("unexpected reopen")
			}}
			req := map[string]any{"op": op, "path": "GARMIN/test.txt", "opcode": uint32(0x100b)}

			_, err := d.call(t.Context(), req)
			if !errors.Is(err, device.ErrDisconnected) || reopens != 0 || input.writes != 1 {
				t.Fatalf("mutation/raw call: err=%v, reopens=%d, requests=%d", err, reopens, input.writes)
			}

			if _, changed := req["id"]; changed {
				t.Fatal("call changed the caller's request")
			}
		})
	}
}

func TestIndependentPathReadRetriesOnlyOnce(t *testing.T) {
	t.Parallel()

	old, oldInput := scriptedBridge("{\"id\":1,\"ok\":false,\"error\":\"0x802A0002\"}\n")
	fresh, freshInput := scriptedBridge("{\"id\":1,\"ok\":true,\"b64\":\"c2FtcGxl\"}\n")
	opens := 0
	d := &Device{bridge: old, info: device.Info{ID: "test-watch"}, open: func(_ context.Context, id string) (*Device, error) {
		opens++

		if id != "test-watch" {
			t.Error("reopened a different device")
		}

		return &Device{bridge: fresh}, nil
	}}

	data, err := d.Read(t.Context(), "GARMIN/test.txt")
	if err != nil || string(data) != "sample" || opens != 1 || freshInput.writes != 1 || oldInput.writes != 1 || d.Reconnects() != 1 {
		t.Fatalf("independent read: data=%q, err=%v, opens=%d", data, err, opens)
	}
}

func TestManagedPathReadDoesNotReconnectInsideGroup(t *testing.T) {
	t.Parallel()

	b, input := scriptedBridge("{\"id\":1,\"ok\":false,\"error\":\"0x802A0002\"}\n")
	opens := 0
	d := &Device{bridge: b, open: func(context.Context, string) (*Device, error) { opens++; return nil, errors.New("unexpected reopen") }}
	d.SetAutoReconnect(false)

	if _, err := d.Read(t.Context(), "GARMIN/test"); !errors.Is(err, device.ErrDisconnected) || opens != 0 || input.writes != 1 {
		t.Fatalf("managed read: err=%v opens=%d writes=%d", err, opens, input.writes)
	}
}

func TestClosedDeviceDoesNotReopenFromRetainedReference(t *testing.T) {
	t.Parallel()

	b, input := scriptedBridge("{\"id\":1,\"ok\":true}\n")
	opens := 0

	d := &Device{bridge: b, open: func(context.Context, string) (*Device, error) {
		opens++
		return nil, errors.New("unexpected reopen")
	}}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	writes := input.writes
	if _, err := d.Read(t.Context(), "GARMIN/test.txt"); !errors.Is(err, device.ErrDisconnected) || opens != 0 || input.writes != writes {
		t.Fatalf("closed reference: err=%v, opens=%d, requests=%d", err, opens, input.writes)
	}
}

func TestBridgeRejectsBeforeIOWithoutBreakingSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  map[string]any
		stop bool
		want error
	}{
		{name: "cancelled before write", req: pathReq("read", "GARMIN/test.txt"), stop: true, want: context.Canceled},
		{name: "invalid path", req: pathReq("read", "GARMIN/../test.txt"), want: device.ErrInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b, input := scriptedBridge("{\"id\":1,\"ok\":true}\n")

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if tt.stop {
				cancel()
			}

			if _, err := b.call(ctx, tt.req); !errors.Is(err, tt.want) || errors.Is(err, device.ErrDisconnected) || b.closed.Load() || input.writes != 0 {
				t.Fatalf("rejection: %v, closed=%t, writes=%d", err, b.closed.Load(), input.writes)
			}

			if _, err := b.call(t.Context(), pathReq("read", "GARMIN/test.txt")); err != nil {
				t.Fatalf("session unusable after pre-I/O rejection: %v", err)
			}
		})
	}
}

func TestBridgeBrokenResponseIsDisconnectedWithoutPayload(t *testing.T) {
	t.Parallel()

	for _, reply := range []string{"", "{\"b64\":\"secret-file-content\"\n"} {
		t.Run(reply, func(t *testing.T) {
			t.Parallel()

			b, _ := scriptedBridge(reply)
			if _, err := b.call(t.Context(), pathReq("read", "GARMIN/test.txt")); !errors.Is(err, device.ErrDisconnected) || strings.Contains(err.Error(), "secret-file-content") {
				t.Fatalf("broken response = %v", err)
			}
		})
	}
}

func TestRemoteErrorClassification(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		message  string
		notFound bool
		want     error
		never    error
	}{
		{name: "bridge reports absence", message: "not found: GARMIN/test.txt", notFound: true, want: device.ErrNotFound},
		{name: "absence is not a disconnect", message: "not found", notFound: true, never: device.ErrDisconnected},
		{name: "busy", message: "0x800700AA: processing another operation", want: device.ErrBusy},
		{name: "unplug", message: "0x8007048F: device not found", want: device.ErrDisconnected},
		{name: "unplug is not absence", message: "0x8007048F: device not found", never: device.ErrNotFound},
		{name: "ordinary error mentioning bridge", message: "invalid application bridge label", never: device.ErrDisconnected},
		{name: "failure whose text mentions not found", message: "Element not found. (Exception from HRESULT: 0x80070490)", never: device.ErrNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := remoteError(&response{Error: tt.message, NotFound: tt.notFound})
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("classification = %v, want %v", err, tt.want)
			}

			if tt.never != nil && errors.Is(err, tt.never) {
				t.Fatalf("classification = %v, must not be %v", err, tt.never)
			}
		})
	}
}

func TestBridgeAbsenceComesFromItsFlagNotItsText(t *testing.T) {
	t.Parallel()

	flagged, _ := scriptedBridge("{\"id\":1,\"ok\":false,\"error\":\"GARMIN/test.txt is missing\",\"notFound\":true}\n")
	if _, err := flagged.call(t.Context(), pathReq("read", "GARMIN/test.txt")); !errors.Is(err, device.ErrNotFound) {
		t.Fatalf("flagged absence = %v", err)
	}

	unflagged, _ := scriptedBridge("{\"id\":1,\"ok\":false,\"error\":\"storage object not found while reading\"}\n")
	if _, err := unflagged.call(t.Context(), pathReq("read", "GARMIN/test.txt")); err == nil || errors.Is(err, device.ErrNotFound) {
		t.Fatalf("failure reported as absence: %v", err)
	}
}

// lateCancelContext reports cancellation from its nth Err call on, as a context
// cancelled while the caller waits for the bridge lock does.
type lateCancelContext struct{ calls, live int }

func (*lateCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*lateCancelContext) Done() <-chan struct{}       { return nil }
func (*lateCancelContext) Value(any) any               { return nil }

func (c *lateCancelContext) Err() error {
	c.calls++
	if c.calls > c.live {
		return context.Canceled
	}

	return nil
}

func TestBridgeCallCancelledWhileWaitingWritesNothing(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		live   int
		nextID int
	}{
		{name: "cancelled before the lock is acquired", live: 1},
		{name: "cancelled while the request is encoded", live: 2, nextID: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b, input := scriptedBridge(fmt.Sprintf("{\"id\":%d,\"ok\":true}\n", tt.nextID+1))
			ctx := &lateCancelContext{live: tt.live}

			if _, err := b.call(ctx, pathReq("read", "GARMIN/test.txt")); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled call = %v", err)
			}

			if input.writes != 0 || b.nextID != tt.nextID {
				t.Fatalf("cancelled call reached the bridge: writes=%d nextID=%d", input.writes, b.nextID)
			}

			if _, err := b.call(t.Context(), pathReq("read", "GARMIN/test.txt")); err != nil {
				t.Fatalf("bridge unusable after a cancelled call: %v", err)
			}
		})
	}
}

func TestBridgeCancelledAfterWriteIsDisconnected(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()

	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	b := &Bridge{stdin: cancelInput{cancel: cancel}, out: bufio.NewReader(reader), stdout: reader}
	if _, err := b.call(ctx, pathReq("read", "GARMIN/test.txt")); !errors.Is(err, context.Canceled) || !errors.Is(err, device.ErrDisconnected) {
		t.Fatalf("in-flight cancellation = %v", err)
	}
}

func TestRawStatusIsClassifiedWithoutReplayingOpcode(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		code uint32
		want error
	}{
		{name: "busy", code: 0x2019, want: device.ErrBusy},
		{name: "session lost", code: 0x2003, want: device.ErrDisconnected},
		{name: "missing handle", code: 0x2009, want: device.ErrNotFound},
		{name: "invalid parameter", code: 0x201d, want: device.ErrInvalidArgument},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b, input := scriptedBridge(fmt.Sprintf("{\"id\":1,\"ok\":true,\"code\":%d}\n", tt.code))
			d := &Device{bridge: b}

			code, _, err := d.MtpNoData(t.Context(), 0x100b, []uint32{42, 0})
			if !errors.Is(err, tt.want) || code != tt.code || input.writes != 1 {
				t.Fatalf("raw response code=%04x, err=%v, requests=%d", code, err, input.writes)
			}
		})
	}
}

type cancelInput struct{ cancel context.CancelFunc }

func (w cancelInput) Write(data []byte) (int, error) { w.cancel(); return len(data), nil }
func (cancelInput) Close() error                     { return nil }

func TestPick(t *testing.T) {
	t.Parallel()

	const iface = "{6ac27878-a6fa-4155-ba85-f98f491d4f33}"

	watch := device.Info{ID: `\\?\usb#vid_091e&pid_514a#0000e4f9adae#` + iface, Name: "vívoactive 5", Manufacturer: "Garmin"}
	// A USB drive: Windows lists mass storage through the same API.
	ssd := device.Info{ID: `\\?\swd#wpdbusenum#{4f2da7fb-bd09-11f1-bd01-34fd70d7c8b8}#0000000000100000#` + iface, Name: "Ubuntu", Manufacturer: "TUF"}
	phone := device.Info{ID: `\\?\usb#vid_18d1&pid_4ee1#1234#` + iface, Name: "Pixel", Manufacturer: "Google"}
	unnamed := device.Info{ID: `\\?\usb#VID_091E&PID_514A#0000#` + iface, Name: "MTP Device"}

	tests := []struct {
		name    string
		devices []device.Info
		id      string
		want    string // ID of the chosen device
		wantErr string // part of the error text
	}{
		{name: "nothing connected", wantErr: "no portable device connected"},
		{name: "nothing connected, explicit id", id: watch.ID, wantErr: "no portable device connected"},
		{name: "watch alone", devices: []device.Info{watch}, want: watch.ID},
		{name: "usb drive alone is not the watch", devices: []device.Info{ssd}, wantErr: "no Garmin device among 1 portable device(s): Ubuntu (TUF)"},
		{name: "phone and drive, no watch", devices: []device.Info{phone, ssd}, wantErr: "among 2 portable device(s): Pixel (Google), Ubuntu (TUF)"},
		{name: "watch after other devices", devices: []device.Info{ssd, phone, watch}, want: watch.ID},
		{name: "garmin by usb vendor id only", devices: []device.Info{ssd, unnamed}, want: unnamed.ID},
		{name: "explicit id wins over the watch", devices: []device.Info{watch, ssd}, id: ssd.ID, want: ssd.ID},
		{name: "explicit id missing", devices: []device.Info{watch}, id: "nope", wantErr: `device "nope" not found among 1 portable devices`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := pick(tt.devices, tt.id)
			if err != nil {
				if tt.wantErr == "" || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("pick() error = %v, want %q", err, tt.wantErr)
				}

				return
			}

			if tt.wantErr != "" {
				t.Fatalf("pick() = %q, want error %q", got.ID, tt.wantErr)
			}

			if got.ID != tt.want {
				t.Errorf("pick() = %q, want %q", got.ID, tt.want)
			}
		})
	}
}

func TestTrimInfo(t *testing.T) {
	t.Parallel()

	got := trimInfo([]device.Info{
		{ID: ` \\?\swd#wpdbusenum#x `, Name: "Ubuntu ", Manufacturer: "TUF     ", Description: " Ubuntu\t"},
		{ID: "watch", Name: "vívoactive 5", Manufacturer: "Garmin"},
	})

	want := []device.Info{
		{ID: ` \\?\swd#wpdbusenum#x `, Name: "Ubuntu", Manufacturer: "TUF", Description: "Ubuntu"},
		{ID: "watch", Name: "vívoactive 5", Manufacturer: "Garmin"},
	}

	if !slices.Equal(got, want) {
		t.Errorf("trimInfo() = %q, want %q", got, want)
	}
}

func TestBridgeReplyBeforeCancellationIsSuccess(t *testing.T) {
	t.Parallel()

	b, input := scriptedBridge("{\"id\":1,\"ok\":true}\n{\"id\":2,\"ok\":true}\n")
	// Live through admission; cancelled once the request is on the pipe.
	ctx := &lateCancelContext{live: 3}

	if _, err := b.call(ctx, pathReq("delete", "GARMIN/NewFiles/a.fit")); err != nil {
		t.Fatalf("answered request reported as failed: %v", err)
	}

	if b.closed.Load() {
		t.Fatal("cancellation after the reply stopped the bridge")
	}

	if _, err := b.call(t.Context(), pathReq("stat", "GARMIN")); err != nil || input.writes != 2 {
		t.Fatalf("bridge unusable after an answered request: %v, writes=%d", err, input.writes)
	}
}

func TestWriteFinishesAfterCallerCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	b := &Bridge{cmd: &exec.Cmd{}, stdin: cancelInput{cancel: cancel}, out: bufio.NewReader(strings.NewReader("{\"id\":1,\"ok\":true}\n")), stderr: &bridgeDiagnostics{}}
	d := &Device{bridge: b}

	if err := d.Write(ctx, "GARMIN/Music/book.mp3", []byte("audio")); err != nil {
		t.Fatalf("admitted write interrupted by caller: %v", err)
	}

	if b.closed.Load() {
		t.Fatal("caller cancellation stopped the bridge during a write")
	}

	cancelled, stop := context.WithCancel(t.Context())
	stop()

	if err := d.Write(cancelled, "GARMIN/Music/book.mp3", []byte("audio")); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled write = %v", err)
	}
}

// cancelOnWrite cancels the caller when the bridge receives request number at.
type cancelOnWrite struct {
	bridgeInput

	at     int
	cancel context.CancelFunc
}

func (w *cancelOnWrite) Write(data []byte) (int, error) {
	n, err := w.bridgeInput.Write(data)
	if w.writes == w.at {
		w.cancel()
	}

	return n, err
}

func mtpReply(id int, data []byte, params ...uint32) string {
	p, _ := json.Marshal(params)
	if params == nil {
		p = []byte("[]")
	}

	return fmt.Sprintf("{\"id\":%d,\"ok\":true,\"code\":%d,\"params\":%s,\"b64\":%q}\n", id, ptpOK, p, base64.StdEncoding.EncodeToString(data))
}

func handleList(handles ...uint32) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(handles))) //nolint:gosec // small test fixtures.
	for _, h := range handles {
		b = binary.LittleEndian.AppendUint32(b, h)
	}

	return b
}

func TestWriteHiddenReplaceFinishesAfterCancellationAtDelete(t *testing.T) {
	t.Parallel()

	oldInfo, err := objectInfoDataset(0x10001, 1, "OLD.PRG", 7)
	if err != nil {
		t.Fatal(err)
	}

	newInfo, err := objectInfoDataset(0x10001, 1, "OLD.PRG", 8)
	if err != nil {
		t.Fatal(err)
	}

	replies := "{\"id\":1,\"ok\":true,\"objectId\":\"o1\",\"storage\":\"s10001\"}\n" +
		mtpReply(2, handleList(5)) + mtpReply(3, oldInfo) + mtpReply(4, handleList(5)) +
		mtpReply(5, nil) + // DeleteObject of the old file
		mtpReply(6, nil, 0x10001, 1, 6) + mtpReply(7, nil) +
		mtpReply(8, handleList(6)) + mtpReply(9, newInfo) + mtpReply(10, handleList(6))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	input := &cancelOnWrite{at: 5, cancel: cancel}
	b := &Bridge{cmd: &exec.Cmd{}, stdin: input, out: bufio.NewReader(strings.NewReader(replies)), stderr: &bridgeDiagnostics{}}
	d := &Device{bridge: b}

	if err := d.WriteHidden(ctx, "GARMIN", "OLD.PRG", []byte("new app!")); err != nil {
		t.Fatalf("replace interrupted after deleting the old file: %v", err)
	}

	if input.writes != 10 || ctx.Err() == nil {
		t.Fatalf("requests=%d, caller cancelled=%v", input.writes, ctx.Err())
	}
}

func TestStorageRejectsInfoWithoutDescription(t *testing.T) {
	t.Parallel()

	b, _ := scriptedBridge("{\"id\":1,\"ok\":true,\"objectId\":\"o1\",\"storage\":\"s10001\"}\n" + mtpReply(2, make([]byte, 26)))
	d := &Device{bridge: b}

	if _, err := d.Storage(t.Context()); err == nil {
		t.Fatal("accepted a 26-byte StorageInfo")
	}
}

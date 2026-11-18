package mtp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/device"
)

func TestProtocolPreCancelledRequestKeepsSession(t *testing.T) {
	t.Parallel()

	w := &testWire{packets: [][]byte{packet(containerResponse, responseOK, 0, nil)}}
	p := protocol{wire: w}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, _, err := p.exchange(ctx, 0x1009, nil, nil); !errors.Is(err, context.Canceled) || errors.Is(err, device.ErrDisconnected) || p.broken || len(w.writes) != 0 {
		t.Fatalf("pre-cancelled request = %v, broken=%t, writes=%d", err, p.broken, len(w.writes))
	}

	if _, _, err := p.exchange(t.Context(), 0x1009, nil, nil); err != nil {
		t.Fatalf("pre-I/O cancellation damaged the session: %v", err)
	}
}

type cancelWriteWire struct {
	testWire
	cancel context.CancelFunc
}

func (w *cancelWriteWire) WriteContext(ctx context.Context, data []byte) (int, error) {
	w.cancel()
	return w.testWire.WriteContext(ctx, data)
}

func TestProtocolAdmittedCancellationBreaksSessionEvenWithZeroBytes(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w := &cancelWriteWire{cancel: cancel}

	p := protocol{wire: w}
	if _, _, err := p.exchange(ctx, 0x100d, nil, []byte("test")); !errors.Is(err, context.Canceled) || !errors.Is(err, device.ErrDisconnected) || !p.broken {
		t.Fatalf("admitted cancellation = %v, broken=%t", err, p.broken)
	}

	if len(w.writes) != 0 {
		t.Fatal("test must exercise zero reported bytes after transport admission")
	}
}

func TestProtocolResponseClassification(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		code   uint16
		want   error
		broken bool
	}{
		{name: "busy", code: 0x2019, want: device.ErrBusy},
		{name: "already open", code: 0x201e, want: device.ErrBusy},
		{name: "missing object", code: 0x2009, want: device.ErrNotFound},
		{name: "invalid parameter", code: 0x201d, want: device.ErrInvalidArgument},
		{name: "lost session", code: 0x2003, want: device.ErrDisconnected, broken: true},
		{name: "invalid transaction", code: 0x2004, want: device.ErrDisconnected, broken: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &testWire{packets: [][]byte{packet(containerResponse, tt.code, 0, nil), packet(containerResponse, responseOK, 1, nil)}}
			p := protocol{wire: w}

			_, _, err := p.exchange(t.Context(), 0x1009, nil, nil)
			if !errors.Is(err, tt.want) || p.broken != tt.broken {
				t.Fatalf("response = %v, broken=%t", err, p.broken)
			}

			writes := len(w.writes)

			_, _, err = p.exchange(t.Context(), 0x1009, nil, nil)
			if tt.broken {
				if !errors.Is(err, device.ErrDisconnected) || len(w.writes) != writes {
					t.Fatal("broken session continued I/O")
				}
			} else if err != nil {
				t.Fatalf("recoverable response damaged the session: %v", err)
			}
		})
	}
}

type testWire struct {
	packets [][]byte
	writes  [][]byte
	closed  bool
}

func (w *testWire) ReadContext(ctx context.Context, b []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	if len(w.packets) == 0 {
		return 0, io.EOF
	}

	packet := w.packets[0]

	n := copy(b, packet)
	if n == len(packet) {
		w.packets = w.packets[1:]
	} else {
		w.packets[0] = packet[n:]
	}

	return n, nil
}

func (w *testWire) WriteContext(ctx context.Context, b []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	w.writes = append(w.writes, bytes.Clone(b))

	return len(b), nil
}
func (w *testWire) PacketSize() int { return 512 }
func (w *testWire) Close() error    { w.closed = true; return nil }

func packet(kind, code uint16, id uint32, data []byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(12+len(data))) //nolint:gosec // small test fixtures.
	b = binary.LittleEndian.AppendUint16(b, kind)
	b = binary.LittleEndian.AppendUint16(b, code)
	b = binary.LittleEndian.AppendUint32(b, id)

	return append(b, data...)
}

func TestProtocolRead(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("Garmin"), 20000)
	data := packet(containerData, 0x1009, 7, payload)

	response := packet(containerResponse, responseOK, 7, nil)
	for _, packets := range [][][]byte{
		{append(bytes.Clone(data), response...)},
		{data[:6], data[6:12], data[12:], {}, response},
	} {
		t.Run("packet-boundaries", func(t *testing.T) {
			t.Parallel()

			w := &testWire{packets: packets}
			p := protocol{wire: w, transaction: 7}

			got, _, err := p.exchange(t.Context(), 0x1009, []uint32{123}, nil)
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("read: %d bytes, %v", len(got), err)
			}

			if p.transaction != 8 {
				t.Fatal("transaction ID did not advance")
			}
		})
	}
}

func TestProtocolRejectsBrokenStream(t *testing.T) {
	t.Parallel()

	badLength := packet(containerResponse, responseOK, 0, nil)
	binary.LittleEndian.PutUint32(badLength, maxContainer+1)

	for name, reply := range map[string][]byte{
		"wrong transaction": packet(containerResponse, responseOK, 99, nil),
		"wrong opcode":      packet(containerData, 0x1008, 0, nil),
		"too large":         badLength,
		"truncated":         {12, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			w := &testWire{packets: [][]byte{reply}}

			p := protocol{wire: w}
			if _, _, err := p.exchange(t.Context(), 0x1009, nil, nil); err == nil {
				t.Fatal("accepted invalid response")
			}

			writes := len(w.writes)
			if _, _, err := p.exchange(t.Context(), 0x1009, nil, nil); err == nil || len(w.writes) != writes {
				t.Fatal("continued a broken USB session")
			}
		})
	}
}

func TestProtocolResponseErrorAllowsNextOperation(t *testing.T) {
	t.Parallel()

	w := &testWire{packets: [][]byte{packet(containerResponse, 0x2005, 0, nil), packet(containerResponse, responseOK, 1, nil)}}
	p := protocol{wire: w}
	_, _, err := p.exchange(t.Context(), 0x9002, nil, nil)

	var code responseError
	if !errors.As(err, &code) || code != 0x2005 {
		t.Fatalf("got %v", err)
	}

	if _, _, err := p.exchange(t.Context(), 0x1003, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolWriteEmptyAndPacketMultiple(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 500, 65524, 100000} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()

			payload := bytes.Repeat([]byte{0x42}, size)
			w := &testWire{packets: [][]byte{packet(containerResponse, responseOK, 0, nil)}}

			p := protocol{wire: w}
			if _, _, err := p.exchange(t.Context(), 0x100d, nil, payload); err != nil {
				t.Fatal(err)
			}

			all := bytes.Join(w.writes[1:], nil)
			if !bytes.Equal(all, packet(containerData, 0x100d, 0, payload)) {
				t.Fatal("data was not framed correctly")
			}

			last := w.writes[len(w.writes)-1]
			if (size+12)%512 == 0 && len(last) != 0 {
				t.Fatal("missing zero-length packet")
			}
		})
	}
}

// slowWire takes a little time for every transfer and records its deadline.
type slowWire struct {
	testWire

	delay     time.Duration
	deadlines []time.Time
	stall     bool
}

func (w *slowWire) ReadContext(ctx context.Context, b []byte) (int, error) {
	deadline, _ := ctx.Deadline()
	w.deadlines = append(w.deadlines, deadline)

	if w.stall {
		<-ctx.Done()
		return 0, ctx.Err()
	}

	time.Sleep(w.delay)

	return w.testWire.ReadContext(ctx, b)
}

func TestProtocolTimeoutAppliesPerTransfer(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte{0x42}, 40*64)
	data := packet(containerData, 0x1009, 0, payload)

	w := &slowWire{delay: 5 * time.Millisecond}

	for len(data) > 0 {
		n := min(64, len(data))
		w.packets = append(w.packets, data[:n])
		data = data[n:]
	}

	w.packets = append(w.packets, packet(containerResponse, responseOK, 0, nil))

	// The whole exchange takes longer than one transfer may.
	p := protocol{wire: w, timeout: 100 * time.Millisecond}

	got, _, err := p.exchange(t.Context(), 0x1009, nil, nil)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("slow read: %d bytes, %v", len(got), err)
	}

	for i := 1; i < len(w.deadlines); i++ {
		if w.deadlines[i].IsZero() || !w.deadlines[i].After(w.deadlines[i-1]) {
			t.Fatalf("transfer %d did not get its own deadline", i)
		}
	}

	stalled := &slowWire{stall: true}
	p = protocol{wire: stalled, timeout: 10 * time.Millisecond}

	if _, _, err := p.exchange(t.Context(), 0x1009, nil, nil); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, device.ErrDisconnected) {
		t.Fatalf("stalled transfer = %v", err)
	}
}

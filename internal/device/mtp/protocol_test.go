package mtp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
	"testing"
)

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

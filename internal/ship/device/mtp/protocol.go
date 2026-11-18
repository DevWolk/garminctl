// Package mtp implements Garmin's MTP file store over a native USB transport.
package mtp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/DevWolk/garminctl/internal/ship/device"
)

const (
	containerCommand  = 1
	containerData     = 2
	containerResponse = 3
	responseOK        = 0x2001
	maxContainer      = 512<<20 + 12
)

type wire interface {
	ReadContext(context.Context, []byte) (int, error)
	WriteContext(context.Context, []byte) (int, error)
	Close() error
	PacketSize() int
}

type protocol struct {
	wire        wire
	transaction uint32
	pending     []byte
	broken      bool
	// This is the transport-owned cancellation lifetime, not a retained request.
	lifetime context.Context //nolint:containedctx // Abort must interrupt a transaction without acquiring its lock.
}

type responseError uint16

func (e responseError) Error() string { return fmt.Sprintf("MTP response 0x%04X", uint16(e)) }

func (e responseError) Unwrap() error {
	switch e {
	case 0x2003, 0x2004: // SessionNotOpen, InvalidTransactionID.
		return device.ErrDisconnected
	case 0x2009: // InvalidObjectHandle.
		return device.ErrNotFound
	case 0x2019, 0x201e: // DeviceBusy, SessionAlreadyOpen.
		return device.ErrBusy
	case 0x201d: // InvalidParameter.
		return device.ErrInvalidArgument
	default:
		return nil
	}
}

// exchange must be serialized with the complete operation, especially the
// SendObjectInfo/SendObject pair. A failed USB transfer invalidates the session;
// resuming halfway through a container would risk sending data to the wrong op.
func (p *protocol) exchange(ctx context.Context, op uint16, params []uint32, data []byte) ([]byte, []uint32, error) {
	if p.broken {
		return nil, nil, fmt.Errorf("%w: MTP session interrupted; reconnect the watch", device.ErrDisconnected)
	}

	if p.lifetime != nil && p.lifetime.Err() != nil {
		p.broken = true
		return nil, nil, fmt.Errorf("%w: MTP session aborted: %w", device.ErrDisconnected, p.lifetime.Err())
	}

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	if len(params) > 5 || len(data) > maxContainer-12 {
		return nil, nil, fmt.Errorf("%w: MTP request is too large", device.ErrInvalidArgument)
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	if p.lifetime != nil {
		stop := context.AfterFunc(p.lifetime, cancel)
		defer stop()
	}

	command := make([]byte, 0, 20)
	for _, v := range params {
		command = binary.LittleEndian.AppendUint32(command, v)
	}

	err := p.send(ctx, containerCommand, op, command)
	if err == nil && data != nil {
		err = p.send(ctx, containerData, op, data)
	}

	if err != nil {
		p.broken = true
		return nil, nil, fmt.Errorf("%w: MTP transfer: %w", device.ErrDisconnected, err)
	}

	result, reply, err := p.receive(ctx, op)

	var code responseError
	if err != nil && !errors.As(err, &code) {
		p.broken = true
		err = fmt.Errorf("%w: MTP response: %w", device.ErrDisconnected, err)
	}

	if errors.Is(err, device.ErrDisconnected) {
		p.broken = true
	}

	p.transaction++

	return result, reply, err
}

func (p *protocol) send(ctx context.Context, kind, code uint16, payload []byte) error {
	header := binary.LittleEndian.AppendUint32(nil, uint32(len(payload)+12)) //nolint:gosec // exchange bounds payload to 512 MiB.
	header = binary.LittleEndian.AppendUint16(header, kind)
	header = binary.LittleEndian.AppendUint16(header, code)
	header = binary.LittleEndian.AppendUint32(header, p.transaction)
	// Keep header and the first data bytes in one transfer. All intermediate
	// transfers are full packets; only the final transfer terminates the phase.
	for offset := 0; offset < len(payload) || len(header) > 0; {
		chunk := make([]byte, 0, 64<<10)
		chunk = append(chunk, header...)
		header = nil
		n := min(cap(chunk)-len(chunk), len(payload)-offset)
		chunk = append(chunk, payload[offset:offset+n]...)
		offset += n

		written, err := p.wire.WriteContext(ctx, chunk)
		if err != nil {
			return err
		}

		if written != len(chunk) {
			return io.ErrShortWrite
		}
	}

	if (len(payload)+12)%p.wire.PacketSize() == 0 {
		_, err := p.wire.WriteContext(ctx, []byte{})
		return err
	}

	return nil
}

func (p *protocol) receive(ctx context.Context, op uint16) ([]byte, []uint32, error) {
	kind, code, body, err := p.readContainer(ctx)
	if err != nil {
		return nil, nil, err
	}

	var data []byte

	if kind == containerData {
		if code != op {
			return nil, nil, errors.New("MTP data opcode mismatch")
		}

		data = body

		kind, code, body, err = p.readContainer(ctx)
		if err != nil {
			return nil, nil, err
		}
	}

	if kind != containerResponse || len(body)%4 != 0 || len(body) > 20 {
		return nil, nil, errors.New("invalid MTP response")
	}

	if code != responseOK {
		return nil, nil, responseError(code)
	}

	params := make([]uint32, len(body)/4)
	for i := range params {
		params[i] = binary.LittleEndian.Uint32(body[4*i:])
	}

	return data, params, nil
}

func (p *protocol) fill(ctx context.Context, size int) error {
	for len(p.pending) < size {
		if err := ctx.Err(); err != nil {
			return err
		}

		packet := make([]byte, 64<<10)

		n, err := p.wire.ReadContext(ctx, packet)
		if err != nil {
			return err
		}

		if n == 0 {
			continue
		} // A zero-length packet terminates an exact-multiple data phase.

		p.pending = append(p.pending, packet[:n]...)
	}

	return nil
}

func (p *protocol) readContainer(ctx context.Context) (uint16, uint16, []byte, error) {
	if err := p.fill(ctx, 12); err != nil {
		return 0, 0, nil, err
	}

	size := binary.LittleEndian.Uint32(p.pending)
	if size < 12 || size > maxContainer {
		return 0, 0, nil, fmt.Errorf("invalid MTP container length: %d", size)
	}

	if binary.LittleEndian.Uint32(p.pending[8:]) != p.transaction {
		return 0, 0, nil, errors.New("MTP transaction ID mismatch")
	}

	if err := p.fill(ctx, int(size)); err != nil {
		return 0, 0, nil, err
	}

	kind := binary.LittleEndian.Uint16(p.pending[4:])
	code := binary.LittleEndian.Uint16(p.pending[6:])
	body := append([]byte(nil), p.pending[12:size]...)
	p.pending = append([]byte(nil), p.pending[size:]...)

	return kind, code, body, nil
}

package updatetransport

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strings"
)

type stageOutput struct {
	operation, version, key, digest string
	size, written                   int64
	bytes                           int
	partial                         []byte
	complete                        bool
	err                             error
	progress                        func(StageProgress)
	cancel                          context.CancelFunc
}

func (o *stageOutput) Write(data []byte) (int, error) {
	length := len(data)

	o.bytes += length
	if o.bytes > maxStageOutput {
		return 0, o.fail(errors.New("stager output exceeds the byte limit"))
	}

	for len(data) > 0 {
		index := bytes.IndexByte(data, '\n')
		if index < 0 {
			index = len(data)
		}

		if len(o.partial)+index > maxStageFrame {
			return 0, o.fail(errors.New("stager frame exceeds the byte limit"))
		}

		o.partial = append(o.partial, data[:index]...)
		if index == len(data) {
			break
		}

		if err := o.receive(o.partial); err != nil {
			return 0, o.fail(err)
		}

		o.partial = o.partial[:0]
		data = data[index+1:]
	}

	return length, nil
}

func (o *stageOutput) fail(err error) error { o.err = err; o.cancel(); return err }

func (o *stageOutput) receive(data []byte) error {
	var event stageEvent
	if err := decodeStageJSON(data, &event); err != nil {
		return err
	}

	if o.complete || event.Protocol != stageProtocol || event.Operation != o.operation || event.Total != o.size || event.Written < o.written || event.Written > o.size {
		return errors.New("invalid stager event identity or byte count")
	}

	o.written = event.Written
	switch event.Kind {
	case "progress":
		if o.progress != nil {
			o.progress(StageProgress{Written: event.Written, Total: event.Total})
		}
	case "complete":
		return o.acceptComplete(event)
	default:
		return errors.New("unknown stager event")
	}

	return nil
}

func (o *stageOutput) acceptComplete(event stageEvent) error {
	digest, err := hex.DecodeString(event.Digest)
	if err != nil || len(digest) != 32 || event.BuildVersion != o.version || event.KeyID != o.key || event.Written != o.size {
		return errors.New("invalid completed stager receipt")
	}

	o.complete = true
	o.digest = event.Digest

	return nil
}

func (o *stageOutput) finish() error {
	if len(o.partial) != 0 || !o.complete {
		return errors.New("stager exited without one complete receipt")
	}

	return nil
}

type stageDiagnostics struct{ data []byte }

func (d *stageDiagnostics) Write(data []byte) (int, error) {
	length := len(data)

	remaining := (8 << 10) - len(d.data)
	if remaining > 0 {
		d.data = append(d.data, data[:min(remaining, length)]...)
	}

	return length, nil
}
func (d *stageDiagnostics) String() string { return strings.TrimSpace(string(d.data)) }

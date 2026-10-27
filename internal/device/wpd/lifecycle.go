package wpd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
)

const (
	bridgeGracePeriod     = 500 * time.Millisecond
	bridgeCleanupTimeout  = 2 * time.Second
	bridgeDiagnosticLimit = 4096
)

func startBridge(cmd *exec.Cmd) (*Bridge, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}

	b := &Bridge{cmd: cmd, stdin: stdin, stdout: stdout, out: bufio.NewReaderSize(stdout, 1<<20), stderr: &bridgeDiagnostics{}}
	cmd.Stderr = b.stderr
	// Bound the exec-owned stderr copier if a descendant inherits its pipe.
	cmd.WaitDelay = bridgeGracePeriod
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()

		return nil, err
	}

	b.startWorkers()

	return b, nil
}

func (b *Bridge) startWorkers() {
	b.workers.Do(func() {
		b.stop = make(chan struct{})
		// One pending response permits reading EOF without another caller.
		b.responses = make(chan response, 1)
		b.readerDone = make(chan struct{})

		b.processDone = make(chan struct{})
		go b.readLoop()
		go func() {
			// StdoutPipe must be drained or closed before Wait closes it.
			<-b.readerDone

			if b.cmd != nil && b.cmd.Process != nil {
				b.waitErr = b.cmd.Wait()
			}

			close(b.processDone)
		}()
	})
}

func (b *Bridge) readLoop() {
	defer close(b.readerDone)
	defer close(b.responses)

	for {
		line, err := b.out.ReadBytes('\n')
		if err != nil {
			if len(line) > 0 {
				err = io.ErrUnexpectedEOF
			}

			b.readErr = fmt.Errorf("bridge response stream: %w", err)

			return
		}

		var resp response
		if err := json.Unmarshal(line, &resp); err != nil {
			b.readErr = errors.New("bridge sent invalid JSON")
			return
		}

		select {
		case b.responses <- resp:
		case <-b.stop:
			return
		}
	}
}

func (b *Bridge) readResponse(ctx context.Context) (*response, error) {
	b.startWorkers()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp, ok := <-b.responses:
		if !ok {
			return nil, errors.Join(device.ErrDisconnected, b.readErr)
		}

		return &resp, nil
	}
}

// abort interrupts an admitted exchange without waiting for the call mutex.
func (b *Bridge) abort() {
	b.startWorkers()
	b.closed.Store(true)
	b.stopOnce.Do(func() {
		close(b.stop)
		// Kill first: this also releases a writer blocked in the child's stdin.
		if b.cmd != nil && b.cmd.Process != nil {
			_ = b.cmd.Process.Kill()
		}

		if b.stdout != nil {
			_ = b.stdout.Close()
		}

		_ = b.stdin.Close()
	})
}

func (b *Bridge) fail(cause error) error {
	b.abort()

	err := errors.Join(device.ErrDisconnected, cause, b.awaitExit(bridgeCleanupTimeout))
	select {
	case <-b.processDone:
		err = errors.Join(err, b.waitErr)
	default:
	}

	if diagnostic := b.stderr.summary(); diagnostic != "" {
		return fmt.Errorf("%w (%s)", err, diagnostic)
	}

	return err
}

func (b *Bridge) awaitExit(timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-b.processDone:
		return nil
	case <-timer.C:
		return errors.New("bridge cleanup timed out")
	}
}

// Close is idempotent. Idle bridges receive EOF; busy or unresponsive bridges
// are terminated. Neither path waits for an active call to release mu.
func (b *Bridge) Close() error {
	b.startWorkers()
	b.closeOnce.Do(func() {
		b.closed.Store(true)

		if b.mu.TryLock() {
			_ = b.stdin.Close()
			b.mu.Unlock()
			_ = b.awaitExit(bridgeGracePeriod)
		}

		b.abort()
		b.closeErr = b.awaitExit(bridgeCleanupTimeout)
	})

	return b.closeErr
}

// Keep bounded diagnostic text under a mutex: exec writes concurrently with
// startup/call failures. Only recognized error identifiers leave this buffer.
type bridgeDiagnostics struct {
	mu   sync.Mutex
	tail string
}

func (d *bridgeDiagnostics) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.tail += string(p[max(0, len(p)-bridgeDiagnosticLimit):])
	if len(d.tail) > bridgeDiagnosticLimit {
		d.tail = d.tail[len(d.tail)-bridgeDiagnosticLimit:]
	}

	return len(p), nil
}

var bridgeErrorIdentifier = regexp.MustCompile(`(?i)\b(?:0x[0-9a-f]{8}|CS[0-9]{4}|ParserError|COMException|UnauthorizedAccessException|OutOfMemoryException)\b`)

func (d *bridgeDiagnostics) summary() string {
	if d == nil {
		return ""
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.tail == "" {
		return ""
	}

	identifiers := bridgeErrorIdentifier.FindAllString(d.tail, 8)
	if len(identifiers) == 0 {
		return "bridge stderr omitted"
	}

	return "bridge stderr: " + strings.Join(identifiers, ", ")
}

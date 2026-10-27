package garmin

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/DevWolk/garminctl/internal/device"
)

type mutationTestDevice struct {
	*cacheTestDevice
	failure      error
	writeEntered chan struct{}
	writeGate    <-chan struct{}
	handleReads  atomic.Int32
}

func (d *mutationTestDevice) Write(context.Context, string, []byte) error {
	d.replace("changed", false)
	return d.failure
}

func (d *mutationTestDevice) Delete(context.Context, string) error {
	d.replace("changed", false)
	return d.failure
}

func (d *mutationTestDevice) Mkdir(context.Context, string) error {
	d.replace("changed", false)
	return d.failure
}

func (*mutationTestDevice) ListHidden(context.Context, string) ([]device.Entry, error) {
	return []device.Entry{{Name: "hidden.PRG"}}, nil
}

func (d *mutationTestDevice) WriteHidden(ctx context.Context, dir, name string, data []byte) error {
	return d.Write(ctx, dir+"/"+name, data)
}

func (d *mutationTestDevice) DeleteHidden(ctx context.Context, dir, name string) error {
	return d.Delete(ctx, dir+"/"+name)
}

func (d *mutationTestDevice) MtpRead(context.Context, uint32, []uint32) ([]byte, uint32, []uint32, error) {
	d.replace("changed", false)
	return nil, 0x2001, nil, d.failure
}

func (*mutationTestDevice) Handles(context.Context, string) (uint32, uint32, error) {
	return 1, 2, nil
}

func (*mutationTestDevice) AllChildren(context.Context, uint32, uint32) ([]device.MTPObject, error) {
	return []device.MTPObject{{Handle: 3, Name: "hidden.PRG"}}, nil
}

func (*mutationTestDevice) FindChild(context.Context, uint32, uint32, string) (device.MTPObject, bool, error) {
	return device.MTPObject{Handle: 3, Name: "hidden.PRG"}, true, nil
}

func (d *mutationTestDevice) SendFile(ctx context.Context, _, _ uint32, _ string, _ []byte) (uint32, error) {
	if d.writeEntered != nil {
		close(d.writeEntered)

		select {
		case <-d.writeGate:
		case <-ctx.Done():
			return 0, errors.Join(ctx.Err(), d.failure)
		}
	}

	d.replace("changed", false)

	return 3, d.failure
}

func (d *mutationTestDevice) ReadHandle(ctx context.Context, _ uint32) ([]byte, error) {
	d.handleReads.Add(1)
	return d.Read(ctx, "raw handle")
}

func (d *mutationTestDevice) DeleteHandle(context.Context, uint32) error {
	d.replace("changed", false)
	return d.failure
}

func TestFailedMutationInvalidatesRawCache(t *testing.T) {
	t.Parallel()

	operations := map[string]func(context.Context, *Service) error{
		"write":        func(ctx context.Context, s *Service) error { return s.WriteFile(ctx, "file", nil) },
		"delete":       func(ctx context.Context, s *Service) error { return s.DeleteFile(ctx, "file") },
		"mkdir":        func(ctx context.Context, s *Service) error { return s.Mkdir(ctx, "dir") },
		"hidden write": func(ctx context.Context, s *Service) error { return s.WriteHidden(ctx, "dir", "file", nil) },
		"hidden delete": func(ctx context.Context, s *Service) error {
			return s.DeleteHidden(ctx, "dir", "file")
		},
		"raw opcode": func(ctx context.Context, s *Service) error {
			_, _, _, err := s.RawMTPRead(ctx, 0x9999, nil)
			return err
		},
		"handle write": func(ctx context.Context, s *Service) error {
			return s.WithDiagnostics(ctx, func(d *Diagnostics) error { _, err := d.SendFile(1, 2, "file", nil); return err })
		},
		"handle delete": func(ctx context.Context, s *Service) error {
			return s.WithDiagnostics(ctx, func(d *Diagnostics) error { return d.DeleteHandle(3) })
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			failure := errors.New("failure after possible storage change")
			d := &mutationTestDevice{cacheTestDevice: &cacheTestDevice{data: []byte("old")}, failure: failure}
			svc := New(d)
			readCachedTestValue(t, svc, true)

			if err := operation(t.Context(), svc); !errors.Is(err, failure) {
				t.Fatalf("mutation error = %v", err)
			}

			if got := readCachedTestValue(t, svc, true); string(got) != "changed" || d.readCount() != 2 {
				t.Fatalf("failed mutation retained stale cache: %q, reads=%d", got, d.readCount())
			}
		})
	}
}

func TestOptionalCapabilitiesAreExplicitlyUnsupported(t *testing.T) {
	t.Parallel()

	svc := New(&cacheTestDevice{})
	_, storageErr := svc.StorageInfo(t.Context())
	_, listErr := svc.ListHidden(t.Context(), "dir")
	_, _, _, rawErr := svc.RawMTPRead(t.Context(), 0x9999, nil)
	callbackCalled := false

	diagnosticErr := svc.WithDiagnostics(t.Context(), func(*Diagnostics) error { callbackCalled = true; return nil })
	for _, err := range []error{
		storageErr, listErr, rawErr, diagnosticErr,
		svc.WriteHidden(t.Context(), "dir", "file", nil), svc.DeleteHidden(t.Context(), "dir", "file"),
	} {
		if !errors.Is(err, ErrCapabilityUnavailable) {
			t.Fatalf("unsupported capability error = %v", err)
		}
	}

	if callbackCalled {
		t.Fatal("unsupported diagnostics callback ran")
	}
}

func TestReadFilePreservesDirectReadCachePolicy(t *testing.T) {
	t.Parallel()

	d := &cacheTestDevice{data: []byte("fresh source")}
	svc := New(d)

	first, err := svc.ReadFile(t.Context(), "file")
	if err != nil {
		t.Fatal(err)
	}

	d.mu.Lock()
	d.lastRead[0] = 'x'
	d.mu.Unlock()

	if string(first) != "fresh source" {
		t.Fatalf("fresh bytes alias the transport buffer: %q", first)
	}

	d.replace("cached snapshot", false)

	if got, err := svc.ReadCached(t.Context(), "file"); err != nil || string(got) != "cached snapshot" {
		t.Fatalf("fresh read unexpectedly populated the cache: %q, %v", got, err)
	}

	d.replace("new source", false)

	if got, err := svc.ReadFile(t.Context(), "file"); err != nil || string(got) != "new source" {
		t.Fatalf("fresh read used cached bytes: %q, %v", got, err)
	}

	if got, err := svc.ReadCached(t.Context(), "file"); err != nil || string(got) != "cached snapshot" || d.readCount() != 3 {
		t.Fatalf("fresh read replaced the cache: %q, %v, reads=%d", got, err, d.readCount())
	}
}

func TestMutationDoesNotDispatchWithCancelledContext(t *testing.T) {
	t.Parallel()

	d := &mutationTestDevice{cacheTestDevice: &cacheTestDevice{data: []byte("original")}}
	svc := New(d)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for _, err := range []error{
		svc.WriteFile(ctx, "file", nil), svc.DeleteFile(ctx, "file"), svc.Mkdir(ctx, "dir"),
		svc.WriteHidden(ctx, "dir", "file", nil), svc.DeleteHidden(ctx, "dir", "file"),
	} {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-dispatch cancellation = %v", err)
		}
	}

	if got := readCachedTestValue(t, svc, true); string(got) != "original" {
		t.Fatalf("cancelled mutation touched storage: %q", got)
	}
}

func TestLateReadCannotRefillCacheAfterFailedWrite(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})

	var release sync.Once

	unblock := func() { release.Do(func() { close(gate) }) }
	t.Cleanup(unblock)

	d := &mutationTestDevice{
		cacheTestDevice: &cacheTestDevice{data: []byte("old"), entered: make(chan struct{}), release: gate},
		failure:         errors.New("failure after side effect"),
	}
	svc := New(d)
	result := make(chan cacheReadResult, 1)

	ctx := t.Context()
	go func() { data, err := svc.ReadCached(ctx, "file"); result <- cacheReadResult{data, err} }()

	<-d.entered

	if err := svc.WriteFile(ctx, "file", nil); !errors.Is(err, d.failure) {
		t.Fatalf("write error = %v", err)
	}

	if got, err := svc.ReadCached(ctx, "file"); err != nil || string(got) != "changed" {
		t.Fatalf("new cache value = %q, %v", got, err)
	}

	unblock()

	if old := <-result; old.err != nil || string(old.data) != "old" {
		t.Fatalf("old snapshot = %q, %v", old.data, old.err)
	}

	if got, err := svc.ReadCached(ctx, "file"); err != nil || string(got) != "changed" || d.readCount() != 2 {
		t.Fatalf("late read replaced post-write cache: %q, %v, reads=%d", got, err, d.readCount())
	}
}

func TestDiagnosticsCannotOutliveCallback(t *testing.T) {
	t.Parallel()

	d := &mutationTestDevice{cacheTestDevice: &cacheTestDevice{data: []byte("data")}}
	svc := New(d)

	var borrowed *Diagnostics

	if err := svc.WithDiagnostics(t.Context(), func(scope *Diagnostics) error { borrowed = scope; return nil }); err != nil {
		t.Fatal(err)
	}

	if _, _, err := borrowed.Handles("GARMIN"); !errors.Is(err, context.Canceled) {
		t.Fatalf("escaped diagnostics scope still works: %v", err)
	}

	if _, err := borrowed.SendFile(1, 2, "file", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("escaped mutation scope still works: %v", err)
	}

	if got := readCachedTestValue(t, svc, true); string(got) != "data" {
		t.Fatalf("escaped mutation changed storage: %q", got)
	}
}

func TestDiagnosticsCancelsAndJoinsAdmittedOperation(t *testing.T) {
	t.Parallel()

	d := &mutationTestDevice{
		cacheTestDevice: &cacheTestDevice{data: []byte("data")}, failure: device.ErrDisconnected,
		writeEntered: make(chan struct{}), writeGate: make(chan struct{}),
	}
	result := make(chan error, 1)

	err := New(d).WithDiagnostics(t.Context(), func(scope *Diagnostics) error {
		go func() { _, err := scope.SendFile(1, 2, "file", nil); result <- err }()

		<-d.writeEntered

		return nil
	})
	if !errors.Is(err, device.ErrDisconnected) {
		t.Fatalf("ignored child disconnect escaped the owner: %v", err)
	}

	if childErr := <-result; !errors.Is(childErr, context.Canceled) {
		t.Fatalf("admitted operation did not unwind: %v", childErr)
	}
}

func TestDiagnosticDisconnectStopsFurtherCommands(t *testing.T) {
	t.Parallel()

	d := &mutationTestDevice{cacheTestDevice: &cacheTestDevice{data: []byte("data")}, failure: device.ErrDisconnected}

	err := New(d).WithDiagnostics(t.Context(), func(scope *Diagnostics) error {
		if _, err := scope.SendFile(1, 2, "file", nil); !errors.Is(err, device.ErrDisconnected) {
			t.Fatalf("write error = %v", err)
		}

		if _, err := scope.ReadHandle(3); !errors.Is(err, device.ErrDisconnected) {
			t.Fatalf("subsequent operation lost the disconnect cause: %v", err)
		}

		return nil // ignoring the callback's error must not hide a broken transport
	})
	if !errors.Is(err, device.ErrDisconnected) || d.handleReads.Load() != 0 {
		t.Fatalf("broken scope sent more commands: error=%v reads=%d", err, d.handleReads.Load())
	}
}

func TestDiagnosticsSerializesCompleteOperations(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		d := &mutationTestDevice{
			cacheTestDevice: &cacheTestDevice{data: []byte("data")},
			writeEntered:    make(chan struct{}), writeGate: gate,
		}

		err := New(d).WithDiagnostics(t.Context(), func(scope *Diagnostics) error {
			write := make(chan error, 1)
			read := make(chan error, 1)

			go func() { _, err := scope.SendFile(1, 2, "file", nil); write <- err }()

			<-d.writeEntered

			go func() { _, err := scope.ReadHandle(3); read <- err }()

			synctest.Wait()

			if d.handleReads.Load() != 0 {
				t.Error("read interleaved with a multi-command write")
			}

			close(gate)

			return errors.Join(<-write, <-read)
		})
		if err != nil || d.handleReads.Load() != 1 {
			t.Fatalf("serialized operations error=%v reads=%d", err, d.handleReads.Load())
		}
	})
}

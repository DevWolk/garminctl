package mcpx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

const (
	watchEventAcquire = "acquire"
	watchEventBody    = "body"
	watchEventRelease = "release"
	watchEventCancel  = "cancel"
	watchEventDrop    = "drop"
)

type toolLease struct {
	owner   *toolOwner
	release error
}

func (l *toolLease) Context() context.Context          { return l.owner.ctx }
func (*toolLease) Epoch() watchsession.ConnectionEpoch { return 7 }
func (l *toolLease) Release() error {
	l.owner.events = append(l.owner.events, watchEventRelease)
	return l.release
}

type toolOwner struct {
	ctx     context.Context //nolint:containedctx // records the context borrowed by a test acquisition until release
	lease   *toolLease
	acquire error
	drop    error
	events  []string
	epoch   watchsession.ConnectionEpoch
}

func (o *toolOwner) Acquire(ctx context.Context) (*toolLease, error) {
	o.ctx = ctx

	o.events = append(o.events, watchEventAcquire)
	if o.acquire != nil {
		return nil, o.acquire
	}

	return o.lease, nil
}

func (o *toolOwner) DropAt(epoch watchsession.ConnectionEpoch) error {
	o.events = append(o.events, watchEventDrop)
	o.epoch = epoch

	return o.drop
}

type watchCase struct {
	name    string
	acquire error
	body    error
	release error
	drop    error
	events  []string
}

func TestWithWatchErrorAndCleanupContract(t *testing.T) {
	t.Parallel()

	failure, cleanup := errors.New("body failure"), errors.New("cleanup failure")
	for _, test := range []watchCase{
		{name: "success", events: []string{watchEventAcquire, watchEventBody, watchEventRelease, watchEventCancel}},
		{name: "acquire error", acquire: device.ErrBusy, events: []string{watchEventAcquire, watchEventCancel}},
		{name: "body and cleanup error", body: failure, release: cleanup, events: []string{watchEventAcquire, watchEventBody, watchEventRelease, watchEventCancel}},
		{name: "disconnect before release", body: device.ErrDisconnected, drop: failure, release: cleanup, events: []string{watchEventAcquire, watchEventBody, watchEventDrop, watchEventRelease, watchEventCancel}},
		{name: "cancel is not disconnect", body: context.Canceled, events: []string{watchEventAcquire, watchEventBody, watchEventRelease, watchEventCancel}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			checkWatchHandler(t, test)
		})
	}
}

func checkWatchHandler(t *testing.T, test watchCase) {
	t.Helper()

	owner := &toolOwner{acquire: test.acquire, drop: test.drop}
	owner.lease = &toolLease{owner: owner, release: test.release}

	bind := func(ctx context.Context) (context.Context, context.CancelFunc) {
		bound, cancel := context.WithCancel(ctx)

		return bound, func() { owner.events = append(owner.events, watchEventCancel); cancel() }
	}
	handler := WithWatch(owner, bind, func(ctx context.Context, lease *toolLease, _ struct{}) (metadataOutput, error) {
		owner.events = append(owner.events, watchEventBody)

		if ctx != lease.Context() || ctx.Err() != nil {
			t.Error("body did not receive the live bound lease context")
		}

		return metadataOutput{Reply: "result"}, test.body
	})

	result, out, err := handler(t.Context(), nil, struct{}{})
	if result != nil || !slices.Equal(owner.events, test.events) || !errors.Is(owner.ctx.Err(), context.Canceled) {
		t.Fatalf("result=%v events=%v bound error=%v", result, owner.events, owner.ctx.Err())
	}

	requireWatchErrors(t, err, test.acquire, test.body, test.release, test.drop)

	wantOut := "result"
	if test.acquire != nil {
		wantOut = ""
	}

	if out.Reply != wantOut {
		t.Fatalf("output=%+v error=%v", out, err)
	}

	if errors.Is(test.body, device.ErrDisconnected) && owner.epoch != 7 {
		t.Fatalf("dropped wrong epoch %d", owner.epoch)
	}
}

func TestWithWatchAcceptsSessionManagerDirectly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}

	manager := watchsession.NewManager(func(context.Context) (device.Device, error) { return device.NewDirFS(root) })

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		if err := manager.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	handler := WithWatch(manager, nil, func(ctx context.Context, lease *watchsession.Lease, _ struct{}) (metadataOutput, error) {
		data, err := lease.Session().ReadFile(ctx, "file")
		return metadataOutput{Reply: string(data)}, err
	})

	_, out, err := handler(t.Context(), nil, struct{}{})
	if err != nil || out.Reply != "value" || manager.Gate().Snapshot().Operations != 0 {
		t.Fatalf("output=%+v error=%v admission=%+v", out, err, manager.Gate().Snapshot())
	}
}

func requireWatchErrors(t *testing.T, err error, expected ...error) {
	t.Helper()

	wantError := false

	for _, want := range expected {
		if want != nil {
			wantError = true

			if !errors.Is(err, want) {
				t.Fatalf("lost error %v: %v", want, err)
			}
		}
	}

	if !wantError && err != nil {
		t.Fatalf("unexpected tool error: %v", err)
	}
}

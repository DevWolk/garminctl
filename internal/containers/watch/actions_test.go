package watch_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/watch"
	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/fit"
	"github.com/DevWolk/garminctl/internal/ship/testdump"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

func TestSummaryUsesSuppliedRegistryCapability(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "count", want: 8},
		{name: "optional error", err: errors.New("registry unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dev, _ := testdump.Watch(t)
			sess := watchsession.New(dev)

			t.Cleanup(func() { _ = sess.Close() })

			calls := 0
			count := func(ctx context.Context, got *watchsession.Session) (int, error) {
				calls++

				if got != sess || ctx != t.Context() {
					t.Error("counter received a different Session/context")
				}

				return 8, test.err
			}

			sum, err := watch.NewSummaryAction(fit.MustProfile(), count).Run(t.Context(), sess)
			if err != nil || sum == nil || sum.AppCount != test.want || sum.NewFiles != 2 || calls != 1 {
				t.Fatalf("summary=%+v error=%v counter calls=%d", sum, err, calls)
			}
		})
	}
}

type readWatch struct {
	device.Device
	reads int
}

func (d *readWatch) Read(ctx context.Context, p string) ([]byte, error) {
	d.reads++
	return d.Device.Read(ctx, p)
}

func TestDecodeRetainsFreshReadAndCachePopulation(t *testing.T) {
	t.Parallel()

	dev, _ := testdump.Watch(t)
	d := &readWatch{Device: dev}
	sess := watchsession.New(d)

	t.Cleanup(func() { _ = sess.Close() })

	action := watch.NewDecodeAction(fit.MustProfile())

	first, err := action.Run(t.Context(), sess, watchsession.PathSettings)
	if err != nil {
		t.Fatal(err)
	}

	second, err := action.Run(t.Context(), sess, watchsession.PathSettings)
	if err != nil || len(first) == 0 || !reflect.DeepEqual(first, second) || d.reads != 2 {
		t.Fatalf("fresh decode reads=%d error=%v", d.reads, err)
	}

	if _, err := sess.ReadCached(t.Context(), watchsession.PathSettings); err != nil || d.reads != 2 {
		t.Fatalf("decode did not populate shared cache: reads=%d error=%v", d.reads, err)
	}
}

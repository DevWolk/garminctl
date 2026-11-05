package settings

import (
	"bytes"
	"context"
	"math"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
)

// writeCountDevice counts write attempts before delegating them.
type writeCountDevice struct {
	device.Device
	writes int
}

func (d *writeCountDevice) Write(ctx context.Context, path string, data []byte) error {
	d.writes++
	return d.Device.Write(ctx, path, data)
}

// writeCountFixture serves the shared dump, without a NewFiles directory,
// through a write counter.
func writeCountFixture(t *testing.T) (*watchsession.Session, *writeCountDevice) {
	t.Helper()

	fs, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = fs.Close() })

	d := &writeCountDevice{Device: fs}

	return watchsession.New(d), d
}

func TestSettingsRejectInvalidValuesBeforeWriting(t *testing.T) {
	t.Parallel()

	negativeIndex, largeIndex := -1, math.MaxUint8+1
	cases := []Change{
		{Mesg: "2", Field: "58", Index: -1, Value: 1},
		{Mesg: "2", Field: "58", ArrayIndex: &negativeIndex, Value: 1},
		{Mesg: "2", Field: "58", ArrayIndex: &largeIndex, Value: 1},
		{Mesg: "2", Field: "58", Value: math.NaN()},
		{Mesg: "2", Field: "58", Value: math.Inf(1)},
		{Mesg: "2", Field: "58", Value: math.Inf(-1)},
		{Mesg: "2", Field: "58", Value: float64(0x1p63)},
		{Mesg: "65000", Field: "1", Value: 1.5},
	}

	sess, d := writeCountFixture(t)

	apply := NewApplyAction(fit.MustProfile())
	for _, change := range cases {
		result, err := apply.Run(t.Context(), sess, []Change{change})
		if err == nil || result != nil || d.writes != 0 {
			t.Fatalf("invalid setting %+v staged: %+v, %v, writes=%d", change, result, err, d.writes)
		}
	}
}

func TestApplyChangesDoesNotWriteAfterEncodingFailure(t *testing.T) {
	t.Parallel()

	sess, d := writeCountFixture(t)
	change := Change{Mesg: "2", Field: "58", Index: math.MaxUint16 + 1, Value: 1500}

	result, err := NewApplyAction(fit.MustProfile()).Run(t.Context(), sess, []Change{change})
	if err == nil || result != nil || d.writes != 0 {
		t.Fatalf("invalid settings staged: result=%+v error=%v writes=%d", result, err, d.writes)
	}
}

func TestSettingsQueuesTwoChangesWithoutRewritingSource(t *testing.T) {
	t.Parallel()

	sess, d := actionFixture(t)

	before, err := d.Read(t.Context(), PathSettings)
	if err != nil {
		t.Fatal(err)
	}

	apply := NewApplyAction(fit.MustProfile())
	start := fit.Timestamp(time.Now())

	first, err := apply.Run(t.Context(), sess, []Change{{Mesg: "2", Field: "58", Value: 1500}})
	if err != nil {
		t.Fatal(err)
	}

	second, err := apply.Run(t.Context(), sess, []Change{{Mesg: "2", Field: "58", Value: 1600}})
	if err != nil {
		t.Fatal(err)
	}

	if first.Path == second.Path {
		t.Fatal("successive apply replaced one pending file")
	}

	entries, err := d.List(t.Context(), PathNewFiles)
	if err != nil || len(entries) != 2 {
		t.Fatalf("pending=%v error=%v", entries, err)
	}

	for i, result := range []*ApplyResult{first, second} {
		data, err := d.Read(t.Context(), result.Path)
		if err != nil {
			t.Fatal(err)
		}

		checkQueuedSettingsFIT(t, data, uint64(1500+i*100), start)

		if result.Bytes != len(data) || len(result.Changes) != 1 || result.Note == "" {
			t.Fatalf("apply result=%+v", result)
		}
	}

	after, err := d.Read(t.Context(), PathSettings)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("Settings.fit was rewritten: %v", err)
	}
}

func checkQueuedSettingsFIT(t *testing.T, data []byte, want uint64, start uint32) {
	t.Helper()

	f, err := fit.Decode(data)
	if err != nil {
		t.Fatal(err)
	}

	messages := map[uint16]*fit.Message{}

	for _, m := range f.Messages() {
		if !m.Def.BigEndian || m.Def.LocalNum != 0 {
			t.Fatal("pending settings definition changed")
		}

		messages[m.Def.GlobalNum] = m
	}

	for _, num := range []uint16{0, 49, 217} {
		if messages[num] == nil {
			t.Fatalf("pending message %d missing", num)
		}
	}

	for _, field := range []struct {
		message uint16
		field   uint8
		want    uint64
	}{
		{0, 0, 41},
		{0, 3, 1234567890},
		{49, 0, 1705},
		{217, 0, 2},
		{217, 2, 58},
		{217, 4, want},
	} {
		if got := messages[field.message].Value(field.field); got != field.want {
			t.Fatalf("pending message %d field %d=%v; want %d", field.message, field.field, got, field.want)
		}
	}

	id := messages[0]

	ts, ok := id.Value(4).(uint64)
	if !ok || ts < uint64(start) || ts > uint64(fit.Timestamp(time.Now())) {
		t.Fatalf("pending timestamp=%v", id.Value(4))
	}
}

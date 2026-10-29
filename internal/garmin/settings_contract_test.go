package garmin

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestSettingsDTOJSONContract(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "row zero", value: SettingsRow{}, want: `{"mesg":"","mesgNum":0,"index":0,"field":"","fieldNum":0,"known":false,"value":null,"raw":null}`},
		{name: "row empty options", value: SettingsRow{Options: []string{}}, want: `{"mesg":"","mesgNum":0,"index":0,"field":"","fieldNum":0,"known":false,"value":null,"raw":null}`},
		{name: "change zero", value: Change{}, want: `{"mesg":"","field":"","value":null}`},
		{name: "resolved zero", value: ResolvedChange{}, want: `{"mesg":"","field":"","value":null,"mesgNum":0,"fieldNum":0,"raw":0}`},
		{name: "resolved previous zero", value: ResolvedChange{Raw: -1, Previous: 0}, want: `{"mesg":"","field":"","value":null,"mesgNum":0,"fieldNum":0,"raw":-1,"previous":0}`},
		{name: "result nil", value: ApplyResult{}, want: `{"path":"","bytes":0,"changes":null,"note":""}`},
		{name: "result empty", value: ApplyResult{Changes: []ResolvedChange{}}, want: `{"path":"","bytes":0,"changes":[],"note":""}`},
		{name: "schema zero", value: SchemaField{}, want: `{"num":0,"name":"","type":"","base":"","array":false}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(test.value)
			if err != nil || string(data) != test.want {
				t.Fatalf("JSON=%s error=%v; want %s", data, err, test.want)
			}
		})
	}
}

func TestSettingsQueuesTwoChangesWithoutRewritingSource(t *testing.T) {
	t.Parallel()

	d := newSettingsContractDevice(t)

	before, err := d.Read(t.Context(), PathSettings)
	if err != nil {
		t.Fatal(err)
	}

	svc := New(d)
	start := fit.Timestamp(time.Now())

	first, err := svc.ApplyChanges(t.Context(), []Change{{Mesg: "2", Field: "58", Value: 1500}})
	if err != nil {
		t.Fatal(err)
	}

	second, err := svc.ApplyChanges(t.Context(), []Change{{Mesg: "2", Field: "58", Value: 1600}})
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

func newSettingsContractDevice(t *testing.T) device.Device {
	t.Helper()

	d, err := device.NewDirFS(testdump.Root(t))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = d.Close() })

	if err := d.Mkdir(t.Context(), PathNewFiles); err != nil {
		t.Fatal(err)
	}

	return d
}

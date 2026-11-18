package settings

import (
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/fit"
)

const (
	deviceSettings = "deviceSettings"
	timeZoneOffset = "timeZoneOffset"
)

// rangeView holds deviceSettings (message 2) with an undocumented uint8 array
// field 104 and the documented sint8 timeZoneOffset (field 5).
func rangeView() *View {
	return &View{Messages: []fit.NamedMessage{{
		Num: 2, Name: deviceSettings, Known: true,
		Fields: []fit.NamedField{
			{Num: 104, Base: "uint8", Raw: []any{uint8(1), uint8(2), uint8(3), uint8(4), uint8(5)}},
			{Num: 5, Name: timeZoneOffset, Base: "sint8", Known: true, Raw: []any{int8(0)}},
		},
	}}}
}

func TestResolveParsesTextForUndocumentedField(t *testing.T) {
	t.Parallel()

	change, err := ParseChangeSpec("deviceSettings.104[4]=8")
	if err != nil {
		t.Fatal(err)
	}

	rc, err := Resolve(fit.MustProfile(), change, rangeView())
	if err != nil {
		t.Fatal(err)
	}

	if rc.MesgNum != 2 || rc.FieldNum != 104 || rc.Raw != 8 || rc.Previous != uint8(5) {
		t.Fatalf("resolved %+v", rc)
	}

	if _, err := Resolve(fit.MustProfile(), Change{Mesg: "2", Field: "104", Value: "eight"}, rangeView()); err == nil {
		t.Fatal("non-numeric text accepted for an undocumented field")
	}
}

func TestResolveRoundsNegativeScaledValue(t *testing.T) {
	t.Parallel()

	rc, err := Resolve(fit.MustProfile(), Change{Mesg: deviceSettings, Field: timeZoneOffset, Value: "-5"}, rangeView())
	if err != nil || rc.Raw != -20 {
		t.Fatalf("timeZoneOffset -5 h: raw %d, %v, want -20", rc.Raw, err)
	}
}

func TestResolveRefusesValuesOutsideBaseType(t *testing.T) {
	t.Parallel()

	view := rangeView()
	cases := []Change{
		{Mesg: "2", Field: "104", Value: "300"},
		{Mesg: "2", Field: "104", Value: "-5"},
		{Mesg: deviceSettings, Field: timeZoneOffset, Value: "-40"}, // -160 raw
		{Mesg: deviceSettings, Field: timeZoneOffset, Value: 32},    // 128 raw
		{Mesg: deviceSettings, Field: "activeTimeZone", Value: 256}, // profile uint8, not in the view
		{Mesg: deviceSettings, Field: "activeTimeZone", Value: -1},
	}

	for _, c := range cases {
		if rc, err := Resolve(fit.MustProfile(), c, view); err == nil {
			t.Fatalf("%+v accepted as raw %d", c, rc.Raw)
		}
	}

	for _, c := range []Change{
		{Mesg: "2", Field: "104", Value: "255"},
		{Mesg: deviceSettings, Field: timeZoneOffset, Value: "-32"},
		{Mesg: "65000", Field: "1", Value: -5}, // no definition to check against
	} {
		if _, err := Resolve(fit.MustProfile(), c, view); err != nil {
			t.Fatalf("%+v refused: %v", c, err)
		}
	}
}

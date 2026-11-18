package fit

import (
	"math"
	"testing"
)

func TestProfileRejectsScaledIntegerOverflowBeforeCast(t *testing.T) {
	t.Parallel()

	p := &Profile{}

	fp := &FieldProfile{Name: "scaled", BaseType: "uint32", Scale: 100}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0x1p63, -0x1p63} {
		if got, err := p.ParseValue(fp, value); err == nil || got != nil {
			t.Fatalf("scaled %v accepted: %v, %v", value, got, err)
		}
	}

	for value, want := range map[float64]int64{1.25: 125, 1.255: 125, -1.25: -125, -1.255: -125, -1.256: -126} {
		got, err := p.ParseValue(fp, value)
		if err != nil || got != want {
			t.Fatalf("scaled %v: %v, %v, want %d", value, got, err, want)
		}
	}
}

func TestProfileRoundsNegativeScaledValuesAwayFromZero(t *testing.T) {
	t.Parallel()

	p := &Profile{}

	// timeZoneOffset: sint8, scale 4 (hours).
	fp := &FieldProfile{Name: "timeZoneOffset", BaseType: "sint8", Scale: 4}
	for value, want := range map[string]int64{"-5": -20, "-4.75": -19, "-0.125": -1, "0.125": 1, "5.5": 22} {
		got, err := p.ParseValue(fp, value)
		if err != nil || got != want {
			t.Fatalf("%s: %v, %v, want %d", value, got, err, want)
		}
	}
}

func TestProfileParsesTextForFieldsWithoutProfile(t *testing.T) {
	t.Parallel()

	p := &Profile{}

	for value, want := range map[string]any{"8": int64(8), "-3": int64(-3), "2.5": 2.5, "true": int64(1), "false": int64(0)} {
		got, err := p.ParseValue(nil, value)
		if err != nil || got != want {
			t.Fatalf("%q: %v (%T), %v, want %v", value, got, got, err, want)
		}
	}

	if got, err := p.ParseValue(nil, "eight"); err == nil {
		t.Fatalf("non-numeric text accepted: %v", got)
	}

	if got, err := p.ParseValue(nil, int64(7)); err != nil || got != int64(7) {
		t.Fatalf("number changed: %v, %v", got, err)
	}
}

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

	for value, want := range map[float64]int64{1.25: 125, 1.255: 125, -1.25: -124} {
		got, err := p.ParseValue(fp, value)
		if err != nil || got != want {
			t.Fatalf("existing scale/rounding changed for %v: %v, %v, want %d", value, got, err, want)
		}
	}
}

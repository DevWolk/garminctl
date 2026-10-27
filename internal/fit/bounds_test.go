package fit

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

var integerBaseTypes = []BaseType{
	BaseSint8, BaseSint16, BaseSint32, BaseSint64,
	BaseEnum, BaseByte, BaseUint8, BaseUint8z, BaseUint16, BaseUint16z,
	BaseUint32, BaseUint32z, BaseUint64, BaseUint64z,
}

const (
	elementMutation = "element"
	sliceMutation   = "slice"
)

type integerValueCase struct {
	value any
	bits  uint64
}

func TestFieldIntegerBounds(t *testing.T) {
	t.Parallel()

	for _, bt := range integerBaseTypes {
		for _, big := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/big=%v", bt, big), func(t *testing.T) {
				t.Parallel()

				valid, invalid := integerBoundaryCases(bt)
				for _, tc := range valid {
					assertValidIntegerMutations(t, bt, big, tc)
				}

				for _, value := range invalid {
					assertRejectedIntegerMutations(t, bt, big, value)
				}
			})
		}
	}
}

func integerBoundaryCases(bt BaseType) ([]integerValueCase, []any) {
	valid := []integerValueCase{{false, 0}, {true, 1}, {float64(3), 3}, {float32(4), 4}}
	invalid := []any{1.5, math.NaN(), math.Inf(1), math.Inf(-1), "12"}
	bits := bt.Size() * 8

	switch bt {
	case BaseSint8, BaseSint16, BaseSint32, BaseSint64:
		minValue, maxValue := int64(math.MinInt64), int64(math.MaxInt64)
		if bits < 64 {
			maxValue = (int64(1) << (bits - 1)) - 1
			minValue = -maxValue - 1
			invalid = append(invalid, minValue-1, maxValue+1)
		} else {
			invalid = append(invalid, uint64(math.MaxInt64)+1, math.Nextafter(-0x1p63, math.Inf(-1)))
		}

		valid = append(valid, integerValueCase{minValue, uint64(minValue)}, integerValueCase{maxValue, uint64(maxValue)}) //nolint:gosec // expected two's-complement bit patterns
	default:
		maxValue := uint64(math.MaxUint64) >> (64 - bits)
		valid = append(valid, integerValueCase{uint64(0), 0}, integerValueCase{maxValue, maxValue})
		invalid = append(invalid, -1)

		if bits < 64 {
			invalid = append(invalid, maxValue+1)
		} else {
			valid = append(valid, integerValueCase{uint64(math.MaxInt64) + 1, 0x8000000000000000})
			x := math.Nextafter(0x1p64, 0)
			valid = append(valid, integerValueCase{x, uint64(x)})
			invalid = append(invalid, float64(0x1p64))
		}
	}

	return valid, invalid
}

func integerTestField(bt BaseType) Field {
	return Field{Def: FieldDef{BaseType: bt}, Raw: bytes.Repeat([]byte{0xa5}, 2*bt.Size())}
}

func integerBytes(bt BaseType, bits uint64, big bool) []byte {
	var data [8]byte
	if big {
		binary.BigEndian.PutUint64(data[:], bits)
		return data[8-bt.Size():]
	}

	binary.LittleEndian.PutUint64(data[:], bits)

	return data[:bt.Size()]
}

func assertValidIntegerMutations(t *testing.T, bt BaseType, big bool, tc integerValueCase) {
	t.Helper()

	for _, mode := range []string{"scalar", sliceMutation, elementMutation} {
		f := integerTestField(bt)
		want := bytes.Clone(f.Raw)

		offset := 0
		if mode == elementMutation {
			offset = bt.Size()
		}

		copy(want[offset:], integerBytes(bt, tc.bits, big))

		if err := mutateIntegerField(&f, mode, tc.value, big); err != nil {
			t.Fatalf("%s(%v): %v", mode, tc.value, err)
		}

		if !bytes.Equal(f.Raw, want) {
			t.Fatalf("%s(%v) bytes=%x, want=%x", mode, tc.value, f.Raw, want)
		}
	}
}

func mutateIntegerField(f *Field, mode string, value any, big bool) error {
	switch mode {
	case elementMutation:
		return f.SetElement(1, value, big)
	case sliceMutation:
		return f.SetValue([]any{value}, big)
	default:
		return f.SetValue(value, big)
	}
}

func assertRejectedIntegerMutations(t *testing.T, bt BaseType, big bool, value any) {
	t.Helper()

	for _, mode := range []string{"scalar", sliceMutation, elementMutation} {
		f := integerTestField(bt)
		before := bytes.Clone(f.Raw)

		var err error

		if mode == sliceMutation {
			err = f.SetValue([]any{1, value}, big)
		} else {
			err = mutateIntegerField(&f, mode, value, big)
		}

		if err == nil || !bytes.Equal(f.Raw, before) {
			t.Fatalf("%s(%v) error=%v raw=%x, original=%x", mode, value, err, f.Raw, before)
		}
	}
}

func TestFieldInvalidSentinelsAndBackingArray(t *testing.T) {
	t.Parallel()

	for _, bt := range integerBaseTypes {
		for _, big := range []bool{false, true} {
			f := integerTestField(bt)

			original := f.Raw
			if err := f.SetValue([]any{nil}, big); err != nil {
				t.Fatal(err)
			}

			if &f.Raw[0] != &original[0] || !bytes.Equal(f.Raw[:bt.Size()], bt.InvalidBytes(big)) {
				t.Fatalf("%s nil changed backing storage or invalid sentinel", bt)
			}

			if !bytes.Equal(f.Raw[bt.Size():], bytes.Repeat([]byte{0xa5}, bt.Size())) {
				t.Fatalf("%s short slice changed the existing tail", bt)
			}
		}
	}
}

func TestFieldElementRejectsInvalidIndices(t *testing.T) {
	t.Parallel()

	for _, bt := range append([]BaseType{BaseString}, integerBaseTypes...) {
		f := integerTestField(bt)

		before := bytes.Clone(f.Raw)
		for _, index := range []int{-1, math.MinInt, 2, math.MaxInt} {
			if err := f.SetElement(index, 1, false); err == nil || !bytes.Equal(f.Raw, before) {
				t.Fatalf("%s index=%d error=%v raw=%x", bt, index, err, f.Raw)
			}
		}
	}
}

func TestSignedFloat64ExclusiveUpperBound(t *testing.T) {
	t.Parallel()

	for _, big := range []bool{false, true} {
		assertRejectedIntegerMutations(t, BaseSint64, big, float64(0x1p63))
		assertValidIntegerMutations(t, BaseSint64, big, integerValueCase{float64(-0x1p63), 0x8000000000000000})
	}
}

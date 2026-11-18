package fit

import (
	"fmt"
	"math"
)

// integerBits validates a value before producing its two's-complement bits.
// FIT's reserved invalid values remain writable within each type's full range;
// decoding/encoding untouched Raw bytes does not call this mutation helper.
func integerBits(bt BaseType, v any) (uint64, error) {
	switch bt {
	case BaseSint8, BaseSint16, BaseSint32, BaseSint64:
		return signedBits(bt, v)
	default:
		u, err := toUint(v)
		if err != nil {
			return 0, err
		}

		bits := bt.Size() * 8
		if bits < 64 && u >= uint64(1)<<bits {
			return 0, fmt.Errorf("integer %d does not fit %s", u, bt)
		}

		return u, nil
	}
}

func signedBits(bt BaseType, v any) (uint64, error) {
	i, err := toInt(v)
	if err != nil {
		return 0, err
	}

	bits := bt.Size() * 8
	if bits < 64 {
		limit := int64(1) << (bits - 1)
		if i < -limit || i >= limit {
			return 0, fmt.Errorf("integer %d does not fit %s", i, bt)
		}
	}

	return uint64(i), nil //nolint:gosec // validated signed range; encode the two's-complement representation
}

func toUint(v any) (uint64, error) {
	if u, ok := unsignedInt(v); ok {
		return u, nil
	}

	switch x := v.(type) {
	case float32:
		return floatToUint(float64(x))
	case float64:
		return floatToUint(x)
	default:
		i, err := toInt(v)
		if err != nil {
			return 0, err
		}

		if i < 0 {
			return 0, fmt.Errorf("unsigned integer expected, got %d", i)
		}

		return uint64(i), nil
	}
}

func floatToUint(x float64) (uint64, error) {
	if x != math.Trunc(x) {
		return 0, fmt.Errorf("integer expected, got %v", x)
	}

	// MaxUint64 rounds up to 2^64 as a float, which is outside the range.
	if x < 0 || x >= 0x1p64 {
		return 0, fmt.Errorf("integer %v is outside uint64 range", x)
	}

	return uint64(x), nil
}

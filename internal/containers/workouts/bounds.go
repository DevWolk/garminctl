package workouts

import (
	"errors"
	"fmt"
	"math"
	"strconv"
)

func numericTargetRange(lo, hi string) (float64, float64, error) {
	a, err := strconv.ParseFloat(lo, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("target lower bound: %w", err)
	}

	b, err := strconv.ParseFloat(hi, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("target upper bound: %w", err)
	}

	return a, b, nil
}

func validPaceDuration(seconds float64) bool {
	return seconds > 0 && !math.IsNaN(seconds) && !math.IsInf(seconds, 0)
}

// validateFITUint32 checks before truncating fractional protocol units. It does
// not introduce a new rounding rule or impose physiological target limits.
func validateFITUint32(name string, value float64) error {
	if math.IsNaN(value) || value < 0 || value >= 0x1p32 {
		return fmt.Errorf("%s must be finite and fit an unsigned 32-bit slot", name)
	}

	return nil
}

func validateFITTarget(t target) error {
	if math.IsNaN(t.low) || math.IsNaN(t.hi) || t.low < 0 || t.hi < 0 {
		return errors.New("target bounds must be finite nonnegative numbers")
	}

	scale, offset := 1.0, 0.0

	switch t.kind {
	case targetNone, targetHRZone:
		return nil
	case targetHR:
		offset = 100
	case targetPace, targetSpeed:
		scale = 1000
	case targetPower:
		offset = 1000
	}

	if err := validateFITUint32("target lower bound", (t.low+offset)*scale); err != nil {
		return err
	}

	return validateFITUint32("target upper bound", (t.hi+offset)*scale)
}

func validateFITStepCount(steps []StepSpec) error {
	remaining := math.MaxUint16
	for _, st := range steps {
		if remaining == 0 {
			return errors.New("workout has too many FIT steps")
		}

		remaining--
		if st.Repeat > 0 {
			if len(st.Steps) > remaining {
				return errors.New("workout has too many FIT steps")
			}

			remaining -= len(st.Steps)
		}
	}

	return nil
}

func validateStepNumbers(st StepSpec) error {
	if st.Duration != "" {
		sec, err := parseDurationSec(st.Duration)
		if err != nil {
			return err
		}

		if err := validateFITUint32("duration milliseconds", sec*1000); err != nil {
			return err
		}
	}

	if st.Distance != "" {
		m, err := parseDistanceM(st.Distance)
		if err != nil {
			return err
		}

		if err := validateFITUint32("distance centimetres", m*100); err != nil {
			return err
		}
	}

	target, err := parseTarget(st.Target)
	if err != nil {
		return err
	}

	return validateFITTarget(target)
}

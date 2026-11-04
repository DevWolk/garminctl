package workouts

import "math"

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	case uint32:
		return float64(t)
	case uint16:
		return float64(t)
	case uint8:
		return float64(t)
	case int32:
		return float64(t)
	case int16:
		return float64(t)
	case int8:
		return float64(t)
	}

	return math.NaN()
}

func asInt(m map[string]any, k string) int {
	if v, ok := m[k].(float64); ok {
		return int(math.Round(v))
	}

	return 0
}

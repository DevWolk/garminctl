package clix

import (
	"encoding/json"
	"strconv"
	"strings"
)

// ParseScalar accepts booleans, numbers and JSON objects or arrays, otherwise a string.
func ParseScalar(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	}

	if n, err := strconv.ParseFloat(v, 64); err == nil && strconv.FormatFloat(n, 'g', -1, 64) == v {
		return n
	}

	if strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
		var j any
		if json.Unmarshal([]byte(v), &j) == nil {
			return j
		}
	}

	return v
}

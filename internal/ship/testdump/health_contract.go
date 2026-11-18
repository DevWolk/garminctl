package testdump

import (
	"fmt"
	"regexp"
	"testing"
	"time"
)

// HealthEnvironment isolates caches shared by the runtime and compatibility API.
// Callers must run sequentially because cache paths depend on process variables.
func HealthEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
}

// NormalizeHealthDates retains daily order and offsets while removing today's
// calendar value. Fixed historical dates and all metric values remain unchanged.
func NormalizeHealthDates(data []byte) []byte {
	today := time.Now()

	dates := make(map[string]string, 367)
	for offset := range 367 {
		dates[today.AddDate(0, 0, -offset).Format(time.DateOnly)] = fmt.Sprintf("<day-%03d>", offset)
	}

	return regexp.MustCompile(`\d{4}-\d{2}-\d{2}`).ReplaceAllFunc(data, func(value []byte) []byte {
		if replacement, ok := dates[string(value)]; ok {
			return []byte(replacement)
		}

		return value
	})
}

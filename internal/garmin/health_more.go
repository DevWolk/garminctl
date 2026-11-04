package garmin

import "github.com/DevWolk/garminctl/internal/containers/health"

// FormatDuration preserves existing CLI record formatting.
func FormatDuration(sec float64) string { return health.FormatDuration(sec) }

// Package connect owns Garmin Connect synchronization contracts.
package connect

// PulledFile is one message delivered to the watch.
type PulledFile struct {
	MessageID int64  `json:"messageId"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Bytes     int    `json:"bytes"`
	Error     string `json:"error,omitempty"`
}

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

// Written reports how many files reached the watch and the first delivery
// error. A failed delivery is listed with Error set and no Path.
func Written(files []PulledFile) (count int, failure string) {
	for _, f := range files {
		switch {
		case f.Error == "" && f.Path != "":
			count++
		case failure == "" && f.Error != "":
			failure = f.Error
		}
	}

	return count, failure
}

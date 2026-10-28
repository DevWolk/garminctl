package httpx

import "testing"

const loopback = "127.0.0.1"

func TestHostAllowed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		host       string
		listenHost string
		port       string
		expected   bool
	}{
		{name: "loopback with port", host: loopback + ":8787", listenHost: loopback, port: "8787", expected: true},
		{name: "localhost", host: "localhost:8787", listenHost: loopback, port: "8787", expected: true},
		{name: "ipv6 loopback", host: "[::1]:8787", listenHost: loopback, port: "8787", expected: true},
		{name: "lan ip when listening everywhere", host: "192.168.1.5:8787", listenHost: "0.0.0.0", port: "8787", expected: true},
		{name: "configured name", host: "watchbox.lan:8787", listenHost: "watchbox.lan", port: "8787", expected: true},
		{name: "other port", host: loopback + ":80", listenHost: loopback, port: "8787", expected: false},
		{name: "rebound name", host: "garmin.example:8787", listenHost: loopback, port: "8787", expected: false},
		{name: "rebound name on open listener", host: "garmin.example:8787", listenHost: "0.0.0.0", port: "8787", expected: false},
		{name: "any port when unknown", host: "localhost:1", port: "", expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := hostAllowed(tt.host, tt.listenHost, tt.port); got != tt.expected {
				t.Fatalf("hostAllowed(%q, %q, %q) = %v, want %v", tt.host, tt.listenHost, tt.port, got, tt.expected)
			}
		})
	}
}

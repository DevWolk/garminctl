package gc

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestValidateSavedStateUsesAccountAndMFATypesWithoutExpiryMutation(t *testing.T) {
	t.Parallel()

	state := pendingMFAState{Version: 1, ID: "fixture", Domain: DefaultDomain, Email: "fixture@example.invalid", Method: "email", Params: url.Values{"clientId": {clientID}, "service": {"https://mobile.integration.garmin.com/gcm/android"}}, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	state.ExpiresAt = state.CreatedAt.Add(pendingTTL)

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		data           string
		pending, valid bool
	}{
		{`{"email":"edited@example.invalid","oauth1":{"oauth_token":"fake"},"savedAt":"2026-01-01T00:00:00Z"}`, false, true},
		{string(data), true, true},
		{`{"oauth2":{"expires_at":"future-format"}}`, false, false},
		{`{"version":99}`, true, false},
		{`{"email":123}`, false, false},
		{`{"email":"fixture@example.invalid","futureToken":"must not be dropped"}`, false, false},
	} {
		if err := ValidateSavedState(strings.NewReader(tc.data), tc.pending); (err == nil) != tc.valid {
			t.Fatalf("pending=%v valid=%v error=%v", tc.pending, tc.valid, err)
		}
	}
}

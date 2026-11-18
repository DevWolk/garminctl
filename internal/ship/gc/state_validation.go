package gc

import (
	"encoding/json"
	"errors"
	"io"
	"time"
)

// ValidateSavedState uses the account/MFA reader types without loading a client,
// changing permissions or state, expiring a challenge or contacting Garmin.
func ValidateSavedState(reader io.Reader, pending bool) error {
	if !pending {
		var session Session
		return validateStateJSON(reader, &session)
	}

	var state pendingMFAState
	if err := validateStateJSON(reader, &state); err != nil {
		return err
	}

	if state.CreatedAt.IsZero() {
		return errors.New("verification state has no creation time")
	}

	if err := state.validate(state.CreatedAt); err != nil {
		return err
	}

	_, err := restoreCookies(state.Domain, state.Cookies, func() time.Time { return state.CreatedAt })

	return err
}

func validateStateJSON(reader io.Reader, value any) error {
	decoder := json.NewDecoder(reader)
	// A normal reader can ignore fields, but a compatibility decision cannot
	// promise those fields survive the target build's next write.
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(value); err != nil {
		return err
	}

	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected trailing saved account data")
	}

	return nil
}

package connect

import (
	"encoding/json"
	"errors"
	"io"
)

// ValidateSavedLedger checks the persisted upload-reader contract without IO or uploads.
func ValidateSavedLedger(reader io.Reader) error {
	var value ledger

	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&value); err != nil {
		return err
	}

	if value == nil {
		return errors.New("upload ledger must be a JSON object")
	}

	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected trailing upload ledger data")
	}

	return nil
}

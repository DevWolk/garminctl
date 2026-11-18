package updatetransport

import (
	"errors"
	"fmt"
	"syscall"
)

// errBlockedByPolicy is ERROR_SYSTEM_INTEGRITY_POLICY_VIOLATION: Windows
// Application Control refused to run the file.
const errBlockedByPolicy = syscall.Errno(4551)

// explainHelperStart names the cause when Windows refuses to run the private
// copy of the CLI. That happens with Smart App Control on and a release signed
// by a certificate Windows does not know; the raw error only names the file.
func explainHelperStart(err error) error {
	if errors.Is(err, errBlockedByPolicy) {
		return fmt.Errorf("garminctl's helper program was blocked by Windows Application Control (for example Smart App Control); nothing was checked or installed: %w", err)
	}

	return err
}

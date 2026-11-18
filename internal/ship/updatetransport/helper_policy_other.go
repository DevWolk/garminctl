//go:build !windows

package updatetransport

// explainHelperStart has nothing to add where no application control policy
// decides whether the private copy of the CLI may run.
func explainHelperStart(err error) error { return err }

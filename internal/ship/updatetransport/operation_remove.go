package updatetransport

import (
	"os"
	"time"
)

const (
	removeOperationWait  = 2 * time.Second
	removeOperationPause = 25 * time.Millisecond
)

// removeOperation deletes an operation directory after its child has exited.
// Windows can keep the file of a program that has just exited locked for a
// moment, so a failed removal is repeated briefly before it is reported.
func removeOperation(directory string) error {
	deadline := time.Now().Add(removeOperationWait)

	for {
		err := os.RemoveAll(directory)
		if err == nil || time.Now().After(deadline) {
			return err
		}

		time.Sleep(removeOperationPause)
	}
}

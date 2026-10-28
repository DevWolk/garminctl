package clix

import (
	"context"
	"errors"
	"flag"
	"fmt"
)

// UsageError is a mistake in the command line; main exits with 2 for it.
type UsageError struct{ msg string }

func (e *UsageError) Error() string { return e.msg }

// UsageErrorf formats a command-line error that ExitCode maps to 2.
func UsageErrorf(format string, a ...any) error {
	return &UsageError{msg: fmt.Sprintf(format, a...)}
}

// ExitCode maps the outcome of run to a Unix exit status.
func ExitCode(err error) int {
	var ue *UsageError

	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &ue):
		return 2
	case errors.Is(err, context.Canceled):
		return 130
	}

	return 1
}

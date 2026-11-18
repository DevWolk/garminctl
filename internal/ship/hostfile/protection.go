// Package hostfile provides private host-file permissions and replacement.
// It owns no application data format, account or update policy.
package hostfile

import "errors"

// ErrInvalidPath rejects symlinks and mismatched file/directory types.
var ErrInvalidPath = errors.New("private storage requires a regular file in a real directory")

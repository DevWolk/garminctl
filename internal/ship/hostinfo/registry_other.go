//go:build !windows

package hostinfo

import "os"

func registeredDirectory(string) (string, error) { return "", os.ErrNotExist }

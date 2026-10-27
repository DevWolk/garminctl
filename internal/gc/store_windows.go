package gc

import (
	"errors"

	"github.com/DevWolk/garminctl/internal/hostfile"
)

func protectStorePath(path string, directory bool) error {
	err := hostfile.Protect(path, directory)
	if errors.Is(err, hostfile.ErrInvalidPath) {
		return errors.New("account store must use a regular file in a real directory")
	}

	return err
}

func replaceStore(from, to string) error { return hostfile.Replace(from, to) }

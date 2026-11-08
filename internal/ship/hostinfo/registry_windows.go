package hostinfo

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

func registeredDirectory(path string) (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, kind, err := key.GetStringValue("InstallDir")
	if err != nil {
		return "", err
	}
	if kind != registry.SZ || value == "" {
		return "", errors.New("installer registry location is not a plain path")
	}
	return value, nil
}

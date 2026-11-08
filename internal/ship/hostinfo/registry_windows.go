package hostinfo

import "golang.org/x/sys/windows/registry"

func registeredDirectory(path string) (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}

	return readInstallDirectory(key, registry.SZ)
}

package hostinfo

import "errors"

type directoryValueKey interface {
	GetStringValue(string) (string, uint32, error)
	Close() error
}

func readInstallDirectory(key directoryValueKey, plainString uint32) (directory string, err error) {
	defer func() { err = errors.Join(err, key.Close()) }()

	directory, kind, err := key.GetStringValue("InstallDir")
	if err != nil {
		return "", err
	}

	if kind != plainString || directory == "" {
		return "", errors.New("installer registry location is not a plain path")
	}

	return directory, nil
}

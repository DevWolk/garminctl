package application

import (
	"io/fs"
	"regexp"
	"strings"
)

var (
	stateLedger   = regexp.MustCompile(`^uploaded-[0-9]+\.json$`)
	stateRecovery = regexp.MustCompile(`^(.+)\-([0-9]{8})-([0-9]{6})-([A-Za-z0-9]+)$`)
)

// State areas name the roots a data backup copies: the config directory that
// holds the account store and the cache directory.
const (
	StateAreaConfig = "config"
	StateAreaCache  = "cache"
)

// StatePathKind describes an explicitly selected local state path.
type StatePathKind int

// Ignored directories are not traversed by the storage port.
const (
	StateIgnore StatePathKind = iota
	StateFile
	StateDirectory
)

// StatePath selects opaque account, delivery-ledger and watch recovery data.
func StatePath(area, relative string) StatePathKind {
	if !fs.ValidPath(relative) || strings.Contains(relative, `\`) {
		return StateIgnore
	}

	if area == StateAreaConfig {
		if relative == "garmin-connect.json" || relative == "garmin-connect.json.mfa" {
			return StateFile
		}

		return StateIgnore
	}

	if area != StateAreaCache {
		return StateIgnore
	}

	if stateLedger.MatchString(relative) {
		return StateFile
	}

	return recoveryStatePath(relative)
}

func recoveryStatePath(relative string) StatePathKind {
	parts := strings.Split(relative, "/")

	match := stateRecovery.FindStringSubmatch(parts[0])
	if len(match) == 0 {
		return StateIgnore
	}

	prefix := match[1]
	if prefix != "OUT.BIN" && !strings.HasSuffix(strings.ToUpper(prefix), ".SET") {
		return StateIgnore
	}

	if len(parts) == 1 {
		return StateDirectory
	}

	if len(parts) == 2 && (parts[1] == prefix || (prefix == "OUT.BIN" && parts[1] == "IN.BIN")) {
		return StateFile
	}

	return StateIgnore
}

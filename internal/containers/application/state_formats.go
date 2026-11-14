package application

// StateFormats records the reader/writer contract of the build that made a backup.
// A format changes when a previous reader can no longer use newly written data.
// These are application contracts, not fields added to account or watch files.
type StateFormats struct {
	Account, MFA, UploadLedger, WatchRecovery, BackupMetadata int
}

// CurrentStateFormats must change with incompatible persisted-format changes.
func CurrentStateFormats() StateFormats {
	return StateFormats{Account: 1, MFA: 1, UploadLedger: 1, WatchRecovery: 1, BackupMetadata: 1}
}

// KeepsCurrentData requires explicit provenance, not a SemVer compatibility guess.
func KeepsCurrentData(target, safety RecoverySnapshot) bool {
	return target.Formats != nil && safety.Formats != nil &&
		*target.Formats == CurrentStateFormats() && *safety.Formats == *target.Formats
}

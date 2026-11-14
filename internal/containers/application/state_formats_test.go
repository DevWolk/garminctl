package application

import "testing"

func TestKeepCurrentDataRequiresExplicitMatchingFormatProvenance(t *testing.T) {
	t.Parallel()

	formats := CurrentStateFormats()
	old := RecoverySnapshot{Formats: &formats}

	safety := cloneRecoverySnapshot(old)
	if !KeepsCurrentData(old, safety) {
		t.Fatal("matching explicit formats refused")
	}

	safety.Formats.Account++
	if KeepsCurrentData(old, safety) || old.Formats.Account != 1 {
		t.Fatal("changed formats accepted or clone aliases provenance")
	}

	if KeepsCurrentData(RecoverySnapshot{}, old) || KeepsCurrentData(old, RecoverySnapshot{}) {
		t.Fatal("legacy metadata guessed compatible")
	}

	old.Formats.Account++
	if KeepsCurrentData(old, safety) {
		t.Fatal("unknown common format treated as known compatible")
	}
}

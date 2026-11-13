package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type recoveryFixture struct {
	rows    []PackageRecord
	exports int
	result  *RecoveryExport
	err     error
}

func (s *recoveryFixture) List(context.Context) ([]PackageRecord, error) {
	return slices.Clone(s.rows), s.err
}

func (s *recoveryFixture) Export(ctx context.Context, _ string) (*RecoveryExport, error) {
	s.exports++
	return s.result, errors.Join(s.err, ctx.Err())
}

func recoveryRecord(id, tag string) PackageRecord {
	return PackageRecord{ID: strings.Repeat(id, 64), Tag: tag, Digest: strings.Repeat("d", 64), OS: "linux", Arch: "amd64", Installation: "deb", Bytes: 123}
}

func TestRecoveryListsOlderAndPrereleaseMetadataWithoutExport(t *testing.T) {
	t.Parallel()

	old, newer := recoveryRecord("a", "v1.2.0"), recoveryRecord("b", "v2.0.0-rc.1")
	newer.Prerelease = true
	foreign := recoveryRecord("c", "v3.0.0")
	foreign.OS = "windows"
	draft := recoveryRecord("e", "v4.0.0")
	draft.Draft = true
	source := &recoveryFixture{rows: []PackageRecord{old, foreign, newer, draft}}
	action := NewRecoveryAction(source, "linux", "amd64")

	rows, err := action.List(t.Context())
	if err != nil || !slices.Equal(rows, []PackageRecord{newer, old}) || source.exports != 0 {
		t.Fatalf("metadata=%+v exports=%d error=%v", rows, source.exports, err)
	}

	rows[0].Tag = "changed"
	if source.rows[2].Tag != newer.Tag {
		t.Fatal("metadata aliases the store")
	}
}

func TestRecoveryExportsExplicitOlderPackageWithoutInventory(t *testing.T) {
	t.Parallel()

	row := recoveryRecord("a", "v1.2.0")
	receipt := &RecoveryExport{Record: row, Directory: "/private/recovery", Path: "/private/recovery/package.deb", SignaturePath: "/private/recovery/package.deb.sig"}
	source := &recoveryFixture{rows: []PackageRecord{row}, result: receipt}

	result, err := NewRecoveryAction(source, "linux", "amd64").Export(t.Context(), row.ID)
	if err != nil || result == nil || *result != *receipt || source.exports != 1 {
		t.Fatalf("export=%+v attempts=%d error=%v", result, source.exports, err)
	}

	result.Path = "changed"
	if receipt.Path == result.Path {
		t.Fatal("export receipt aliases the port")
	}
}

func TestRecoveryRejectsAmbiguousForeignOrInvalidSelectionBeforeExport(t *testing.T) {
	t.Parallel()

	row := recoveryRecord("a", "v1.2.0")
	foreign := row

	foreign.OS = "windows"
	for _, tc := range []struct {
		name, id string
		rows     []PackageRecord
	}{
		{name: "invalid id", id: "../outside", rows: []PackageRecord{row}},
		{name: "missing id", id: strings.Repeat("b", 64), rows: []PackageRecord{row}},
		{name: "foreign platform", id: row.ID, rows: []PackageRecord{foreign}},
		{name: "ambiguous id", id: row.ID, rows: []PackageRecord{row, row}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source := &recoveryFixture{rows: tc.rows}

			result, err := NewRecoveryAction(source, "linux", "amd64").Export(t.Context(), tc.id)
			if err == nil || result != nil || source.exports != 0 {
				t.Fatalf("export=%+v attempts=%d error=%v", result, source.exports, err)
			}
		})
	}
}

func TestRecoveryRejectsUnverifiedMismatchedAndCanceledExports(t *testing.T) {
	t.Parallel()

	row := recoveryRecord("a", "v1.2.0")
	for _, tc := range []struct {
		name   string
		result *RecoveryExport
		err    error
	}{
		{name: "verification failure", err: errors.New("invalid signature")},
		{name: "nil receipt"},
		{name: "other package", result: &RecoveryExport{Record: recoveryRecord("b", "v1.2.0"), Directory: "private", Path: "package", SignaturePath: "signature"}},
		{name: "missing paths", result: &RecoveryExport{Record: row}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source := &recoveryFixture{rows: []PackageRecord{row}, result: tc.result, err: tc.err}

			result, err := NewRecoveryAction(source, "linux", "amd64").Export(t.Context(), row.ID)
			if result != nil || err == nil {
				t.Fatalf("accepted invalid export=%+v error=%v", result, err)
			}
		})
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	source := &recoveryFixture{rows: []PackageRecord{row}}
	if result, err := NewRecoveryAction(source, "linux", "amd64").Export(ctx, row.ID); result != nil || !errors.Is(err, context.Canceled) || source.exports != 0 {
		t.Fatalf("canceled export=%+v attempts=%d error=%v", result, source.exports, err)
	}
}

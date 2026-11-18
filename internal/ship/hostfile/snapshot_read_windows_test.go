package hostfile

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSnapshotReaderWindowsOwnerOnlyBackupPaths(t *testing.T) {
	t.Parallel()
	reader, snapshot, _ := snapshotReadFixture(t)

	if _, err := reader.Open(t.Context(), snapshot.ID); err != nil {
		t.Fatal(err)
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ path, inherit string }{
		{reader.directory, "OICI"}, {snapshot.Directory, "OICI"}, {filepath.Join(snapshot.Directory, "config"), "OICI"}, {filepath.Join(snapshot.Directory, "manifest.json"), ""}, {filepath.Join(snapshot.Directory, "config", "state.json"), ""},
	} {
		got, err := windows.GetNamedSecurityInfo(tc.path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}

		want, err := windows.SecurityDescriptorFromString("D:P(A;" + tc.inherit + ";FA;;;" + user.User.Sid.String() + ")")
		if err != nil {
			t.Fatal(err)
		}
		// AI is an optional control flag; P and the complete sole owner ACE must match.
		if actual, expected := got.String(), want.String(); actual != expected && actual != strings.Replace(expected, "D:P(", "D:PAI(", 1) {
			t.Errorf("ACL=%s want=%s", actual, expected)
		}
	}
}

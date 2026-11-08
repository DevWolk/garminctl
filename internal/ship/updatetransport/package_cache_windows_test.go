package updatetransport_test

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPackageCacheWindowsPrivateCatalogAndPayload(t *testing.T) {
	t.Parallel()
	_, entry, directory := retainedPackageForTest(t)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, inherit string }{
		{directory, "OICI"}, {filepath.Join(directory, entry.ID), "OICI"},
		{filepath.Join(directory, entry.ID, "entry.json"), ""}, {filepath.Join(directory, entry.ID, entry.Release.Package.Name), ""}, {filepath.Join(directory, entry.ID, entry.Release.Signature.Name), ""},
	} {
		got, err := windows.GetNamedSecurityInfo(tc.path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		want, err := windows.SecurityDescriptorFromString("D:P(A;" + tc.inherit + ";FA;;;" + user.User.Sid.String() + ")")
		if err != nil {
			t.Fatal(err)
		}
		if canonicalStageDACL(got.String()) != canonicalStageDACL(want.String()) {
			t.Errorf("ACL=%s want=%s", got.String(), want.String())
		}
	}
}

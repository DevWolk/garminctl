package updatetransport_test

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPackageExportWindowsPrivateCopy(t *testing.T) {
	t.Parallel()
	cache, entry, _ := retainedPackageForTest(t)
	directory := filepath.Join(t.TempDir(), "recovery")

	value, err := cache.Export(t.Context(), entry.ID, directory, nil)
	if err != nil {
		t.Fatal(err)
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ path, inherit string }{
		{directory, stageDirectoryInheritance}, {value.Directory, stageDirectoryInheritance}, {value.Path, ""}, {value.SignaturePath, ""},
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

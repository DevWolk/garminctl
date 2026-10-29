package apps

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func assertRegistryBackupProtection(t *testing.T, path string) {
	t.Helper()

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}

	for name, inherit := range map[string]string{
		path: "", filepath.Dir(path): "OICI", filepath.Dir(filepath.Dir(path)): "OICI",
	} {
		got, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}

		want, err := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;" + user.User.Sid.String() + ")")
		if err != nil {
			t.Fatal(err)
		}

		// Canonicalize SID aliases and the auto-inherited control flag, retaining
		// the protected flag and checking that no inherited/extra ACE survives.
		if strings.Replace(got.String(), "D:PAI", "D:P", 1) != strings.Replace(want.String(), "D:PAI", "D:P", 1) {
			t.Fatalf("%s DACL = %s, want %s", name, got.String(), want.String())
		}
	}
}

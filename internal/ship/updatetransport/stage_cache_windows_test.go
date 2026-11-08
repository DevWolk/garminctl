package updatetransport_test

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

const stageDirectoryInheritance = "OICI"

func TestStagerWindowsCacheAndFilesAreCurrentUserOnly(t *testing.T) {
	t.Parallel()
	stager, cache := stagerForTest(t)
	request, release := stageSelection(tag120)

	result, err := stager.Stage(t.Context(), request, release, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := result.Close(); err != nil {
			t.Error(err)
		}
	})

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}

	directory := filepath.Dir(result.Path())
	for _, tc := range []struct{ path, inherit string }{
		{path: cache, inherit: stageDirectoryInheritance},
		{path: directory, inherit: stageDirectoryInheritance},
		{path: result.Path()},
		{path: result.SignaturePath()},
		{path: filepath.Join(directory, "garminctl.exe")},
	} {
		actual, err := windows.GetNamedSecurityInfo(tc.path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}

		want, err := windows.SecurityDescriptorFromString("D:P(A;" + tc.inherit + ";FA;;;" + user.User.Sid.String() + ")")
		if err != nil {
			t.Fatal(err)
		}

		if canonicalStageDACL(actual.String()) != canonicalStageDACL(want.String()) {
			t.Errorf("private path %s ACL=%s want=%s", tc.path, actual.String(), want.String())
		}
	}
}

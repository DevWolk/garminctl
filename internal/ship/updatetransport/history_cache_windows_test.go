package updatetransport_test

import (
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
	"golang.org/x/sys/windows"
)

func TestHistoryCacheWindowsCurrentUserDACL(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "history")
	cache, err := updatetransport.NewHistoryCache(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Save(t.Context(), historySnapshot()); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, inherit string }{{directory, "OICI"}, {filepath.Join(directory, "history.json"), ""}} {
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

package gc

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestStoreRepairsWindowsPermissions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.json")
	c := New(path)

	if err := c.saveSession(testSession(oldTestEmail)); err != nil {
		t.Fatal(err)
	}

	sid := currentUserSID(t)
	setTestDACL(t, filepath.Dir(path), "D:P(A;OICI;FA;;;"+sid+")(A;OICI;GR;;;WD)")
	setTestDACL(t, path, "D:P(A;;FA;;;"+sid+")(A;;GR;;;WD)")

	fresh := New(path)
	if err := fresh.StoreError(); err != nil {
		t.Fatal(err)
	}

	assertTestDACL(t, filepath.Dir(path), "D:P(A;OICI;FA;;;"+sid+")")
	assertTestDACL(t, path, "D:P(A;;FA;;;"+sid+")")

	if err := fresh.saveSession(testSession("new@example.invalid")); err != nil {
		t.Fatal(err)
	}

	assertTestDACL(t, path, "D:P(A;;FA;;;"+sid+")")
	assertSessionFile(t, path, "new@example.invalid")
}

func TestStoreLockedWindowsTargetPreservesSession(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.json")
	c := New(path)

	if err := c.saveSession(testSession(oldTestEmail)); err != nil {
		t.Fatal(err)
	}

	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}

	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := windows.CloseHandle(h); err != nil {
			t.Error(err)
		}
	})

	err = c.saveSession(testSession("new@example.invalid"))
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("locked replacement = %v", err)
	}

	if c.Email() != oldTestEmail {
		t.Fatal("failed replacement published new credentials")
	}

	assertSessionFile(t, path, oldTestEmail)
	assertNoStoreTemps(t, path)
}

func currentUserSID(t *testing.T) string {
	t.Helper()

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}

	return user.User.Sid.String()
}

func setTestDACL(t *testing.T, path, sddl string) {
	t.Helper()

	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}

	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}

	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)

	runtime.KeepAlive(sd)

	if err != nil {
		t.Fatal(err)
	}
}

func assertTestDACL(t *testing.T, path, want string) {
	t.Helper()

	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}

	expected, err := windows.SecurityDescriptorFromString(want)
	if err != nil {
		t.Fatal(err)
	}

	// Windows may retain the auto-inherited control flag; it does not add an
	// inherited ACE or remove the protected-DACL flag checked by this string.
	// Canonicalize both descriptors because Windows represents some SIDs with
	// aliases, including LA for the built-in administrator on CI runners.
	want = strings.Replace(expected.String(), "D:PAI", "D:P", 1)
	if got := strings.Replace(sd.String(), "D:PAI", "D:P", 1); got != want {
		t.Fatalf("DACL = %s, want %s", got, want)
	}
}

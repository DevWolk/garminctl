package gc

import (
	"errors"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

func protectStorePath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return errors.New("account store must use a regular file in a real directory")
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}

	inherit := ""
	if directory {
		inherit = "OICI"
	}

	sd, err := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}

	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}

	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)

	runtime.KeepAlive(sd)

	return err
}

func replaceStore(from, to string) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}

	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}

	return windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

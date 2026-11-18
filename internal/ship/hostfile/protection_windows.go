package hostfile

import (
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// Protect restricts an existing real directory/file to the current owner.
func Protect(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return ErrInvalidPath
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

// Replace publishes a same-directory temporary file with replace/write-through flags.
func Replace(from, to string) error {
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

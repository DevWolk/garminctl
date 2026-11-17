//go:build !linux && !windows

package updatetransport

func preflightNativeInstall(installRequest, string) error { return nil }

//go:build !linux && !windows

package updatetransport

func preflightNativeInstall(installRequest) error { return nil }

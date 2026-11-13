//go:build !windows

package panel

func retryableInstallRename(error) bool { return false }

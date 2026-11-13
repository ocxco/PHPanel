//go:build !windows

package main

import "fmt"

func isProcessElevated() bool { return false }
func launchElevatedRestart(string, elevationRequest) error {
	return fmt.Errorf("管理员重启仅支持 Windows")
}
func prepareElevatedStartup(elevationRequest) error {
	return fmt.Errorf("管理员重启仅支持 Windows")
}

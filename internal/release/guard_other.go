//go:build !windows

package release

import "fmt"

func checkProgramFiles(string) error       { return nil }
func checkRegisteredPrograms(string) error { return nil }
func serviceExists(string) (bool, error) {
	return false, fmt.Errorf("安装器检查仅支持 Windows")
}
func cleanupUserSettings(string) error { return fmt.Errorf("卸载清理仅支持 Windows") }

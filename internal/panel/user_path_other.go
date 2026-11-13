//go:build !windows

package panel

import "fmt"

func readUserAndSystemPath() (string, string, error) {
	return "", "", fmt.Errorf("用户 PATH 功能仅支持 Windows")
}
func writeUserPath(string) error { return fmt.Errorf("用户 PATH 功能仅支持 Windows") }

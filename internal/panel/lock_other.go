//go:build !windows

package panel

import "os"

func lockDataRoot(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
}

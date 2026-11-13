//go:build !windows

package panel

import "path/filepath"

func PhysicalPath(path string) (string, error) { return filepath.EvalSymlinks(path) }

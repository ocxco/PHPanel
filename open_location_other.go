//go:build !windows

package main

import (
	"fmt"
	"phpanel/internal/panel"
)

func openLocation(location panel.Location) error {
	return fmt.Errorf("当前目录打开功能需要 Windows")
}

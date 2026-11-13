//go:build windows

package panel

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockDataRoot(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("无法独占数据目录（可能有另一个 PHPanel 正在运行）: %w", err)
	}
	return os.NewFile(uintptr(handle), path), nil
}

//go:build windows

package panel

import (
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"syscall"
	"unsafe"
)

func readUserAndSystemPath() (string, string, error) {
	user := ""
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err == nil {
		user, _, err = key.GetStringValue("Path")
		key.Close()
		if err == registry.ErrNotExist {
			err = nil
		}
	} else if err == registry.ErrNotExist {
		err = nil
	}
	if err != nil {
		return "", "", err
	}
	system := ""
	key, err = registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, registry.QUERY_VALUE)
	if err == nil {
		system, _, _ = key.GetStringValue("Path")
		key.Close()
	}
	return user, system, nil
}
func writeUserPath(value string) error {
	if len(value) > 30000 {
		return fmt.Errorf("用户 PATH 过长，无法添加命令入口")
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	_, existingType, _ := key.GetStringValue("Path")
	if existingType == registry.SZ {
		err = key.SetStringValue("Path", value)
	} else {
		err = key.SetExpandStringValue("Path", value)
	}
	if err != nil {
		return err
	}
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	message, _ := syscall.UTF16PtrFromString("Environment")
	var result uintptr
	_, _, _ = proc.Call(0xffff, 0x001a, 0, uintptr(unsafe.Pointer(message)), 0x0002, 1500, uintptr(unsafe.Pointer(&result)))
	return nil
}

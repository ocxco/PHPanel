//go:build windows

package release

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/mgr"
)

func checkProgramFiles(root string) error {
	for _, name := range []string{"PHPanel.exe", "phpanel-cli.exe", "phpanel-service.exe"} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		ptr, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		h, err := windows.CreateFile(ptr, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			return fmt.Errorf("%s 正在运行或不可写，请退出面板，并停止所有使用此程序目录的实例/托管程序后重试。", name)
		}
		windows.CloseHandle(h)
	}
	return nil
}
func serviceExists(name string) (bool, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false, err
	}
	defer windows.CloseServiceHandle(scm)
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false, err
	}
	service, err := windows.OpenService(scm, ptr, windows.SERVICE_QUERY_STATUS)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	windows.CloseServiceHandle(service)
	return true, nil
}
func checkRegisteredPrograms(root string) error {
	handle, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return err
	}
	manager := &mgr.Mgr{Handle: handle}
	defer manager.Disconnect()
	names, err := manager.ListServices()
	if err != nil {
		return err
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "PHPanel-") {
			continue
		}
		ptr, _ := windows.UTF16PtrFromString(name)
		serviceHandle, err := windows.OpenService(handle, ptr, windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS)
		if err != nil {
			return fmt.Errorf("无法核对 PHPanel 服务 %s，请先在面板中注销相关服务", name)
		}
		service := &mgr.Service{Name: name, Handle: serviceHandle}
		config, err := service.Config()
		service.Close()
		if err != nil {
			return err
		}
		args, err := windows.DecomposeCommandLine(config.BinaryPathName)
		if err == nil && len(args) > 0 && samePath(args[0], filepath.Join(root, "phpanel-service.exe")) {
			return fmt.Errorf("请先在 PHPanel 中停止并注销系统服务 %s，再卸载程序。", name)
		}
	}
	return nil
}
func cleanupUserSettings(root string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err == nil {
		value, kind, readErr := key.GetStringValue("Path")
		if readErr == nil {
			next := RemoveManagedPath(value, filepath.Join(root, "cli"))
			if next != value {
				if kind == registry.EXPAND_SZ {
					err = key.SetExpandStringValue("Path", next)
				} else {
					err = key.SetStringValue("Path", next)
				}
			}
		} else if !errors.Is(readErr, registry.ErrNotExist) {
			err = readErr
		}
		key.Close()
		if err != nil {
			return err
		}
	} else if !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	key, err = registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err == nil {
		value, _, readErr := key.GetStringValue("PHPanel")
		if readErr == nil {
			args, parseErr := windows.DecomposeCommandLine(value)
			if parseErr == nil && len(args) > 0 && samePath(args[0], filepath.Join(root, "PHPanel.exe")) {
				err = key.DeleteValue("PHPanel")
			}
		} else if !errors.Is(readErr, registry.ErrNotExist) {
			err = readErr
		}
		key.Close()
		if err != nil {
			return err
		}
	} else if !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	text, _ := windows.UTF16PtrFromString("Environment")
	var result uintptr
	windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW").Call(0xffff, 0x1a, 0, uintptr(unsafe.Pointer(text)), 2, 1500, uintptr(unsafe.Pointer(&result)))
	return nil
}

//go:build windows

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
)

type shellExecuteInfo struct {
	Size                              uint32
	Mask                              uint32
	Window                            windows.Handle
	Verb, File, Parameters, Directory *uint16
	Show                              int32
	Instance                          windows.Handle
	IDList                            uintptr
	Class                             *uint16
	ClassKey                          windows.Handle
	HotKey                            uint32
	Icon, Process                     windows.Handle
}

func isProcessElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }
func restartEventName(nonce, kind string) string {
	return `Local\PHPanel-elevation-` + nonce + "-" + kind
}
func restartEvents(request elevationRequest) (windows.Handle, windows.Handle, error) {
	dacl := "D:P(A;;GA;;;SY)(A;;GA;;;BA)"
	if request.OriginalUserSID != "" {
		sid, err := windows.StringToSid(request.OriginalUserSID)
		if err != nil {
			return 0, 0, err
		}
		dacl += "(A;;GA;;;" + sid.String() + ")"
	}
	sd, err := windows.SecurityDescriptorFromString(dacl)
	if err != nil {
		return 0, 0, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	readyName, _ := windows.UTF16PtrFromString(restartEventName(request.Nonce, "ready"))
	abortName, _ := windows.UTF16PtrFromString(restartEventName(request.Nonce, "abort"))
	ready, err := windows.CreateEvent(&sa, 1, 0, readyName)
	if err != nil {
		return 0, 0, err
	}
	abort, err := windows.CreateEvent(&sa, 1, 0, abortName)
	if err != nil {
		windows.CloseHandle(ready)
		return 0, 0, err
	}
	return ready, abort, nil
}
func elevationShellRequest(exe string, request elevationRequest) (shellExecuteInfo, error) {
	arg, err := encodeElevationRequest(request)
	if err != nil {
		return shellExecuteInfo{}, err
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return shellExecuteInfo{}, err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return shellExecuteInfo{}, err
	}
	params, err := windows.UTF16PtrFromString(windows.EscapeArg(arg))
	if err != nil {
		return shellExecuteInfo{}, err
	}
	dir, err := windows.UTF16PtrFromString(filepath.Dir(exe))
	if err != nil {
		return shellExecuteInfo{}, err
	}
	return shellExecuteInfo{Size: uint32(unsafe.Sizeof(shellExecuteInfo{})), Mask: 0x40 | 0x100 | 0x400, Verb: verb, File: file, Parameters: params, Directory: dir, Show: windows.SW_SHOWNORMAL}, nil
}
func launchElevatedRestart(exe string, request elevationRequest) (err error) {
	ready, abort, err := restartEvents(request)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ready)
	defer windows.CloseHandle(abort)
	defer func() {
		if err != nil {
			_ = windows.SetEvent(abort)
		}
	}()
	info, err := elevationShellRequest(exe, request)
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	comErr := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED)
	if comErr == nil {
		defer ole.CoUninitialize()
	} else if code, ok := comErr.(*ole.OleError); ok && code.Code() == 1 {
		defer ole.CoUninitialize()
	} else if code, ok := comErr.(*ole.OleError); !ok || uint32(code.Code()) != 0x80010106 {
		return fmt.Errorf("Windows COM 初始化失败: %w", comErr)
	}
	proc := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")
	ok, _, callErr := proc.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return errElevationCancelled
		}
		return fmt.Errorf("Windows 提权启动失败: %w", callErr)
	}
	if info.Process == 0 {
		return fmt.Errorf("Windows 未返回管理员进程句柄")
	}
	defer windows.CloseHandle(info.Process)
	return awaitElevationReady(ready, info.Process, 15000)
}
func awaitElevationReady(ready, process windows.Handle, timeout uint32) error {
	status, err := windows.WaitForMultipleObjects([]windows.Handle{ready, process}, false, timeout)
	if err != nil {
		return err
	}
	if status == windows.WAIT_OBJECT_0 {
		return nil
	}
	if status == windows.WAIT_OBJECT_0+1 {
		return fmt.Errorf("管理员进程在完成重启握手前退出")
	}
	return fmt.Errorf("管理员进程启动握手超时")
}

func prepareElevatedStartup(request elevationRequest) error {
	if !isProcessElevated() {
		return fmt.Errorf("管理员重启未获得提升后的 Windows 令牌")
	}
	if err := verifyElevationDataRoot(request.DataRoot); err != nil {
		return err
	}
	parent, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(request.ParentPID))
	if err != nil && !errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return err
	}
	if parent != 0 {
		defer windows.CloseHandle(parent)
	}
	readyName, _ := windows.UTF16PtrFromString(restartEventName(request.Nonce, "ready"))
	abortName, _ := windows.UTF16PtrFromString(restartEventName(request.Nonce, "abort"))
	ready, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, readyName)
	if err != nil {
		if parent == 0 {
			return nil
		}
		status, _ := windows.WaitForSingleObject(parent, 0)
		if status == windows.WAIT_OBJECT_0 {
			return nil
		}
		return fmt.Errorf("原面板的重启请求已失效")
	}
	defer windows.CloseHandle(ready)
	abort, err := windows.OpenEvent(windows.SYNCHRONIZE, false, abortName)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(abort)
	if err := windows.SetEvent(ready); err != nil {
		return err
	}
	if parent == 0 {
		return nil
	}
	return awaitOriginalExit(parent, abort, uint32((2*time.Minute)/time.Millisecond))
}
func awaitOriginalExit(parent, abort windows.Handle, timeout uint32) error {
	status, err := windows.WaitForMultipleObjects([]windows.Handle{parent, abort}, false, timeout)
	if err != nil {
		return err
	}
	if status == windows.WAIT_OBJECT_0 {
		return nil
	}
	if status == windows.WAIT_OBJECT_0+1 {
		return fmt.Errorf("原面板已取消管理员重启")
	}
	return fmt.Errorf("原面板未在两分钟内退出，管理员重启已取消")
}

//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const trayCallback = 0x8001
const trayRestore = 1001
const trayExit = 1002

var trayUser32 = windows.NewLazySystemDLL("user32.dll")
var trayShell32 = windows.NewLazySystemDLL("shell32.dll")

type trayWindowClass struct {
	Size, Style                        uint32
	Procedure                          uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background windows.Handle
	Menu, Name                         *uint16
	SmallIcon                          windows.Handle
}
type trayMessage struct {
	Window         windows.Handle
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}
type trayPoint struct{ X, Y int32 }
type trayNotifyData struct {
	Size                uint32
	Window              windows.Handle
	ID, Flags, Callback uint32
	Icon                windows.Handle
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                windows.GUID
	BalloonIcon         windows.Handle
}
type windowsTray struct {
	window         atomic.Uintptr
	ready          atomic.Bool
	done           chan struct{}
	open, exit     func()
	data           trayNotifyData
	createdMessage uint32
}

func newTrayIcon(open, exit func()) (trayIcon, error) {
	t := &windowsTray{done: make(chan struct{}), open: open, exit: exit}
	result := make(chan error, 1)
	go t.run(result)
	if err := <-result; err != nil {
		return nil, err
	}
	return t, nil
}
func (t *windowsTray) Ready() bool { return t.ready.Load() }
func (t *windowsTray) Close() {
	if window := t.window.Load(); window != 0 {
		trayUser32.NewProc("PostMessageW").Call(window, 0x10, 0, 0)
	}
	<-t.done
}
func (t *windowsTray) notify(operation uintptr) bool {
	result, _, _ := trayShell32.NewProc("Shell_NotifyIconW").Call(operation, uintptr(unsafe.Pointer(&t.data)))
	return result != 0
}
func (t *windowsTray) add() bool {
	t.data.Version = 0
	if !t.notify(0) {
		t.ready.Store(false)
		return false
	}
	t.data.Version = 4
	t.notify(4)
	t.ready.Store(true)
	return true
}
func (t *windowsTray) run(result chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)
	module, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	instance := windows.Handle(module)
	if instance == 0 {
		result <- err
		return
	}
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf("PHPanel.Tray.%d", os.Getpid()))
	class := trayWindowClass{Size: uint32(unsafe.Sizeof(trayWindowClass{})), Instance: instance, Name: name}
	class.Procedure = syscall.NewCallback(func(window uintptr, message uint32, wparam, lparam uintptr) uintptr {
		switch message {
		case 0x11:
			return 1
		case 0x16:
			if wparam != 0 {
				go t.exit()
			}
			return 0
		case trayCallback:
			switch uint32(lparam) & 0xffff {
			case 0x202, 0x203, 0x400, 0x401:
				go t.open()
			case 0x205, 0x7b:
				t.menu(window)
			}
			return 0
		case 0x10:
			trayUser32.NewProc("DestroyWindow").Call(window)
			return 0
		case 0x2:
			t.notify(2)
			t.ready.Store(false)
			trayUser32.NewProc("PostQuitMessage").Call(0)
			return 0
		}
		if t.createdMessage != 0 && message == t.createdMessage {
			t.add()
			return 0
		}
		value, _, _ := trayUser32.NewProc("DefWindowProcW").Call(window, uintptr(message), wparam, lparam)
		return value
	})
	registered, _, err := trayUser32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class)))
	if registered == 0 {
		result <- fmt.Errorf("托盘窗口类注册失败: %w", err)
		return
	}
	defer trayUser32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), uintptr(instance))
	window, _, err := trayUser32.NewProc("CreateWindowExW").Call(0x80, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0x80000000, 0, 0, 0, 0, 0, 0, uintptr(instance), 0)
	if window == 0 {
		result <- fmt.Errorf("托盘窗口创建失败: %w", err)
		return
	}
	t.window.Store(window)
	defer t.window.Store(0)
	created, _ := windows.UTF16PtrFromString("TaskbarCreated")
	message, _, _ := trayUser32.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(created)))
	t.createdMessage = uint32(message)
	var large, small windows.Handle
	exe, _ := os.Executable()
	exeName, _ := windows.UTF16PtrFromString(exe)
	count, _, _ := trayShell32.NewProc("ExtractIconExW").Call(uintptr(unsafe.Pointer(exeName)), 0, uintptr(unsafe.Pointer(&large)), uintptr(unsafe.Pointer(&small)), 1)
	if large != 0 {
		trayUser32.NewProc("DestroyIcon").Call(uintptr(large))
	}
	icon := small
	if count == 0 || icon == 0 {
		value, _, _ := trayUser32.NewProc("LoadIconW").Call(0, 32512)
		icon = windows.Handle(value)
	} else {
		defer trayUser32.NewProc("DestroyIcon").Call(uintptr(icon))
	}
	t.data = trayNotifyData{Size: uint32(unsafe.Sizeof(trayNotifyData{})), Window: windows.Handle(window), ID: 1, Flags: 1 | 2 | 4, Callback: trayCallback, Icon: icon}
	copy(t.data.Tip[:], windows.StringToUTF16("PHPanel · 本地开发环境"))
	if !t.add() {
		trayUser32.NewProc("DestroyWindow").Call(window)
		result <- fmt.Errorf("Windows 未接受托盘图标")
		return
	}
	result <- nil
	var msg trayMessage
	for {
		value, _, _ := trayUser32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(value) <= 0 {
			break
		}
		trayUser32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		trayUser32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
}
func (t *windowsTray) menu(window uintptr) {
	menu, _, _ := trayUser32.NewProc("CreatePopupMenu").Call()
	if menu == 0 {
		return
	}
	defer trayUser32.NewProc("DestroyMenu").Call(menu)
	open, _ := windows.UTF16PtrFromString("打开 PHPanel")
	exit, _ := windows.UTF16PtrFromString("退出 PHPanel")
	trayUser32.NewProc("AppendMenuW").Call(menu, 0, trayRestore, uintptr(unsafe.Pointer(open)))
	trayUser32.NewProc("AppendMenuW").Call(menu, 0x800, 0, 0)
	trayUser32.NewProc("AppendMenuW").Call(menu, 0, trayExit, uintptr(unsafe.Pointer(exit)))
	var point trayPoint
	trayUser32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
	trayUser32.NewProc("SetForegroundWindow").Call(window)
	command, _, _ := trayUser32.NewProc("TrackPopupMenuEx").Call(menu, 0x100|0x80|0x2, uintptr(point.X), uintptr(point.Y), window, 0)
	trayUser32.NewProc("PostMessageW").Call(window, 0, 0, 0)
	switch command {
	case trayRestore:
		go t.open()
	case trayExit:
		go t.exit()
	}
}

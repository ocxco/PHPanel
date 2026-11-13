//go:build windows

package main

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestStartupCommandQuotesExecutableAndRoot(t *testing.T) {
	exe := `D:\应用 space\PHPanel.exe`
	root := `D:\data space\环境`
	args, err := windows.DecomposeCommandLine(startupCommand(exe, root))
	if err != nil || len(args) != 3 || args[0] != exe || args[1] != startupArgument {
		t.Fatal(args, err)
	}
	parsed, err := parseDesktopLaunch(args[1:])
	if err != nil || parsed != root {
		t.Fatal(parsed, err)
	}
}
func TestTrayNativeStructureLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		if size := unsafe.Sizeof(trayNotifyData{}); size != 976 {
			t.Fatal("NOTIFYICONDATAW", size)
		}
		if size := unsafe.Sizeof(trayWindowClass{}); size != 80 {
			t.Fatal("WNDCLASSEXW", size)
		}
		if size := unsafe.Sizeof(trayMessage{}); size != 48 {
			t.Fatal("MSG", size)
		}
	}
}

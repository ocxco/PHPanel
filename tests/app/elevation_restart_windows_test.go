//go:build windows

package main

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestElevationShellUsesRunasAndExactExecutable(t *testing.T) {
	r := restartFixture(t)
	exe := `D:\应用 space\PHPanel.exe`
	info, err := elevationShellRequest(exe, r)
	if err != nil {
		t.Fatal(err)
	}
	if windows.UTF16PtrToString(info.Verb) != "runas" || windows.UTF16PtrToString(info.File) != exe || info.Size != uint32(unsafe.Sizeof(shellExecuteInfo{})) || info.Mask&0x40 == 0 {
		t.Fatal("wrong shell elevation parameters")
	}
	args, err := windows.DecomposeCommandLine(windows.UTF16PtrToString(info.Parameters))
	if err != nil || len(args) != 1 {
		t.Fatal(args, err)
	}
	parsed, err := parseElevationRequest(args)
	if err != nil || parsed.DataRoot != r.DataRoot {
		t.Fatal(parsed, err)
	}
}
func TestElevationHandshakeUsesEventsAndParentExitBarrier(t *testing.T) {
	r := restartFixture(t)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	r.OriginalUserSID = user.User.Sid.String()
	ready, abort, err := restartEvents(r)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(ready)
	defer windows.CloseHandle(abort)
	exited, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(exited)
	if err := awaitElevationReady(ready, exited, 1); err == nil {
		t.Fatal("ready timeout ignored")
	}
	_ = windows.SetEvent(ready)
	if err := awaitElevationReady(ready, exited, 1); err != nil {
		t.Fatal(err)
	}
	_ = windows.ResetEvent(ready)
	_ = windows.SetEvent(exited)
	if err := awaitElevationReady(ready, exited, 1); err == nil {
		t.Fatal("premature child exit ignored")
	}
	_ = windows.ResetEvent(exited)
	if err := awaitOriginalExit(exited, abort, 1); err == nil {
		t.Fatal("parent wait timeout ignored")
	}
	_ = windows.SetEvent(abort)
	if err := awaitOriginalExit(exited, abort, 1); err == nil {
		t.Fatal("abort ignored")
	}
	_ = windows.ResetEvent(abort)
	_ = windows.SetEvent(exited)
	if err := awaitOriginalExit(exited, abort, 1); err != nil {
		t.Fatal(err)
	}
}

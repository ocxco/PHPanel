//go:build windows

package panel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processGuard struct{ job windows.Handle }

// Some upstream Windows builds parse paths through their ANSI runtime. Prefer
// existing 8.3 paths for those processes without renaming any user directory.
func nativePath(path string) string {
	if strings.IndexFunc(path, func(r rune) bool { return r > 127 }) < 0 {
		return path
	}
	existing := path
	suffix := []string{}
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return path
		}
		suffix = append([]string{filepath.Base(existing)}, suffix...)
		existing = parent
	}
	ptr, err := windows.UTF16PtrFromString(existing)
	if err != nil {
		return path
	}
	buf := make([]uint16, 32768)
	count, err := windows.GetShortPathName(ptr, &buf[0], uint32(len(buf)))
	if err != nil || count == 0 || count >= uint32(len(buf)) {
		return path
	}
	result := windows.UTF16ToString(buf[:count])
	for _, part := range suffix {
		result = filepath.Join(result, part)
	}
	return result
}

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
func guardProcess(cmd *exec.Cmd) (*processGuard, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(process)
	if err = windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("无法管理进程树: %w", err)
	}
	return &processGuard{job: job}, nil
}
func (g *processGuard) kill() error { return windows.TerminateJobObject(g.job, 1) }
func (g *processGuard) close()      { _ = windows.CloseHandle(g.job) }

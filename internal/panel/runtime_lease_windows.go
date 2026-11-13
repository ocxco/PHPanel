//go:build windows

package panel

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type runtimeLease struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// A named mutex enforces instance limits across the GUI and SCM hosts. Its
// owner is a dedicated OS thread, so Windows also releases it after a crash.
func acquireRuntimeLease(root string, i Instance) (*runtimeLease, error) {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(root)) + "\x00" + instanceStartKey(i)))
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`Global\PHPanel-run-%x`, sum[:16]))
	if err != nil {
		return nil, err
	}
	lease := &runtimeLease{stop: make(chan struct{}), done: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(lease.done)
		// Both the user's GUI and LocalSystem service hosts must access the lease.
		sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;IU)")
		if err != nil {
			result <- err
			return
		}
		sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
		h, err := windows.CreateMutex(&sa, false, name)
		if err != nil {
			result <- fmt.Errorf("无法取得实例运行锁: %w", err)
			return
		}
		defer windows.CloseHandle(h)
		status, err := windows.WaitForSingleObject(h, 0)
		if err != nil || (status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED) {
			result <- fmt.Errorf("此环境已有实例正在运行或变更（包括 Windows 系统服务）")
			return
		}
		result <- nil
		<-lease.stop
		_ = windows.ReleaseMutex(h)
	}()
	if err := <-result; err != nil {
		return nil, err
	}
	return lease, nil
}

func (l *runtimeLease) release() {
	if l != nil {
		l.once.Do(func() { close(l.stop); <-l.done })
	}
}

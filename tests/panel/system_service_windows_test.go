//go:build windows

package panel

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRuntimeLeaseExcludesServiceAndGUIAndReleases(t *testing.T) {
	root := t.TempDir()
	i := Instance{Component: "nginx", PackageID: "v1"}
	lease, err := acquireRuntimeLease(root, i)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	if other, err := acquireRuntimeLease(root, Instance{Component: "nginx", PackageID: "v2"}); err == nil {
		other.release()
		t.Fatal("second Nginx version acquired lock")
	}
	redis, err := acquireRuntimeLease(root, Instance{Component: "redis", PackageID: "v1"})
	if err != nil {
		t.Fatal("unrelated component blocked", err)
	}
	redis.release()
	lease.release()
	other, err := acquireRuntimeLease(root, i)
	if err != nil {
		t.Fatal("released lock unavailable", err)
	}
	other.release()
}

func TestRuntimeLeaseReacquiresExistingMutex(t *testing.T) {
	root := t.TempDir()
	i := Instance{Component: "nginx", PackageID: "v1"}
	lease, err := acquireRuntimeLease(root, i)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lease.release)

	// A second host can keep the named mutex alive after its owner releases it.
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(root)) + "\x00" + instanceStartKey(i)))
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`Global\PHPanel-run-%x`, sum[:16]))
	if err != nil {
		t.Fatal(err)
	}
	observer, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
	if err != nil {
		t.Fatal("could not retain existing mutex", err)
	}
	t.Cleanup(func() {
		if observer != 0 {
			_ = windows.CloseHandle(observer)
		}
	})
	lease.release()

	for attempt := 0; attempt < 3; attempt++ {
		lease, err := acquireRuntimeLease(root, i)
		if err != nil {
			t.Fatal("unowned existing mutex unavailable", err)
		}
		t.Cleanup(lease.release)
		if other, err := acquireRuntimeLease(root, i); err == nil {
			other.release()
			t.Fatal("owned existing mutex acquired twice")
		} else if !strings.Contains(err.Error(), "此环境已有实例正在运行或变更") {
			t.Fatal("contention was not checked on the existing mutex", err)
		}
		lease.release()
	}

	if err := windows.CloseHandle(observer); err != nil {
		t.Fatal(err)
	}
	observer = 0
	if handle, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name); err == nil {
		_ = windows.CloseHandle(handle)
		t.Fatal("released mutex still has a leaked handle")
	} else if err != windows.ERROR_FILE_NOT_FOUND {
		t.Fatal("unexpected mutex cleanup error", err)
	}
}

func TestServiceCommandQuotesPathsAndPermissionMessage(t *testing.T) {
	b := &windowsServices{root: `D:\work space\数据`, helper: `D:\app space\phpanel-service.exe`}
	i := Instance{ID: "fixture", Component: "redis"}
	command := b.command(i)
	args, err := windows.DecomposeCommandLine(command)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 7 || args[0] != b.helper || args[2] != b.root || args[4] != i.ID || args[6] != systemServiceName(b.root, i) {
		t.Fatal("service command quoting failed", args)
	}
	if !strings.Contains(serviceAccessError(windows.ERROR_ACCESS_DENIED).Error(), "管理员") {
		t.Fatal("missing administrator instruction")
	}
}

func TestElevationHandoffPreservesOriginalRuntimeUserSID(t *testing.T) {
	m, err := NewWithRuntimeUserSID(t.TempDir(), "S-1-5-18")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if m.Snapshot().RuntimeUserSID != "S-1-5-18" {
		t.Fatal("original user SID replaced by elevated account")
	}
	if _, err := NewWithRuntimeUserSID(t.TempDir(), "invalid-sid"); err == nil {
		t.Fatal("invalid runtime SID accepted")
	}
}

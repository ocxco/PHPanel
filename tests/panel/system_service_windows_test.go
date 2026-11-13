//go:build windows

package panel

import (
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

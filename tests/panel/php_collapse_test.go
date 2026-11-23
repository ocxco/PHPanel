package panel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigratePHPCollapseMergesInstanceIntoPackage(t *testing.T) {
	m := testManager(t)
	p, err := m.ImportArchive("php", "8.3.36", writeFixture(t, testZIP(t, map[string][]byte{
		executableName("php"):    fakePE(),
		"ext/php_pdo_sqlite.dll": []byte("dll"),
	})), "")
	if err != nil {
		t.Fatal(err)
	}
	// Legacy instance: config lives at <instance>/config with a conf.d managed
	// block. Its main config carries a user hand-edit and an instance-absolute
	// error_log path that must be rebased to the package directory.
	dir := filepath.Join(m.root, "instances", "instance-php8.3.36")
	// A pre-merge diagnostic php.ini may already exist in the package with stale
	// instance paths; the migration must rebuild it from the instance.
	staleINI := "[PHP]\nerror_log=\"" + filepath.ToSlash(filepath.Join(dir, "logs", "php-error.log")) + "\"\nextension=\"php_stale.dll\"\n"
	if err := os.WriteFile(filepath.Join(p.Path, "php.ini"), []byte(staleINI), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config")
	if err := os.MkdirAll(filepath.Join(cfg, "conf.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	legacyMain := "[PHP]\nmemory_limit=512M\nerror_log=\"" + filepath.ToSlash(filepath.Join(dir, "logs", "php-error.log")) + "\"\n"
	if err := os.WriteFile(filepath.Join(cfg, "php.ini"), []byte(legacyMain), 0o600); err != nil {
		t.Fatal(err)
	}
	managed := "[PHP]\nextension=\"php_zip.dll\"\n[opcache]\nopcache.enable=0\n"
	if err := os.WriteFile(filepath.Join(cfg, "conf.d", phpManagedINI), []byte(managed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.change(func(s *State) error {
		// Legacy packages carried no runtime state of their own.
		for n := range s.Packages {
			if s.Packages[n].ID == p.ID {
				s.Packages[n].Port = 0
				s.Packages[n].Status = ""
			}
		}
		s.Instances = append(s.Instances, Instance{
			ID: "legacy-php", PackageID: p.ID, Name: "php", Component: "php",
			Version: "8.3.36", Port: 9183, Path: dir, Status: "running", PID: 4242,
		})
		s.Sites = append(s.Sites, Site{ID: "site1", Name: "cm83", PHPID: "legacy-php"})
		s.Tools.PHPInstanceID = "legacy-php"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	stale, err := m.migratePHPCollapse()
	if err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot()
	for _, i := range snap.Instances {
		if i.Component == "php" {
			t.Fatal("PHP 实例行未被移除")
		}
	}
	if snap.Sites[0].PHPID != p.ID {
		t.Fatalf("站点 PHPID = %q，期望包 ID", snap.Sites[0].PHPID)
	}
	if snap.Tools.PHPPackageID != p.ID || snap.Tools.PHPInstanceID != "" {
		t.Fatalf("工具链未重指向: %+v", snap.Tools)
	}
	var pkg InstalledPackage
	for _, x := range snap.Packages {
		if x.ID == p.ID {
			pkg = x
		}
	}
	if pkg.Port != 9183 || pkg.Status != "stopped" || pkg.PID != 0 {
		t.Fatalf("包运行时字段未继承: %+v", pkg)
	}

	bodyBytes, err := os.ReadFile(filepath.Join(pkg.Path, "php.ini"))
	if err != nil {
		t.Fatal("包 php.ini 未生成:", err)
	}
	body := string(bodyBytes)
	if !strings.Contains(body, "memory_limit=512M") {
		t.Fatalf("用户手改丢失:\n%s", body)
	}
	if strings.Contains(body, filepath.ToSlash(dir)) {
		t.Fatalf("旧实例路径未重写:\n%s", body)
	}
	if strings.Contains(body, "php_stale.dll") {
		t.Fatalf("旧诊断包配置未重建:\n%s", body)
	}
	if !strings.Contains(body, `extension="php_pdo_sqlite.dll"`) {
		t.Fatalf("基线默认扩展未并入旧管理块:\n%s", body)
	}
	if !strings.Contains(body, `extension="php_zip.dll"`) {
		t.Fatalf("旧管理块用户扩展丢失:\n%s", body)
	}
	if !strings.Contains(body, phpManagedBegin) || !strings.Contains(body, phpManagedEnd) {
		t.Fatalf("管理块标记缺失:\n%s", body)
	}
	backups, _ := filepath.Glob(m.statePath() + ".bak-*")
	if len(backups) == 0 {
		t.Fatal("迁移前未备份 panel.json")
	}
	if len(stale) != 1 || stale[0] != dir {
		t.Fatalf("待清理目录 = %v", stale)
	}

	// Idempotent: a second run plans no deletions and keeps the package config.
	again, err := m.migratePHPCollapse()
	if err != nil {
		t.Fatal("迁移不幂等:", err)
	}
	if len(again) != 0 {
		t.Fatalf("二次迁移仍计划删除: %v", again)
	}
	after, err := os.ReadFile(filepath.Join(pkg.Path, "php.ini"))
	if err != nil || string(after) != body {
		t.Fatal("二次迁移改写了包配置")
	}
}

func TestMigratePHPCollapseFailureKeepsStateFile(t *testing.T) {
	m := testManager(t)
	p, err := m.ImportArchive("php", "8.3.36", writeFixture(t, testZIP(t, map[string][]byte{
		executableName("php"): fakePE(),
	})), "")
	if err != nil {
		t.Fatal(err)
	}
	// Make the package directory unusable (it is a regular file) so the config
	// write fails part-way through the migration.
	broken := filepath.Join(m.root, "packages", "broken-package")
	if err := os.WriteFile(broken, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(m.root, "instances", "instance-broken")
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "php.ini"), []byte("[PHP]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.change(func(s *State) error {
		for n := range s.Packages {
			if s.Packages[n].ID == p.ID {
				s.Packages[n].Path = broken
				s.Packages[n].Executable = filepath.Join(broken, "php.exe")
				s.Packages[n].Port = 0
			}
		}
		s.Instances = append(s.Instances, Instance{ID: "legacy-broken", PackageID: p.ID, Component: "php", Version: "8.3.36", Port: 9184, Path: dir})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(m.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.migratePHPCollapse(); err == nil {
		t.Fatal("不可写目录下迁移应失败")
	}
	after, err := os.ReadFile(m.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("迁移失败后 panel.json 被改动")
	}
	backups, _ := filepath.Glob(m.statePath() + ".bak-*")
	if len(backups) == 0 {
		t.Fatal("迁移前未备份 panel.json")
	}
}

func TestMigratePHPCollapseBackfillsInstanceLessPackage(t *testing.T) {
	m := testManager(t)
	p, err := m.ImportArchive("php", "8.4.27", writeFixture(t, testZIP(t, map[string][]byte{
		executableName("php"):    fakePE(),
		"ext/php_pdo_sqlite.dll": []byte("dll"),
	})), "")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-merge package that never got a runtime: no php.ini, no port.
	ini := filepath.Join(p.Path, "php.ini")
	if err := os.Remove(ini); err != nil {
		t.Fatal(err)
	}
	if err := m.change(func(s *State) error {
		for n := range s.Packages {
			if s.Packages[n].ID == p.ID {
				s.Packages[n].Port = 0
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.migratePHPCollapse(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(ini)
	if err != nil {
		t.Fatal("未为无实例的 PHP 包生成基线:", err)
	}
	if !strings.Contains(string(body), `extension="php_pdo_sqlite.dll"`) {
		t.Fatalf("基线缺少 pdo_sqlite:\n%s", body)
	}
	var port int
	for _, x := range m.Snapshot().Packages {
		if x.ID == p.ID {
			port = x.Port
		}
	}
	if port == 0 {
		t.Fatal("无实例的 PHP 包未分配端口")
	}
}

package panel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePHPBaseConfig(t *testing.T) {
	dir := t.TempDir()
	extDir := filepath.Join(dir, "ext")
	if err := os.MkdirAll(extDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, dll := range []string{"php_pdo_sqlite.dll", "php_sqlite3.dll", "php_mbstring.dll"} {
		if err := os.WriteFile(filepath.Join(extDir, dll), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "php.exe"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := InstalledPackage{Component: "php", Path: dir, Executable: filepath.Join(dir, "php.exe")}
	if err := writePHPBaseConfig(p); err != nil {
		t.Fatalf("writePHPBaseConfig: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "php.ini"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	// TR-4.1: baseline php.ini has relative extension_dir and core SQLite extensions.
	for _, want := range []string{
		`extension_dir="ext"`,
		`extension="php_pdo_sqlite.dll"`,
		`extension="php_sqlite3.dll"`,
		`extension="php_mbstring.dll"`,
		phpManagedBegin,
		phpManagedEnd,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("php.ini missing %q:\n%s", want, s)
		}
	}
	// A base extension whose DLL is absent must not be enabled.
	if strings.Contains(s, "php_openssl.dll") {
		t.Fatalf("openssl enabled despite missing DLL:\n%s", s)
	}
	// Absolute runtime paths point into the package dir (quoted() emits forward slashes).
	if !strings.Contains(s, filepath.ToSlash(filepath.Join(dir, "logs", "php-error.log"))) {
		t.Fatalf("error_log not absolute under package logs:\n%s", s)
	}
	for _, d := range []string{"logs", "temp"} {
		if info, err := os.Stat(filepath.Join(dir, d)); err != nil || !info.IsDir() {
			t.Fatalf("package %s dir missing: %v", d, err)
		}
	}
}

func TestCreateInstanceRejectsPHP(t *testing.T) {
	m := testManager(t)
	pkg := InstalledPackage{
		ID: "php-8.5-reject", Component: "php", Version: "8.5.0",
		Path: t.TempDir(), Executable: `C:\php\php.exe`,
	}
	if err := m.change(func(s *State) error {
		s.Packages = append(s.Packages, pkg)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// TR-4.2: CreateInstance must refuse PHP and create no instance row.
	if _, err := m.CreateInstance(pkg.ID, "should-fail", 9100); err == nil {
		t.Fatal("expected error creating PHP instance, got nil")
	}
	snap := m.Snapshot()
	if len(snap.Instances) != 0 {
		t.Fatalf("expected no instance rows, got %d: %+v", len(snap.Instances), snap.Instances)
	}
}

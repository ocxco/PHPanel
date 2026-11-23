package panel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func flatPublicationFixture(t *testing.T) (*Manager, InstalledPackage, PreparedPHPExtension) {
	t.Helper()
	m, p, item := publicationFixture(t)
	old := filepath.Join(p.Path, "extensions", "redis", "php_redis.dll")
	flat := filepath.Join(p.Path, "extensions", "php_redis.dll")
	if err := os.Rename(old, flat); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(old)); err != nil {
		t.Fatal(err)
	}
	item.Record.RelativeDLL = "extensions/php_redis.dll"
	if err := m.change(func(s *State) error {
		s.ExtensionPackages[0].RelativeDLL = item.Record.RelativeDLL
		s.PreparedExtensions[0] = item
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return m, p, item
}
func TestFlatExtensionUpdatePreservesOtherExtensions(t *testing.T) {
	m, p, item := flatPublicationFixture(t)
	other := filepath.Join(p.Path, "extensions", "php_xdebug.dll")
	before := []byte("another extension")
	if err := os.WriteFile(other, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.publishPreparedExtension(item, p); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(other)
	if err != nil || string(after) != string(before) {
		t.Fatal("unrelated extension changed", err)
	}
	if len(m.Snapshot().ExtensionPackages[0].Files) != 1 {
		t.Fatal("file manifest missing")
	}
}
func TestFlatExtensionSharedDependencyCollision(t *testing.T) {
	m, p, item := flatPublicationFixture(t)
	dependency := filepath.Join(p.Path, "extensions", "common.dll")
	if err := os.WriteFile(dependency, []byte("shared dependency"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := fileHash(dependency)
	if err := m.change(func(s *State) error {
		s.ExtensionPackages = append(s.ExtensionPackages, InstalledPHPExtension{PackageID: p.ID, Name: "xdebug", RelativeDLL: "extensions/php_xdebug.dll", Files: map[string]string{"common.dll": hash}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stage, _ := m.preparedExtensionStage(item)
	if err := os.WriteFile(filepath.Join(stage, "common.dll"), []byte("different dependency"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.publishPreparedExtension(item, p); err == nil || !strings.Contains(err.Error(), "其它扩展") {
		t.Fatal("dependency overwritten", err)
	}
	data, _ := os.ReadFile(dependency)
	if string(data) != "shared dependency" {
		t.Fatal("dependency changed")
	}
	if err := os.WriteFile(filepath.Join(stage, "common.dll"), []byte("shared dependency"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.publishPreparedExtension(item, p); err != nil {
		t.Fatal("identical dependency not reusable", err)
	}
}
func TestFlatPublicationCrashRecovery(t *testing.T) {
	for _, phase := range []string{"before-backup", "after-backup", "after-new-file"} {
		t.Run(phase, func(t *testing.T) {
			m, p, item := flatPublicationFixture(t)
			j, err := m.planFlatPublication(item, p)
			if err != nil {
				t.Fatal(err)
			}
			root, backup, journal, err := m.flatPublicationPaths(j, p)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Mkdir(backup, 0700); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(j)
			if err = os.WriteFile(journal, body, 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "php_redis.dll")
			if phase != "before-backup" {
				if err = os.Rename(target, filepath.Join(backup, "php_redis.dll")); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "after-new-file" {
				stage, _ := m.preparedExtensionStage(item)
				if err = copyExtensionFile(filepath.Join(stage, "php_redis.dll"), target); err != nil {
					t.Fatal(err)
				}
			}
			if err = m.recoverExtensionPublications(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "old DLL fixture" {
				t.Fatal("old DLL not restored", err)
			}
			if _, err = m.preparedExtensionStage(item); err != nil {
				t.Fatal("staged file lost", err)
			}
		})
	}
}
func TestLegacyExtensionMigrationPreservesConfigAndCleansDirectory(t *testing.T) {
	m, p, _ := publicationFixture(t)
	oldPath := filepath.Join(p.Path, "extensions", "redis", "php_redis.dll")
	// Post-merge PHP has no separate instance; migrateFlatExtensions still
	// flattens the on-disk extension layout for the package and must remain
	// idempotent. (Instance config rewriting runs against pre-collapse
	// instances and is consumed by migratePHPCollapse.)
	if err := m.migrateFlatExtensions(); err != nil {
		t.Fatal(err)
	}
	record := m.Snapshot().ExtensionPackages[0]
	if record.RelativeDLL != "extensions/php_redis.dll" {
		t.Fatal(record.RelativeDLL)
	}
	if _, err := installedExtensionDLL(p, record); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(oldPath)); !os.IsNotExist(err) {
		t.Fatal("old folder retained")
	}
	if err := m.migrateFlatExtensions(); err != nil {
		t.Fatal("migration not idempotent", err)
	}
}

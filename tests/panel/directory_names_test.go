package panel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyNamingFixture(t *testing.T) (*Manager, InstalledPackage, Instance) {
	t.Helper()
	m := testManager(t)
	p, err := m.ImportArchive("redis", "7.2.0", writeFixture(t, testZIP(t, map[string][]byte{executableName("redis"): fakePE()})), "")
	if err != nil {
		t.Fatal(err)
	}
	i, err := m.CreateInstance(p.ID, "custom PHP name", 19400)
	if err != nil {
		t.Fatal(err)
	}
	oldPkg, oldInstance := p.Path, i.Path
	p.Path = filepath.Join(m.root, "packages", p.ID)
	p.Executable = filepath.Join(p.Path, executableName("redis"))
	p.DirectoryName = ""
	i.Path = filepath.Join(m.root, "instances", i.ID)
	i.DirectoryName = ""
	if err := os.Rename(oldPkg, p.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldInstance, i.Path); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(i.Path, "config", "redis.conf")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	seed := "# redis config\n" + normalized(oldPkg) + "\n" + normalized(oldInstance) + "\n"
	body := strings.ReplaceAll(strings.ReplaceAll(seed, normalized(oldPkg), normalized(p.Path)), normalized(oldInstance), normalized(i.Path)) + "\n; user configuration must survive\n"
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(i.Path, "data", "keep.bin"), []byte("persistent data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.change(func(s *State) error { s.Packages = []InstalledPackage{p}; s.Instances = []Instance{i}; return nil }); err != nil {
		t.Fatal(err)
	}
	return m, p, i
}
func TestReadableNamesMigrationPreservesIDsConfigurationAndData(t *testing.T) {
	m, p, i := legacyNamingFixture(t)
	m.Close()
	again, err := New(m.root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	state := again.Snapshot()
	pkg, instance := state.Packages[0], state.Instances[0]
	if pkg.ID != p.ID || instance.ID != i.ID || instance.PackageID != p.ID || instance.Name != i.Name {
		t.Fatal("internal references or user name changed")
	}
	if filepath.Base(pkg.Path) != "package-redis7.2.0" || filepath.Base(instance.Path) != "instance-redis7.2.0" {
		t.Fatalf("names: %s %s", pkg.Path, instance.Path)
	}
	body, err := os.ReadFile(filepath.Join(instance.Path, "config", "redis.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "user configuration must survive") || !strings.Contains(string(body), normalized(pkg.Path)) || strings.Contains(string(body), normalized(i.Path)) {
		t.Fatal("configuration not rebased or user content lost")
	}
	data, err := os.ReadFile(filepath.Join(instance.Path, "data", "keep.bin"))
	if err != nil || string(data) != "persistent data" {
		t.Fatal("instance data lost")
	}
	if _, err := os.Stat(pkg.Executable); err != nil {
		t.Fatal("package executable not rebased")
	}
	if err := again.DeleteInstance(i.ID); err != nil {
		t.Fatal("readable instance cannot be deleted:", err)
	}
}
func TestDirectoryMigrationSaveFailureRollsBack(t *testing.T) {
	m, p, i := legacyNamingFixture(t)
	if err := os.Mkdir(m.statePath()+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.migrateDirectoryNames(); err == nil {
		t.Fatal("failed save accepted")
	}
	if m.Snapshot().Packages[0].Path != p.Path || m.Snapshot().Instances[0].Path != i.Path {
		t.Fatal("metadata not rolled back")
	}
	if _, err := os.Stat(p.Executable); err != nil {
		t.Fatal("package not restored")
	}
	body, err := os.ReadFile(filepath.Join(i.Path, "config", "redis.conf"))
	if err != nil || !strings.Contains(string(body), normalized(i.Path)) {
		t.Fatal("configuration not restored")
	}
	if err := os.Remove(m.statePath() + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := m.migrateDirectoryNames(); err != nil {
		t.Fatal("retry migration:", err)
	}
}
func TestDirectoryMigrationRecoversInterruptedRename(t *testing.T) {
	m, p, _ := legacyNamingFixture(t)
	from, _ := filepath.Rel(m.root, p.Path)
	to := filepath.Join("packages", "package-redis7.2.0")
	j := directoryJournal{Moves: []directoryMove{{Kind: "packages", ID: p.ID, From: filepath.ToSlash(from), To: filepath.ToSlash(to)}}}
	body, _ := json.Marshal(j)
	if err := os.WriteFile(m.directoryJournalPath(), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p.Path, filepath.Join(m.root, to)); err != nil {
		t.Fatal(err)
	}
	if err := m.recoverDirectoryMigration(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Executable); err != nil {
		t.Fatal("interrupted migration did not restore package")
	}
}
func TestReadableDirectoryCollisionDoesNotOverwrite(t *testing.T) {
	m := testManager(t)
	create := func(payload string) InstalledPackage {
		p, err := m.ImportArchive("redis", "7.0.0", writeFixture(t, testZIP(t, map[string][]byte{executableName("redis"): fakePE(), "payload.txt": []byte(payload)})), "")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, b := create("first"), create("second")
	if filepath.Base(a.Path) != "package-redis7.0.0" || filepath.Base(b.Path) != "package-redis7.0.0-2" {
		t.Fatal("package directory collision not disambiguated")
	}
	for _, p := range []InstalledPackage{a, b} {
		if _, err := os.Stat(p.Executable); err != nil {
			t.Fatal(err)
		}
	}
	i, err := m.CreateInstance(a.ID, "one", 19401)
	if err != nil {
		t.Fatal(err)
	}
	k, err := m.CreateInstance(b.ID, "two", 19402)
	if err != nil {
		t.Fatal(err)
	}
	if i.Path == k.Path {
		t.Fatal("instances share a directory")
	}
}
func TestThemePersistenceAndNetworkSettingsIsolation(t *testing.T) {
	m := testManager(t)
	before := m.Snapshot().Settings
	if err := m.SaveTheme("dark"); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Settings.ProxyMode != before.ProxyMode {
		t.Fatal("theme changed network preferences")
	}
	if err := m.SaveTheme("invalid"); err == nil {
		t.Fatal("invalid theme accepted")
	}
	m.Close()
	again, err := New(m.root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Snapshot().Settings.ThemeMode != "dark" {
		t.Fatal("theme not persisted")
	}
}

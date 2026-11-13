package panel

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDeleteInstanceRemovesOnlyItsFilesAndFreesPort(t *testing.T) {
	m, _, _, p, q := siteFixture(t)
	file := filepath.Join(p.Path, "data", "keep.txt")
	if err := os.WriteFile(file, []byte("instance data"), 0600); err != nil {
		t.Fatal(err)
	}
	_, pkg, err := m.lookupInstance(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteInstance(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Path); !os.IsNotExist(err) {
		t.Fatalf("instance directory remained: %v", err)
	}
	if _, _, err := m.lookupInstance(p.ID); err == nil {
		t.Fatal("instance record remained")
	}
	if _, err := os.Stat(pkg.Executable); err != nil {
		t.Fatal("installed program deleted")
	}
	if _, _, err := m.lookupInstance(q.ID); err != nil {
		t.Fatal("sibling instance deleted")
	}
	if _, err := os.Stat(q.Path); err != nil {
		t.Fatal("sibling directory deleted")
	}
	if _, err := m.CreateInstance(p.PackageID, "replacement", p.Port); err != nil {
		t.Fatal("deleted port not freed:", err)
	}
}
func TestDeleteDatabaseInstancesRemovesDataAndKeepsPackages(t *testing.T) {
	for _, component := range []string{"mysql", "redis"} {
		t.Run(component, func(t *testing.T) {
			m := testManager(t)
			pkg, err := m.ImportArchive(component, "fixture", writeFixture(t, testZIP(t, map[string][]byte{executableName(component): fakePE()})), "")
			if err != nil {
				t.Fatal(err)
			}
			i, err := m.CreateInstance(pkg.ID, component, 19931)
			if err != nil {
				t.Fatal(err)
			}
			data := filepath.Join(i.Path, "data", "database", "record")
			if err := os.MkdirAll(filepath.Dir(data), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(data, []byte("persistent database data"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := m.DeleteInstance(i.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(data); !os.IsNotExist(err) {
				t.Fatal("database data remained")
			}
			if len(m.Snapshot().Packages) != 1 || len(m.Snapshot().Instances) != 0 {
				t.Fatal("wrong deletion scope")
			}
			if _, err := os.Stat(pkg.Executable); err != nil {
				t.Fatal("database program removed")
			}
		})
	}
}
func TestDeleteInstanceRejectsRunningBusyAndWebsiteReferences(t *testing.T) {
	m, a, _, p, q := siteFixture(t)
	for _, status := range []string{"starting", "running", "stopping"} {
		if err := m.setInstance(q.ID, status, "", 0); err != nil {
			t.Fatal(err)
		}
		if err := m.DeleteInstance(q.ID); err == nil || !strings.Contains(err.Error(), "停止") {
			t.Fatalf("status %s allowed deletion: %v", status, err)
		}
	}
	_ = m.setInstance(q.ID, "stopped", "", 0)
	m.mu.Lock()
	m.processes[q.ID] = &managedProcess{}
	m.mu.Unlock()
	err := m.DeleteInstance(q.ID)
	m.mu.Lock()
	delete(m.processes, q.ID)
	m.operations[q.ID] = true
	m.mu.Unlock()
	if err == nil {
		t.Fatal("owned process allowed deletion")
	}
	err = m.DeleteInstance(q.ID)
	m.mu.Lock()
	delete(m.operations, q.ID)
	m.mu.Unlock()
	if err == nil {
		t.Fatal("busy instance allowed deletion")
	}
	if _, err := m.CreateSiteWithOptions("uses-instance", "", a.ID, p.ID, 19932, "php"); err != nil {
		t.Fatal(err)
	}
	for _, i := range []Instance{p} {
		if err := m.DeleteInstance(i.ID); err == nil || !strings.Contains(err.Error(), "uses-instance") {
			t.Fatalf("referenced instance deleted: %v", err)
		}
	}
	if _, err := os.Stat(q.Path); err != nil {
		t.Fatal("rejected deletion changed files")
	}
}
func TestDeleteInstanceSaveFailureRestoresFilesAndRecord(t *testing.T) {
	m, _, _, p, _ := siteFixture(t)
	file := filepath.Join(p.Path, "data", "keep.txt")
	_ = os.WriteFile(file, []byte("keep"), 0600)
	before := m.Snapshot()
	if err := os.Mkdir(m.statePath()+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteInstance(p.ID); err == nil {
		t.Fatal("metadata save failure ignored")
	}
	if m.Snapshot().Revision != before.Revision || len(m.Snapshot().Instances) != len(before.Instances) {
		t.Fatal("record changed after save failure")
	}
	body, err := os.ReadFile(file)
	if err != nil || string(body) != "keep" {
		t.Fatal("files not restored")
	}
	if err := os.Remove(m.statePath() + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteInstance(p.ID); err != nil {
		t.Fatal("retry after save failure:", err)
	}
}
func TestDeleteInstanceRejectsOutsideAndSiblingPaths(t *testing.T) {
	for _, kind := range []string{"outside", "sibling"} {
		t.Run(kind, func(t *testing.T) {
			m, _, _, p, q := siteFixture(t)
			target := t.TempDir()
			if kind == "sibling" {
				target = q.Path
			}
			file := filepath.Join(target, "keep.txt")
			_ = os.WriteFile(file, []byte("do not delete"), 0600)
			if err := m.change(func(s *State) error {
				for n := range s.Instances {
					if s.Instances[n].ID == p.ID {
						s.Instances[n].Path = target
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := m.DeleteInstance(p.ID); err == nil {
				t.Fatal("unsafe path accepted")
			}
			body, err := os.ReadFile(file)
			if err != nil || string(body) != "do not delete" {
				t.Fatal("unrelated files deleted")
			}
			if len(m.Snapshot().Instances) != 4 {
				t.Fatal("unsafe deletion changed records")
			}
		})
	}
}
func TestDeleteInstanceRejectsDirectoryLink(t *testing.T) {
	m, _, _, p, _ := siteFixture(t)
	outside := t.TempDir()
	file := filepath.Join(outside, "keep.txt")
	_ = os.WriteFile(file, []byte("keep"), 0600)
	original := p.Path + ".original"
	if err := os.Rename(p.Path, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, p.Path); err != nil {
		t.Skip("directory symlinks unavailable:", err)
	}
	if err := m.DeleteInstance(p.ID); err == nil {
		t.Fatal("linked directory allowed deletion")
	}
	body, err := os.ReadFile(file)
	if err != nil || string(body) != "keep" {
		t.Fatal("linked project files deleted")
	}
}
func TestDeleteInstanceMissingDirectoryRemovesRecord(t *testing.T) {
	m, _, _, p, _ := siteFixture(t)
	if err := os.RemoveAll(p.Path); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteInstance(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.lookupInstance(p.ID); err == nil {
		t.Fatal("missing-directory record remained")
	}
	if err := m.DeleteInstance(p.ID); err == nil {
		t.Fatal("missing instance accepted")
	}
}
func TestDeleteInstanceLockedDirectoryKeepsFilesAndRecord(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows directory sharing semantics")
	}
	m, _, _, p, _ := siteFixture(t)
	f, err := os.Open(filepath.Join(p.Path, "config", "php.ini"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = m.DeleteInstance(p.ID)
	if err == nil {
		t.Fatal("locked directory deletion succeeded")
	}
	if _, _, err := m.lookupInstance(p.ID); err != nil {
		t.Fatal("locked deletion removed record")
	}
	if _, err := os.Stat(p.Path); err != nil {
		t.Fatal("locked deletion changed directory")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteInstance(p.ID); err != nil {
		t.Fatal("unlocked deletion failed:", err)
	}
}

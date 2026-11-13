package panel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocationsResolveActualRootPackagesAndInstanceData(t *testing.T) {
	m := testManager(t)
	root, err := m.ResolveLocation("root", "")
	if err != nil || root.Path != m.root || root.IsFile {
		t.Fatalf("root: %+v %v", root, err)
	}
	p, err := m.ImportArchive("nginx", "fixture", writeFixture(t, testZIP(t, map[string][]byte{"nginx.exe": fakePE()})), "")
	if err != nil {
		t.Fatal(err)
	}
	i, err := m.CreateInstance(p.ID, "fixture", 18789)
	if err != nil {
		t.Fatal(err)
	}
	for kind, want := range map[string]string{"package": p.Path, "instance": i.Path, "config": filepath.Join(i.Path, "config"), "data": filepath.Join(i.Path, "data"), "logs": filepath.Join(i.Path, "logs")} {
		key := i.ID
		if kind == "package" {
			key = p.ID
		}
		location, err := m.ResolveLocation(kind, key)
		if err != nil || location.Path != want {
			t.Fatalf("%s: %+v %v", kind, location, err)
		}
	}
	if _, err := m.ResolveLocation("package", "../outside"); err == nil {
		t.Fatal("unknown ID resolved")
	}
	if _, err := m.ResolveLocation("shell", ""); err == nil {
		t.Fatal("arbitrary location kind resolved")
	}
}
func TestDownloadLocationSelectsVerifiedCacheFile(t *testing.T) {
	m := testManager(t)
	a := m.Snapshot().Artifacts[0]
	key := id("download")
	path := filepath.Join(m.root, "downloads", "cache", a.SHA256+".zip")
	if err := m.change(func(s *State) error {
		s.Tasks = append(s.Tasks, Task{ID: key, ArtifactID: a.ID, Status: "completed"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResolveLocation("archive", key); err == nil {
		t.Fatal("missing cache file presented as existing")
	}
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	location, err := m.ResolveLocation("archive", key)
	if err != nil || !location.IsFile || location.Path != path {
		t.Fatalf("archive: %+v %v", location, err)
	}
}

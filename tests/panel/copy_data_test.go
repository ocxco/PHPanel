package panel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyDataRootKeepsOriginalAndRebasesRecords(t *testing.T) {
	source := testManager(t)
	archive := writeFixture(t, testZIP(t, map[string][]byte{"nginx.exe": fakePE()}))
	p, err := source.ImportArchive("nginx", "fixture", archive, "")
	if err != nil {
		t.Fatal(err)
	}
	instance, err := source.CreateInstance(p.ID, "fixture", 19980)
	if err != nil {
		t.Fatal(err)
	}
	dataFile := filepath.Join(instance.Path, "data", "kept.txt")
	if err := os.WriteFile(dataFile, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	sourceRoot := source.root
	source.Close()
	target := t.TempDir()
	executable := filepath.Join(target, "PHPanel.exe")
	if err := os.WriteFile(executable, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CopyDataRoot(sourceRoot, target); err != nil {
		t.Fatal(err)
	}
	copied, err := New(target)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	state := copied.Snapshot()
	if len(state.Packages) != 1 || len(state.Instances) != 1 || state.Instances[0].Path == instance.Path {
		t.Fatal("copied records not rebased")
	}
	bytes, err := os.ReadFile(filepath.Join(state.Instances[0].Path, "data", "kept.txt"))
	if err != nil || string(bytes) != "keep" {
		t.Fatal("instance data lost")
	}
	if _, err := os.Stat(dataFile); err != nil {
		t.Fatal("original data removed")
	}
	bytes, _ = os.ReadFile(executable)
	if string(bytes) != "untouched" {
		t.Fatal("executable overwritten")
	}
	if err := CopyDataRoot(sourceRoot, target); err == nil {
		t.Fatal("existing target overwritten")
	}
}
func TestCopyRejectsRunningSourceAndNestedDestination(t *testing.T) {
	source := testManager(t)
	if err := CopyDataRoot(source.root, t.TempDir()); err == nil {
		t.Fatal("running source copied")
	}
	if err := CopyDataRoot(source.root, filepath.Join(source.root, "nested")); err == nil {
		t.Fatal("nested destination accepted")
	}
}

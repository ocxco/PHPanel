package panel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhysicalRootAndOwnedPathsRemainConsistent(t *testing.T) {
	m := testManager(t)
	actual, err := PhysicalPath(m.root)
	if err != nil || m.Snapshot().Root != actual {
		t.Fatalf("actual root: %q %v", actual, err)
	}
	previous := filepath.Join(t.TempDir(), "old-alias")
	state := State{Packages: []InstalledPackage{{Path: filepath.Join(previous, "packages", "fixture"), Executable: filepath.Join(previous, "packages", "fixture", "php-cgi.exe")}}, Instances: []Instance{{Path: filepath.Join(previous, "instances", "fixture")}}, Sites: []Site{{Root: filepath.Join(previous, "sites", "fixture")}, {Root: "external-project"}}}
	rebaseStatePaths(&state, previous, m.root)
	if state.Packages[0].Path != filepath.Join(m.root, "packages", "fixture") || state.Instances[0].Path != filepath.Join(m.root, "instances", "fixture") || state.Sites[1].Root != "external-project" {
		t.Fatal("path rebasing changed external data or missed owned paths")
	}
}
func TestConfigAliasCorrectionKeepsBackup(t *testing.T) {
	m := testManager(t)
	m.previousRoot = filepath.Join(t.TempDir(), "old-alias")
	i := Instance{Component: "php", Path: filepath.Join(m.root, "instances", "fixture")}
	if err := os.MkdirAll(filepath.Join(i.Path, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(i.Path, "config", "php.ini")
	before := "extension_dir=\"" + strings.ReplaceAll(filepath.Join(m.previousRoot, "packages", "php", "ext"), "\\", "/") + "\"\n"
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.updateConfigRoot(i); err != nil {
		t.Fatal(err)
	}
	updated, _ := os.ReadFile(path)
	backup, _ := os.ReadFile(path + ".before-path-fix")
	if string(backup) != before || strings.Contains(string(updated), strings.ReplaceAll(m.previousRoot, "\\", "/")) {
		t.Fatal("config correction lost backup or left alias")
	}
}

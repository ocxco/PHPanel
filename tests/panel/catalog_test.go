package panel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStoredCatalogueGetsNewBuiltinsWithoutOverwritingUserSources(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	seeds := seedCatalog()
	var mysql Artifact
	for _, a := range seeds {
		if a.Component == "mysql" && a.Version == "8.4.6" {
			mysql = a
			break
		}
	}
	if mysql.ID == "" {
		t.Fatal("missing existing MySQL version")
	}
	mysql.Sources = append([]Source{{Name: "自定义镜像", URL: "https://example.invalid/mysql.zip"}}, mysql.Sources...)
	old := State{Artifacts: []Artifact{mysql}, Packages: []InstalledPackage{}, Instances: []Instance{}, Sites: []Site{}, Tasks: []Task{}, Settings: Settings{ProxyMode: "direct", SourceMode: "custom"}}
	encoded, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state", "panel.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	state := m.Snapshot()
	if len(state.Artifacts) != len(seeds) {
		t.Fatalf("upgrade kept %d/%d artifacts", len(state.Artifacts), len(seeds))
	}
	for _, a := range state.Artifacts {
		if a.ID == mysql.ID && a.Sources[0].URL != "https://example.invalid/mysql.zip" {
			t.Fatal("user source overwritten")
		}
	}
	if state.Settings.SourceMode != "custom" {
		t.Fatal("user settings overwritten")
	}
	revision := state.Revision
	if err := m.syncMySQLCatalog(); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Revision != revision {
		t.Fatal("no-op catalogue sync created an update")
	}
}
func TestMySQLSyncAndSeedIntegrity(t *testing.T) {
	m := testManager(t)
	if err := m.change(func(s *State) error {
		kept := []Artifact{}
		for _, a := range s.Artifacts {
			if a.Component != "mysql" {
				kept = append(kept, a)
			}
		}
		s.Artifacts = kept
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.syncMySQLCatalog(); err != nil {
		t.Fatal(err)
	}
	count := 0
	branches := map[string]bool{}
	for _, a := range m.Snapshot().Artifacts {
		if err := validateArtifact(a); err != nil {
			t.Fatalf("invalid seed %s: %v", a.Version, err)
		}
		if a.Component == "mysql" {
			count++
			branches[a.Version[:3]] = true
		}
	}
	if count < 8 || !branches["5.7"] || !branches["8.0"] || !branches["8.4"] || !branches["9.3"] {
		t.Fatalf("incomplete MySQL catalogue: %d %v", count, branches)
	}
}

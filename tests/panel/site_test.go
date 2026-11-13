package panel

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func siteFixture(t *testing.T) (*Manager, Instance, Instance, Instance, Instance) {
	t.Helper()
	m := testManager(t)
	nginx, err := m.ImportArchive("nginx", "fixture", writeFixture(t, testZIP(t, map[string][]byte{"nginx.exe": fakePE()})), "")
	if err != nil {
		t.Fatal(err)
	}
	php, err := m.ImportArchive("php", "fixture", writeFixture(t, testZIP(t, map[string][]byte{"php-cgi.exe": fakePE()})), "")
	if err != nil {
		t.Fatal(err)
	}
	create := func(p InstalledPackage, name string, port int) Instance {
		i, err := m.CreateInstance(p.ID, name, port)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	a := create(nginx, "first", 18081)
	b := create(nginx, "second", 18082)
	p := create(php, "php-first", 19001)
	q := create(php, "php-second", 19002)
	m.nginxValidator = func(Instance, InstalledPackage, string) error { return nil }
	return m, a, b, p, q
}
func TestSiteEditMoveAndDeletePreserveProjectFiles(t *testing.T) {
	m, a, b, p, q := siteFixture(t)
	site, err := m.CreateSiteWithOptions("before", "", a.ID, p.ID, 18081, "php")
	if err != nil {
		t.Fatal(err)
	}
	originalFile := filepath.Join(site.Root, "index.php")
	originalContent, _ := os.ReadFile(originalFile)
	newRoot := t.TempDir()
	projectFile := filepath.Join(newRoot, "keep.txt")
	_ = os.WriteFile(projectFile, []byte("keep"), 0600)
	updated, err := m.UpdateSiteWithOptions(site.ID, "after", newRoot, b.ID, q.ID, 18082, "php")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != site.ID || updated.URL != "http://after.localhost:18082" {
		t.Fatalf("updated: %+v", updated)
	}
	newConfig, _ := os.ReadFile(filepath.Join(m.root, filepath.FromSlash(siteConfigName(site))))
	if bytes.Contains(newConfig, []byte("before.localhost")) || !bytes.Contains(newConfig, []byte("after.localhost")) || !bytes.Contains(newConfig, []byte("127.0.0.1:19002")) {
		t.Fatal("shared website configuration not updated")
	}
	if err := m.DeleteSite(site.ID); err != nil {
		t.Fatal(err)
	}
	if len(m.Snapshot().Sites) != 0 {
		t.Fatal("deleted site remained in metadata")
	}
	newConfig, _ = os.ReadFile(filepath.Join(m.root, filepath.FromSlash(siteConfigName(site))))
	if bytes.Contains(newConfig, []byte("after.localhost")) {
		t.Fatal("deleted vhost remained")
	}
	kept, _ := os.ReadFile(projectFile)
	previous, _ := os.ReadFile(originalFile)
	if string(kept) != "keep" || !bytes.Equal(previous, originalContent) {
		t.Fatal("website files changed or deleted")
	}
}
func TestSiteValidationFailureKeepsRecordsAndConfiguration(t *testing.T) {
	m, a, _, p, _ := siteFixture(t)
	first, err := m.CreateSite("first", "", a.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.CreateSite("second", "", a.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateSite(first.ID, second.Name, first.Root, a.ID, p.ID); err == nil {
		t.Fatal("duplicate name accepted")
	}
	if _, err := m.UpdateSite(first.ID, "bad", filepath.Join(t.TempDir(), "missing"), a.ID, p.ID); err == nil {
		t.Fatal("missing root accepted")
	}
	config := filepath.Join(a.Path, "config", "nginx.conf")
	before, _ := os.ReadFile(config)
	m.nginxValidator = func(Instance, InstalledPackage, string) error { return fmt.Errorf("invalid candidate") }
	if _, err := m.UpdateSite(first.ID, "changed", first.Root, a.ID, p.ID); err == nil {
		t.Fatal("failed config check saved edit")
	}
	if err := m.DeleteSite(first.ID); err == nil {
		t.Fatal("failed config check saved deletion")
	}
	after, _ := os.ReadFile(config)
	if !bytes.Equal(before, after) || len(m.Snapshot().Sites) != 2 || m.Snapshot().Sites[0].Name != "first" {
		t.Fatal("failed operation mutated state")
	}
}
func TestReloadFailureRestoresConfigurationAndSiteRecord(t *testing.T) {
	m, a, _, p, _ := siteFixture(t)
	site, err := m.CreateSite("original", "", a.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(m.root, filepath.FromSlash(siteConfigName(site)))
	before, _ := os.ReadFile(config)
	m.mu.Lock()
	m.processes[a.ID] = &managedProcess{}
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.processes, a.ID); m.mu.Unlock() }()
	reloads := 0
	m.nginxReloader = func(Instance, InstalledPackage) error {
		reloads++
		if reloads == 1 {
			return fmt.Errorf("reload failure")
		}
		return nil
	}
	if _, err := m.UpdateSite(site.ID, "changed", site.Root, a.ID, p.ID); err == nil {
		t.Fatal("failed reload saved edit")
	}
	after, _ := os.ReadFile(config)
	if !bytes.Equal(before, after) || m.Snapshot().Sites[0].Name != "original" || reloads != 2 {
		t.Fatal("reload rollback incomplete")
	}
}
func TestEditingOnlyNameKeepsExistingRootAndUserContent(t *testing.T) {
	m, a, _, p, _ := siteFixture(t)
	site, err := m.CreateSite("original", "", a.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(site.Root, "index.php")
	_ = os.WriteFile(file, []byte("user content"), 0600)
	updated, err := m.UpdateSite(site.ID, "renamed", "", a.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(file)
	if updated.Root != site.Root || strings.TrimSpace(string(content)) != "user content" {
		t.Fatal("edit replaced website directory or welcome content")
	}
}

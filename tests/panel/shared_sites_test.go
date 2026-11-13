package panel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSharedSitesMigrationAndDeletingAllNginxPreservesConfig(t *testing.T) {
	m, a, b, p, _ := siteFixture(t)
	site, err := m.CreateSiteWithOptions("shared", "", a.ID, p.ID, 18311, "php")
	if err != nil {
		t.Fatal(err)
	}
	sharedPath := filepath.Join(m.root, "sites", site.ID+".conf")
	generated, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatal(err)
	}
	custom := append(generated, []byte("\n# custom rule survives migration\n")...)
	legacyPath := filepath.Join(a.Path, "config", "sites", site.ID+".conf")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, custom, 0600); err != nil {
		t.Fatal(err)
	}
	backup := []byte("# earlier user configuration\n")
	if err := os.WriteFile(legacyPath+".before-site-change", backup, 0600); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(a.Path, "config", "nginx.conf")
	main, _ := os.ReadFile(mainPath)
	main = bytes.ReplaceAll(main, []byte("worker_processes 1;"), []byte("worker_processes 2;"))
	main = bytes.ReplaceAll(main, []byte(nginxInclude(m.root)), []byte(legacyNginxInclude(a)))
	if err := os.WriteFile(mainPath, main, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sharedPath); err != nil {
		t.Fatal(err)
	}
	m.Close()
	again, err := New(m.root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	actual, err := os.ReadFile(sharedPath)
	if err != nil || !bytes.Equal(actual, custom) {
		t.Fatal("custom website config lost in migration")
	}
	retainedBackup, _ := os.ReadFile(sharedPath + ".before-site-change")
	if !bytes.Equal(retainedBackup, backup) {
		t.Fatal("website config backup not migrated")
	}
	updatedMain, _ := os.ReadFile(mainPath)
	if !bytes.Contains(updatedMain, []byte("worker_processes 2;")) || !bytes.Contains(updatedMain, []byte(nginxInclude(again.root))) || bytes.Contains(updatedMain, []byte(legacyNginxInclude(a))) {
		t.Fatal("main migration overwrote custom settings or kept old include")
	}
	for _, i := range []Instance{a, b} {
		if err := again.DeleteInstance(i.ID); err != nil {
			t.Fatal(err)
		}
	}
	actual, err = os.ReadFile(sharedPath)
	if err != nil || !bytes.Equal(actual, custom) {
		t.Fatal("deleting Nginx removed shared website config")
	}
	if len(again.Snapshot().Sites) != 1 || again.Snapshot().Sites[0].NginxID != "" {
		t.Fatal("website removed or preferred deleted Nginx retained")
	}
	if err := again.DeleteSite(site.ID); err != nil {
		t.Fatal("delete website without any Nginx:", err)
	}
	if _, err := os.Stat(sharedPath); !os.IsNotExist(err) {
		t.Fatal("deleted website config remained")
	}
	if _, err := os.Stat(filepath.Join(site.Root, "index.php")); err != nil {
		t.Fatal("website project files removed")
	}
}
func TestConcurrentNginxCreationDoesNotResurrectDeletedWebsiteConfig(t *testing.T) {
	m, a, _, p, _ := siteFixture(t)
	site, err := m.CreateSiteWithOptions("remove-during-create", "", a.ID, p.ID, 18313, "php")
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.processes[a.ID] = &managedProcess{}
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.processes, a.ID); m.mu.Unlock() }()
	removed := make(chan struct{})
	release := make(chan struct{})
	var signaled, unblock sync.Once
	defer unblock.Do(func() { close(release) })
	m.nginxReloader = func(Instance, InstalledPackage) error { signaled.Do(func() { close(removed) }); <-release; return nil }
	deleted := make(chan error, 1)
	created := make(chan error, 1)
	go func() { deleted <- m.DeleteSite(site.ID) }()
	select {
	case <-removed:
	case <-time.After(2 * time.Second):
		t.Fatal("website deletion did not reach reload")
	}
	go func() { _, err := m.CreateInstance(a.PackageID, "new-nginx", 0); created <- err }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		reserved := m.operations[a.PackageID]
		m.mu.Unlock()
		if reserved {
			break
		}
		time.Sleep(time.Millisecond)
	}
	unblock.Do(func() { close(release) })
	for _, done := range []chan error{deleted, created} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent operation did not complete")
		}
	}
	if len(m.Snapshot().Sites) != 0 {
		t.Fatal("deleted website record returned")
	}
	if _, err := os.Stat(filepath.Join(m.root, "sites", site.ID+".conf")); !os.IsNotExist(err) {
		t.Fatal("new Nginx resurrected deleted website config")
	}
}
func TestSharedEditorAndOnlyValidationSelectionChangeKeepsCustomContent(t *testing.T) {
	m, a, b, p, _ := siteFixture(t)
	site, err := m.CreateSiteWithOptions("edited", "", a.ID, p.ID, 18312, "php")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := m.ReadConfig(b.ID, siteConfigName(site))
	if err != nil {
		t.Fatal("another Nginx cannot read shared config:", err)
	}
	if doc.Path != filepath.Join(m.root, "sites", site.ID+".conf") {
		t.Fatalf("editor path: %s", doc.Path)
	}
	custom := doc.Content + "\n# custom setting\n"
	if _, err := m.SaveConfig(b.ID, doc.Name, custom, doc.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateSiteWithOptions(site.ID, site.Name, site.Root, b.ID, p.ID, site.Port, site.Template); err != nil {
		t.Fatal(err)
	}
	current, _ := m.ReadConfig(a.ID, doc.Name)
	if current.Content != custom {
		t.Fatal("choosing validation version overwrote custom website config")
	}
	phpConfig, err := m.ReadConfig(p.ID, "php.ini")
	if err != nil {
		t.Fatal("PHP config path regressed:", err)
	}
	if phpConfig.Path != filepath.Join(p.Path, "config", "php.ini") {
		t.Fatal("PHP config moved out of instance")
	}
	if _, err := m.SaveConfig(p.ID, "php.ini", phpConfig.Content+"\n; user setting\n", phpConfig.Revision); err != nil {
		t.Fatal(err)
	}
}
func TestPHPStartRulesRemainPerPackageAndNginxStartBlocksConfigChanges(t *testing.T) {
	m, a, _, p, q := siteFixture(t)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processes[p.ID] = &managedProcess{}
	defer delete(m.processes, p.ID)
	if err := m.canStartLocked(q); err == nil || !strings.Contains(err.Error(), "已安装版本") {
		t.Fatal("PHP package singleton lost")
	}
	other := q
	other.PackageID = "different-php-version"
	if err := m.canStartLocked(other); err != nil {
		t.Fatal("different PHP versions blocked:", err)
	}
	m.operations["nginx-config"] = true
	defer delete(m.operations, "nginx-config")
	if err := m.canStartLocked(a); err == nil {
		t.Fatal("Nginx started during shared config publication")
	}
}

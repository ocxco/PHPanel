package panel

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStartReservationIsPerInstalledPackage(t *testing.T) {
	m, a, b, p, _ := siteFixture(t)
	if a.Port != 0 || b.Port != 0 {
		t.Fatal("Nginx instance still has a port")
	}
	m.mu.Lock()
	m.processes[a.ID] = &managedProcess{}
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.processes, a.ID); m.mu.Unlock() }()
	if err := m.StartInstance(b.ID); err == nil || !strings.Contains(err.Error(), "Nginx") {
		t.Fatalf("duplicate start: %v", err)
	}
	m.mu.Lock()
	if err := m.canStartLocked(p); err != nil {
		t.Fatal(err)
	}
	other := b
	other.PackageID = "different-version"
	if err := m.canStartLocked(other); err == nil {
		m.mu.Unlock()
		t.Fatal("different Nginx version allowed alongside running Nginx")
	}
	delete(m.processes, a.ID)
	m.mu.Unlock()
	var wg sync.WaitGroup
	wins := 0
	for _, i := range []Instance{a, b} {
		wg.Add(1)
		go func(i Instance) {
			defer wg.Done()
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.canStartLocked(i) == nil {
				m.operations[instanceStartKey(i)] = true
				wins++
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("concurrent start reservations: %d", wins)
	}
	m.mu.Lock()
	delete(m.operations, instanceStartKey(a))
	m.mu.Unlock()
}
func TestTemplatesSplitFilesAndConfigEditor(t *testing.T) {
	m, a, _, p, _ := siteFixture(t)
	for n, template := range []string{"php", "laravel", "thinkphp", "wordpress", "static"} {
		site, err := m.CreateSiteWithOptions(template, "", a.ID, p.ID, 18100+n, template)
		if err != nil {
			t.Fatal(err)
		}
		if template == "static" && site.PHPID != "" {
			t.Fatal("static site bound to PHP")
		}
		name := siteConfigName(site)
		doc, err := m.ReadConfig(a.ID, name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc.Content, fmt.Sprintf("    listen 127.0.0.1:%d;", site.Port)) {
			t.Fatal("missing indented site port")
		}
		if template == "static" && (strings.Contains(doc.Content, "fastcgi_pass") || !strings.Contains(doc.Content, "return 404")) {
			t.Fatal("static template serves PHP")
		}
		if template == "thinkphp" && !strings.Contains(doc.Content, "s=$uri&$query_string") {
			t.Fatal("ThinkPHP route missing")
		}
		saved, err := m.SaveConfig(a.ID, name, doc.Content+"\n# custom rule\n", doc.Revision)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.SaveConfig(a.ID, name, doc.Content, doc.Revision); err == nil {
			t.Fatal("stale editor overwrote file")
		}
		if err := m.writeNginxConfig(a); err != nil {
			t.Fatal(err)
		}
		current, _ := m.ReadConfig(a.ID, name)
		if current.Content != saved.Content {
			t.Fatal("startup overwrote custom config")
		}
		if _, err := m.CreateSiteWithOptions(template+"-other", "", a.ID, p.ID, site.Port, template); err != nil {
			t.Fatal(err)
		}
		current, _ = m.ReadConfig(a.ID, name)
		if current.Content != saved.Content {
			t.Fatal("unrelated site edit overwrote custom config")
		}
	}
	main, _ := m.ReadConfig(a.ID, "nginx.conf")
	if strings.Contains(main.Content, "server_name php.localhost") || !strings.Contains(main.Content, "    include ") {
		t.Fatal("main config contains vhosts or lacks include")
	}
	if _, err := m.ReadConfig(a.ID, "../nginx.pid"); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err := m.ReadConfig(p.ID, "credentials.txt"); err == nil {
		t.Fatal("non-config read accepted")
	}
	if _, err := m.SaveConfig(a.ID, "nginx.conf", "events {}\nhttp {}", main.Revision); err == nil {
		t.Fatal("managed include removed")
	}
	m.nginxValidator = func(Instance, InstalledPackage, string) error { return fmt.Errorf("invalid") }
	if _, err := m.SaveConfig(a.ID, "nginx.conf", main.Content+"invalid;", main.Revision); err == nil {
		t.Fatal("invalid config saved")
	}
	current, _ := m.ReadConfig(a.ID, "nginx.conf")
	if current.Content != main.Content {
		t.Fatal("failed save changed config")
	}
}
func TestSitePortsAndLegacyMigration(t *testing.T) {
	m, a, b, p, _ := siteFixture(t)
	site, err := m.CreateSiteWithOptions("legacy", "", a.ID, p.ID, 18200, "php")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSiteWithOptions("shared-port", "", b.ID, p.ID, 18200, "php"); err != nil {
		t.Fatal("global pool rejected shared port:", err)
	}
	if _, err := m.CreateSiteWithOptions("invalid", "", a.ID, p.ID, 0, "php"); err == nil {
		t.Fatal("port zero accepted")
	}
	if _, err := m.CreateSiteWithOptions("php-port", "", a.ID, p.ID, p.Port, "php"); err == nil {
		t.Fatal("service port conflict accepted")
	}
	legacy := []byte("events { worker_connections 1024; }\nhttp { server { listen 127.0.0.1:18200; } }\n")
	path := filepath.Join(a.Path, "config", "nginx.conf")
	_ = os.WriteFile(path, legacy, 0600)
	_ = os.Remove(filepath.Join(m.root, filepath.FromSlash(siteConfigName(site))))
	if err := m.change(func(s *State) error {
		for n := range s.Instances {
			if s.Instances[n].ID == a.ID {
				s.Instances[n].Port = 18200
			}
		}
		s.Sites[0].Port = 0
		s.Sites[0].Template = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.Close()
	again, err := New(m.root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	migrated := again.Snapshot().Sites[0]
	if migrated.Port != 18200 || migrated.Template != "php" || migrated.URL != "http://legacy.localhost:18200" {
		t.Fatalf("bad migration: %+v", migrated)
	}
	backup, _ := os.ReadFile(path + ".before-split")
	if !bytes.Equal(backup, legacy) {
		t.Fatal("legacy configuration not backed up")
	}
	if _, err := again.ReadConfig(a.ID, siteConfigName(site)); err != nil {
		t.Fatal(err)
	}
}

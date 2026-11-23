package panel

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstancePortChangesPreserveConfiguration(t *testing.T) {
	for _, component := range []string{"redis", "mysql", "php"} {
		t.Run(component, func(t *testing.T) {
			m := testManager(t)
			p, err := m.ImportArchive(component, "fixture", writeFixture(t, testZIP(t, map[string][]byte{executableName(component): fakePE()})), "")
			if err != nil {
				t.Fatal(err)
			}
			var i Instance
			if component == "php" {
				i = phpRuntimeFromPackage(t, m, p, 19111)
			} else {
				i, err = m.CreateInstance(p.ID, "port test", 19111)
				if err != nil {
					t.Fatal(err)
				}
			}
			filename := map[string]string{"redis": "redis.conf", "mysql": "my.ini", "php": "php.ini"}[component]
			dir := filepath.Join(i.Path, "config")
			if component == "php" {
				dir = i.Path
			}
			path := filepath.Join(dir, filename)
			body, _ := os.ReadFile(path)
			body = append(body, []byte("\n# personal preference\n")...)
			if err = os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			updated, err := m.UpdateInstancePort(i.ID, 19112)
			if err != nil {
				t.Fatal(err)
			}
			if updated.Port != 19112 {
				t.Fatal(updated)
			}
			after, _ := os.ReadFile(path)
			if !strings.Contains(string(after), "personal preference") {
				t.Fatal("custom configuration lost")
			}
			if component == "php" && string(after) != string(body) {
				t.Fatal("PHP ini rewritten unnecessarily")
			}
			if component != "php" && !strings.Contains(string(after), "19112") {
				t.Fatal(string(after))
			}
		})
	}
}
func TestPHPPortChangeUpdatesOnlySiteEndpoint(t *testing.T) {
	m, a, _, p, _ := siteFixture(t)
	site, err := m.CreateSite("port-site", "", a.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.root, "sites", site.ID+".conf")
	body, _ := os.ReadFile(path)
	body = append(body, []byte("\n# preserve custom site comment\n")...)
	if err = os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.UpdateInstancePort(p.ID, 19121); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	expected := strings.ReplaceAll(string(body), fmt.Sprintf("fastcgi_pass 127.0.0.1:%d;", p.Port), "fastcgi_pass 127.0.0.1:19121;")
	if string(after) != expected {
		t.Fatal("site overrides lost", string(after))
	}
	before := string(after)
	m.nginxValidator = func(Instance, InstalledPackage, string) error { return fmt.Errorf("bad candidate") }
	if _, err = m.UpdateInstancePort(p.ID, 19122); err == nil {
		t.Fatal("invalid candidate accepted")
	}
	after, _ = os.ReadFile(path)
	current, _, _ := m.lookupInstance(p.ID)
	if string(after) != before || current.Port != 19121 {
		t.Fatal("failed validation changed data")
	}
}
func TestPortChangeRejectsRunningAndCollisions(t *testing.T) {
	m, _, _, p, q := siteFixture(t)
	if _, err := m.UpdateInstancePort(p.ID, q.Port); err == nil {
		t.Fatal("instance port collision accepted")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if _, err = m.UpdateInstancePort(p.ID, listener.Addr().(*net.TCPAddr).Port); err == nil {
		t.Fatal("external listener collision accepted")
	}
	if err = m.setInstance(p.ID, "running", "", 42); err != nil {
		t.Fatal(err)
	}
	if _, err = m.UpdateInstancePort(p.ID, 19131); err == nil {
		t.Fatal("running edit accepted")
	}
	_ = m.setInstance(p.ID, "stopped", "", 0)
}

//go:build windows && integration

package panel

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeSiteEditDeleteHotReload(t *testing.T) {
	m := nativeManager(t)
	nginx := realPackage(t, m, "nginx", "1.28.3")
	php, err := m.ImportArchive("php", "fixture", writeFixture(t, testZIP(t, map[string][]byte{"php-cgi.exe": fakePE()})), "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.CreateInstance(nginx.ID, "first", 18481)
	if err != nil {
		t.Fatal(err)
	}
	older, err := m.ImportArchive("nginx", "1.26.3", filepath.Join(os.Getenv("PHPANEL_SMOKE_ARCHIVES_DIR"), "nginx-1.26.3.zip"), "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.CreateInstance(older.ID, "second", 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.CreateInstance(php.ID, "php", 19481)
	if err != nil {
		t.Fatal(err)
	}
	rootA := filepath.Join(m.root, "project-a")
	rootB := filepath.Join(m.root, "project-b")
	for root, body := range map[string]string{rootA: "project A", rootB: "project B"} {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	site, err := m.CreateSiteWithOptions("original", rootA, first.ID, p.ID, 18481, "php")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSiteWithOptions("keeper", rootA, second.ID, "", 18482, "static"); err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(second.ID); err == nil {
		t.Fatal("two different Nginx versions started together")
	}
	duplicate, err := m.CreateInstance(nginx.ID, "duplicate", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(duplicate.ID); err == nil {
		t.Fatal("same installed package started twice")
	}
	pidA, _, _ := m.lookupInstance(first.ID)
	updated, err := m.UpdateSiteWithOptions(site.ID, "edited", rootB, second.ID, p.ID, 18482, "php")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != site.ID {
		t.Fatal("site identity changed")
	}
	expectHTTP := func(url, host, expected string) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		client := &http.Client{Timeout: time.Second}
		for time.Now().Before(deadline) {
			req, _ := http.NewRequest("GET", url, nil)
			req.Host = host
			res, err := client.Do(req)
			if err == nil {
				body, _ := io.ReadAll(res.Body)
				res.Body.Close()
				if res.StatusCode == 200 && strings.Contains(string(body), expected) {
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("HTTP did not reach %q", expected)
	}
	expectHTTP("http://127.0.0.1:18482", "edited.localhost", "project B")
	nowA, _, _ := m.lookupInstance(first.ID)
	if nowA.PID != pidA.PID {
		t.Fatal("Nginx restarted instead of reloading")
	}
	if err := m.StopInstance(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(second.ID); err != nil {
		t.Fatal(err)
	}
	expectHTTP("http://127.0.0.1:18482", "edited.localhost", "project B")
	shared, _ := m.ReadConfig(second.ID, siteConfigName(site))
	if err := m.DeleteInstance(first.ID); err != nil {
		t.Fatal(err)
	}
	retained, _ := m.ReadConfig(second.ID, siteConfigName(site))
	if retained.Content != shared.Content {
		t.Fatal("deleting old Nginx changed shared site config")
	}
	if err := m.DeleteSite(site.ID); err != nil {
		t.Fatal(err)
	}
	expectHTTP("http://127.0.0.1:18482", "edited.localhost", "project A")
	if body, err := os.ReadFile(filepath.Join(rootB, "index.html")); err != nil || string(body) != "project B" {
		t.Fatal("project files deleted")
	}
	t.Log("edited, moved and deleted a site through live Nginx reload; original project files preserved")
}

func TestNativeConcurrentNginxStarts(t *testing.T) {
	m := nativeManager(t)
	p := realPackage(t, m, "nginx", "1.28.3")
	a, err := m.CreateInstance(p.ID, "one", 0)
	if err != nil {
		t.Fatal(err)
	}
	older, err := m.ImportArchive("nginx", "1.26.3", filepath.Join(os.Getenv("PHPANEL_SMOKE_ARCHIVES_DIR"), "nginx-1.26.3.zip"), "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.CreateInstance(older.ID, "two", 0)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, i := range []Instance{a, b} {
		wg.Add(1)
		go func(i Instance) { defer wg.Done(); errs <- m.StartInstance(i.ID) }(i)
	}
	wg.Wait()
	close(errs)
	wins := 0
	for err := range errs {
		if err == nil {
			wins++
		} else if !strings.Contains(err.Error(), "Nginx") {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("successful concurrent starts: %d", wins)
	}
}

func TestNativeTemplatesAndConfigEditor(t *testing.T) {
	m := nativeManager(t)
	nginx := realPackage(t, m, "nginx", "1.28.3")
	php := realPackage(t, m, "php", "8.3.36-1")
	n, err := m.CreateInstance(nginx.ID, "nginx", 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.CreateInstance(php.ID, "php", 19491)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(p.ID); err != nil {
		t.Fatal(err)
	}
	var sites []Site
	for index, template := range []string{"laravel", "thinkphp", "wordpress", "static"} {
		site, err := m.CreateSiteWithOptions(template, "", n.ID, p.ID, 18491+index, template)
		if err != nil {
			t.Fatal(err)
		}
		sites = append(sites, site)
		if template != "static" {
			if err := os.WriteFile(filepath.Join(site.Root, "index.php"), []byte("<?php header('Content-Type: text/plain'); echo '"+template+"|'.($_GET['s'] ?? $_SERVER['REQUEST_URI']);"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := m.StartInstance(n.ID); err != nil {
		t.Fatal(err)
	}
	expect := func(site Site, path, want string, status int) {
		t.Helper()
		client := &http.Client{Timeout: time.Second}
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", site.Port, path), nil)
			req.Host = site.Name + ".localhost"
			res, err := client.Do(req)
			if err == nil {
				body, _ := io.ReadAll(res.Body)
				res.Body.Close()
				if res.StatusCode == status && strings.Contains(string(body), want) {
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("%s %s did not return %d %q", site.Template, path, status, want)
	}
	for _, site := range sites {
		if site.Template == "static" {
			expect(site, "/", "PHPanel", 200)
			expect(site, "/source.php", "", 404)
		} else {
			expect(site, "/nested/path?x=1", site.Template+"|/nested/path", 200)
		}
	}
	site := sites[0]
	doc, err := m.ReadConfig(n.ID, siteConfigName(site))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveConfig(n.ID, doc.Name, doc.Content+"\ninvalid_directive;\n", doc.Revision); err == nil {
		t.Fatal("invalid native Nginx config saved")
	}
	current, _ := m.ReadConfig(n.ID, doc.Name)
	if current.Content != doc.Content {
		t.Fatal("invalid save changed file")
	}
	before, _, _ := m.lookupInstance(n.ID)
	custom := strings.TrimSuffix(doc.Content, "}\n") + "\n    location = /editor-probe {\n        default_type text/plain;\n        return 200 'editor applied';\n    }\n}\n"
	if _, err := m.SaveConfig(n.ID, doc.Name, custom, doc.Revision); err != nil {
		t.Fatal(err)
	}
	expect(site, "/editor-probe", "editor applied", 200)
	after, _, _ := m.lookupInstance(n.ID)
	if before.PID != after.PID {
		t.Fatal("editor restarted Nginx")
	}
	if err := m.StopInstance(n.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(n.ID); err != nil {
		t.Fatal(err)
	}
	expect(site, "/editor-probe", "editor applied", 200)
	t.Log("framework routing, static PHP protection, invalid-save rollback, editor hot reload and restart persistence verified")
}

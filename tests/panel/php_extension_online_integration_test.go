//go:build windows && integration

package panel

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func copiedPHPForOnlineTest(t *testing.T, m *Manager, version string) InstalledPackage {
	t.Helper()
	root := os.Getenv("PHPANEL_NATIVE_PACKAGES_DIR")
	if root == "" {
		t.Skip("set PHPANEL_NATIVE_PACKAGES_DIR")
	}
	source := filepath.Join(root, "package-php"+version)
	dest := filepath.Join(m.root, "packages", "package-php"+version)
	if err := os.MkdirAll(dest, 0700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if entry.Name() == "ext" {
				if err = os.CopyFS(filepath.Join(dest, "ext"), os.DirFS(filepath.Join(source, "ext"))); err != nil {
					t.Fatal(err)
				}
			}
			continue
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".dll") && !strings.EqualFold(filepath.Ext(entry.Name()), ".exe") {
			continue
		}
		input, err := os.Open(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out, err := os.Create(filepath.Join(dest, entry.Name()))
		if err != nil {
			input.Close()
			t.Fatal(err)
		}
		_, err = io.Copy(out, input)
		input.Close()
		out.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	p := InstalledPackage{ID: "php-online-" + version, Component: "php", Version: version, Path: dest, DirectoryName: filepath.Base(dest), Executable: filepath.Join(dest, "php-cgi.exe")}
	if err = m.change(func(s *State) error { s.Packages = append(s.Packages, p); return nil }); err != nil {
		t.Fatal(err)
	}
	return p
}
func waitExtensionTask(t *testing.T, m *Manager, key string) Task {
	t.Helper()
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		task, err := extensionTask(m.Snapshot(), key)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == "completed" || task.Status == "error" || task.Status == "awaiting_restart" {
			return task
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatal("extension task timeout")
	return Task{}
}
func TestNativePHPOnlineExtensions(t *testing.T) {
	fixtures := os.Getenv("PHPANEL_EXTENSION_FIXTURES_DIR")
	if fixtures == "" {
		t.Skip("set PHPANEL_EXTENSION_FIXTURES_DIR")
	}
	for index, version := range []string{"7.4.33", "8.3.36-1", "8.5.12-1"} {
		t.Run(version, func(t *testing.T) {
			m := nativeManager(t)
			p := copiedPHPForOnlineTest(t, m, version)
			i, err := m.CreateInstance(p.ID, "online extensions", 19870+index)
			if err != nil {
				t.Fatal(err)
			}
			if err = m.StartInstance(i.ID); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, a := range extensionSeed() {
					if r.URL.Path == "/"+a.ID {
						http.ServeFile(w, r, filepath.Join(fixtures, fmt.Sprintf("%s-%s-%s.%s", a.Name, a.PHPBranch, a.Version, a.Format)))
						return
					}
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			if err = m.change(func(s *State) error {
				for n := range s.ExtensionArtifacts {
					s.ExtensionArtifacts[n].Sources = []Source{{Name: "verified fixture", URL: server.URL + "/" + s.ExtensionArtifacts[n].ID}}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"redis", "apcu", "xdebug"} {
				report, err := m.GetPHPExtensions(i.ID)
				if err != nil {
					t.Fatal(err)
				}
				online, err := m.GetPHPOnlineExtensions(i.ID, false)
				if err != nil {
					t.Fatal(err)
				}
				var artifact PHPExtensionArtifact
				for _, a := range online.Items {
					if a.Name == name {
						artifact = a
						break
					}
				}
				if artifact.ID == "" {
					t.Fatalf("no compatible %s for %s", name, report.Version)
				}
				taskID, err := m.InstallPHPExtension(i.ID, artifact.ID, report.Revision, true)
				if err != nil {
					t.Fatal(err)
				}
				task := waitExtensionTask(t, m, taskID)
				if task.Status != "completed" || task.Warning != "" {
					t.Fatalf("%s: %+v", name, task)
				}
				report, err = m.GetPHPExtensions(i.ID)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, row := range report.Extensions {
					if row.Name == name {
						found = true
						if !row.Editable || !row.Configured || row.Loaded || row.State != "pending" {
							t.Fatalf("save implicitly applied %s: %+v", name, row)
						}
					}
				}
				if !found {
					t.Fatal("managed extension absent", name)
				}
				installed := m.Snapshot().ExtensionPackages
				for _, e := range installed {
					if e.Name == name {
						if e.RelativeDLL != "extensions/php_"+name+".dll" {
							t.Fatal("version folder created", e.RelativeDLL)
						}
					}
				}
			}
			report, err := m.GetPHPExtensions(i.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = m.RestartPHPWithExtensions(i.ID, report.Revision); err != nil {
				t.Fatal(err)
			}
			report, err = m.GetPHPExtensions(i.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !report.RuntimeKnown || report.Pending {
				t.Fatal("live PHP state", report.RuntimeError, report.Pending)
			}
			for _, name := range []string{"redis", "apcu", "xdebug"} {
				loaded := false
				for _, row := range report.Extensions {
					if row.Name == name && row.Loaded {
						loaded = true
					}
				}
				if !loaded {
					t.Fatal("not loaded after restart", name)
				}
			}
			if err = m.StopInstance(i.ID); err != nil {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(filepath.Join(p.Path, "extensions"))
			if len(entries) != 3 {
				t.Fatal("unexpected files/version directories", entries)
			}
			staged, _ := os.ReadDir(filepath.Join(m.root, "downloads", "staged"))
			if len(staged) != 0 {
				t.Fatal("staging directories retained", staged)
			}
			t.Logf("PHP %s: Redis/APCu/Xdebug HTTP download, checksum, flat directories, no implicit restart, and actual FastCGI verified", report.Version)
		})
	}
}

func TestNativePHPOnlineUpdateAndConflict(t *testing.T) {
	fixtures := os.Getenv("PHPANEL_EXTENSION_FIXTURES_DIR")
	if fixtures == "" {
		t.Skip("set PHPANEL_EXTENSION_FIXTURES_DIR")
	}
	m := nativeManager(t)
	p := copiedPHPForOnlineTest(t, m, "8.3.36-1")
	i, err := m.CreateInstance(p.ID, "update extensions", 19885)
	if err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(fixtures, "redis-8.3-6.2.0.zip")
	oldHash, err := fileHash(oldFile)
	if err != nil {
		t.Fatal(err)
	}
	var current PHPExtensionArtifact
	for _, a := range extensionSeed() {
		if a.Name == "redis" && a.PHPBranch == "8.3" {
			current = a
		}
	}
	if current.ID == "" {
		t.Fatal("missing Redis seed")
	}
	old := current
	old.Version = "6.2.0"
	old.SHA256 = oldHash
	old.ID = extensionArtifactID(old)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.ServeFile(w, r, oldFile)
		} else {
			http.ServeFile(w, r, filepath.Join(fixtures, "redis-8.3-"+current.Version+".zip"))
		}
	}))
	defer server.Close()
	old.Sources = []Source{{Name: "older verified build", URL: server.URL + "/old"}}
	current.Sources = []Source{{Name: "current verified build", URL: server.URL + "/new"}}
	if err = m.change(func(s *State) error {
		mergeExtensionArtifacts(s, []PHPExtensionArtifact{old, current})
		for n := range s.ExtensionArtifacts {
			if s.ExtensionArtifacts[n].ID == old.ID {
				s.ExtensionArtifacts[n].Sources = old.Sources
			}
			if s.ExtensionArtifacts[n].ID == current.ID {
				s.ExtensionArtifacts[n].Sources = current.Sources
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	report, err := m.GetPHPExtensions(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := m.InstallPHPExtension(i.ID, old.ID, report.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if task := waitExtensionTask(t, m, taskID); task.Status != "completed" || task.Warning != "" {
		t.Fatal(task)
	}
	if err = m.StartInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	report, err = m.GetPHPExtensions(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldRecord := m.Snapshot().ExtensionPackages[0]
	oldDLL, _ := installedExtensionDLL(p, oldRecord)
	beforeHash, _ := fileHash(oldDLL)
	taskID, err = m.InstallPHPExtension(i.ID, current.ID, report.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	task := waitExtensionTask(t, m, taskID)
	if task.Status != "awaiting_restart" || task.Error != "" {
		t.Fatal("update did not wait", task)
	}
	afterHash, _ := fileHash(oldDLL)
	if beforeHash != afterHash {
		t.Fatal("loaded DLL overwritten")
	}
	report, err = m.GetPHPExtensions(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Pending {
		t.Fatal("prepared update absent from report")
	}
	if err = m.RestartPHPWithExtensions(i.ID, report.Revision); err != nil {
		t.Fatal(err)
	}
	task, err = extensionTask(m.Snapshot(), taskID)
	if err != nil || task.Status != "completed" || task.Warning != "" {
		t.Fatal(task, err)
	}
	report, err = m.GetPHPExtensions(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded := false
	for _, row := range report.Extensions {
		if row.Name == "redis" && row.Loaded && row.Version == current.Version {
			loaded = true
		}
	}
	if !loaded {
		t.Fatal("new Redis not actually loaded")
	}
	entries, _ := os.ReadDir(filepath.Join(p.Path, "extensions"))
	if len(entries) != 1 || entries[0].Name() != "php_redis.dll" {
		t.Fatal("old version/backups retained", entries)
	}
	if err = m.StopInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	// A file downloaded while php.ini changes may install, but must not enable
	// using the stale revision or discard the user's newly written preference.
	var apcu PHPExtensionArtifact
	for _, a := range extensionSeed() {
		if a.Name == "apcu" && a.PHPBranch == "8.3" {
			apcu = a
		}
	}
	apcuServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := os.OpenFile(filepath.Join(i.Path, "config", "php.ini"), os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Error(err)
			return
		}
		_, err = f.WriteString("\n; preference changed during download\nmemory_limit=512M\n")
		f.Close()
		if err != nil {
			t.Error(err)
		}
		http.ServeFile(w, r, filepath.Join(fixtures, "apcu-8.3-"+apcu.Version+".zip"))
	}))
	defer apcuServer.Close()
	apcu.Sources = []Source{{Name: "verified APCu fixture", URL: apcuServer.URL + "/apcu"}}
	if err = m.change(func(s *State) error {
		for n := range s.ExtensionArtifacts {
			if s.ExtensionArtifacts[n].ID == apcu.ID {
				s.ExtensionArtifacts[n] = apcu
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	report, err = m.GetPHPExtensions(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err = m.InstallPHPExtension(i.ID, apcu.ID, report.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	task = waitExtensionTask(t, m, taskID)
	if task.Status != "completed" || !strings.Contains(task.Warning, "未启用") {
		t.Fatal("stale config overwritten", task)
	}
	report, err = m.GetPHPExtensions(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range report.Extensions {
		if row.Name == "apcu" && row.Configured {
			t.Fatal("conflicted APCu enabled")
		}
	}
	master, _ := os.ReadFile(filepath.Join(i.Path, "config", "php.ini"))
	if !strings.Contains(string(master), "memory_limit=512M") {
		t.Fatal("user preference discarded")
	}
	t.Log("loaded DLL retained until explicit restart; old directory removed; config revision conflict leaves installed-but-disabled extension")
}
func TestNativeLegacyExtensionFlatMigration(t *testing.T) {
	fixtures := os.Getenv("PHPANEL_EXTENSION_FIXTURES_DIR")
	if fixtures == "" {
		t.Skip("set PHPANEL_EXTENSION_FIXTURES_DIR")
	}
	m := nativeManager(t)
	p := copiedPHPForOnlineTest(t, m, "8.3.36-1")
	i, err := m.CreateInstance(p.ID, "legacy migration", 19931)
	if err != nil {
		t.Fatal(err)
	}
	var a PHPExtensionArtifact
	for _, item := range extensionSeed() {
		if item.Name == "redis" && item.PHPBranch == "8.3" {
			a = item
		}
	}
	report, err := m.GetPHPExtensions(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := m.ImportPHPExtensionArchive(i.ID, a.ID, report.Revision, filepath.Join(fixtures, "redis-8.3-"+a.Version+".zip"), true)
	if err != nil {
		t.Fatal(err)
	}
	task := waitExtensionTask(t, m, taskID)
	if task.Status != "completed" || task.Warning != "" {
		t.Fatal(task)
	}
	flat := filepath.Join(p.Path, "extensions", "php_redis.dll")
	legacy := filepath.Join(p.Path, "extensions", "redis", "php_redis.dll")
	if err = os.Mkdir(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(flat, legacy); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(i.Path, "config", "conf.d", phpManagedINI)
	body, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	body = rewriteExtensionDirectivePath(body, flat, legacy)
	if err = os.WriteFile(config, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err = m.change(func(s *State) error {
		for n := range s.ExtensionPackages {
			s.ExtensionPackages[n].RelativeDLL = "extensions/redis/php_redis.dll"
			s.ExtensionPackages[n].Files = nil
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	root := m.root
	m.Close()
	reopened, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	records := reopened.Snapshot().ExtensionPackages
	if len(records) != 1 || records[0].RelativeDLL != "extensions/php_redis.dll" {
		t.Fatal(records)
	}
	if _, err = os.Stat(filepath.Dir(legacy)); !os.IsNotExist(err) {
		t.Fatal("legacy directory retained")
	}
	if err = reopened.StartInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	report, err = reopened.GetPHPExtensions(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded := false
	for _, row := range report.Extensions {
		if row.Name == "redis" && row.Loaded && row.Editable && row.Version == a.Version {
			loaded = true
		}
	}
	if !loaded || report.Pending {
		t.Fatal("migrated Redis not loaded", report.RuntimeError)
	}
	if err = reopened.StopInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	t.Log("legacy installation/config migrated on startup; real Redis DLL loaded from extensions root")
}

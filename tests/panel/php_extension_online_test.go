package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPHPOnlineCatalogMatching(t *testing.T) {
	body := []byte(`<a href="php_redis-6.3.0-8.3-nts-vs16-x64.zip">a</a><a href="php_redis-6.3.0-8.3-ts-vs16-x64.zip">b</a><a href="php_redis-6.3.0-8.4-nts-vs17-x64.zip">c</a><a href="php_redis-6.3.0-8.3-nts-vs16-x64.zip">duplicate</a><a href="../php_redis-bad.zip">bad</a>`)
	items := peclExtensionArtifacts(body, "redis", "6.3.0")
	if len(items) != 3 {
		t.Fatal(items)
	}
	base := PHPProbe{Version: "8.3.36", Bits: 64, Build: "API20230831,NTS,VS16"}
	count := 0
	for _, a := range items {
		if extensionCompatible(a, base) {
			count++
		}
		if err := validateExtensionArtifact(a); err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatal("incompatible builds included", count)
	}
	base.Build = "API20230831,NTS,VS16,debug"
	for _, a := range items {
		if extensionCompatible(a, base) {
			t.Fatal("debug ABI matched")
		}
	}
	sha := strings.Repeat("a", 64)
	xdebug := xdebugExtensionArtifacts([]byte(`<a title="SHA256:&nbsp;` + sha + `" href='/files/php_xdebug-3.5.3-8.3-nts-vs16-x86_64.dll'>x</a><a title="SHA256:&nbsp;` + sha + `" href='/files/php_xdebug-3.6.0alpha1-8.3-nts-vs16-x86_64.dll'>alpha</a>`))
	if len(xdebug) != 1 || xdebug[0].SHA256 != sha || xdebug[0].Kind != "zend" {
		t.Fatal(xdebug)
	}
	for _, address := range []string{"https://downloads.php.net.evil/~windows/pecl/releases/redis/6.3.0/php_redis-6.3.0-8.3-nts-vs16-x64.zip", "http://xdebug.org/files/php_xdebug-3.5.3-8.3-nts-vs16-x86_64.dll", "https://xdebug.org/files/../../evil.dll"} {
		if trustedExtensionSource(address) {
			t.Fatal("untrusted source accepted", address)
		}
	}
}
func TestExtensionSnapshotsDoNotShareMutableData(t *testing.T) {
	a := PHPExtensionArtifact{Name: "redis", Sources: []Source{{Name: "original", URL: "https://example.test/a.zip"}}}
	state := State{ExtensionArtifacts: []PHPExtensionArtifact{a}, ExtensionCatalogUpdated: map[string]string{"build": "old"}, Tasks: []Task{{ExtensionArtifact: &a}}}
	copy := cloneState(state)
	copy.ExtensionArtifacts[0].Sources[0].Name = "changed"
	copy.Tasks[0].ExtensionArtifact.Sources[0].Name = "changed"
	copy.ExtensionCatalogUpdated["build"] = "new"
	if state.ExtensionArtifacts[0].Sources[0].Name != "original" || state.Tasks[0].ExtensionArtifact.Sources[0].Name != "original" || state.ExtensionCatalogUpdated["build"] != "old" {
		t.Fatal("mutable snapshot aliases manager")
	}
}
func publicationFixture(t *testing.T) (*Manager, InstalledPackage, PreparedPHPExtension) {
	t.Helper()
	m := testManager(t)
	p := InstalledPackage{ID: "php-fixture", Component: "php", Version: "8.3.36-1", Path: filepath.Join(m.root, "packages", "package-php8.3.36-1")}
	p.Executable = filepath.Join(p.Path, "php-cgi.exe")
	p.DirectoryName = filepath.Base(p.Path)
	if err := os.MkdirAll(filepath.Join(p.Path, "extensions", "redis"), 0700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(p.Path, "extensions", "redis", "php_redis.dll")
	if err := os.WriteFile(oldPath, []byte("old DLL fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	oldHash, _ := fileHash(oldPath)
	stage := filepath.Join(m.root, "downloads", "staged", "fixture")
	if err := os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "php_redis.dll"), []byte("new DLL fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := fileHash(filepath.Join(stage, "php_redis.dll"))
	item := PreparedPHPExtension{TaskID: "download-fixture", StageRelative: "downloads/staged/fixture", Record: InstalledPHPExtension{ID: "new", PackageID: p.ID, Name: "redis", Version: "6.3.0", RelativeDLL: "extensions/redis/php_redis.dll", DLLSHA256: hash}}
	if err := m.change(func(s *State) error {
		s.Packages = append(s.Packages, p)
		s.ExtensionPackages = []InstalledPHPExtension{{ID: "old", PackageID: p.ID, Name: "redis", Version: "6.2.0", RelativeDLL: item.Record.RelativeDLL, DLLSHA256: oldHash}}
		s.PreparedExtensions = []PreparedPHPExtension{item}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return m, p, item
}
func TestExtensionPublicationKeepsOneDirectory(t *testing.T) {
	m, p, item := flatPublicationFixture(t)
	if err := m.publishPreparedExtension(item, p); err != nil {
		t.Fatal(err)
	}
	path, err := installedExtensionDLL(p, item.Record)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(path)) != "extensions" {
		t.Fatal("version subdirectory created", path)
	}
	entries, _ := os.ReadDir(filepath.Join(p.Path, "extensions"))
	if len(entries) != 1 || entries[0].Name() != "php_redis.dll" {
		t.Fatal("old version or backup retained", entries)
	}
	state := m.Snapshot()
	if len(state.ExtensionPackages) != 1 || state.ExtensionPackages[0].ID != "new" || len(state.PreparedExtensions) != 0 {
		t.Fatal("publication metadata not replaced")
	}
}
func TestExtensionPublicationCrashRecovery(t *testing.T) {
	for _, phase := range []string{"before-rename", "after-old-rename", "after-new-rename"} {
		t.Run(phase, func(t *testing.T) {
			m, p, item := publicationFixture(t)
			j := extensionPublication{Prepared: item, BackupName: ".before-redis-download-fixture"}
			target, backup, journal, err := m.publicationPaths(j, p)
			if err != nil {
				t.Fatal(err)
			}
			stage, _ := m.preparedExtensionStage(item)
			body, _ := json.Marshal(j)
			if err = os.WriteFile(journal, body, 0600); err != nil {
				t.Fatal(err)
			}
			if phase != "before-rename" {
				if err = os.Rename(target, backup); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "after-new-rename" {
				if err = os.Rename(stage, target); err != nil {
					t.Fatal(err)
				}
			}
			if err = m.recoverExtensionPublications(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(target, "php_redis.dll"))
			if err != nil || string(data) != "old DLL fixture" {
				t.Fatal("old files not restored", err, string(data))
			}
			if _, err = m.preparedExtensionStage(item); err != nil {
				t.Fatal("new files lost", err)
			}
			if _, err = os.Stat(journal); !os.IsNotExist(err) {
				t.Fatal("recovery journal retained")
			}
		})
	}
}
func TestExtensionPublicationRejectsForeignDirectoryAndPaths(t *testing.T) {
	m, p, item := flatPublicationFixture(t)
	if err := m.change(func(s *State) error { s.ExtensionPackages = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.publishPreparedExtension(item, p); err == nil {
		t.Fatal("foreign folder overwritten")
	}
	item.StageRelative = "../../outside"
	if _, err := m.preparedExtensionStage(item); err == nil {
		t.Fatal("unsafe staged path accepted")
	}
}
func TestExtensionDownloadRejectsWrongHashBeforeInstall(t *testing.T) {
	m := testManager(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("wrong download bytes")) }))
	defer server.Close()
	a := PHPExtensionArtifact{ID: "fixture", Name: "redis", Version: "6.3.0", Format: "zip", SHA256: strings.Repeat("a", 64), Sources: []Source{{Name: "fixture", URL: server.URL + "/redis"}}}
	if err := m.change(func(s *State) error {
		s.Tasks = append(s.Tasks, Task{ID: "download-hash", Kind: "php-extension", ExtensionArtifact: &a})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err := m.runPHPExtensionDownload(context.Background(), "download-hash", a)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatal("wrong hash accepted", err)
	}
	if len(m.Snapshot().ExtensionPackages) != 0 {
		t.Fatal("failed file installed")
	}
}

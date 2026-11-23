package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestManagedUserPathPreservesUnrelatedEntries(t *testing.T) {
	dir := `D:\PHPanel\cli`
	original := `C:\Tools;%JAVA_HOME%\bin;"D:\PHPanel\cli";C:\User Tools`
	result := prependManagedUserPath(original, dir)
	if result != `D:\PHPanel\cli;C:\Tools;%JAVA_HOME%\bin;C:\User Tools` {
		t.Fatal(result)
	}
	if prependManagedUserPath(result, dir) != result {
		t.Fatal("duplicate PATH entry")
	}
}
func TestCLISelectionDoesNotModifySystemPath(t *testing.T) {
	m, _, _, p, q := siteFixture(t)
	pkg := m.Snapshot().Packages
	for _, item := range pkg {
		if item.Component == "php" {
			if err := os.WriteFile(filepath.Join(filepath.Dir(item.Executable), "php.exe"), fakePE(), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	helper := filepath.Join(t.TempDir(), "helper.exe")
	if err := os.WriteFile(helper, fakePE(), 0600); err != nil {
		t.Fatal(err)
	}
	m.cliHelperPath = helper
	user := `C:\Other Tools`
	system := `C:\Windows\System32`
	m.userPathReader = func() (string, string, error) { return user, system, nil }
	m.userPathWriter = func(value string) error { user = value; return nil }
	status, err := m.SelectCLIPHP(p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !status.UserPathEnabled || m.Snapshot().Tools.PHPInstanceID != p.ID {
		t.Fatal(status)
	}
	before := user
	if _, err = m.SelectCLIPHP(q.ID, true); err != nil {
		t.Fatal(err)
	}
	if user != before || system != `C:\Windows\System32` {
		t.Fatal("switch changed unrelated PATH")
	}
	invocation, err := ResolveCLIInvocation(m.root, "php", []string{"-r", "echo 123;"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(invocation.Args, []string{"-c", nativePath(filepath.Join(q.Path, "php.ini")), "-r", "echo 123;"}) {
		t.Fatal(invocation.Args)
	}
	env := strings.Join(invocation.Env, "\n")
	if !strings.Contains(env, "PHP_INI_SCAN_DIR=") {
		t.Fatal("scan dir missing")
	}
	if !strings.Contains(env, "PHPRC="+nativePath(filepath.Join(q.Path, "php.ini"))) {
		t.Fatal("inheritable PHPRC missing")
	}
}
func TestComposerDirectoryChoosesSupportedStable(t *testing.T) {
	body := []byte(`{"stable":[{"version":"1.0.0","path":"/download/1.0.0/composer.phar","eol":true},{"version":"2.2.32","path":"/download/2.2.32/composer.phar","lts":true},{"version":"2.11.2","path":"/download/2.11.2/composer.phar","min-php":70205}]}`)
	a, err := composerArtifactFromBody(body)
	if err != nil || a.Version != "2.11.2" || a.MinPHP != 70205 {
		t.Fatal(a, err)
	}
	if phpVersionNumber("8.3.36") != 80336 {
		t.Fatal("PHP version comparison")
	}
}
func TestCLIComposerUsesChosenPHPAndChecksFileHash(t *testing.T) {
	m, _, _, p, _ := siteFixture(t)
	for _, pkg := range m.Snapshot().Packages {
		if pkg.Component == "php" {
			_ = os.WriteFile(filepath.Join(filepath.Dir(pkg.Executable), "php.exe"), fakePE(), 0600)
		}
	}
	dir := filepath.Join(m.root, "tools", "composer")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "composer.phar")
	if err := os.WriteFile(path, []byte("phar fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := fileHash(path)
	state := m.Snapshot()
	state.Tools = ToolchainSettings{PHPInstanceID: p.ID, ComposerVersion: "2.11.2", ComposerSHA256: hash}
	body, _ := json.Marshal(state)
	if err := os.WriteFile(m.statePath(), body, 0600); err != nil {
		t.Fatal(err)
	}
	invocation, err := ResolveCLIInvocation(m.root, "composer", []string{"install"})
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Args[2] != path || invocation.Args[3] != "install" {
		t.Fatal(invocation.Args)
	}
	_ = os.WriteFile(path, []byte("changed"), 0600)
	if _, err = ResolveCLIInvocation(m.root, "composer", nil); err == nil {
		t.Fatal("modified phar executed")
	}
}

func cliPathFixture(t *testing.T) (*Manager, Instance, *string) {
	t.Helper()
	m, _, _, p, _ := siteFixture(t)
	for _, pkg := range m.Snapshot().Packages {
		if pkg.Component == "php" {
			if err := os.WriteFile(filepath.Join(filepath.Dir(pkg.Executable), "php.exe"), fakePE(), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	helper := filepath.Join(t.TempDir(), "helper.exe")
	if err := os.WriteFile(helper, fakePE(), 0600); err != nil {
		t.Fatal(err)
	}
	m.cliHelperPath = helper
	user := `C:\Other Tools;%JAVA_HOME%\bin`
	m.userPathReader = func() (string, string, error) { return user, `C:\Windows\System32`, nil }
	m.userPathWriter = func(value string) error { user = value; return nil }
	if _, err := m.SelectCLIPHP(p.ID, true); err != nil {
		t.Fatal(err)
	}
	return m, p, &user
}
func installCLIComposerFixture(t *testing.T, m *Manager) {
	t.Helper()
	dir := filepath.Join(m.root, "tools", "composer")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "composer.phar")
	if err := os.WriteFile(path, []byte("composer fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := fileHash(path)
	if err := m.change(func(s *State) error { s.Tools.ComposerVersion = "2.11.2"; s.Tools.ComposerSHA256 = hash; return nil }); err != nil {
		t.Fatal(err)
	}
}
func TestRemoveManagedUserPathKeepsOtherEntries(t *testing.T) {
	dir := `D:\PHPanel\cli`
	input := `C:\Tools;;"D:\PHPanel\cli";%JAVA_HOME%\bin;d:/PHPanel/cli/;C:\Tools\php;`
	if result := removeManagedUserPath(input, dir); result != `C:\Tools;;%JAVA_HOME%\bin;C:\Tools\php;` {
		t.Fatal(result)
	}
	if removeManagedUserPath(`C:\Tools`, dir) != `C:\Tools` {
		t.Fatal("unrelated PATH changed")
	}
}
func TestDisablePHPPathAndReenable(t *testing.T) {
	m, p, user := cliPathFixture(t)
	status, err := m.DisableCLIPHPPath()
	if err != nil {
		t.Fatal(err)
	}
	if status.PHPPathEnabled || status.UserPathEnabled || *user != `C:\Other Tools;%JAVA_HOME%\bin` {
		t.Fatal(status, *user)
	}
	if m.Snapshot().Tools.PHPInstanceID != p.ID || !m.Snapshot().Tools.PHPPathDisabled {
		t.Fatal("selected PHP for Composer was lost")
	}
	if _, err := os.Stat(filepath.Join(m.root, "cli", "php.exe")); !os.IsNotExist(err) {
		t.Fatal("PHP entry remained", err)
	}
	if _, err := m.DisableCLIPHPPath(); err != nil {
		t.Fatal("repeat cancel failed", err)
	}
	status, err = m.SelectCLIPHP(p.ID, true)
	if err != nil || !status.PHPPathEnabled || m.Snapshot().Tools.PHPPathDisabled {
		t.Fatal("PHP environment could not be restored", status, err)
	}
}
func TestDisablePHPPathKeepsComposerAndEnablingComposerDoesNotRestorePHP(t *testing.T) {
	m, p, user := cliPathFixture(t)
	installCLIComposerFixture(t, m)
	before := *user
	status, err := m.DisableCLIPHPPath()
	if err != nil {
		t.Fatal(err)
	}
	if status.PHPPathEnabled || !status.ComposerPathEnabled || !status.UserPathEnabled || *user != before {
		t.Fatal(status, *user)
	}
	invocation, err := ResolveCLIInvocation(m.root, "composer", []string{"--version"})
	if err != nil {
		t.Fatal("Composer lost its PHP configuration", err)
	}
	if invocation.Args[1] != nativePath(filepath.Join(p.Path, "php.ini")) {
		t.Fatal(invocation.Args)
	}
	status, err = m.EnableComposerPath()
	if err != nil || status.PHPPathEnabled || !status.ComposerPathEnabled {
		t.Fatal("Composer restored cancelled PHP", status, err)
	}
	if _, err := os.Stat(filepath.Join(m.root, "cli", "php.exe")); !os.IsNotExist(err) {
		t.Fatal("PHP entry was recreated")
	}
}
func TestDisablePHPPathFailureRestoresPathAndEntry(t *testing.T) {
	for _, mode := range []string{"path-write", "metadata-save"} {
		t.Run(mode, func(t *testing.T) {
			m, _, user := cliPathFixture(t)
			before := *user
			entry := filepath.Join(m.root, "cli", "php.exe")
			hash, _ := fileHash(entry)
			if mode == "path-write" {
				m.userPathWriter = func(string) error { return fmt.Errorf("registry failed") }
			} else {
				if err := os.Mkdir(m.statePath()+".tmp", 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.DisableCLIPHPPath(); err == nil {
				t.Fatal("failure ignored")
			}
			after, _ := fileHash(entry)
			if after != hash || *user != before || m.Snapshot().Tools.PHPPathDisabled {
				t.Fatal("cancel was partially committed", *user)
			}
			files, _ := os.ReadDir(filepath.Join(m.root, "cli"))
			for _, file := range files {
				if strings.HasPrefix(file.Name(), ".php-disable-") {
					t.Fatal("rollback left temporary entry")
				}
			}
		})
	}
}
func TestDisablePHPPathPreservesForeignEntry(t *testing.T) {
	m, _, user := cliPathFixture(t)
	before := *user
	entry := filepath.Join(m.root, "cli", "php.exe")
	body := []byte("another application's entry")
	if err := os.WriteFile(entry, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DisableCLIPHPPath(); err == nil {
		t.Fatal("foreign entry removed")
	}
	after, _ := os.ReadFile(entry)
	if string(after) != string(body) || *user != before {
		t.Fatal("foreign entry or PATH changed")
	}
}

func TestFailedPHPEnvironmentRestoreDoesNotRecreateGlobalPHP(t *testing.T) {
	m, p, _ := cliPathFixture(t)
	installCLIComposerFixture(t, m)
	if _, err := m.DisableCLIPHPPath(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.statePath()+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SelectCLIPHP(p.ID, true); err == nil {
		t.Fatal("metadata failure ignored")
	}
	if cliEntryExists(filepath.Join(m.root, "cli"), "php") {
		t.Fatal("failed restore left a global PHP entry")
	}
}

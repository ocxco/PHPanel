package release

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReleasePATHRemovalPreservesUnrelatedAndQuotedEntries(t *testing.T) {
	dir := `D:\PHPanel\cli`
	value := `C:\Tools;;"D:\PHPanel\cli";%JAVA_HOME%\bin;d:/phpanel/cli/;D:\Other\cli;`
	if result := RemoveManagedPath(value, dir); result != `C:\Tools;;%JAVA_HOME%\bin;D:\Other\cli;` {
		t.Fatal(result)
	}
}
func TestReleaseGuardReadsServicesAndProtectsInvalidMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state", "panel.json")
	if err := os.WriteFile(path, []byte(`{"instances":[{"serviceName":"PHPanel-redis-fixture"},{"serviceName":""}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	services, err := registeredServices(root)
	if err != nil || len(services) != 1 || services[0] != "PHPanel-redis-fixture" {
		t.Fatal(services, err)
	}
	_ = os.WriteFile(path, []byte("invalid json"), 0600)
	if _, err := registeredServices(root); err == nil {
		t.Fatal("corrupt metadata permitted uninstall")
	}
	if err := Check(root, "invalid-mode"); err == nil {
		t.Fatal("invalid mode accepted")
	}
}
func TestReleaseGuardDoesNotModifyEnvironmentData(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"packages", "instances", "sites"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "keep"), []byte("user data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Check(root, "update"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"packages", "instances", "sites"} {
		body, err := os.ReadFile(filepath.Join(root, name, "keep"))
		if err != nil || string(body) != "user data" {
			t.Fatal("guard modified user files", name, err)
		}
	}
}

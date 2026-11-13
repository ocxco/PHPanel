//go:build windows && integration

package panel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Packages are read-only; every test instance and configuration is isolated.
func TestNativePHPExtensions(t *testing.T) {
	root := os.Getenv("PHPANEL_NATIVE_PACKAGES_DIR")
	if root == "" {
		t.Skip("set PHPANEL_NATIVE_PACKAGES_DIR to a read-only installed packages directory")
	}
	for index, version := range []string{"7.4.33", "8.3.36-1", "8.5.12-1"} {
		t.Run(version, func(t *testing.T) {
			m := nativeManager(t)
			path := filepath.Join(root, "package-php"+version)
			if _, err := os.Stat(filepath.Join(path, "php.exe")); err != nil {
				t.Fatal(err)
			}
			p := InstalledPackage{ID: "php-test-" + version, Component: "php", Version: version, Path: path, Executable: filepath.Join(path, "php-cgi.exe")}
			if err := m.change(func(s *State) error { s.Packages = append(s.Packages, p); return nil }); err != nil {
				t.Fatal(err)
			}
			i, err := m.CreateInstance(p.ID, "extensions", 19670+index)
			if err != nil {
				t.Fatal(err)
			}
			master := filepath.Join(i.Path, "config", "php.ini")
			f, err := os.OpenFile(master, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString("\n; unrelated custom preference\nmemory_limit=384M\n")
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			report, err := m.GetPHPExtensions(i.ID)
			if err != nil {
				t.Fatal(err)
			}
			find := func(r PHPExtensionReport, name string) PHPExtension {
				for _, row := range r.Extensions {
					if row.Name == name {
						return row
					}
				}
				t.Fatalf("missing %s", name)
				return PHPExtension{}
			}
			gd := find(report, "gd")
			if version == "7.4.33" && gd.Filename != "php_gd2.dll" {
				t.Fatal(gd)
			}
			opcache := find(report, "zend opcache")
			if strings.HasPrefix(version, "8.5") && (opcache.Kind != "builtin" || opcache.Editable) {
				t.Fatal("builtin OPcache", opcache)
			}
			if !strings.HasPrefix(version, "8.5") && (opcache.Kind != "zend" || !opcache.Editable) {
				t.Fatal("Zend OPcache", opcache)
			}
			if report.Running {
				t.Fatal("test unexpectedly running")
			}
			if err = m.StartInstance(i.ID); err != nil {
				t.Fatal(err)
			}
			report, err = m.GetPHPExtensions(i.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !report.RuntimeKnown {
				t.Fatal("FastCGI probe", report.RuntimeError)
			}
			if !find(report, "mysqli").Loaded || find(report, "curl").Loaded {
				t.Fatal("initial runtime inventory")
			}
			choices := []PHPSelection{{"curl", true}, {"gd", true}, {"intl", true}}
			if opcache.Editable {
				choices = append(choices, PHPSelection{"zend opcache", true})
			}
			saved, err := m.SavePHPExtensions(i.ID, PHPSettingsRequest{Revision: report.Revision, Extensions: choices, Settings: map[string]string{"opcache.enable": "1", "opcache.revalidate_freq": "0"}})
			if err != nil {
				t.Fatal(err)
			}
			curl := find(saved, "curl")
			if !curl.Configured || curl.Loaded || curl.State != "pending" || !saved.Pending {
				t.Fatal("save implicitly applied", curl, saved.Pending)
			}
			main, err := os.ReadFile(master)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(main, []byte("memory_limit=384M")) || !bytes.Contains(main, []byte("; unrelated custom preference")) {
				t.Fatal("custom config lost")
			}
			if _, err = m.SavePHPExtensions(i.ID, PHPSettingsRequest{Revision: report.Revision}); err == nil {
				t.Fatal("stale revision accepted")
			}
			if err = m.RestartPHPWithExtensions(i.ID, saved.Revision); err != nil {
				t.Fatal(err)
			}
			applied, err := m.GetPHPExtensions(i.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"curl", "gd", "intl", "zend opcache"} {
				if !find(applied, name).Loaded {
					t.Fatal("not loaded", name, applied.RuntimeError)
				}
			}
			if applied.Pending || applied.Settings["opcache.revalidate_freq"] != "0" {
				t.Fatal("configuration still pending", applied)
			}
			// Missing native dependency must reject the candidate without touching either file.
			badReport := applied
			iniBefore, _ := readPHPINI(i)
			if _, err = m.SavePHPExtensions(i.ID, PHPSettingsRequest{Revision: badReport.Revision, Extensions: []PHPSelection{{"oci8", true}}}); err == nil {
				t.Fatal("OCI without Oracle client accepted")
			}
			iniAfter, _ := readPHPINI(i)
			if iniBefore.revision != iniAfter.revision {
				t.Fatal("failed validation changed config")
			}
			disabled, err := m.SavePHPExtensions(i.ID, PHPSettingsRequest{Revision: applied.Revision, Extensions: []PHPSelection{{"curl", false}}})
			if err != nil {
				t.Fatal(err)
			}
			if !find(disabled, "curl").Loaded || find(disabled, "curl").Configured {
				t.Fatal("disable silently applied")
			}
			if err = m.RestartPHPWithExtensions(i.ID, disabled.Revision); err != nil {
				t.Fatal(err)
			}
			done, err := m.GetPHPExtensions(i.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !done.RuntimeKnown || done.Pending || find(done, "curl").Loaded {
				t.Fatal("disable not applied", done)
			}
			if err = m.StopInstance(i.ID); err != nil {
				t.Fatal(err)
			}
			files, err := os.ReadDir(filepath.Join(i.Path, "temp"))
			if err != nil || len(files) != 0 {
				t.Fatal("private diagnostic/staging not cleaned", files, err)
			}
			t.Logf("PHP %s: build, GD alias, OPcache, validation rollback, stale revision, live FastCGI and explicit restart verified", report.Version)
		})
	}
}

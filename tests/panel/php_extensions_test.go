package panel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPHPDirectiveParsing(t *testing.T) {
	body := []byte("\ufeff[PHP]\r\n;extension=disabled\r\nextension = \"php_gd2.dll\" ; image\r\nzend_extension='C:\\ext\\php_opcache.dll'\r\nopcache.enable=0 ; off\r\n[PATH=/private]\r\nextension=curl\r\n[opcache]\r\nopcache.enable=1\r\n")
	items := phpDirectives(body)
	if len(items) != 5 || items[0].value != "php_gd2.dll" || items[1].value != "C:\\ext\\php_opcache.dll" || items[2].value != "0" || !items[3].scoped || items[4].scoped {
		t.Fatalf("%+v", items)
	}
	for raw, want := range map[string]string{"php_gd2.dll": "gd", "C:\\ext\\php_opcache.dll": "zend opcache", "mysqli": "mysqli", "php_oci8_19.dll": "oci8"} {
		if got := phpModuleName(raw); got != want {
			t.Fatalf("%s = %s", raw, got)
		}
	}
}
func TestPHPCandidatePreservesUnrelatedConfiguration(t *testing.T) {
	set := phpINISet{main: []byte("; user comment\r\n[PHP]\r\nmemory_limit=512M\r\nextension=mysqli ; database\r\nextension=php_mysqli.dll\r\nextension=C:\\manual\\custom.dll\r\n[opcache]\r\nopcache.enable=0\r\n"), scan: map[string][]byte{phpManagedINI: []byte("; keep me\nextension=php_curl.dll\nxdebug.mode=debug\n")}}
	rows := []PHPExtension{{Name: "mysqli", Filename: "php_mysqli.dll", Editable: true, Configured: true}, {Name: "curl", Filename: "php_curl.dll", Editable: true, Configured: true}}
	main, managed, err := phpCandidate(set, rows, PHPSettingsRequest{Extensions: []PHPSelection{{"mysqli", true}, {"curl", false}}, Settings: map[string]string{"opcache.enable": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"; user comment\r\n", "memory_limit=512M\r\n", "extension=C:\\manual\\custom.dll\r\n"} {
		if !strings.Contains(string(main), keep) {
			t.Fatal("lost unrelated content", string(main))
		}
	}
	if !strings.Contains(string(managed), "; keep me") || !strings.Contains(string(managed), "xdebug.mode=debug") || !strings.Contains(string(managed), "opcache.enable=1") || strings.Count(string(managed), "extension=\"php_mysqli.dll\"") != 1 || strings.Contains(string(managed), "extension=php_curl.dll") {
		t.Fatal(string(managed))
	}
	for _, d := range phpDirectives(main) {
		if d.key == "extension" && phpModuleName(d.value) == "mysqli" || d.key == "opcache.enable" {
			t.Fatal("active managed declaration left in master", d)
		}
	}
}
func TestPHPSelectionRestrictions(t *testing.T) {
	rows := []PHPExtension{{Name: "core", Kind: "builtin", Configured: true}, {Name: "mbstring", Filename: "php_mbstring.dll", Editable: true}, {Name: "exif", Filename: "php_exif.dll", Editable: true, Dependencies: []string{"mbstring"}}}
	set := phpINISet{main: []byte("[PHP]\n"), scan: map[string][]byte{}}
	for _, req := range []PHPSettingsRequest{
		{Extensions: []PHPSelection{{"core", false}}},
		{Extensions: []PHPSelection{{"exif", true}}},
		{Extensions: []PHPSelection{{"missing", true}}},
		{Settings: map[string]string{"extension_dir": "C:/bad"}},
		{Settings: map[string]string{"opcache.enable": "1\nextension=bad"}},
		{Settings: map[string]string{"opcache.memory_consumption": "999999"}},
	} {
		if _, _, err := phpCandidate(set, rows, req); err == nil {
			t.Fatalf("accepted invalid request: %+v", req)
		}
	}
	_, managed, err := phpCandidate(set, rows, PHPSettingsRequest{Extensions: []PHPSelection{{"exif", true}, {"mbstring", true}}})
	if err != nil || bytes.Index(managed, []byte("php_mbstring")) > bytes.Index(managed, []byte("php_exif")) {
		t.Fatal("dependency order", string(managed), err)
	}
	set.scan["99-user.ini"] = []byte("opcache.enable=0\n")
	if _, _, err := phpCandidate(set, rows, PHPSettingsRequest{Settings: map[string]string{"opcache.enable": "1"}}); err == nil {
		t.Fatal("manual setting overwritten")
	}
}
func TestPHPINIRevisionAndEditorFiles(t *testing.T) {
	root := t.TempDir()
	i := Instance{Path: root, Component: "php"}
	main := filepath.Join(root, "php.ini")
	body := "; user preamble outside the managed block\n" + phpManagedBegin + "\n[PHP]\nextension=\"php_curl.dll\"\n" + phpManagedEnd + "\n"
	if err := os.WriteFile(main, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := readPHPINI(i)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte(strings.Replace(body, "php_curl.dll", "php_mbstring.dll", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := readPHPINI(i)
	if err != nil {
		t.Fatal(err)
	}
	if a.revision == b.revision {
		t.Fatal("managed block change omitted from revision")
	}
	names := configNames(State{}, i)
	if len(names) != 1 || names[0] != "php.ini" {
		t.Fatal(names)
	}
}

func TestPHPSettingsBooleanAliasesAndScopedPreservation(t *testing.T) {
	for input, want := range map[string]string{"On": "1", "true": "1", "Off": "0", "No": "0", "1": "1"} {
		if got := canonicalPHPSetting("opcache.enable", input); got != want {
			t.Fatal(input, got)
		}
	}
	set := phpINISet{main: []byte("[PHP]\n"), scan: map[string][]byte{phpManagedINI: []byte("[PATH=/private]\nopcache.enable=0\n; preserve scoped preference\n")}}
	rows := []PHPExtension{{Name: "curl", Editable: true, Filename: "php_curl.dll"}}
	if _, _, err := phpCandidate(set, rows, PHPSettingsRequest{Settings: map[string]string{"opcache.enable": "1"}}); err == nil {
		t.Fatal("scoped OPcache preference overwritten")
	}
	_, managed, err := phpCandidate(set, rows, PHPSettingsRequest{Extensions: []PHPSelection{{"curl", true}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range phpDirectives(managed) {
		if d.key == "extension" && d.scoped {
			t.Fatal("generated extension inherits manual scope")
		}
	}
}

func TestPHPInventoryKeepsManualDeclarationsReadOnly(t *testing.T) {
	root := t.TempDir()
	ext := filepath.Join(root, "ext")
	if err := os.Mkdir(ext, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"php_curl.dll", "php_gd2.dll", "php_opcache.dll", "php_dl_test.dll"} {
		if err := os.WriteFile(filepath.Join(ext, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	base := PHPProbe{Modules: map[string]string{"core": "8.3"}}
	set := phpINISet{main: []byte("extension=php_gd2.dll\nextension=C:\\custom\\php_curl.dll\n"), scan: map[string][]byte{"99-user.ini": []byte("zend_extension=php_opcache.dll\nextension=missing.dll\n")}}
	rows, err := phpInventory(InstalledPackage{Executable: filepath.Join(root, "php-cgi.exe")}, base, set)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		switch row.Name {
		case "gd":
			if !row.Editable || !row.Configured {
				t.Fatal(row)
			}
		case "curl", "zend opcache", "missing":
			if row.Editable || !row.Configured {
				t.Fatal(row)
			}
		case "core":
			if row.Kind != "builtin" || row.Editable {
				t.Fatal(row)
			}
		case "dl_test":
			t.Fatal("developer test DLL shown")
		}
	}
}

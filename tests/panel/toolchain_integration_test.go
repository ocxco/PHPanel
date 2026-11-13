//go:build windows && integration

package panel

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeComposerInstallAndCLISelection(t *testing.T) {
	fixture := os.Getenv("PHPANEL_COMPOSER_FIXTURE")
	helper := os.Getenv("PHPANEL_CLI_HELPER")
	if fixture == "" || helper == "" {
		t.Skip("set PHPANEL_COMPOSER_FIXTURE and PHPANEL_CLI_HELPER")
	}
	m := nativeManager(t)
	p := copiedPHPForOnlineTest(t, m, "8.3.36-1")
	i, err := m.CreateInstance(p.ID, "Composer PHP", 19983)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := fileHash(fixture)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, fixture) }))
	defer server.Close()
	m.composerFetcher = func(context.Context, *http.Client) (ComposerArtifact, error) {
		return ComposerArtifact{Version: "2.11.2", URL: server.URL + "/composer.phar", SHA256: hash, MinPHP: 70205}, nil
	}
	taskID, err := m.InstallComposer(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := waitExtensionTask(t, m, taskID)
	if task.Status != "completed" {
		t.Fatal(task)
	}
	m.cliHelperPath = helper
	userPath := "C:\\Other Tools"
	m.userPathReader = func() (string, string, error) { return userPath, "C:\\Windows\\System32", nil }
	m.userPathWriter = func(value string) error { userPath = value; return nil }
	if _, err = m.EnableComposerPath(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(m.root, "cli", "composer.exe"), "--version", "--no-ansi")
	hideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Composer version 2.11.2") {
		t.Fatal(err, string(output))
	}
	second, err := m.CreateInstance(p.ID, "second CLI config", 19984)
	if err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(second.Path, "config", "php.ini")
	body, _ := os.ReadFile(ini)
	body = append(body, []byte("\nmemory_limit=384M\n")...)
	if err = os.WriteFile(ini, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.SelectCLIPHP(second.ID, true); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(filepath.Join(m.root, "cli", "php.exe"), "-r", `echo ini_get('memory_limit');`)
	hideWindow(cmd)
	output, err = cmd.CombinedOutput()
	if err != nil || string(output) != "384M" {
		t.Fatal("CLI not switched", err, string(output))
	}
	status, err := m.DisableCLIPHPPath()
	if err != nil || status.PHPPathEnabled || !status.ComposerPathEnabled {
		t.Fatal("PHP cancel affected Composer", status, err)
	}
	cmd = exec.Command(filepath.Join(m.root, "cli", "composer.exe"), "--version", "--no-ansi")
	hideWindow(cmd)
	output, err = cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Composer version 2.11.2") {
		t.Fatal("Composer failed after PHP environment cancellation", err, string(output))
	}
	if _, err = m.SelectCLIPHP(second.ID, true); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(filepath.Join(m.root, "cli", "php.exe"), "-r", `echo ini_get('memory_limit');`)
	hideWindow(cmd)
	output, err = cmd.CombinedOutput()
	if err != nil || string(output) != "384M" {
		t.Fatal("PHP environment restore failed", err, string(output))
	}
	t.Log("Composer PHAR checksum/install verified; console entries use selected instance configuration; real user PATH was not changed")
}
func TestNativePHPPortChangeWithNginx(t *testing.T) {
	m := nativeManager(t)
	p := copiedPHPForOnlineTest(t, m, "8.3.36-1")
	nginx := realPackage(t, m, "nginx", "1.28.3")
	i, err := m.CreateInstance(p.ID, "port test", 19973)
	if err != nil {
		t.Fatal(err)
	}
	n, err := m.CreateInstance(nginx.ID, "nginx port test", 0)
	if err != nil {
		t.Fatal(err)
	}
	site, err := m.CreateSiteWithOptions("port-change", "", n.ID, i.ID, 18973, "php")
	if err != nil {
		log, _ := m.ReadLog(n.ID)
		t.Fatal(err, log)
	}
	if _, err = m.UpdateInstancePort(i.ID, 19974); err != nil {
		log, _ := m.ReadLog(n.ID)
		t.Fatal(err, log)
	}
	if err = m.StartInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	if err = m.StartInstance(n.ID); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest("GET", "http://127.0.0.1:18973", nil)
	request.Host = site.Name + ".localhost"
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(fmt.Sprint(response.StatusCode))
	}
	if err = m.StopInstance(n.ID); err != nil {
		t.Fatal(err)
	}
	if err = m.StopInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	t.Log("PHP edited while stopped; Nginx site reaches the new FastCGI port")
}
func TestNativeDatabasePortChanges(t *testing.T) {
	for index, entry := range []struct{ name, version string }{{"redis", "8.2.10"}, {"mysql", "8.4.6"}} {
		t.Run(entry.name, func(t *testing.T) {
			m := nativeManager(t)
			p := realPackage(t, m, entry.name, entry.version)
			i, err := m.CreateInstance(p.ID, "database port", 19861+index)
			if err != nil {
				t.Fatal(err)
			}
			updated, err := m.UpdateInstancePort(i.ID, 19871+index)
			if err != nil {
				t.Fatal(err)
			}
			if err = m.StartInstance(i.ID); err != nil {
				t.Fatal(err)
			}
			connection, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", updated.Port))
			if err != nil {
				t.Fatal(err)
			}
			connection.Close()
			if _, err = m.UpdateInstancePort(i.ID, 19881+index); err == nil {
				t.Fatal("running instance port changed")
			}
			if err = m.StopInstance(i.ID); err != nil {
				t.Fatal(err)
			}
			t.Log("service actually bound edited port; running edits rejected")
		})
	}
}

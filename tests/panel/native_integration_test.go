//go:build windows && integration

package panel

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNativeRepeatedPHPInstall(t *testing.T) {
	for attempt := 0; attempt < 3; attempt++ {
		t.Run(fmt.Sprint(attempt+1), func(t *testing.T) {
			m := nativeManager(t)
			p := realPackage(t, m, "php", "8.3.36-1")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, p.Executable, "-n", "-v")
			hideWindow(cmd)
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "PHP 8.3.36") {
				t.Fatalf("installed PHP: %v\n%s", err, output)
			}
			if len(m.Snapshot().Packages) != 1 {
				t.Fatal("install metadata missing")
			}
			t.Log("PHP 8.3.36-1 extracted, published and executed")
		})
	}
}
func TestNativeInstanceDeletion(t *testing.T) {
	m := nativeManager(t)
	p := realPackage(t, m, "php", "8.3.36-1")
	i, err := m.CreateInstance(p.ID, "delete-test", 19583)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteInstance(i.ID); err == nil {
		t.Fatal("running PHP deleted")
	}
	if err := m.StopInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(i.Path); !os.IsNotExist(err) {
		t.Fatal("real instance files remained")
	}
	replacement, err := m.CreateInstance(p.ID, "replacement", 19583)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(replacement.ID); err != nil {
		t.Fatal("retained program or port unusable:", err)
	}
	if err := m.StopInstance(replacement.ID); err != nil {
		t.Fatal(err)
	}
	t.Log("running deletion rejected; stopped instance deleted; retained PHP package recreated and restarted")
}

func realPackage(t *testing.T, m *Manager, component, version string) InstalledPackage {
	t.Helper()
	root := os.Getenv("PHPANEL_SMOKE_ARCHIVES_DIR")
	if root == "" {
		t.Skip("set PHPANEL_SMOKE_ARCHIVES_DIR to run real-binary verification")
	}
	path := filepath.Join(root, component+"-"+version+".zip")
	var hash string
	for _, a := range m.Snapshot().Artifacts {
		if a.Component == component && a.Version == version {
			hash = a.SHA256
			break
		}
	}
	if hash == "" {
		t.Fatalf("no trusted digest for %s %s", component, version)
	}
	p, err := m.ImportArchive(component, version, path, hash)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func nativeManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(filepath.Join(t.TempDir(), "work space"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}
func TestNativeNginxPHP(t *testing.T) {
	m := nativeManager(t)
	nginx := realPackage(t, m, "nginx", "1.28.3")
	state := m.Snapshot()
	var phpVersions []string
	for _, a := range state.Artifacts {
		if a.Component == "php" && (strings.HasPrefix(a.Version, "8.3.") || strings.HasPrefix(a.Version, "8.4.")) {
			phpVersions = append(phpVersions, a.Version)
		}
	}
	if len(phpVersions) != 2 {
		t.Fatalf("need two PHP branches: %v", phpVersions)
	}
	n, err := m.CreateInstance(nginx.ID, "nginx", 18380)
	if err != nil {
		t.Fatal(err)
	}
	for index, version := range phpVersions {
		php := realPackage(t, m, "php", version)
		p, err := m.CreateInstance(php.ID, "php", 19300+index)
		if err != nil {
			t.Fatal(err)
		}
		site, err := m.CreateSiteWithOptions("site-"+strings.ReplaceAll(version, ".", "-"), "", n.ID, p.ID, 18380, "php")
		if err != nil {
			t.Fatal(err)
		}
		if err := m.StartInstance(p.ID); err != nil {
			log, _ := m.ReadLog(p.ID)
			t.Fatalf("PHP: %v\n%s", err, log)
		}
		if err := m.StartInstance(n.ID); err != nil {
			log, _ := m.ReadLog(n.ID)
			t.Fatalf("Nginx: %v\n%s", err, log)
		}
		req, _ := http.NewRequest("GET", "http://127.0.0.1:18380", nil)
		req.Host = site.Name + ".localhost"
		client := &http.Client{Timeout: 5 * time.Second}
		res, err := client.Do(req)
		if err != nil {
			log, _ := m.ReadLog(n.ID)
			main, _ := m.ReadConfig(n.ID, "nginx.conf")
			vhost, _ := m.ReadConfig(n.ID, siteConfigName(site))
			t.Fatalf("%v\n%s\n%s\n%s", err, log, main.Content, vhost.Content)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !strings.Contains(string(body), "PHP "+strings.Split(version, "-")[0]) {
			t.Fatalf("HTTP %d: %s", res.StatusCode, body)
		}
		t.Logf("Verified HTTP 200 through Nginx + PHP %s", version)
		if err := m.StopInstance(n.ID); err != nil {
			t.Fatal(err)
		}
		if err := m.StopInstance(p.ID); err != nil {
			t.Fatal(err)
		}
	}
}
func TestNativeRedis(t *testing.T) {
	m := nativeManager(t)
	p := realPackage(t, m, "redis", "8.2.10")
	i, err := m.CreateInstance(p.ID, "redis", 16379)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(i.ID); err != nil {
		log, _ := m.ReadLog(i.ID)
		t.Fatalf("Redis: %v\n%s", err, log)
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:16379", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = conn.Write([]byte("*1\r\n$4\r\nPING\r\n"))
	b := make([]byte, 32)
	n, err := conn.Read(b)
	conn.Close()
	if err != nil || string(b[:n]) != "+PONG\r\n" {
		t.Fatalf("Redis PING: %q %v", b[:n], err)
	}
	if err := m.StopInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	t.Log("Verified community Redis PING/PONG and shutdown")
}
func TestNativeMySQL(t *testing.T) {
	verifyNativeMySQL(t, "8.4.6", 13306)
}
func TestNativeMySQLBranches(t *testing.T) {
	for index, version := range []string{"5.7.44", "8.0.44", "9.3.0"} {
		t.Run(version, func(t *testing.T) { verifyNativeMySQL(t, version, 14306+index) })
	}
}
func verifyNativeMySQL(t *testing.T, version string, port int) {
	t.Helper()
	m := nativeManager(t)
	p := realPackage(t, m, "mysql", version)
	i, err := m.CreateInstance(p.ID, "mysql", port)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(i.ID); err != nil {
		log, _ := m.ReadLog(i.ID)
		t.Fatalf("MySQL: %v\n%s", err, log)
	}
	credentials, err := m.Credentials(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(credentials, "\n")
	password := strings.TrimPrefix(lines[1], "密码: ")
	cfg := filepath.Join(i.Path, "config", "test-client.ini")
	if err := os.WriteFile(cfg, []byte("[client]\nuser=root\npassword="+password+"\nhost=127.0.0.1\nport="+strconv.Itoa(port)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(cfg)
	query := func(sql string) string {
		cmd := exec.Command(filepath.Join(filepath.Dir(p.Executable), "mysql.exe"), "--defaults-extra-file="+nativePath(cfg), "--batch", "--skip-column-names", "-e", sql)
		hideWindow(cmd)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("MySQL query failed: %v %s", err, output)
		}
		return string(output)
	}
	if output := query("SELECT VERSION(); CREATE DATABASE phpanel_verify; CREATE TABLE phpanel_verify.records (value INT); INSERT INTO phpanel_verify.records VALUES (42);"); !strings.Contains(output, version) {
		t.Fatalf("wrong MySQL version: %s", output)
	}
	if err := m.StopInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	if result := strings.TrimSpace(query("SELECT value FROM phpanel_verify.records;")); result != "42" {
		t.Fatalf("data lost after restart: %q", result)
	}
	if err := m.StopInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	t.Log("Verified MySQL authenticated query and persisted data after restart")
}

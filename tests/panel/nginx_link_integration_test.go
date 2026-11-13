//go:build windows && integration

package panel

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func linkedNativePackages(t *testing.T, m *Manager) (InstalledPackage, InstalledPackage, InstalledPackage) {
	t.Helper()
	path := os.Getenv("PHPANEL_LINK_FIXTURES")
	if path == "" {
		t.Skip("set PHPANEL_LINK_FIXTURES")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []struct{ Component, Version, Archive, SHA256 string }
	if err := json.Unmarshal(body, &records); err != nil {
		t.Fatal(err)
	}
	var packages []InstalledPackage
	for _, record := range records {
		p, err := m.ImportArchive(record.Component, record.Version, record.Archive, record.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		packages = append(packages, p)
	}
	if len(packages) != 3 || packages[0].Component != "nginx" || packages[1].Component != "php" || packages[2].Component != "php" {
		t.Fatal("need nginx and two PHP versions")
	}
	return packages[0], packages[1], packages[2]
}
func configureNativePHPBackend(t *testing.T, m *Manager) {
	t.Helper()
	helper := os.Getenv("PHPANEL_LINK_HELPER")
	if helper == "" {
		t.Skip("set PHPANEL_LINK_HELPER")
	}
	m.phpRuntime = &windowsPHPRuntime{root: m.root, helper: helper}
	m.detachedPHP = true
}
func assertLinkedHTTP(t *testing.T, site Site) {
	t.Helper()
	request, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d", site.Port), nil)
	request.Host = site.Name + ".localhost"
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 {
		t.Fatalf("HTTP %d: %s", response.StatusCode, body)
	}
}
func TestNativeNginxPHPLinkAndReuse(t *testing.T) {
	m := nativeManager(t)
	configureNativePHPBackend(t, m)
	npkg, first, second := linkedNativePackages(t, m)
	n, err := m.CreateInstance(npkg.ID, "linked nginx", 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.CreateInstance(first.ID, "prestarted", 19901)
	if err != nil {
		t.Fatal(err)
	}
	q, err := m.CreateInstance(second.ID, "automatic", 19902)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []Instance{p, q} {
		instance := i
		t.Cleanup(func() { _ = m.phpBackend().Stop(instance, "") })
	}
	a, err := m.CreateSiteWithOptions("linked-a", "", n.ID, p.ID, 19980, "php")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.CreateSiteWithOptions("linked-b", "", n.ID, q.ID, 19980, "php")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(p.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := m.phpBackend().Query(p)
	if err := m.StartInstance(n.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := m.phpBackend().Query(p)
	if before.PID != after.PID {
		t.Fatal("reused PHP restarted")
	}
	assertLinkedHTTP(t, a)
	assertLinkedHTTP(t, b)
	report, err := m.GetPHPExtensions(q.ID)
	if err != nil || !report.Running || !report.RuntimeKnown {
		t.Fatal("remote extension report", err, report.RuntimeError)
	}
	saved, err := m.SavePHPExtensions(q.ID, PHPSettingsRequest{Revision: report.Revision, Extensions: []PHPSelection{{Name: "curl", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RestartPHPWithExtensions(q.ID, saved.Revision); err != nil {
		t.Fatal(err)
	}
	assertLinkedHTTP(t, b)
	report, err = m.GetPHPExtensions(q.ID)
	if err != nil || !report.RuntimeKnown || report.Pending {
		t.Fatal("remote PHP restart/extension state", err, report.RuntimeError)
	}
	if err := m.StopInstance(n.ID); err != nil {
		t.Fatal(err)
	}
	status, _ := m.phpBackend().Query(q)
	if !status.Exists || status.Status != "running" {
		t.Fatal("default stop killed PHP")
	}
	if err := m.StopNginxWithPHP(n.ID); err != nil {
		t.Fatal(err)
	}
	status, _ = m.phpBackend().Query(q)
	if status.Exists {
		t.Fatal("combined stop left automatic PHP")
	}
	status, _ = m.phpBackend().Query(p)
	if !status.Exists || status.PID != before.PID {
		t.Fatal("combined stop killed reused PHP")
	}
	m.Close()
	status, _ = m.phpBackend().Query(p)
	if status.Exists {
		t.Fatal("GUI exit left unprotected manual PHP")
	}
	t.Log("two PHP versions served HTTP 200; existing PID reused; remote extensions restarted; combined stop retained prestarted PHP")
}
func TestNativeNginxServicePHPLinkSurvivesGUIAndNginxStop(t *testing.T) {
	m := nativeManager(t)
	configureNativePHPBackend(t, m)
	f := fakeServices()
	m.systemServices = f
	npkg, first, second := linkedNativePackages(t, m)
	n, err := m.CreateInstance(npkg.ID, "nginx service", 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.CreateInstance(first.ID, "manual reused", 19911)
	if err != nil {
		t.Fatal(err)
	}
	q, err := m.CreateInstance(second.ID, "service automatic", 19912)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []Instance{p, q} {
		instance := i
		t.Cleanup(func() { _ = m.phpBackend().Stop(instance, "") })
	}
	a, err := m.CreateSiteWithOptions("service-a", "", n.ID, p.ID, 19981, "php")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.CreateSiteWithOptions("service-b", "", n.ID, q.ID, 19981, "php")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(p.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := m.phpBackend().Query(p)
	if err := m.RegisterSystemService(n.ID, true); err != nil {
		t.Fatal(err)
	}
	n, _, _ = m.lookupInstance(n.ID)
	host, instance, err := loadServiceHost(m.root, n.ID, n.ServiceName)
	if err != nil {
		t.Fatal(err)
	}
	configureNativePHPBackend(t, host)
	requests := make(chan svc.ChangeRequest, 4)
	changes := make(chan svc.Status, 256)
	done := make(chan uint32, 1)
	go func() {
		_, code := (&instanceServiceHost{manager: host, instance: instance}).Execute(nil, requests, changes)
		done <- code
	}()
	t.Cleanup(func() { requests <- svc.ChangeRequest{Cmd: svc.Stop}; host.Close() })
	deadline := time.After(45 * time.Second)
	running := false
	for !running {
		select {
		case status := <-changes:
			running = status.State == svc.Running
		case code := <-done:
			log, _ := host.ReadLog(n.ID)
			t.Fatalf("host exited %d: %s", code, log)
		case <-deadline:
			t.Fatal("host startup timeout")
		}
	}
	assertLinkedHTTP(t, a)
	assertLinkedHTTP(t, b)
	m.Close()
	assertLinkedHTTP(t, a)
	assertLinkedHTTP(t, b)
	status, _ := m.phpBackend().Query(p)
	if status.PID != before.PID || !status.Protected {
		t.Fatal("service did not protect reused PHP", status)
	}
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatal(code)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("stop timeout")
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:19981", time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("Nginx port remained open")
	}
	status, _ = m.phpBackend().Query(q)
	if !status.Exists {
		t.Fatal("default service stop killed automatic PHP")
	}
	if err := host.stopLinkedPHP(n.ID); err != nil {
		t.Fatal(err)
	}
	status, _ = m.phpBackend().Query(q)
	if status.Exists {
		t.Fatal("linked PHP not stopped")
	}
	status, _ = m.phpBackend().Query(p)
	if !status.Exists || status.PID != before.PID {
		t.Fatal("reused PHP was stopped")
	}
	t.Log("SCM handler launched dependencies; reused PID survived GUI exit; default Nginx stop retained PHP; combined stop stopped only owned PHP")
}

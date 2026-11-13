//go:build windows && integration

package panel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

// Exercise the exact SCM handler with real child binaries and isolated data,
// while avoiding persistent service registrations or administrator prompts.
func TestNativeSystemServiceHostLifecycle(t *testing.T) {
	fixturePath := os.Getenv("PHPANEL_SERVICE_FIXTURES")
	if fixturePath == "" {
		t.Skip("set PHPANEL_SERVICE_FIXTURES to a verified archive manifest")
	}
	body, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct{ Component, Version, Archive, SHA256 string }
	if err := json.Unmarshal(body, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 3 {
		t.Fatal("need Nginx, Redis and MySQL fixtures")
	}
	for index, item := range fixtures {
		t.Run(item.Component, func(t *testing.T) {
			m := nativeManager(t)
			f := fakeServices()
			m.systemServices = f
			p, err := m.ImportArchive(item.Component, item.Version, item.Archive, item.SHA256)
			if err != nil {
				t.Fatal(err)
			}
			port := 19780 + index
			i, err := m.CreateInstance(p.ID, "service-host-test", port)
			if err != nil {
				t.Fatal(err)
			}
			if item.Component == "nginx" {
				if _, err := m.CreateSiteWithOptions("service-test", "", i.ID, "", port, "static"); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.RegisterSystemService(i.ID, true); err != nil {
				t.Fatal(err)
			}
			i, _, _ = m.lookupInstance(i.ID)
			original, _ := os.ReadFile(m.statePath())
			host, instance, err := loadServiceHost(m.root, i.ID, i.ServiceName)
			if err != nil {
				t.Fatal(err)
			}
			requests := make(chan svc.ChangeRequest, 4)
			changes := make(chan svc.Status, 256)
			done := make(chan uint32, 1)
			go func() {
				_, code := (&instanceServiceHost{manager: host, instance: instance}).Execute(nil, requests, changes)
				done <- code
			}()
			t.Cleanup(func() { requests <- svc.ChangeRequest{Cmd: svc.Stop}; host.Close() })
			deadline := time.After(150 * time.Second)
			running := false
			for !running {
				select {
				case status := <-changes:
					running = status.State == svc.Running
				case code := <-done:
					log, _ := host.ReadLog(i.ID)
					t.Fatalf("host exited %d: %s", code, log)
				case <-deadline:
					t.Fatal("host did not report Running")
				}
			}
			m.Close()
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
			if err != nil {
				t.Fatal("GUI exit stopped system service child", err)
			}
			conn.Close()
			if lease, err := acquireRuntimeLease(m.root, instance); err == nil {
				lease.release()
				t.Fatal("GUI could acquire service-owned instance lock")
			}
			requests <- svc.ChangeRequest{Cmd: svc.Stop}
			select {
			case code := <-done:
				if code != 0 {
					t.Fatalf("stop returned %d", code)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("graceful stop timeout")
			}
			after, _ := os.ReadFile(m.statePath())
			if !bytes.Equal(original, after) {
				t.Fatal("host overwrote GUI metadata")
			}
			conn, err = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
			if err == nil {
				conn.Close()
				t.Fatal("port still open after system service stop")
			}
			t.Log("SCM handler reached Running; child survived GUI exit; Stop closed process tree; GUI metadata unchanged")
		})
	}
}

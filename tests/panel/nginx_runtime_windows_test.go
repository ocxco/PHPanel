//go:build windows

package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

// Exercise the real pipe without registering a persistent Windows service.
type pipeSystemServices struct {
	*fakeSystemServices
	root string
}

func (b *pipeSystemServices) ReloadNginx(i Instance) error {
	return requestNginxReload(b.root, i)
}

func TestNginxRuntimeReloadPipe(t *testing.T) {
	m, i, _, _, _ := siteFixture(t)
	m.serviceHost = true
	proc := &managedProcess{}
	m.processes[i.ID] = proc
	defer delete(m.processes, i.ID)
	server, err := serveNginxRuntime(m, i, proc)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	reloads := 0
	m.nginxReloader = func(current Instance, pkg InstalledPackage) error {
		if current.ID != i.ID || pkg.ID != i.PackageID {
			t.Error("reload targeted a different instance")
		}
		reloads++
		return nil
	}
	if err := requestNginxReload(m.root, i); err != nil || reloads != 1 {
		t.Fatal("host did not execute reload", err, reloads)
	}
	m.nginxReloader = func(Instance, InstalledPackage) error { return fmt.Errorf("fixture reload failed") }
	if err := requestNginxReload(m.root, i); err == nil || !strings.Contains(err.Error(), "fixture reload failed") {
		t.Fatal("host failure did not reach client", err)
	}
	m.mu.Lock()
	m.operations[i.ID] = true
	m.mu.Unlock()
	if err := requestNginxReload(m.root, i); err == nil || !strings.Contains(err.Error(), "正在变更") {
		t.Fatal("changing instance accepted reload", err)
	}
	m.mu.Lock()
	delete(m.operations, i.ID)
	delete(m.processes, i.ID)
	m.mu.Unlock()
	if err := requestNginxReload(m.root, i); err == nil || !strings.Contains(err.Error(), "已退出") {
		t.Fatal("exited instance accepted reload", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, nginxPipeName(m.root, i.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if err := json.NewEncoder(conn).Encode(nginxRuntimeRequest{Method: "stop"}); err != nil {
		t.Fatal(err)
	}
	var response nginxRuntimeResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil || !strings.Contains(response.Error, "不支持") {
		t.Fatal("unsupported pipe operation was accepted", response, err)
	}
}

func TestNginxRuntimeMissingHostRequiresServiceRestart(t *testing.T) {
	if err := requestNginxReload(t.TempDir(), Instance{ID: "missing"}); err == nil || !strings.Contains(err.Error(), "phpanel-service.exe") || !strings.Contains(err.Error(), "重新启动") {
		t.Fatal("missing host did not explain upgrade/restart", err)
	}
}

func TestNginxPipeNameScopedToRootAndInstance(t *testing.T) {
	root := filepath.Join(t.TempDir(), "panel")
	name := nginxPipeName(root, "one")
	if name != nginxPipeName(strings.ToUpper(root), "one") || name == nginxPipeName(root, "two") || name == nginxPipeName(root+"-other", "one") || name == phpPipeName(root, "one") {
		t.Fatal("pipe name was unstable or collided with another host")
	}
}

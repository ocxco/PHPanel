//go:build windows

package panel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

type nginxRuntimeRequest struct {
	Method string `json:"method"`
}

type nginxRuntimeResponse struct {
	Error string `json:"error,omitempty"`
}

func nginxPipeName(root, id string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(root)) + "\x00" + id))
	return fmt.Sprintf(`\\.\pipe\PHPanel-nginx-%x`, sum[:16])
}

func (b *windowsServices) ReloadNginx(i Instance) error {
	// Verify the SCM registration before contacting the instance's host.
	s, err := b.open(i, 0)
	if err != nil {
		return err
	}
	s.Close()
	return requestNginxReload(b.root, i)
}

func requestNginxReload(root string, i Instance) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, nginxPipeName(root, i.ID))
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return fmt.Errorf("Nginx 服务未提供重载通道，请更新 phpanel-service.exe 并停止、重新启动 Nginx 系统服务后重试")
	}
	if err != nil {
		return fmt.Errorf("无法连接 Nginx 服务重载通道: %w", serviceAccessError(err))
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := json.NewEncoder(conn).Encode(nginxRuntimeRequest{Method: "reload"}); err != nil {
		return err
	}
	var response nginxRuntimeResponse
	if err := json.NewDecoder(io.LimitReader(conn, 64*1024)).Decode(&response); err != nil {
		return fmt.Errorf("读取 Nginx 服务重载结果失败: %w", err)
	}
	if response.Error != "" {
		return fmt.Errorf("%s", response.Error)
	}
	return nil
}

func serveNginxRuntime(m *Manager, i Instance, proc *managedProcess) (runtimeServer, error) {
	dacl, err := runtimePipeSecurity(m)
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(nginxPipeName(m.root, i.ID), &winio.PipeConfig{SecurityDescriptor: dacl, InputBufferSize: 4096, OutputBufferSize: 4096})
	if err != nil {
		return nil, fmt.Errorf("无法建立 Nginx 本地重载通道: %w", err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleNginxRuntime(m, i, proc, conn)
		}
	}()
	return listener, nil
}

func handleNginxRuntime(m *Manager, i Instance, proc *managedProcess, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	var request nginxRuntimeRequest
	if err := json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&request); err != nil {
		return
	}
	var err error
	if request.Method != "reload" {
		err = fmt.Errorf("不支持的 Nginx 管理操作")
	} else {
		err = m.reloadOwnedNginx(i, proc)
	}
	response := nginxRuntimeResponse{}
	if err != nil {
		response.Error = err.Error()
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func (m *Manager) reloadOwnedNginx(i Instance, proc *managedProcess) error {
	m.mu.Lock()
	if m.closing || proc == nil || m.processes[i.ID] != proc || m.operations[i.ID] {
		m.mu.Unlock()
		return fmt.Errorf("Nginx 已退出或正在变更，请稍后重试")
	}
	m.operations[i.ID] = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.operations, i.ID)
		m.mu.Unlock()
		m.wg.Done()
	}()
	current, pkg, err := m.lookupInstance(i.ID)
	if err != nil {
		return err
	}
	// Execute under the same account as the master. Paths always come from the
	// host's own metadata; the client cannot supply commands or config paths.
	return m.reloadSiteConfig(siteConfigChange{instance: current, pkg: pkg, path: filepath.Join(current.Path, "config", "nginx.conf")})
}

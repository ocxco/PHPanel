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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

type windowsPHPRuntime struct{ root, helper string }

func currentRuntimeUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}
func validateRuntimeUserSID(sid string) error { _, err := windows.StringToSid(sid); return err }

type phpRuntimeResponse struct {
	Status phpRuntimeStatus `json:"status"`
	Probe  PHPProbe         `json:"probe"`
	Error  string           `json:"error,omitempty"`
}
type phpRuntimeRequest struct {
	Method string `json:"method"`
	Run    string `json:"run,omitempty"`
}

func newPHPRuntimeBackend(root string) phpRuntimeBackend {
	helper, _ := serviceHelperPath()
	return &windowsPHPRuntime{root: root, helper: helper}
}
func phpPipeName(root, id string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(root)) + "\x00" + id))
	return fmt.Sprintf(`\\.\pipe\PHPanel-php-%x`, sum[:16])
}
func (b *windowsPHPRuntime) request(i Instance, method, run string, timeout time.Duration) (phpRuntimeResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, phpPipeName(b.root, i.ID))
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return phpRuntimeResponse{}, nil
	}
	if err != nil {
		return phpRuntimeResponse{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := json.NewEncoder(conn).Encode(phpRuntimeRequest{Method: method, Run: run}); err != nil {
		return phpRuntimeResponse{}, err
	}
	var response phpRuntimeResponse
	if err := json.NewDecoder(io.LimitReader(conn, 1024*1024)).Decode(&response); err != nil {
		return response, err
	}
	if response.Error != "" {
		return response, fmt.Errorf("%s", response.Error)
	}
	return response, nil
}
func (b *windowsPHPRuntime) Query(i Instance) (phpRuntimeStatus, error) {
	response, err := b.request(i, "query", "", 500*time.Millisecond)
	return response.Status, err
}
func (b *windowsPHPRuntime) Stop(i Instance, run string) error {
	method := "stop"
	if run == "close-unprotected" {
		method = "stop-unprotected"
		run = ""
	}
	_, err := b.request(i, method, run, 15*time.Second)
	if err != nil {
		// The host can exit as its stop reply is being flushed. Absence of the
		// owned pipe confirms completion without treating that EOF as failure.
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			status, queryErr := b.Query(i)
			if queryErr == nil && !status.Exists {
				return nil
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	return err
}
func (b *windowsPHPRuntime) Diagnostic(i Instance) (PHPProbe, error) {
	response, err := b.request(i, "diagnostic", "", 10*time.Second)
	if err == nil && !response.Status.Exists {
		err = fmt.Errorf("后台 PHP 已退出，请刷新")
	}
	return response.Probe, err
}
func (b *windowsPHPRuntime) Protect(i Instance, enabled bool) error {
	method := "unprotect"
	if enabled {
		method = "protect"
	}
	response, err := b.request(i, method, "", time.Second)
	if err == nil && !response.Status.Exists {
		return fmt.Errorf("PHP 已退出，无法复用")
	}
	return err
}
func (b *windowsPHPRuntime) Start(i Instance, owner, run string) (phpRuntimeStatus, error) {
	current, err := b.Query(i)
	if err != nil {
		return current, err
	}
	if current.Exists && current.Status == "running" {
		return current, nil
	}
	var exited <-chan error
	if !current.Exists {
		if _, err := os.Stat(b.helper); err != nil {
			return current, fmt.Errorf("缺少 phpanel-service.exe，无法后台托管 PHP: %w", err)
		}
		logfile, err := os.OpenFile(filepath.Join(i.Path, "logs", "php-host.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return current, err
		}
		cmd := exec.Command(b.helper, "--php-host", "--root", b.root, "--instance", i.ID, "--owner", owner, "--run", run)
		hideWindow(cmd)
		cmd.Stdout, cmd.Stderr = logfile, logfile
		err = cmd.Start()
		logfile.Close()
		if err != nil {
			return current, err
		}
		done := make(chan error, 1)
		exited = done
		go func() { done <- cmd.Wait() }()
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		current, err = b.Query(i)
		if err == nil && current.Exists && current.Status == "running" {
			return current, nil
		}
		if current.Exists && current.Status == "error" {
			return current, fmt.Errorf("PHP %s 启动失败: %s", i.Name, current.Error)
		}
		select {
		case exitErr := <-exited:
			// A concurrent manual start can have won the instance lease.
			current, queryErr := b.Query(i)
			if queryErr == nil && current.Exists && current.Status == "running" {
				return current, nil
			}
			return current, fmt.Errorf("PHP 后台托管退出: %v；请查看 php-host.log 和 console.log", exitErr)
		case <-time.After(100 * time.Millisecond):
		}
	}
	// Do not leave a late starting child after a failed dependency transaction.
	_ = b.Stop(i, run)
	return current, fmt.Errorf("PHP %s 未在 30 秒内启动，请查看实例日志", i.Name)
}

func servePHPRuntime(m *Manager, i Instance, proc *managedProcess) (runtimeServer, error) {
	security, err := windows.GetNamedSecurityInfo(m.statePath(), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	owner, _, err := security.Owner()
	if err != nil {
		return nil, err
	}
	userSID := owner.String()
	if m.state.RuntimeUserSID != "" {
		sid, err := windows.StringToSid(m.state.RuntimeUserSID)
		if err != nil {
			return nil, err
		}
		userSID = sid.String()
	}
	dacl := "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + userSID + ")"
	listener, err := winio.ListenPipe(phpPipeName(m.root, i.ID), &winio.PipeConfig{SecurityDescriptor: dacl, InputBufferSize: 4096, OutputBufferSize: 4096})
	if err != nil {
		return nil, fmt.Errorf("无法建立 PHP 本地管理通道: %w", err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handlePHPRuntime(m, i, proc, conn)
		}
	}()
	return listener, nil
}
func handlePHPRuntime(m *Manager, i Instance, proc *managedProcess, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	var request phpRuntimeRequest
	if err := json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&request); err != nil {
		return
	}
	current, _, err := m.lookupInstance(i.ID)
	response := phpRuntimeResponse{Status: phpRuntimeStatus{Exists: true, Status: current.Status, PID: current.PID, Error: current.Error, Owner: current.LinkedNginxID, Run: current.LinkedRun}}
	m.mu.Lock()
	response.Status.Persistent = m.serviceHost
	response.Status.Protected = m.phpHostProtected
	m.mu.Unlock()
	if err == nil {
		switch request.Method {
		case "query":
		case "protect", "unprotect":
			if !m.serviceHost {
				err = fmt.Errorf("此 PHP 仍由旧面板进程管理，请先停止 PHP，再由新版面板重新启动")
			} else {
				m.mu.Lock()
				if m.phpHostStopping {
					err = fmt.Errorf("PHP 正在退出，无法复用")
				} else {
					m.phpHostProtected = request.Method == "protect"
				}
				m.mu.Unlock()
			}
		case "diagnostic":
			response.Probe, err = m.probeRunningPHP(current, proc)
		case "stop", "stop-unprotected":
			if request.Run != "" && request.Run != current.LinkedRun {
				err = fmt.Errorf("此 PHP 不属于本次 Nginx 联动，已保留")
			} else {
				m.mu.Lock()
				keep := request.Method == "stop-unprotected" && m.phpHostProtected
				if !keep {
					m.phpHostStopping = true
				}
				m.mu.Unlock()
				if !keep {
					err = m.stopInstance(i.ID, true)
				}
			}
		default:
			err = fmt.Errorf("不支持的 PHP 管理操作")
		}
	}
	if err != nil {
		response.Error = err.Error()
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func RunLinkedPHPHost(root, key, owner, run string) error {
	m, err := loadRuntimeState(root)
	if err != nil {
		return err
	}
	i, p, err := m.lookupInstance(key)
	if err != nil {
		return err
	}
	if i.Component != "php" || (owner != "" && run == "") || !within(filepath.Join(m.root, "instances"), i.Path) || !within(filepath.Join(m.root, "packages"), p.Executable) {
		return fmt.Errorf("后台 PHP 实例或程序路径无效")
	}
	if owner != "" {
		n, _, err := m.lookupInstance(owner)
		if err != nil || n.Component != "nginx" {
			return fmt.Errorf("关联 Nginx 实例不存在")
		}
		m.phpHostProtected = n.ServiceName != ""
	}
	m.phpHostOwner, m.phpHostRun = owner, run
	if err := m.startInstance(key, false); err != nil {
		return err
	}
	m.mu.Lock()
	proc := m.processes[key]
	m.mu.Unlock()
	if proc != nil {
		<-proc.done
	}
	m.Close()
	return nil
}

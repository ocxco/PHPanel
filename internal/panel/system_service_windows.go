//go:build windows

package panel

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type windowsServices struct{ root, helper string }

func newSystemServiceBackend(root string) systemServiceBackend {
	helper, _ := serviceHelperPath()
	return &windowsServices{root: root, helper: helper}
}
func (b *windowsServices) args(i Instance) []string {
	return []string{"--root", b.root, "--instance", i.ID, "--name", systemServiceName(b.root, i)}
}
func (b *windowsServices) command(i Instance) string {
	args := append([]string{b.helper}, b.args(i)...)
	for n := range args {
		args[n] = windows.EscapeArg(args[n])
	}
	return strings.Join(args, " ")
}
func serviceAccessError(err error) error {
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return fmt.Errorf("[ADMIN_REQUIRED] 此操作需要管理员权限，可以授权后重启 PHPanel 再继续")
	}
	return err
}

// Queries work for standard users. Mutations ask only for their required rights.
func (b *windowsServices) open(i Instance, rights uint32) (*mgr.Service, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, serviceAccessError(err)
	}
	defer windows.CloseServiceHandle(h)
	name, err := windows.UTF16PtrFromString(systemServiceName(b.root, i))
	if err != nil {
		return nil, err
	}
	sh, err := windows.OpenService(h, name, rights|windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return nil, serviceAccessError(err)
	}
	s := &mgr.Service{Name: systemServiceName(b.root, i), Handle: sh}
	cfg, err := s.Config()
	if err == nil && !strings.EqualFold(cfg.BinaryPathName, b.command(i)) {
		err = fmt.Errorf("系统服务命令或软件位置已变化，拒绝控制；请在原位置注销服务后重新注册")
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (b *windowsServices) Query(i Instance) (systemServiceStatus, error) {
	s, err := b.open(i, 0)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return systemServiceStatus{}, nil
	}
	if err != nil {
		return systemServiceStatus{Exists: true}, err
	}
	defer s.Close()
	cfg, err := s.Config()
	if err != nil {
		return systemServiceStatus{Exists: true}, err
	}
	status, err := s.Query()
	if err != nil {
		return systemServiceStatus{Exists: true}, err
	}
	result := systemServiceStatus{Exists: true, AutoStart: cfg.StartType == mgr.StartAutomatic, PID: int(status.ProcessId)}
	switch status.State {
	case svc.Running:
		result.Status = "running"
	case svc.StartPending, svc.ContinuePending:
		result.Status = "starting"
	case svc.StopPending:
		result.Status = "stopping"
	case svc.Stopped:
		result.Status, result.PID = "stopped", 0
		if status.Win32ExitCode != 0 {
			result.Error = fmt.Sprintf("系统服务上次启动或运行失败（Windows 错误 %d / 服务错误 %d），请查看实例日志", status.Win32ExitCode, status.ServiceSpecificExitCode)
		}
	default:
		result.Status = "error"
		result.Error = "系统服务已暂停或处于未知状态，请在 Windows 服务管理器中检查"
	}
	return result, nil
}
func (b *windowsServices) Install(i Instance, auto bool) error {
	if _, err := os.Stat(b.helper); err != nil {
		return fmt.Errorf("缺少系统服务托管程序 phpanel-service.exe，请使用完整构建: %w", err)
	}
	m, err := mgr.Connect()
	if err != nil {
		return serviceAccessError(err)
	}
	defer m.Disconnect()
	start := uint32(mgr.StartManual)
	if auto {
		start = mgr.StartAutomatic
	}
	s, err := m.CreateService(systemServiceName(b.root, i), b.helper, mgr.Config{DisplayName: "PHPanel · " + i.Name, Description: "PHPanel 托管的 " + i.Component + " " + i.Version + "；数据目录：" + b.root, StartType: start, ErrorControl: mgr.ErrorNormal}, b.args(i)...)
	if err != nil {
		return serviceAccessError(err)
	}
	return s.Close()
}
func (b *windowsServices) SetAutoStart(i Instance, enabled bool) error {
	s, err := b.open(i, windows.SERVICE_CHANGE_CONFIG)
	if err != nil {
		return err
	}
	defer s.Close()
	cfg, err := s.Config()
	if err != nil {
		return err
	}
	cfg.StartType = mgr.StartManual
	if enabled {
		cfg.StartType = mgr.StartAutomatic
	}
	return serviceAccessError(s.UpdateConfig(cfg))
}
func (b *windowsServices) Remove(i Instance) error {
	s, err := b.open(i, windows.DELETE)
	if err != nil {
		return err
	}
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State != svc.Stopped {
		return fmt.Errorf("请先停止系统服务再注销")
	}
	return serviceAccessError(s.Delete())
}
func waitService(s *mgr.Service, target svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := s.Query()
		if err != nil {
			return err
		}
		if status.State == target {
			return nil
		}
		if target == svc.Running && status.State == svc.Stopped {
			return fmt.Errorf("系统服务启动失败（Windows 错误 %d / 服务错误 %d），请查看实例 logs/console.log 和 service-host.log", status.Win32ExitCode, status.ServiceSpecificExitCode)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("等待系统服务状态超时，请稍后刷新或查看实例日志")
}
func (b *windowsServices) Start(i Instance) error {
	s, err := b.open(i, windows.SERVICE_START)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Start(); err != nil {
		return serviceAccessError(err)
	}
	return waitService(s, svc.Running, 160*time.Second)
}
func (b *windowsServices) Stop(i Instance) error {
	s, err := b.open(i, windows.SERVICE_STOP)
	if err != nil {
		return err
	}
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if _, err := s.Control(svc.Stop); err != nil {
		return serviceAccessError(err)
	}
	return waitService(s, svc.Stopped, 35*time.Second)
}

type instanceServiceHost struct {
	manager  *Manager
	instance Instance
}

func (h *instanceServiceHost) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	status := svc.Status{State: svc.StartPending, Accepts: svc.AcceptStop | svc.AcceptShutdown, WaitHint: 180000, CheckPoint: 1}
	changes <- status
	result := make(chan error, 1)
	go func() {
		if h.instance.Component == "nginx" {
			result <- h.manager.startNginxLinked(h.instance.ID)
		} else {
			result <- h.manager.startInstance(h.instance.ID, false)
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var exited <-chan struct{}
	for {
		select {
		case err := <-result:
			if err != nil {
				h.log(err)
				h.manager.Close()
				return true, 1
			}
			h.manager.mu.Lock()
			proc := h.manager.processes[h.instance.ID]
			if proc != nil {
				exited = proc.done
			}
			h.manager.mu.Unlock()
			if proc == nil {
				h.log(fmt.Errorf("实例启动后退出"))
				return true, 1
			}
			status = svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
			changes <- status
			result = nil
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- status
			case svc.Stop, svc.Shutdown:
				status = svc.Status{State: svc.StopPending, WaitHint: 30000, CheckPoint: 1}
				changes <- status
				done := make(chan struct{})
				go func() {
					h.manager.Close()
					if h.instance.Component == "nginx" && (h.instance.NginxStopPHP || request.Cmd == svc.Shutdown) {
						if err := h.manager.stopLinkedPHP(h.instance.ID); err != nil {
							h.log(err)
						}
					}
					close(done)
				}()
				for {
					select {
					case <-done:
						return false, 0
					case <-tick.C:
						status.CheckPoint++
						changes <- status
					}
				}
			}
		case <-tick.C:
			if status.State == svc.StartPending {
				status.CheckPoint++
				changes <- status
			}
		case <-exited:
			h.log(fmt.Errorf("实例进程退出，请查看 console.log"))
			h.manager.Close()
			return true, 1
		}
	}
}
func (h *instanceServiceHost) log(err error) { _ = appendServiceHostLog(h.instance, err) }
func RunSystemServiceHost(root, instanceID, name string) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		return fmt.Errorf("托管程序只能由 Windows 服务控制管理器启动，请从 PHPanel 注册系统服务")
	}
	m, i, err := loadServiceHost(root, instanceID, name)
	if err != nil {
		return err
	}
	return svc.Run(name, &instanceServiceHost{manager: m, instance: i})
}

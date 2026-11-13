package panel

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type systemServiceStatus struct {
	Exists    bool
	AutoStart bool
	Status    string
	PID       int
	Error     string
}

// Every operation verifies the registered command before controlling a service.
type systemServiceBackend interface {
	Query(Instance) (systemServiceStatus, error)
	Install(Instance, bool) error
	SetAutoStart(Instance, bool) error
	Remove(Instance) error
	Start(Instance) error
	Stop(Instance) error
}

func supportsSystemService(component string) bool {
	return component == "nginx" || component == "redis" || component == "mysql"
}

func systemServiceName(root string, i Instance) string {
	if i.ServiceName != "" {
		return i.ServiceName
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(root)) + "\x00" + i.ID))
	return fmt.Sprintf("PHPanel-%s-%x", i.Component, sum[:8])
}

func serviceHelperPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "phpanel-service.exe"), nil
}

func (m *Manager) backendLocked() systemServiceBackend {
	if m.systemServices == nil {
		m.systemServices = newSystemServiceBackend(m.root)
	}
	return m.systemServices
}

func (m *Manager) refreshSystemServices() {
	if m.serviceHost {
		return
	}
	m.serviceMu.Lock()
	defer m.serviceMu.Unlock()
	m.refreshSystemServicesLocked()
	m.refreshRemotePHP()
}

func (m *Manager) RefreshSystemServices() { m.refreshSystemServices() }

func (m *Manager) refreshSystemServicesLocked() {
	backend := m.backendLocked()
	if backend == nil {
		return
	}
	for _, i := range m.Snapshot().Instances {
		if !supportsSystemService(i.Component) {
			continue
		}
		status, err := backend.Query(i)
		if !status.Exists && err == nil && i.ServiceName == "" {
			continue
		}
		next := i
		if err != nil {
			// Fail closed: inaccessible or replaced registrations cannot be edited/deleted.
			if !status.Exists && i.ServiceName == "" {
				continue
			}
			next.ServiceName = systemServiceName(m.root, i)
			next.Status, next.PID, next.Error = "error", 0, err.Error()
		} else if status.Exists {
			next.ServiceName = systemServiceName(m.root, i)
			next.ServiceAutoStart = status.AutoStart
			next.Status, next.PID, next.Error = status.Status, status.PID, status.Error
		} else {
			next.ServiceName, next.ServiceAutoStart = "", false
			next.Status, next.PID, next.Error = "stopped", 0, ""
		}
		if next == i {
			continue
		}
		_ = m.change(func(s *State) error {
			for n := range s.Instances {
				if s.Instances[n].ID == i.ID && !m.operations[i.ID] {
					s.Instances[n] = next
				}
			}
			return nil
		})
	}
}

func (m *Manager) reserveServiceOperation(i Instance) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || m.operations[i.ID] || m.operations[i.PackageID] || m.operations[instanceStartKey(i)] {
		return nil, fmt.Errorf("实例正在变更，请稍后重试")
	}
	m.operations[i.ID], m.operations[instanceStartKey(i)] = true, true
	m.wg.Add(1)
	return func() {
		m.mu.Lock()
		delete(m.operations, i.ID)
		delete(m.operations, instanceStartKey(i))
		m.mu.Unlock()
		m.wg.Done()
	}, nil
}

func (m *Manager) RegisterSystemService(key string, autoStart bool) error {
	m.serviceMu.Lock()
	defer m.serviceMu.Unlock()
	m.refreshSystemServicesLocked()
	i, _, err := m.lookupInstance(key)
	if err != nil {
		return err
	}
	if !supportsSystemService(i.Component) {
		return fmt.Errorf("只有 Nginx、Redis、MySQL 支持系统服务")
	}
	if i.ServiceName != "" {
		return fmt.Errorf("此实例已注册系统服务")
	}
	if i.Status == "running" || i.Status == "starting" || i.Status == "stopping" {
		return fmt.Errorf("请先停止实例再注册系统服务")
	}
	finish, err := m.reserveServiceOperation(i)
	if err != nil {
		return err
	}
	defer finish()
	lease, err := acquireRuntimeLease(m.root, i)
	if err != nil {
		return err
	}
	defer lease.release()
	for _, other := range m.Snapshot().Instances {
		if other.ID != i.ID && other.ServiceName != "" && (other.PackageID == i.PackageID || i.Component == "nginx" && other.Component == "nginx") {
			return fmt.Errorf("请先注销 %s 的系统服务；Nginx 全部版本、其他环境的同一已安装版本只能注册一个实例", other.Name)
		}
	}
	backend := m.backendLocked()
	if backend == nil {
		return fmt.Errorf("系统服务仅适用于 Windows")
	}
	if err = backend.Install(i, autoStart); err != nil {
		return err
	}
	err = m.change(func(s *State) error {
		for n := range s.Instances {
			if s.Instances[n].ID == key {
				s.Instances[n].ServiceName = systemServiceName(m.root, i)
				s.Instances[n].ServiceAutoStart = autoStart
				s.Instances[n].Status, s.Instances[n].Error = "stopped", ""
				return nil
			}
		}
		return fmt.Errorf("实例不存在")
	})
	if err != nil {
		if rollbackErr := backend.Remove(i); rollbackErr != nil {
			return fmt.Errorf("%v；服务注册回滚失败: %w", err, rollbackErr)
		}
	}
	return err
}

func (m *Manager) SetSystemServiceAutoStart(key string, enabled bool) error {
	return m.changeSystemService(key, func(b systemServiceBackend, i Instance) error { return b.SetAutoStart(i, enabled) })
}

func (m *Manager) UnregisterSystemService(key string) error {
	return m.changeSystemService(key, func(b systemServiceBackend, i Instance) error {
		if i.Status != "stopped" {
			return fmt.Errorf("请先停止系统服务再注销")
		}
		return b.Remove(i)
	})
}

func (m *Manager) changeSystemService(key string, action func(systemServiceBackend, Instance) error) error {
	complete, err := m.trackServiceAction()
	if err != nil {
		return err
	}
	defer complete()
	m.serviceMu.Lock()
	defer m.serviceMu.Unlock()
	m.refreshSystemServicesLocked()
	i, _, err := m.lookupInstance(key)
	if err != nil {
		return err
	}
	if i.ServiceName == "" {
		return fmt.Errorf("此实例尚未注册系统服务")
	}
	finish, err := m.reserveServiceOperation(i)
	if err != nil {
		return err
	}
	backend := m.backendLocked()
	if backend == nil {
		finish()
		return fmt.Errorf("系统服务仅适用于 Windows")
	}
	err = action(backend, i)
	finish()
	m.refreshSystemServicesLocked()
	return err
}

func (m *Manager) controlSystemService(key string, start bool) error {
	return m.changeSystemService(key, func(b systemServiceBackend, i Instance) error {
		if !start {
			return b.Stop(i)
		}
		// This action holds its own start reservation; temporarily omit that check.
		m.mu.Lock()
		delete(m.operations, instanceStartKey(i))
		err := m.canStartLocked(i)
		m.operations[instanceStartKey(i)] = true
		m.mu.Unlock()
		if err != nil {
			return err
		}
		if i.Status != "stopped" {
			return fmt.Errorf("系统服务正在运行或无法查询，请先检查服务状态")
		}
		return b.Start(i)
	})
}

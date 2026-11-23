package panel

import (
	"fmt"
	"io"
)

type runtimeServer interface{ io.Closer }

type phpRuntimeStatus struct {
	Persistent bool   `json:"persistent"`
	Protected  bool   `json:"protected"`
	Exists     bool   `json:"exists"`
	Status     string `json:"status"`
	PID        int    `json:"pid"`
	Error      string `json:"error,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Run        string `json:"run,omitempty"`
}

type phpRuntimeBackend interface {
	Query(Instance) (phpRuntimeStatus, error)
	Start(Instance, string, string) (phpRuntimeStatus, error)
	Stop(Instance, string) error
	Diagnostic(Instance) (PHPProbe, error)
	Protect(Instance, bool) error
}

func (m *Manager) startDetachedPHP(i Instance, restarting bool, owner, run string) error {
	m.mu.Lock()
	if m.closing || m.operations[i.ID] || (!restarting && m.operations["php-restart:"+i.ID]) {
		m.mu.Unlock()
		return fmt.Errorf("PHP 正在变更")
	}
	if err := m.canStartLocked(i); err != nil {
		m.mu.Unlock()
		return err
	}
	m.operations[i.ID], m.operations[instanceStartKey(i)] = true, true
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.operations, i.ID)
		delete(m.operations, instanceStartKey(i))
		m.mu.Unlock()
		m.wg.Done()
	}()
	_ = m.setInstance(i.ID, "starting", "", 0)
	status, err := m.phpBackend().Start(i, owner, run)
	if err != nil {
		_ = m.setInstance(i.ID, "error", err.Error(), 0)
		return err
	}
	m.mu.Lock()
	closing := m.closing
	m.mu.Unlock()
	if closing {
		if !status.Protected {
			_ = m.phpBackend().Stop(i, "close-unprotected")
		}
		return fmt.Errorf("应用正在退出")
	}
	m.mu.Lock()
	if m.ownedDetachedPHP == nil {
		m.ownedDetachedPHP = map[string]bool{}
	}
	m.ownedDetachedPHP[i.ID] = true
	m.mu.Unlock()
	return m.syncRemotePHP(i, status)
}

func (m *Manager) phpBackend() phpRuntimeBackend {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.phpRuntime == nil {
		m.phpRuntime = newPHPRuntimeBackend(m.root)
	}
	return m.phpRuntime
}

func (m *Manager) syncRemotePHP(i Instance, status phpRuntimeStatus) error {
	return m.change(func(s *State) error {
		for n := range s.Packages {
			if s.Packages[n].ID == i.ID && s.Packages[n].Component == "php" {
				s.Packages[n].Status, s.Packages[n].PID, s.Packages[n].Error = status.Status, status.PID, status.Error
				s.Packages[n].LinkedNginxID, s.Packages[n].LinkedRun = status.Owner, status.Run
				return nil
			}
		}
		for n := range s.Instances {
			if s.Instances[n].ID == i.ID {
				s.Instances[n].Status, s.Instances[n].PID, s.Instances[n].Error = status.Status, status.PID, status.Error
				s.Instances[n].LinkedNginxID, s.Instances[n].LinkedRun = status.Owner, status.Run
				return nil
			}
		}
		return fmt.Errorf("PHP 运行时不存在")
	})
}

func (m *Manager) refreshRemotePHP() {
	for _, p := range m.Snapshot().Packages {
		if p.Component != "php" {
			continue
		}
		i := instanceFromPackage(p)
		m.mu.Lock()
		owned, changing := m.processes[i.ID] != nil, m.operations[i.ID] || m.operations["php-restart:"+i.ID]
		m.mu.Unlock()
		if owned || changing {
			continue
		}
		status, err := m.phpBackend().Query(i)
		if err != nil {
			if i.LinkedNginxID != "" {
				_ = m.setInstance(i.ID, "error", "无法查询后台 PHP: "+err.Error(), i.PID)
			}
			continue
		}
		if !status.Exists {
			m.mu.Lock()
			detached := m.ownedDetachedPHP[i.ID]
			m.mu.Unlock()
			if i.LinkedNginxID == "" && !detached {
				continue
			}
			status.Status = "stopped"
		}
		if i.Status == status.Status && i.PID == status.PID && i.Error == status.Error && i.LinkedNginxID == status.Owner && i.LinkedRun == status.Run {
			continue
		}
		_ = m.syncRemotePHP(i, status)
	}
}

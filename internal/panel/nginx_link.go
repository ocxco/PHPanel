package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type nginxLinkJournal struct {
	ProtectedIDs []string `json:"protectedIds,omitempty"`
	NginxID      string   `json:"nginxId"`
	Run          string   `json:"run"`
	PHPIDs       []string `json:"phpIds"`
}

func nginxAutoPHP(i Instance) bool { return i.NginxAutoPHP == nil || *i.NginxAutoPHP }

// All Nginx versions load the shared sites directory, including websites whose
// validation instance is another version or has been deleted.
func nginxPHPDependencies(state State) ([]Instance, error) {
	ids := map[string]bool{}
	for _, site := range state.Sites {
		if site.Template != "static" && site.PHPID != "" {
			ids[site.PHPID] = true
		}
	}
	// A PHP package is its own runtime; resolve each referenced PHPID against
	// the installed PHP packages and synthesize an Instance view.
	var result []Instance
	for _, p := range state.Packages {
		if p.Component != "php" || !ids[p.ID] {
			continue
		}
		result = append(result, instanceFromPackage(p))
		delete(ids, p.ID)
	}
	if len(ids) > 0 {
		return nil, fmt.Errorf("网站引用的 PHP 实例不存在，请先更新网站设置")
	}
	sort.Slice(result, func(a, b int) bool { return result[a].ID < result[b].ID })
	return result, nil
}

func (m *Manager) linkJournalPath(key string) (string, error) {
	if filepath.Base(key) != key || key == "." || key == ".." {
		return "", fmt.Errorf("Nginx 实例标识无效")
	}
	return filepath.Join(m.root, "state", "runtime", key+".json"), nil
}
func (m *Manager) writeLinkJournal(j nginxLinkJournal) error {
	path, err := m.linkJournalPath(j.NginxID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	body, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return atomicConfigWrite(path, body)
}
func (m *Manager) readLinkJournal(key string) (nginxLinkJournal, error) {
	path, err := m.linkJournalPath(key)
	if err != nil {
		return nginxLinkJournal{}, err
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nginxLinkJournal{NginxID: key}, nil
	}
	if err != nil {
		return nginxLinkJournal{}, err
	}
	var j nginxLinkJournal
	if err := json.Unmarshal(body, &j); err != nil {
		return j, err
	}
	if j.NginxID != key {
		return j, fmt.Errorf("Nginx 联动记录不匹配")
	}
	return j, nil
}

func (m *Manager) SaveNginxLinkOptions(key string, autoPHP, stopPHP bool) error {
	m.refreshSystemServices()
	i, _, err := m.lookupInstance(key)
	if err != nil {
		return err
	}
	if i.Component != "nginx" {
		return fmt.Errorf("只有 Nginx 支持 PHP 联动设置")
	}
	finish, err := m.reserveServiceOperation(i)
	if err != nil {
		return err
	}
	defer finish()
	if i.Status != "stopped" && i.Status != "error" || i.ServiceName != "" && i.Status != "stopped" {
		return fmt.Errorf("请先停止 Nginx 再修改联动设置")
	}
	lease, err := acquireRuntimeLease(m.root, i)
	if err != nil {
		return err
	}
	defer lease.release()
	return m.change(func(s *State) error {
		for n := range s.Instances {
			if s.Instances[n].ID == key {
				s.Instances[n].NginxAutoPHP = &autoPHP
				s.Instances[n].NginxStopPHP = stopPHP
				return nil
			}
		}
		return fmt.Errorf("Nginx 实例不存在")
	})
}

func (m *Manager) startNginxLinked(key string) (err error) {
	m.nginxLinkMu.Lock()
	defer m.nginxLinkMu.Unlock()
	i, _, err := m.lookupInstance(key)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.closing || m.operations["nginx-link"] || m.processes[key] != nil {
		m.mu.Unlock()
		return fmt.Errorf("Nginx 正在运行或变更")
	}
	err = m.canStartLocked(i)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	m.operations["nginx-link"] = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.operations, "nginx-link"); m.mu.Unlock(); m.wg.Done() }()
	lease, err := acquireRuntimeLease(m.root, i)
	if err != nil {
		return err
	}
	passed := false
	defer func() {
		if !passed {
			lease.release()
		}
	}()
	if err = m.setInstance(key, "starting", "", 0); err != nil {
		return err
	}
	j := nginxLinkJournal{NginxID: key, Run: id("link"), PHPIDs: []string{}}
	rollback := func(cause error) error {
		for _, id := range j.ProtectedIDs {
			if php, _, err := m.lookupInstance(id); err == nil {
				_ = m.phpBackend().Protect(php, false)
			}
		}
		if stopErr := m.stopLinkJournal(j); stopErr != nil {
			cause = fmt.Errorf("%v；联动 PHP 回滚失败: %w", cause, stopErr)
		}
		_ = m.setInstance(key, "error", cause.Error(), 0)
		return cause
	}
	if err = m.writeLinkJournal(j); err != nil {
		return rollback(err)
	}
	if nginxAutoPHP(i) {
		dependencies, planErr := nginxPHPDependencies(m.Snapshot())
		if planErr != nil {
			return rollback(planErr)
		}
		for _, php := range dependencies {
			if m.serviceHost {
				status, queryErr := m.phpBackend().Query(php)
				if queryErr != nil {
					return rollback(queryErr)
				}
				if status.Exists && status.Status == "running" && !status.Protected {
					if !status.Persistent {
						return rollback(fmt.Errorf("PHP %s 仍由旧面板管理，请先停止后重新启动", php.Name))
					}
					if err := m.phpBackend().Protect(php, true); err != nil {
						return rollback(err)
					}
					j.ProtectedIDs = append(j.ProtectedIDs, php.ID)
				}
			}
			owned, startErr := m.ensureLinkedPHP(php, key, j.Run)
			if startErr != nil {
				return rollback(fmt.Errorf("关联 PHP %s 启动失败: %w", php.Name, startErr))
			}
			if owned {
				j.PHPIDs = append(j.PHPIDs, php.ID)
				if err = m.writeLinkJournal(j); err != nil {
					return rollback(err)
				}
			}
		}
	}
	if m.linkedNginxStarter != nil {
		err = m.linkedNginxStarter(key, lease)
	} else {
		err = m.startInstanceWithLease(key, false, lease)
	}
	if err != nil {
		return rollback(err)
	}
	passed = true
	return nil
}

func (m *Manager) ensureLinkedPHP(i Instance, owner, run string) (bool, error) {
	m.mu.Lock()
	local := m.processes[i.ID] != nil
	m.mu.Unlock()
	if local {
		current, _, err := m.lookupInstance(i.ID)
		if err != nil {
			return false, err
		}
		if current.Status != "running" {
			return false, fmt.Errorf("PHP 实例正在启动或停止，请稍后重试")
		}
		return false, nil
	}
	status, err := m.phpBackend().Query(i)
	if err != nil {
		return false, err
	}
	if status.Exists {
		if status.Status == "running" {
			if !m.serviceHost {
				_ = m.syncRemotePHP(i, status)
			}
			return false, nil
		}
		return false, fmt.Errorf("PHP 实例正在变更，请稍后重试")
	}
	if m.serviceHost {
		status, err = m.phpBackend().Start(i, owner, run)
		if err != nil {
			return false, err
		}
		return status.Owner == owner && status.Run == run, nil
	}
	if m.detachedPHP {
		if err := m.startDetachedPHP(i, false, owner, run); err != nil {
			return false, err
		}
		return true, nil
	}
	if err = m.StartInstance(i.ID); err != nil {
		return false, err
	}
	err = m.change(func(s *State) error {
		for n := range s.Packages {
			if s.Packages[n].ID == i.ID && s.Packages[n].Component == "php" {
				s.Packages[n].LinkedNginxID = owner
				s.Packages[n].LinkedRun = run
				return nil
			}
		}
		for n := range s.Instances {
			if s.Instances[n].ID == i.ID {
				s.Instances[n].LinkedNginxID = owner
				s.Instances[n].LinkedRun = run
			}
		}
		return nil
	})
	if err != nil {
		_ = m.stopInstance(i.ID, true)
	}
	return true, err
}

func (m *Manager) stopLinkJournal(j nginxLinkJournal) error {
	var first error
	for n := len(j.PHPIDs) - 1; n >= 0; n-- {
		i, _, err := m.lookupInstance(j.PHPIDs[n])
		if err != nil {
			continue
		}
		status, err := m.phpBackend().Query(i)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if !status.Exists || status.Owner != j.NginxID || status.Run != j.Run {
			continue
		}
		if err = m.phpBackend().Stop(i, j.Run); err != nil && first == nil {
			first = err
		}
	}
	return first
}
func (m *Manager) stopLinkedPHP(key string) error {
	j, err := m.readLinkJournal(key)
	if err != nil {
		return err
	}
	return m.stopLinkJournal(j)
}
func (m *Manager) StopNginxWithPHP(key string) error {
	i, _, err := m.lookupInstance(key)
	if err != nil {
		return err
	}
	if i.Component != "nginx" {
		return fmt.Errorf("仅支持 Nginx 与联动 PHP 一起停止")
	}
	j, err := m.readLinkJournal(key)
	if err != nil {
		return err
	}
	if err = m.StopInstance(key); err != nil {
		return err
	}
	err = m.stopLinkJournal(j)
	m.refreshSystemServices()
	return err
}

package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) DeleteInstance(key string) error {
	m.refreshSystemServices()
	// Serialize against website references and configuration editing.
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	m.mu.Lock()
	var instance Instance
	for _, i := range m.state.Instances {
		if i.ID == key {
			instance = i
			break
		}
	}
	if instance.ID == "" {
		m.mu.Unlock()
		return fmt.Errorf("实例不存在")
	}
	if instance.ServiceName != "" {
		m.mu.Unlock()
		return fmt.Errorf("请先注销此实例的系统服务，再删除实例")
	}
	if instance.Component == "nginx" {
		for _, php := range m.state.Instances {
			if php.LinkedNginxID == key && (php.PID > 0 || php.Status == "running" || php.Status == "starting" || php.Status == "stopping") {
				m.mu.Unlock()
				return fmt.Errorf("请先停止此 Nginx 联动的 PHP 实例，再删除 Nginx")
			}
		}
	}
	if m.closing || m.operations[key] {
		m.mu.Unlock()
		return fmt.Errorf("实例正在变更或应用正在退出，请稍后重试")
	}
	if m.processes[key] != nil || instance.PID > 0 || instance.Status == "starting" || instance.Status == "running" || instance.Status == "stopping" {
		m.mu.Unlock()
		return fmt.Errorf("请先停止实例，再删除")
	}
	for _, site := range m.state.Sites {
		if site.PHPID == key {
			m.mu.Unlock()
			return fmt.Errorf("实例被网站 %s 引用，请先修改或删除该网站", site.Name)
		}
	}
	for _, prepared := range m.state.PreparedExtensions {
		if prepared.Record.PackageID == instance.PackageID {
			m.mu.Unlock()
			return fmt.Errorf("此 PHP 有待替换的扩展，请先停止并应用更新后再删除实例")
		}
	}
	m.operations[key] = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer func() { m.mu.Lock(); delete(m.operations, key); m.mu.Unlock() }()
	lease, err := acquireRuntimeLease(m.root, instance)
	if err != nil {
		return err
	}
	defer lease.release()
	path, exists, err := m.instanceDeletePath(instance)
	if err != nil {
		return err
	}
	trash := filepath.Join(m.root, "instances", ".deleting-"+instance.ID+"-"+strings.TrimPrefix(id("delete"), "delete-"))
	if exists {
		if err := os.Rename(path, trash); err != nil {
			return fmt.Errorf("实例目录被占用或无法移除，实例未删除: %w", err)
		}
	}
	if err := m.change(func(s *State) error {
		for n, i := range s.Instances {
			if i.ID == key {
				if s.Tools.PHPInstanceID == key {
					s.Tools.PHPInstanceID = ""
				}
				if instance.Component == "nginx" {
					for n := range s.Sites {
						if s.Sites[n].NginxID == key {
							s.Sites[n].NginxID = ""
						}
					}
				}
				s.Instances = append(s.Instances[:n], s.Instances[n+1:]...)
				return nil
			}
		}
		return fmt.Errorf("实例不存在")
	}); err != nil {
		if exists {
			if restoreErr := os.Rename(trash, path); restoreErr != nil {
				return fmt.Errorf("实例记录未删除，但目录恢复失败；文件保留在 %s: %v；%w", trash, restoreErr, err)
			}
		}
		return err
	}
	if exists {
		// Check the resolved absolute path immediately before recursive removal.
		physical, err := PhysicalPath(trash)
		if err == nil {
			var relative string
			relative, err = filepath.Rel(trash, physical)
			if err == nil && relative != "." {
				err = fmt.Errorf("待清理目录路径发生变化")
			}
		}
		if err == nil {
			err = os.RemoveAll(physical)
		}
		if err != nil {
			return fmt.Errorf("实例记录已删除，但文件清理失败；残留文件在 %s，请关闭占用文件的程序后清理: %w", trash, err)
		}
	}
	return nil
}

func (m *Manager) instanceDeletePath(i Instance) (string, bool, error) {
	base := filepath.Join(m.root, "instances")
	if filepath.Base(i.ID) != i.ID || i.ID == "." || i.ID == ".." {
		return "", false, fmt.Errorf("实例标识无效，拒绝删除目录")
	}
	folder := i.DirectoryName
	if folder == "" {
		folder = i.ID
	}
	if filepath.Base(folder) != folder || strings.ContainsAny(folder, "\\/:") {
		return "", false, fmt.Errorf("实例目录标识无效")
	}
	expected := filepath.Join(base, folder)
	path, err := filepath.Abs(i.Path)
	if err != nil {
		return "", false, err
	}
	relative, err := filepath.Rel(expected, path)
	if err != nil || relative != "." || !within(base, path) {
		return "", false, fmt.Errorf("实例目录不属于该实例，拒绝删除")
	}
	physicalBase, err := PhysicalPath(base)
	if err != nil {
		return "", false, err
	}
	relative, err = filepath.Rel(base, physicalBase)
	if err != nil || relative != "." {
		return "", false, fmt.Errorf("实例父目录被重定向，拒绝删除")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return path, false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("实例目录类型无效，拒绝删除")
	}
	physical, err := PhysicalPath(path)
	if err != nil {
		return "", false, err
	}
	relative, err = filepath.Rel(expected, physical)
	if err != nil || relative != "." {
		return "", false, fmt.Errorf("实例目录被重定向，拒绝删除")
	}
	return path, true, nil
}

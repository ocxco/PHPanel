package panel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type ConfigDocument struct {
	Name     string `json:"name"`
	Content  string `json:"content"`
	Revision string `json:"revision"`
	Path     string `json:"path"`
}

func configRevision(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func configNames(state State, i Instance) []string {
	name := map[string]string{"php": "php.ini", "nginx": "nginx.conf", "mysql": "my.ini", "redis": "redis.conf"}[i.Component]
	names := []string{name}
	if i.Component == "php" {
		// PHP 配置是单一的 package/php.ini；不再使用 conf.d。
		return names
	}
	if i.Component == "nginx" {
		for _, site := range state.Sites {
			names = append(names, siteConfigName(site))
		}
	}
	return names
}
func (m *Manager) configPath(i Instance, name string) (string, error) {
	allowed := false
	for _, candidate := range configNames(m.Snapshot(), i) {
		if candidate == name && candidate != "" {
			allowed = true
		}
	}
	if !allowed {
		return "", fmt.Errorf("配置文件不在可编辑列表中")
	}
	dir := filepath.Join(i.Path, "config")
	if i.Component == "php" {
		dir = phpConfigDir(i)
	}
	path := filepath.Join(dir, filepath.FromSlash(name))
	if i.Component == "nginx" {
		path = m.nginxConfigPath(i, name)
	}
	if i.Component == "nginx" && name != "nginx.conf" {
		dir = filepath.Join(m.root, "sites")
	}
	real, err := PhysicalPath(path)
	if err != nil {
		return "", err
	}
	base, err := PhysicalPath(dir)
	if err != nil || !within(base, real) {
		return "", fmt.Errorf("配置路径超出实例目录")
	}
	return path, nil
}
func (m *Manager) ListConfigs(key string) ([]string, error) {
	i, _, err := m.lookupInstance(key)
	if err != nil {
		return nil, err
	}
	return configNames(m.Snapshot(), i), nil
}
func (m *Manager) ReadConfig(key, name string) (ConfigDocument, error) {
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	return m.readConfig(key, name)
}
func (m *Manager) readConfig(key, name string) (ConfigDocument, error) {
	i, _, err := m.lookupInstance(key)
	if err != nil {
		return ConfigDocument{}, err
	}
	path, err := m.configPath(i, name)
	if err != nil {
		return ConfigDocument{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return ConfigDocument{}, err
	}
	if info.Size() > 1024*1024 {
		return ConfigDocument{}, fmt.Errorf("配置超过 1 MB，无法在内置编辑器打开")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return ConfigDocument{}, err
	}
	if !utf8.Valid(body) {
		return ConfigDocument{}, fmt.Errorf("请先将配置转换为 UTF-8 编码")
	}
	return ConfigDocument{Name: name, Content: string(body), Revision: configRevision(body), Path: path}, nil
}
func (m *Manager) SaveConfig(key, name, content, revision string) (ConfigDocument, error) {
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	if len(content) > 1024*1024 || strings.ContainsRune(content, 0) || !utf8.ValidString(content) {
		return ConfigDocument{}, fmt.Errorf("配置须为 UTF-8 文本且不能超过 1 MB")
	}
	current, err := m.readConfig(key, name)
	if err != nil {
		return ConfigDocument{}, err
	}
	if revision != current.Revision {
		return ConfigDocument{}, fmt.Errorf("文件已被修改，请重新打开后再保存")
	}
	i, _, err := m.lookupInstance(key)
	if err != nil {
		return ConfigDocument{}, err
	}
	body := []byte(content)
	if i.Component == "nginx" {
		if name == "nginx.conf" && !strings.Contains(content, "include "+nginxInclude(m.root)+";") {
			return ConfigDocument{}, fmt.Errorf("主配置必须保留管理站点的 include 指令")
		}
		before := m.Snapshot()
		if err := m.applyNginxChanges(before, before.Sites, []string{key}, map[string][]byte{name: body}, false); err != nil {
			return ConfigDocument{}, err
		}
	} else {
		m.mu.Lock()
		if m.closing || m.operations[key] || m.operations["php-restart:"+key] {
			m.mu.Unlock()
			return ConfigDocument{}, fmt.Errorf("实例正在启动或变更，请稍后重试")
		}
		m.operations[key] = true
		m.wg.Add(1)
		m.mu.Unlock()
		defer m.wg.Done()
		defer func() { m.mu.Lock(); delete(m.operations, key); m.mu.Unlock() }()
		path, err := m.configPath(i, name)
		if err != nil {
			return ConfigDocument{}, err
		}
		if err := os.WriteFile(path+".before-edit", []byte(current.Content), 0600); err != nil {
			return ConfigDocument{}, err
		}
		if err := atomicConfigWrite(path, body); err != nil {
			return ConfigDocument{}, err
		}
	}
	return ConfigDocument{Name: name, Content: content, Revision: configRevision(body), Path: current.Path}, nil
}

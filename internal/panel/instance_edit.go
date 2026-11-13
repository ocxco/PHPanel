package panel

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var redisPortDirective = regexp.MustCompile(`(?i)^(\s*port\s+)\d+(.*)$`)
var mysqlPortDirective = regexp.MustCompile(`(?i)^(\s*(?:loose[-_])?port\s*=\s*)\d+(.*)$`)

func rewriteInstancePort(component string, body []byte, port int) ([]byte, error) {
	lines := strings.Split(string(body), "\n")
	value := strconv.Itoa(port)
	found := false
	group := ""
	for n, line := range lines {
		trim := strings.TrimSpace(line)
		if component == "redis" {
			if strings.HasPrefix(strings.ToLower(trim), "include ") {
				return nil, fmt.Errorf("Redis 使用 include 配置，请先在配置编辑器中合并端口设置")
			}
			if match := redisPortDirective.FindStringSubmatch(line); match != nil {
				lines[n] = match[1] + value + match[2]
				found = true
			}
		}
		if component == "mysql" {
			if strings.HasPrefix(trim, "!") {
				return nil, fmt.Errorf("MySQL 使用外部 include 配置，请先在配置编辑器中合并端口设置")
			}
			if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
				group = strings.ToLower(strings.Trim(trim, "[]"))
			}
			server := group == "mysqld" || group == "server" || strings.HasPrefix(group, "mysqld-")
			if server {
				if match := mysqlPortDirective.FindStringSubmatch(line); match != nil {
					lines[n] = match[1] + value + match[2]
					found = true
				}
			}
		}
	}
	if !found {
		if component == "redis" {
			lines = append(lines, "port "+value)
		} else {
			lines = append(lines, "[mysqld]", "port="+value)
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}
func (m *Manager) UpdateInstancePort(key string, port int) (Instance, error) {
	m.refreshSystemServices()
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	i, p, err := m.lookupInstance(key)
	if err != nil {
		return Instance{}, err
	}
	if i.Component == "nginx" {
		return Instance{}, fmt.Errorf("Nginx 端口由站点管理")
	}
	if i.ServiceName != "" && i.Status != "stopped" {
		return Instance{}, fmt.Errorf("请先停止系统服务，确认其状态后再修改端口")
	}
	if port < 1 || port > 65535 {
		return Instance{}, fmt.Errorf("端口须在 1–65535 之间")
	}
	m.mu.Lock()
	if m.closing || m.operations[key] || m.operations[p.ID] || m.operations["php-restart:"+key] || m.processes[key] != nil || i.Status == "running" || i.Status == "starting" || i.Status == "stopping" {
		m.mu.Unlock()
		return Instance{}, fmt.Errorf("请先停止实例再修改端口")
	}
	m.operations[key] = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer func() { m.mu.Lock(); delete(m.operations, key); m.mu.Unlock() }()
	lease, err := acquireRuntimeLease(m.root, i)
	if err != nil {
		return Instance{}, err
	}
	defer lease.release()
	if port == i.Port {
		return i, nil
	}
	before := m.Snapshot()
	for _, other := range before.Instances {
		if other.ID != key && other.Port == port {
			return Instance{}, fmt.Errorf("端口已被实例 %s 使用", other.Name)
		}
	}
	for _, site := range before.Sites {
		if site.Port == port {
			return Instance{}, fmt.Errorf("端口已被网站 %s 使用", site.Name)
		}
	}
	if err = checkPort(port); err != nil {
		return Instance{}, err
	}
	configPath := ""
	var oldConfig, newConfig []byte
	if i.Component == "redis" || i.Component == "mysql" {
		filename := "redis.conf"
		if i.Component == "mysql" {
			filename = "my.ini"
		}
		configPath, err = m.configPath(i, filename)
		if err != nil {
			return Instance{}, err
		}
		oldConfig, err = os.ReadFile(configPath)
		if err != nil {
			return Instance{}, err
		}
		newConfig, err = rewriteInstancePort(i.Component, oldConfig, port)
		if err != nil {
			return Instance{}, err
		}
		if err = os.WriteFile(configPath+".before-port-change", oldConfig, 0600); err != nil {
			return Instance{}, err
		}
	}
	overrides := map[string][]byte{}
	original := map[string][]byte{}
	if i.Component == "php" {
		pattern := regexp.MustCompile(`(?m)(\bfastcgi_pass\s+)127\.0\.0\.1:` + strconv.Itoa(i.Port) + `(\s*;)`)
		for _, site := range before.Sites {
			if site.PHPID != key {
				continue
			}
			name := siteConfigName(site)
			path := m.nginxConfigPath(Instance{}, name)
			body, err := os.ReadFile(path)
			if err != nil {
				return Instance{}, err
			}
			if !pattern.Match(body) {
				return Instance{}, fmt.Errorf("站点 %s 的 fastcgi_pass 已手工修改，请先恢复为此 PHP 实例的原端口", site.Name)
			}
			original[name] = body
			overrides[name] = pattern.ReplaceAll(body, []byte("${1}127.0.0.1:"+strconv.Itoa(port)+"${2}"))
		}
	}
	if len(overrides) > 0 {
		if err = m.applyNginxChanges(before, before.Sites, nil, overrides, false); err != nil {
			return Instance{}, err
		}
	}
	rollback := func(cause error) (Instance, error) {
		if configPath != "" {
			if restoreErr := atomicConfigWrite(configPath, oldConfig); restoreErr != nil {
				cause = fmt.Errorf("%v；原配置恢复失败: %v", cause, restoreErr)
			}
		}
		if len(original) > 0 {
			if restoreErr := m.applyNginxChanges(before, before.Sites, nil, original, false); restoreErr != nil {
				cause = fmt.Errorf("%v；站点配置恢复失败: %v", cause, restoreErr)
			}
		}
		return Instance{}, cause
	}
	if configPath != "" {
		if err = atomicConfigWrite(configPath, newConfig); err != nil {
			return rollback(err)
		}
	}
	if err = m.change(func(s *State) error {
		for n := range s.Instances {
			if s.Instances[n].ID == key {
				s.Instances[n].Port = port
				s.Instances[n].Error = ""
				return nil
			}
		}
		return fmt.Errorf("实例不存在")
	}); err != nil {
		return rollback(err)
	}
	i.Port = port
	i.Error = ""
	return i, nil
}

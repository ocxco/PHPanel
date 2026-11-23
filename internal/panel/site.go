package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var siteNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,48}[a-z0-9])?$`)

func findSiteInstance(state State, key, component string) (Instance, error) {
	for _, i := range state.Instances {
		if i.ID == key && i.Component == component {
			return i, nil
		}
	}
	// PHP packages are their own runtime: synthesize an Instance view from the
	// matching package so website/nginx resolution keeps working without a
	// separate PHP instance row.
	if component == "php" {
		for _, p := range state.Packages {
			if p.ID == key && p.Component == "php" {
				return instanceFromPackage(p), nil
			}
		}
	}
	return Instance{}, fmt.Errorf("请选择有效的 %s 实例", component)
}
func prepareSite(state State, site Site) (Site, error) {
	site.Name = strings.ToLower(strings.TrimSpace(site.Name))
	if !siteNamePattern.MatchString(site.Name) {
		return Site{}, fmt.Errorf("站点名称只能包含小写字母、数字和中间连字符，最长 50 个字符")
	}
	nginx, err := findSiteInstance(state, site.NginxID, "nginx")
	if err != nil && site.NginxID == "" {
		for _, i := range state.Instances {
			if i.Component == "nginx" {
				nginx = i
				err = nil
				if i.Status == "running" {
					break
				}
			}
		}
	}
	if err != nil {
		return Site{}, err
	}
	site.NginxID = nginx.ID
	if site.Template == "" {
		site.Template = "php"
	}
	switch site.Template {
	case "php", "laravel", "thinkphp", "wordpress":
		if _, err := findSiteInstance(state, site.PHPID, "php"); err != nil {
			return Site{}, err
		}
	case "static":
		site.PHPID = ""
	default:
		return Site{}, fmt.Errorf("不支持此网站模板")
	}
	if site.Port < 1 || site.Port > 65535 {
		return Site{}, fmt.Errorf("网站端口须在 1–65535 之间")
	}
	for _, instance := range state.Instances {
		if instance.Component != "nginx" && instance.Port == site.Port {
			return Site{}, fmt.Errorf("端口已被实例 %s 使用", instance.Name)
		}
	}
	for _, p := range state.Packages {
		if p.Component == "php" && p.Port == site.Port {
			return Site{}, fmt.Errorf("端口已被 PHP %s 使用", p.Version)
		}
	}
	for _, old := range state.Sites {
		if old.ID != site.ID && old.Name == site.Name && old.Port == site.Port {
			return Site{}, fmt.Errorf("相同域名和端口的网站已存在")
		}
	}
	root, err := filepath.Abs(site.Root)
	if err != nil || strings.ContainsAny(root, "\";\r\n\x00") {
		return Site{}, fmt.Errorf("网站目录无效")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return Site{}, fmt.Errorf("网站目录不存在")
	}
	if strings.IndexFunc(nativePath(root), func(r rune) bool { return r > 127 }) >= 0 {
		return Site{}, fmt.Errorf("Nginx Windows 发行包暂不支持此网站路径，请选择英文目录（可以包含空格）")
	}
	site.Root = root
	site.URL = fmt.Sprintf("http://%s.localhost:%d", site.Name, site.Port)
	return site, nil
}

func (m *Manager) CreateSite(name, root, nginxID, phpID string) (Site, error) {
	return m.CreateSiteWithOptions(name, root, nginxID, phpID, 8080, "php")
}
func (m *Manager) CreateSiteWithOptions(name, root, nginxID, phpID string, port int, template string) (Site, error) {
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	before := m.Snapshot()
	site := Site{ID: id("site"), Name: name, Root: root, NginxID: nginxID, PHPID: phpID, Port: port, Template: template}
	auto := strings.TrimSpace(root) == ""
	if auto {
		site.Root = filepath.Join(m.root, "sites", site.ID)
		if err := os.MkdirAll(site.Root, 0700); err != nil {
			return Site{}, err
		}
	}
	site, err := prepareSite(before, site)
	if err != nil {
		return Site{}, err
	}
	if auto {
		welcome := "<?php header('Content-Type: text/plain; charset=utf-8'); echo 'PHPanel · " + site.Name + "\nPHP '.PHP_VERSION;"
		file := "index.php"
		if site.Template == "static" {
			file = "index.html"
			welcome = "<!doctype html>\n<html lang=\"zh-CN\">\n    <meta charset=\"UTF-8\">\n    <title>PHPanel</title>\n    <h1>PHPanel · " + site.Name + "</h1>\n</html>\n"
		}
		if err := os.WriteFile(filepath.Join(site.Root, file), []byte(welcome), 0600); err != nil {
			return Site{}, err
		}
	}
	sites := append(append([]Site{}, before.Sites...), site)
	if err := m.applySiteChanges(before, sites, []string{nginxID}); err != nil {
		return Site{}, err
	}
	return site, nil
}
func (m *Manager) UpdateSite(key, name, root, nginxID, phpID string) (Site, error) {
	for _, old := range m.Snapshot().Sites {
		if old.ID == key {
			return m.UpdateSiteWithOptions(key, name, root, nginxID, phpID, old.Port, old.Template)
		}
	}
	return Site{}, fmt.Errorf("网站不存在")
}
func (m *Manager) UpdateSiteWithOptions(key, name, root, nginxID, phpID string, port int, template string) (Site, error) {
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	before := m.Snapshot()
	index := -1
	var old Site
	for n, site := range before.Sites {
		if site.ID == key {
			index = n
			old = site
			break
		}
	}
	if index < 0 {
		return Site{}, fmt.Errorf("网站不存在")
	}
	if strings.TrimSpace(root) == "" {
		root = old.Root
	}
	site, err := prepareSite(before, Site{ID: key, Name: name, Root: root, NginxID: nginxID, PHPID: phpID, Port: port, Template: template})
	if err != nil {
		return Site{}, err
	}
	sites := append([]Site{}, before.Sites...)
	sites[index] = site
	if err := m.applySiteChanges(before, sites, []string{old.NginxID, nginxID}); err != nil {
		return Site{}, err
	}
	return site, nil
}
func (m *Manager) DeleteSite(key string) error {
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	before := m.Snapshot()
	sites := []Site{}
	var found Site
	for _, site := range before.Sites {
		if site.ID == key {
			found = site
		} else {
			sites = append(sites, site)
		}
	}
	if found.ID == "" {
		return fmt.Errorf("网站不存在")
	}
	return m.applySiteChanges(before, sites, []string{found.NginxID})
}
func (m *Manager) SiteURL(key string) (string, error) {
	for _, site := range m.Snapshot().Sites {
		if site.ID == key {
			return site.URL, nil
		}
	}
	return "", fmt.Errorf("网站不存在")
}

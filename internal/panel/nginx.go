package panel

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func siteConfigName(site Site) string { return "sites/" + site.ID + ".conf" }
func renderNginxConfig(i Instance, state State) ([]byte, error) {
	return []byte(fmt.Sprintf(`# PHPanel: each website has its own configuration.
worker_processes 1;
error_log %s;
pid %s;

events {
    worker_connections 1024;
}

http {
    server_names_hash_bucket_size 128;
    default_type application/octet-stream;

    types {
        text/html html htm;
        text/css css;
        application/javascript js;
        application/json json;
        image/png png;
        image/jpeg jpg jpeg;
        image/svg+xml svg;
        image/x-icon ico;
        font/woff2 woff2;
    }

    access_log %s;
    client_body_temp_path %s;
    proxy_temp_path %s;
    fastcgi_temp_path %s;

    include %s;
}
`, quoted(filepath.Join(i.Path, "logs", "nginx-error.log")), quoted(filepath.Join(i.Path, "nginx.pid")), quoted(filepath.Join(i.Path, "logs", "access.log")), quoted(filepath.Join(i.Path, "temp")), quoted(filepath.Join(i.Path, "temp")), quoted(filepath.Join(i.Path, "temp")), nginxInclude(state.Root))), nil
}
func renderSiteConfig(site Site, state State) ([]byte, error) {
	body := fmt.Sprintf("# PHPanel website: %s (%s)\nserver {\n    listen 127.0.0.1:%d;\n    server_name %s.localhost;\n    root %s;\n", site.Name, site.Template, site.Port, site.Name, quoted(site.Root))
	if site.Template == "static" {
		return []byte(body + `    index index.html index.htm;

    location / {
        try_files $uri $uri/ =404;
    }

    # Never serve PHP source files from a static website.
    location ~ \.php(?:/|$) {
        return 404;
    }

    location ~ /\. {
        deny all;
    }
}
`), nil
	}
	php, err := findSiteInstance(state, site.PHPID, "php")
	if err != nil {
		return nil, err
	}
	fallback := "/index.php?$query_string"
	if site.Template == "thinkphp" {
		fallback = "/index.php?s=$uri&$query_string"
	}
	body += fmt.Sprintf("    index index.php index.html;\n\n    location / {\n        try_files $uri $uri/ %s;\n    }\n", fallback)
	if site.Template == "wordpress" {
		body += "\n    location = /favicon.ico {\n        log_not_found off;\n        access_log off;\n    }\n\n    location = /robots.txt {\n        allow all;\n        log_not_found off;\n        access_log off;\n    }\n"
	}
	body += fmt.Sprintf("\n    location ~ \\.php$ {\n        try_files $uri =404;\n        fastcgi_pass 127.0.0.1:%d;\n", php.Port)
	params := [][2]string{{"SCRIPT_FILENAME", "$document_root$fastcgi_script_name"}, {"QUERY_STRING", "$query_string"}, {"REQUEST_METHOD", "$request_method"}, {"CONTENT_TYPE", "$content_type"}, {"CONTENT_LENGTH", "$content_length"}, {"SCRIPT_NAME", "$fastcgi_script_name"}, {"REQUEST_URI", "$request_uri"}, {"DOCUMENT_URI", "$document_uri"}, {"DOCUMENT_ROOT", "$document_root"}, {"SERVER_PROTOCOL", "$server_protocol"}, {"REQUEST_SCHEME", "$scheme"}, {"GATEWAY_INTERFACE", "CGI/1.1"}, {"SERVER_SOFTWARE", "nginx"}, {"REMOTE_ADDR", "$remote_addr"}, {"REMOTE_PORT", "$remote_port"}, {"SERVER_ADDR", "$server_addr"}, {"SERVER_PORT", "$server_port"}, {"SERVER_NAME", "$server_name"}, {"REDIRECT_STATUS", "200"}}
	for _, param := range params {
		body += fmt.Sprintf("        fastcgi_param %-17s %s;\n", param[0], param[1])
	}
	body += "    }\n\n    location ~ /\\. {\n        deny all;\n    }\n}\n"
	return []byte(body), nil
}

// Existing split configurations are never regenerated on service startup.
func (m *Manager) writeNginxConfig(i Instance) error {
	state := m.Snapshot()
	if err := m.migrateSiteConfigs(state); err != nil {
		return err
	}
	path := filepath.Join(i.Path, "config", "nginx.conf")
	previous, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	body, suffix := m.sharedNginxMain(i, state, previous)
	if !bytes.Equal(previous, body) {
		if len(previous) > 0 {
			if err := os.WriteFile(path+suffix, previous, 0600); err != nil {
				return err
			}
		}
		return atomicConfigWrite(path, body)
	}
	return nil
}

type siteConfigChange struct {
	instance Instance
	pkg      InstalledPackage
	path     string
	stage    string
	files    map[string][]byte
	previous map[string][]byte
	running  bool
}

func (m *Manager) validateNginx(i Instance, p InstalledPackage, path string) error {
	if m.nginxValidator != nil {
		return m.nginxValidator(i, p, path)
	}
	return runLogged(p.Executable, []string{"-p", normalized(i.Path) + "/", "-c", normalized(path), "-t"}, i.Path, filepath.Join(i.Path, "logs", "config-check.log"), 20*time.Second)
}
func (m *Manager) reloadSiteConfig(change siteConfigChange) error {
	if m.nginxReloader != nil {
		return m.nginxReloader(change.instance, change.pkg)
	}
	i := change.instance
	if i.ServiceName != "" && !m.serviceHost {
		return m.backendLocked().ReloadNginx(i)
	}
	return runLogged(change.pkg.Executable, []string{"-p", normalized(i.Path) + "/", "-c", normalized(change.path), "-s", "reload"}, i.Path, filepath.Join(i.Path, "logs", "site-reload.log"), 10*time.Second)
}
func (m *Manager) applySiteChanges(before State, sites []Site, affected []string) error {
	return m.applyNginxChanges(before, sites, affected, nil, true)
}

// Called under siteMu. Build and validate the entire candidate set before publishing.
func (m *Manager) applyNginxChanges(before State, sites []Site, affected []string, overrides map[string][]byte, commitSites bool) error {
	m.refreshSystemServices()
	shared := commitSites
	for name := range overrides {
		if name != "nginx.conf" {
			shared = true
		}
	}
	keys := m.siteConfigTargets(before, affected, shared)
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return fmt.Errorf("应用正在退出")
	}
	if m.operations["start:nginx"] {
		m.mu.Unlock()
		return fmt.Errorf("Nginx 正在启动，请稍后修改配置")
	}
	if m.operations["nginx-link"] {
		m.mu.Unlock()
		return fmt.Errorf("Nginx 正在启动关联 PHP，请稍后修改配置")
	}
	for _, key := range keys {
		if m.operations[key] {
			m.mu.Unlock()
			return fmt.Errorf("Nginx 实例正在启动或变更，请稍后重试")
		}
	}
	m.operations["nginx-config"] = true
	for _, key := range keys {
		m.operations[key] = true
	}
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer func() {
		m.mu.Lock()
		for _, key := range keys {
			delete(m.operations, key)
		}
		delete(m.operations, "nginx-config")
		m.mu.Unlock()
	}()
	candidate := cloneState(before)
	candidate.Sites = sites
	changes := []siteConfigChange{}
	defer func() {
		for _, c := range changes {
			if c.stage != "" {
				_ = os.RemoveAll(c.stage)
			}
		}
	}()
	sharedFiles, sharedPrevious, err := m.prepareSharedSiteFiles(before, sites, overrides)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		if !commitSites {
			return fmt.Errorf("请先创建 Nginx 实例用于配置检查")
		}
		changes = append(changes, siteConfigChange{files: sharedFiles, previous: sharedPrevious})
	}
	for _, key := range keys {
		i, p, err := m.lookupInstance(key)
		if err != nil {
			return err
		}
		if i.ServiceName != "" && i.Status != "running" && i.Status != "stopped" {
			return fmt.Errorf("Nginx 系统服务正在变更或无法查询，请确认服务状态后再修改配置")
		}
		m.mu.Lock()
		runningNow := m.processes[key] != nil || (i.ServiceName != "" && i.Status == "running")
		m.mu.Unlock()
		if runningNow && m.nginxValidator == nil {
			for _, site := range sites {
				alreadyListening := false
				for _, old := range before.Sites {
					if old.Port == site.Port {
						alreadyListening = true
						break
					}
				}
				if !alreadyListening {
					if err := checkPort(site.Port); err != nil {
						return err
					}
				}
			}
		}
		dir := filepath.Join(i.Path, "config")
		files := map[string][]byte{}
		previous := map[string][]byte{}
		for name, body := range sharedFiles {
			files[name] = body
		}
		for name, body := range sharedPrevious {
			previous[name] = body
		}
		main, err := os.ReadFile(filepath.Join(dir, "nginx.conf"))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		previous["nginx.conf"] = main
		main, _ = m.sharedNginxMain(i, candidate, main)
		if override, ok := overrides["nginx.conf"]; ok {
			main = override
		}
		files["nginx.conf"] = main
		stage, err := os.MkdirTemp(dir, ".site-change-")
		if err != nil {
			return err
		}
		m.mu.Lock()
		running := m.processes[key] != nil || (i.ServiceName != "" && i.Status == "running")
		m.mu.Unlock()
		change := siteConfigChange{instance: i, pkg: p, path: filepath.Join(dir, "nginx.conf"), stage: stage, files: files, previous: previous, running: running}
		changes = append(changes, change)
		if err := os.MkdirAll(filepath.Join(stage, "sites"), 0700); err != nil {
			return err
		}
		for name, body := range files {
			if name == "nginx.conf" {
				body = bytes.ReplaceAll(body, []byte(nginxInclude(m.root)), []byte(quoted(filepath.Join(stage, "sites", "*.conf"))))
			}
			if err := os.WriteFile(filepath.Join(stage, filepath.FromSlash(name)), body, 0600); err != nil {
				return err
			}
		}
		if err := m.validateNginx(i, p, filepath.Join(stage, "nginx.conf")); err != nil {
			return fmt.Errorf("Nginx %s 配置检查失败，修改未保存；请查看日志: %w", i.Name, err)
		}
	}
	published := 0
	restore := func() error {
		var failures []string
		for n := 0; n < published; n++ {
			c := changes[n]
			for name, body := range c.previous {
				path := m.nginxConfigPath(c.instance, name)
				var err error
				if body == nil {
					err = os.Remove(path)
					if os.IsNotExist(err) {
						err = nil
					}
				} else {
					err = atomicConfigWrite(path, body)
				}
				if err != nil {
					failures = append(failures, err.Error())
				}
			}
		}
		for n := 0; n < published; n++ {
			if changes[n].running {
				if err := m.reloadSiteConfig(changes[n]); err != nil {
					failures = append(failures, err.Error())
				}
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("恢复配置失败: %s", strings.Join(failures, "；"))
		}
		return nil
	}
	rollback := func(err error) error {
		if restoreErr := restore(); restoreErr != nil {
			return fmt.Errorf("%w；%v", err, restoreErr)
		}
		return err
	}
	for _, c := range changes {
		published++ // Includes partially published file sets in rollback.
		if err := os.MkdirAll(filepath.Join(m.root, "sites"), 0700); err != nil {
			return rollback(err)
		}
		for name, old := range c.previous {
			path := m.nginxConfigPath(c.instance, name)
			body, keep := c.files[name]
			if keep && bytes.Equal(body, old) {
				continue
			}
			if old != nil {
				if err := os.WriteFile(path+".before-site-change", old, 0600); err != nil {
					return rollback(err)
				}
			}
			if keep {
				if err := atomicConfigWrite(path, body); err != nil {
					return rollback(err)
				}
			} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return rollback(err)
			}
		}
	}
	for _, c := range changes {
		if c.running {
			if err := m.reloadSiteConfig(c); err != nil {
				return rollback(fmt.Errorf("Nginx 重载失败，正在恢复配置: %w", err))
			}
		}
	}
	if commitSites {
		if err := m.change(func(s *State) error { s.Sites = append([]Site{}, sites...); return nil }); err != nil {
			return rollback(err)
		}
	}
	return nil
}
func atomicConfigWrite(path string, body []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(body)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

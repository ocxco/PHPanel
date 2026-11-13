package panel

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func nginxInclude(root string) string { return quoted(filepath.Join(root, "sites", "*.conf")) }
func legacyNginxInclude(i Instance) string {
	return quoted(filepath.Join(i.Path, "config", "sites", "*.conf"))
}
func (m *Manager) nginxConfigPath(i Instance, name string) string {
	if name == "nginx.conf" {
		return filepath.Join(i.Path, "config", name)
	}
	return filepath.Join(m.root, filepath.FromSlash(name))
}
func (m *Manager) rebaseConfigBody(body []byte) []byte {
	if m.previousRoot == "" || m.previousRoot == m.root {
		return body
	}
	text := strings.ReplaceAll(string(body), strings.ReplaceAll(m.previousRoot, "\\", "/"), strings.ReplaceAll(m.root, "\\", "/"))
	return []byte(strings.ReplaceAll(text, m.previousRoot, m.root))
}
func (m *Manager) migrateSiteConfigs(state State) error {
	dir := filepath.Join(m.root, "sites")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for _, site := range state.Sites {
		if filepath.Base(site.ID) != site.ID || strings.ContainsAny(site.ID, "\\/:") {
			return fmt.Errorf("站点配置标识无效")
		}
		target := m.nginxConfigPath(Instance{}, siteConfigName(site))
		body, err := os.ReadFile(target)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			updated := m.rebaseConfigBody(body)
			if !bytes.Equal(body, updated) {
				if _, err := os.Stat(target + ".before-path-fix"); os.IsNotExist(err) {
					if err := os.WriteFile(target+".before-path-fix", body, 0600); err != nil {
						return err
					}
				}
				if err := atomicConfigWrite(target, updated); err != nil {
					return err
				}
			}
			continue
		}
		var source string
		candidates := []Instance{}
		for _, i := range state.Instances {
			if i.Component == "nginx" && i.ID == site.NginxID {
				candidates = append(candidates, i)
			}
		}
		for _, i := range state.Instances {
			if i.Component == "nginx" && i.ID != site.NginxID {
				candidates = append(candidates, i)
			}
		}
		for _, i := range candidates {
			path := filepath.Join(i.Path, "config", filepath.FromSlash(siteConfigName(site)))
			body, err = os.ReadFile(path)
			if err == nil {
				source = path
				break
			}
			if !os.IsNotExist(err) {
				return err
			}
		}
		if source == "" {
			body, err = renderSiteConfig(site, state)
			if err != nil {
				return err
			}
		}
		if source != "" {
			for _, suffix := range []string{".before-site-change", ".before-edit", ".before-path-fix"} {
				if _, err := os.Stat(target + suffix); err == nil {
					continue
				} else if !os.IsNotExist(err) {
					return err
				}
				backup, err := os.ReadFile(source + suffix)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return err
				}
				if err := atomicConfigWrite(target+suffix, backup); err != nil {
					return err
				}
			}
		}
		if err := atomicConfigWrite(target, m.rebaseConfigBody(body)); err != nil {
			return err
		}

	}
	return nil
}
func (m *Manager) sharedNginxMain(i Instance, state State, body []byte) ([]byte, string) {
	if bytes.Contains(body, []byte(nginxInclude(m.root))) {
		return body, ""
	}
	if bytes.Contains(body, []byte(legacyNginxInclude(i))) {
		return bytes.ReplaceAll(body, []byte(legacyNginxInclude(i)), []byte(nginxInclude(m.root))), ".before-sites-migration"
	}
	generated, _ := renderNginxConfig(i, state)
	return generated, ".before-split"
}
func sameSiteConfig(a, b Site) bool { a.NginxID = ""; b.NginxID = ""; return a == b }

func (m *Manager) prepareSharedSiteFiles(before State, sites []Site, overrides map[string][]byte) (map[string][]byte, map[string][]byte, error) {
	files := map[string][]byte{}
	previous := map[string][]byte{}
	for _, old := range before.Sites {
		previous[siteConfigName(old)] = nil
	}
	for _, site := range sites {
		name := siteConfigName(site)
		previous[name] = nil
		unchanged := false
		for _, old := range before.Sites {
			if sameSiteConfig(old, site) {
				unchanged = true
				break
			}
		}
		var body []byte
		var err error
		if unchanged {
			body, err = os.ReadFile(m.nginxConfigPath(Instance{}, name))
			if err != nil && !os.IsNotExist(err) {
				return nil, nil, err
			}
		}
		if body == nil {
			body, err = renderSiteConfig(site, before)
			if err != nil {
				return nil, nil, err
			}
		}
		files[name] = body
	}
	for name, body := range overrides {
		if name != "nginx.conf" {
			files[name] = body
		}
	}
	for name := range previous {
		body, err := os.ReadFile(m.nginxConfigPath(Instance{}, name))
		if err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
		previous[name] = body
	}
	// Extra user supplied .conf files must participate in syntax checks, but are
	// never edited or deleted as part of a managed website operation.
	entries, err := os.ReadDir(filepath.Join(m.root, "sites"))
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".conf") {
			continue
		}
		name := "sites/" + entry.Name()
		if _, managed := previous[name]; managed {
			continue
		}
		body, err := os.ReadFile(m.nginxConfigPath(Instance{}, name))
		if err != nil {
			return nil, nil, err
		}
		files[name] = body
	}
	return files, previous, nil
}

// Website configurations are shared. Use the active Nginx to check/reload them,
// or the requested validation version when all instances are stopped.
func (m *Manager) siteConfigTargets(before State, affected []string, shared bool) []string {
	if shared {
		m.mu.Lock()
		for _, i := range m.state.Instances {
			if i.Component == "nginx" && (m.processes[i.ID] != nil || i.ServiceName != "" && i.Status == "running") {
				m.mu.Unlock()
				return []string{i.ID}
			}
		}
		m.mu.Unlock()
	}
	for n := len(affected) - 1; n >= 0; n-- {
		for _, i := range before.Instances {
			if i.ID == affected[n] && i.Component == "nginx" {
				return []string{i.ID}
			}
		}
	}
	if shared {
		for _, i := range before.Instances {
			if i.Component == "nginx" {
				return []string{i.ID}
			}
		}
	}
	return nil
}

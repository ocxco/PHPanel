package panel

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

//go:embed php_extension_catalog.seed.json
var extensionSeedJSON []byte

func extensionSeed() []PHPExtensionArtifact {
	items := []PHPExtensionArtifact{}
	_ = json.Unmarshal(extensionSeedJSON, &items)
	return items
}

type PHPOnlineExtensions struct {
	Version   string                 `json:"version"`
	Build     string                 `json:"build"`
	ZendBuild string                 `json:"zendBuild"`
	Items     []PHPExtensionArtifact `json:"items"`
	UpdatedAt string                 `json:"updatedAt"`
	Warnings  []string               `json:"warnings"`
}

var peclVersionPattern = regexp.MustCompile(`href="([0-9]+\.[0-9]+\.[0-9]+)/"`)
var peclExtensionPattern = regexp.MustCompile(`^php_(redis|apcu|xdebug)-([0-9]+\.[0-9]+\.[0-9]+)-([0-9]+\.[0-9]+)-(nts|ts)-(vc[0-9]+|vs[0-9]+)-(x64|x86)\.zip$`)
var peclHrefPattern = regexp.MustCompile(`href="([^"/]+\.zip)"`)
var xdebugLinkPattern = regexp.MustCompile(`<a\s+title="SHA256:(?:&nbsp;|\s)*([a-fA-F0-9]{64})"\s+href=['"](/files/[^'"]+\.dll)['"]`)
var xdebugFilePattern = regexp.MustCompile(`^php_xdebug-([0-9]+\.[0-9]+\.[0-9]+)-([0-9]+\.[0-9]+)-(nts|ts)-(vs[0-9]+|vc[0-9]+)-x86_64\.dll$`)

func peclExtensionArtifacts(body []byte, name, version string) []PHPExtensionArtifact {
	out := []PHPExtensionArtifact{}
	seen := map[string]bool{}
	for _, href := range peclHrefPattern.FindAllStringSubmatch(string(body), 300) {
		file := href[1]
		parts := peclExtensionPattern.FindStringSubmatch(file)
		if len(parts) == 0 || parts[1] != name || parts[2] != version || seen[file] {
			continue
		}
		seen[file] = true
		bits := 64
		if parts[6] == "x86" {
			bits = 32
		}
		kind := "extension"
		if name == "xdebug" {
			kind = "zend"
		}
		a := PHPExtensionArtifact{Name: name, Version: version, PHPBranch: parts[3], ThreadSafe: parts[4] == "ts", Bits: bits, Compiler: parts[5], Kind: kind, Format: "zip", Publisher: "PHP Windows / PECL", Verification: "PHP 官方 HTTPS 来源；安装后记录 SHA-256", Sources: []Source{{Name: "PHP Windows 官方", URL: "https://downloads.php.net/~windows/pecl/releases/" + name + "/" + version + "/" + file}}}
		a.ID = extensionArtifactID(a)
		out = append(out, a)
	}
	return out
}
func xdebugExtensionArtifacts(body []byte) []PHPExtensionArtifact {
	out := []PHPExtensionArtifact{}
	seen := map[string]bool{}
	for _, link := range xdebugLinkPattern.FindAllStringSubmatch(string(body), 200) {
		parts := xdebugFilePattern.FindStringSubmatch(strings.TrimPrefix(link[2], "/files/"))
		if len(parts) == 0 {
			continue
		}
		a := PHPExtensionArtifact{Name: "xdebug", Version: parts[1], PHPBranch: parts[2], ThreadSafe: parts[3] == "ts", Bits: 64, Compiler: parts[4], Kind: "zend", Format: "dll", Publisher: "Xdebug 官方", SHA256: strings.ToLower(link[1]), Verification: "Xdebug 官方发布 SHA-256", Sources: []Source{{Name: "Xdebug 官方", URL: "https://xdebug.org" + link[2]}}}
		a.ID = extensionArtifactID(a)
		if !seen[a.ID] {
			out = append(out, a)
			seen[a.ID] = true
		}
	}
	return out
}
func fetchPHPOnlineCatalog(ctx context.Context, client *http.Client, build PHPProbe) ([]PHPExtensionArtifact, []string) {
	type result struct {
		items   []PHPExtensionArtifact
		warning string
	}
	results := make(chan result, 3)
	for _, name := range []string{"redis", "apcu", "xdebug"} {
		go func(name string) {
			if name == "xdebug" {
				body, err := catalogueBody(ctx, client, "https://xdebug.org/download", 2<<20)
				if err == nil {
					items := xdebugExtensionArtifacts(body)
					compatible := false
					for _, a := range items {
						if extensionCompatible(a, build) {
							compatible = true
						}
					}
					if compatible {
						results <- result{items: items}
						return
					}
				}
			}
			root := "https://downloads.php.net/~windows/pecl/releases/" + name + "/"
			body, err := catalogueBody(ctx, client, root, 2<<20)
			if err != nil {
				results <- result{warning: name + " 目录更新失败：" + err.Error()}
				return
			}
			versions := []string{}
			seen := map[string]bool{}
			for _, match := range peclVersionPattern.FindAllStringSubmatch(string(body), 300) {
				if !seen[match[1]] {
					versions = append(versions, match[1])
					seen[match[1]] = true
				}
			}
			sort.Slice(versions, func(i, j int) bool { return versionLess(versions[j], versions[i]) })
			if len(versions) > 4 {
				versions = versions[:4]
			}
			// PHP 7.4's last supported Xdebug branch remains available upstream.
			if name == "xdebug" && phpBranch(build.Version) == "7.4" {
				for _, match := range peclVersionPattern.FindAllStringSubmatch(string(body), 300) {
					if strings.HasPrefix(match[1], "3.1.") {
						versions = append(versions, match[1])
					}
				}
				sort.Slice(versions, func(i, j int) bool { return versionLess(versions[j], versions[i]) })
			}
			items := []PHPExtensionArtifact{}
			var lastErr error
			for _, version := range versions {
				page, err := catalogueBody(ctx, client, root+version+"/", 2<<20)
				if err != nil {
					lastErr = err
					continue
				}
				candidates := peclExtensionArtifacts(page, name, version)
				matched := false
				for _, a := range candidates {
					if extensionCompatible(a, build) {
						matched = true
					}
				}
				items = append(items, candidates...)
				if matched {
					break
				}
			}
			warning := ""
			if len(items) == 0 && lastErr != nil {
				warning = name + " 目录更新失败：" + lastErr.Error()
			}
			results <- result{items, warning}
		}(name)
	}
	items := []PHPExtensionArtifact{}
	warnings := []string{}
	for n := 0; n < 3; n++ {
		r := <-results
		items = append(items, r.items...)
		if r.warning != "" {
			warnings = append(warnings, r.warning)
		}
	}
	return items, warnings
}

func (m *Manager) GetPHPOnlineExtensions(key string, refresh bool) (PHPOnlineExtensions, error) {
	i, p, release, err := m.reservePHPOperation(key)
	if err != nil {
		return PHPOnlineExtensions{}, err
	}
	base, _, err := probePHP(p, "", "", i.Path)
	release()
	if err != nil {
		return PHPOnlineExtensions{}, err
	}
	buildKey := extensionBuildKey(base)
	warnings := []string{}
	if refresh {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		m.mu.Lock()
		if m.closing || m.extensionRefresh[buildKey] != nil {
			m.mu.Unlock()
			cancel()
			return PHPOnlineExtensions{}, fmt.Errorf("扩展目录正在刷新或应用正在退出")
		}
		m.extensionRefresh[buildKey] = cancel
		m.wg.Add(1)
		m.mu.Unlock()
		defer func() { cancel(); m.mu.Lock(); delete(m.extensionRefresh, buildKey); m.mu.Unlock(); m.wg.Done() }()
		client := m.httpClient()
		defer client.CloseIdleConnections()
		fetcher := m.extensionCatalogFetcher
		if fetcher == nil {
			fetcher = fetchPHPOnlineCatalog
		}
		items, notices := fetcher(ctx, client, base)
		warnings = notices
		if err = m.change(func(s *State) error {
			mergeExtensionArtifacts(s, items)
			if len(warnings) == 0 && len(items) > 0 {
				s.ExtensionCatalogUpdated[buildKey] = timestamp()
			}
			return nil
		}); err != nil {
			return PHPOnlineExtensions{}, err
		}
	}
	state := m.Snapshot()
	items := []PHPExtensionArtifact{}
	for _, a := range state.ExtensionArtifacts {
		if extensionCompatible(a, base) {
			items = append(items, a)
		}
	}
	sort.Slice(items, func(a, b int) bool {
		if items[a].Name != items[b].Name {
			return items[a].Name < items[b].Name
		}
		if items[a].Version != items[b].Version {
			return versionLess(items[b].Version, items[a].Version)
		}
		return items[a].Format == "dll" && items[b].Format != "dll"
	})
	return PHPOnlineExtensions{Version: base.Version, Build: base.Build, ZendBuild: base.ZendBuild, Items: items, Warnings: warnings, UpdatedAt: state.ExtensionCatalogUpdated[buildKey]}, nil
}
func trustedExtensionSource(address string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Host == "downloads.php.net" {
		return strings.HasPrefix(u.Path, "/~windows/pecl/releases/") && peclExtensionPattern.MatchString(u.Path[strings.LastIndex(u.Path, "/")+1:])
	}
	return u.Host == "xdebug.org" && strings.HasPrefix(u.Path, "/files/") && xdebugFilePattern.MatchString(strings.TrimPrefix(u.Path, "/files/"))
}
func (m *Manager) AddPHPExtensionMirror(artifactID, name, address, hash string) error {
	if !validLabel(name) || !validCatalogURL(address) {
		return fmt.Errorf("镜像名称不能为空，地址须为 HTTPS")
	}
	return m.change(func(s *State) error {
		for n := range s.ExtensionArtifacts {
			a := &s.ExtensionArtifacts[n]
			if a.ID != artifactID {
				continue
			}
			if a.SHA256 == "" {
				if !validHash(hash) {
					return fmt.Errorf("此构建尚无已知摘要，请填写可信的 SHA-256 后再使用镜像")
				}
				a.SHA256 = strings.ToLower(hash)
				a.Verification = "用户提供 SHA-256"
			} else if hash != "" && !strings.EqualFold(hash, a.SHA256) {
				return fmt.Errorf("镜像摘要与已有制品不一致")
			}
			if len(a.Sources) >= 12 {
				return fmt.Errorf("下载来源最多 12 个")
			}
			for _, s := range a.Sources {
				if s.URL == address {
					return nil
				}
			}
			a.Sources = append(a.Sources, Source{Name: "自定义 · " + name, URL: address})
			return nil
		}
		return fmt.Errorf("扩展制品不存在")
	})
}

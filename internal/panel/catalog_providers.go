package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

type catalogProvider struct {
	name  string
	fetch func(context.Context, *http.Client) ([]Artifact, error)
}
type providerResult struct {
	name  string
	items []Artifact
	err   error
}

func validCatalogURL(address string) bool {
	if !validURL(address) {
		return false
	}
	u, _ := url.Parse(address)
	return u.Scheme == "https" || u.Hostname() == "localhost" || net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback()
}
func catalogueBody(ctx context.Context, client *http.Client, address string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "PHPanel/0.1 package-catalogue")
	if req.URL.Hostname() == "dev.mysql.com" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) PHPanel/0.1")
	}
	req.Header.Set("Accept", "application/json,text/html,text/plain,*/*")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接失败或超时")
	}
	defer res.Body.Close()
	if req.URL.Scheme == "https" && res.Request.URL.Scheme != "https" {
		return nil, fmt.Errorf("目录或签名请求拒绝 HTTPS 降级")
	}
	if (req.URL.Hostname() == "repo.mysql.com" || req.URL.Hostname() == "nginx.org") && res.Request.URL.Hostname() != req.URL.Hostname() {
		return nil, fmt.Errorf("官方公钥或目录请求重定向到非预期来源")
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("目录响应超过大小限制")
	}
	return body, nil
}
func phpArtifacts(body []byte) ([]Artifact, error) {
	var releases map[string]map[string]json.RawMessage
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, err
	}
	items := []Artifact{}
	for _, release := range releases {
		var version string
		_ = json.Unmarshal(release["version"], &version)
		for build, raw := range release {
			if !strings.HasPrefix(build, "nts-") || !strings.HasSuffix(build, "-x64") {
				continue
			}
			var item struct {
				Zip struct {
					Path string `json:"path"`
					Hash string `json:"sha256"`
				} `json:"zip"`
			}
			if json.Unmarshal(raw, &item) != nil || !validHash(item.Zip.Hash) || path.Base(item.Zip.Path) != item.Zip.Path || !strings.HasSuffix(item.Zip.Path, ".zip") {
				continue
			}
			a := Artifact{Component: "php", Version: version, Build: build, Publisher: "PHP 官方", SHA256: strings.ToLower(item.Zip.Hash), Verification: "PHP 官方发布摘要", Sources: []Source{{Name: "PHP 官方", URL: "https://downloads.php.net/~windows/releases/" + item.Zip.Path}}}
			a.ID = artifactID(a)
			if validateArtifact(a) == nil {
				items = append(items, a)
			}
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("未找到带 SHA-256 的 Windows PHP 包")
	}
	return items, nil
}
func redisArtifacts(body []byte) ([]Artifact, error) {
	var releases []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, err
	}
	items := []Artifact{}
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		for _, file := range release.Assets {
			hash := strings.TrimPrefix(file.Digest, "sha256:")
			if !strings.HasSuffix(file.Name, "x64-cygwin.zip") || !validHash(hash) {
				continue
			}
			a := Artifact{Component: "redis", Version: strings.TrimPrefix(release.Tag, "v"), Build: "Windows x64 / Cygwin", Publisher: "redis-windows 社区", SHA256: hash, Verification: "GitHub Release SHA-256", Sources: []Source{{Name: "GitHub 上游发行页", URL: file.URL}}}
			a.ID = artifactID(a)
			if validateArtifact(a) == nil {
				items = append(items, a)
			}
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("未找到带 SHA-256 的社区 Redis Windows 包")
	}
	return items, nil
}

var nginxFilePattern = regexp.MustCompile(`nginx-([0-9]+\.[0-9]+\.[0-9]+)\.zip\b`)
var mysqlFilePattern = regexp.MustCompile(`mysql-([0-9]+\.[0-9]+\.[0-9]+)-winx64\.zip\b`)
var versionSelectPattern = regexp.MustCompile(`(?is)<select\b[^>]*\bid\s*=\s*["']version["'][^>]*>(.*?)</select>`)
var branchOptionPattern = regexp.MustCompile(`(?i)<option\b[^>]*\bvalue\s*=\s*["']([0-9]+\.[0-9]+)["']`)
var fullVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func nginxArtifacts(body []byte) ([]Artifact, error) {
	seen := map[string]bool{}
	items := []Artifact{}
	for _, match := range nginxFilePattern.FindAllStringSubmatch(string(body), 200) {
		version := match[1]
		if seen[version] {
			continue
		}
		seen[version] = true
		address := "https://nginx.org/download/nginx-" + version + ".zip"
		a := Artifact{Component: "nginx", Version: version, Build: "Windows x86 / x64 兼容", Publisher: "Nginx 官方", Verification: "安装时核验官方 OpenPGP 签名", SignatureAuthority: "nginx", SignatureURL: address + ".asc", Sources: []Source{{Name: "Nginx 官方", URL: address}}}
		a.ID = artifactID(a)
		items = append(items, a)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("未发现 Nginx Windows ZIP，保留旧目录")
	}
	return items, nil
}
func mysqlBranches(body []byte) []string {
	matches := versionSelectPattern.FindStringSubmatch(string(body))
	if len(matches) < 2 {
		return nil
	}
	seen := map[string]bool{}
	branches := []string{}
	for _, match := range branchOptionPattern.FindAllStringSubmatch(matches[1], 12) {
		if !seen[match[1]] {
			seen[match[1]] = true
			branches = append(branches, match[1])
		}
	}
	return branches
}
func mysqlArtifacts(body []byte) ([]Artifact, error) {
	seen := map[string]bool{}
	items := []Artifact{}
	for _, match := range mysqlFilePattern.FindAllStringSubmatch(string(body), 200) {
		version := match[1]
		if seen[version] {
			continue
		}
		seen[version] = true
		a := mysqlVersionArtifact(version, "官方页面")
		items = append(items, a)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("未发现 MySQL Windows x64 ZIP，保留旧目录")
	}
	return items, nil
}
func mysqlVersionArtifact(version, provenance string) Artifact {
	parts := strings.Split(version, ".")
	branch := strings.Join(parts[:2], ".")
	filename := "mysql-" + version + "-winx64.zip"
	address := "https://cdn.mysql.com/Downloads/MySQL-" + branch + "/" + filename
	a := Artifact{Component: "mysql", Version: version, Build: "Windows x64 / " + branch, Publisher: "MySQL Community 官方", Verification: "安装时核验官方 OpenPGP 签名", SignatureAuthority: "mysql", SignatureURL: address + ".asc", Sources: []Source{{Name: "MySQL 官方 CDN（" + provenance + "）", URL: address}, {Name: "MySQL 官方归档", URL: "https://cdn.mysql.com/archives/mysql-" + branch + "/" + filename}}}
	a.ID = artifactID(a)
	return a
}
func fetchMySQL(ctx context.Context, client *http.Client) ([]Artifact, error) {
	body, rootErr := catalogueBody(ctx, client, "https://dev.mysql.com/downloads/mysql/?os=3", 2<<20)
	if rootErr != nil {
		return mysqlFallback(ctx, client, rootErr)
	}
	items, parseErr := mysqlArtifacts(body)
	branches := mysqlBranches(body)
	warnings := []string{}
	if parseErr != nil {
		warnings = append(warnings, parseErr.Error())
	}
	results := make(chan providerResult, len(branches))
	semaphore := make(chan struct{}, 3)
	for _, branch := range branches {
		go func(branch string) {
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				results <- providerResult{err: ctx.Err()}
				return
			}
			defer func() { <-semaphore }()
			body, err := catalogueBody(ctx, client, "https://dev.mysql.com/downloads/mysql/"+branch+".html?os=3", 2<<20)
			var entries []Artifact
			if err == nil {
				entries, err = mysqlArtifacts(body)
			}
			results <- providerResult{name: branch, items: entries, err: err}
		}(branch)
	}
	for range branches {
		result := <-results
		items = append(items, result.items...)
		if result.err != nil {
			warnings = append(warnings, result.name+": "+result.err.Error())
		}
	}
	if len(items) == 0 {
		return mysqlFallback(ctx, client, fmt.Errorf("官方页面格式变化或请求失败"))
	}
	if len(warnings) > 0 {
		return items, fmt.Errorf("部分分支更新失败: %s", strings.Join(warnings, "；"))
	}
	return items, nil
}
func mysqlFallback(ctx context.Context, client *http.Client, primaryErr error) ([]Artifact, error) {
	// These maintained package-manager feeds discover versions only. Their MSI
	// hashes are never reused for ZIPs; ZIP URLs and signatures are checked.
	feeds := []string{"https://raw.githubusercontent.com/ScoopInstaller/Main/master/bucket/mysql.json", "https://raw.githubusercontent.com/ScoopInstaller/Main/master/bucket/mysql-lts.json"}
	items := []Artifact{}
	for _, address := range feeds {
		body, err := catalogueBody(ctx, client, address, 256<<10)
		if err != nil {
			continue
		}
		var feed struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(body, &feed) != nil || !fullVersionPattern.MatchString(feed.Version) {
			continue
		}
		a := mysqlVersionArtifact(feed.Version, "自动发现")
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, a.Sources[0].URL, nil)
		if err != nil {
			continue
		}
		response, err := client.Do(req)
		if err != nil {
			continue
		}
		response.Body.Close()
		if response.StatusCode == 200 {
			items = append(items, a)
		}
	}
	if len(items) == 0 {
		return nil, primaryErr
	}
	return items, fmt.Errorf("官方页面暂不可达，使用备用版本发现源: %v", primaryErr)
}
func remoteArtifacts(body []byte) ([]Artifact, error) {
	var document struct {
		SchemaVersion int        `json:"schemaVersion"`
		Artifacts     []Artifact `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, err
	}
	if document.SchemaVersion != 1 {
		return nil, fmt.Errorf("目录 schemaVersion 必须为 1")
	}
	if len(document.Artifacts) == 0 || len(document.Artifacts) > 5000 {
		return nil, fmt.Errorf("目录需要包含 1–5000 个制品")
	}
	for index := range document.Artifacts {
		a := &document.Artifacts[index]
		a.SHA256 = strings.ToLower(a.SHA256)
		if err := validateArtifact(*a); err != nil {
			return nil, fmt.Errorf("条目 %d: %w", index+1, err)
		}
		a.ID = artifactID(*a)
		if a.Verification == "" {
			a.Verification = "目录源 SHA-256"
		}
		if !validHash(a.SHA256) {
			a.Verification = "安装时核验官方 OpenPGP 签名"
		}
		if a.Publisher == "" {
			a.Publisher = "自定义目录"
		}
		if a.Build == "" {
			a.Build = "Windows x64"
		}
	}
	return document.Artifacts, nil
}
func defaultProviders(settings Settings) []catalogProvider {
	jsonProvider := func(address string, parse func([]byte) ([]Artifact, error)) func(context.Context, *http.Client) ([]Artifact, error) {
		return func(ctx context.Context, client *http.Client) ([]Artifact, error) {
			body, err := catalogueBody(ctx, client, address, 4<<20)
			if err != nil {
				return nil, err
			}
			return parse(body)
		}
	}
	providers := []catalogProvider{{"PHP", jsonProvider("https://downloads.php.net/~windows/releases/releases.json", phpArtifacts)}, {"Redis", jsonProvider("https://api.github.com/repos/redis-windows/redis-windows/releases?per_page=50", redisArtifacts)}, {"Nginx", jsonProvider("https://nginx.org/en/download.html", nginxArtifacts)}, {"MySQL", fetchMySQL}}
	for index, address := range settings.CatalogURLs {
		providers = append(providers, catalogProvider{fmt.Sprintf("额外目录 %d", index+1), jsonProvider(address, remoteArtifacts)})
	}
	return providers
}

func sameRelease(a, b Artifact) bool {
	if a.Component != b.Component || a.Version != b.Version {
		return false
	}
	if a.ID == b.ID || validHash(a.SHA256) && a.SHA256 == b.SHA256 {
		return true
	}
	for _, x := range a.Sources {
		u, err := url.Parse(x.URL)
		if err != nil {
			continue
		}
		for _, y := range b.Sources {
			v, err := url.Parse(y.URL)
			if err == nil && path.Base(u.Path) == path.Base(v.Path) {
				return true
			}
		}
	}
	return false
}
func mergeLiveCatalog(state *State, items []Artifact) int {
	added := 0
	for _, item := range items {
		if validateArtifact(item) != nil {
			continue
		}
		item.Sources = append([]Source{}, item.Sources...)
		found := -1
		for n, old := range state.Artifacts {
			if sameRelease(old, item) {
				found = n
				break
			}
		}
		if found < 0 {
			item.ID = artifactID(item)
			state.Artifacts = append(state.Artifacts, item)
			added++
			continue
		}
		old := &state.Artifacts[found]
		// Keep previously pinned hashes and user-owned source ordering. A
		// hash disagreement becomes a distinct release, never an overwrite.
		if validHash(old.SHA256) && validHash(item.SHA256) && old.SHA256 != item.SHA256 {
			item.ID = artifactID(item)
			already := false
			for _, a := range state.Artifacts {
				if a.ID == item.ID {
					already = true
				}
			}
			if !already {
				state.Artifacts = append(state.Artifacts, item)
				added++
			}
			continue
		}
		if !validHash(old.SHA256) && validHash(item.SHA256) {
			old.SHA256 = item.SHA256
			old.Verification = item.Verification
		}
		if old.SignatureURL == "" && validSignatureMetadata(item) {
			old.SignatureURL = item.SignatureURL
			old.SignatureAuthority = item.SignatureAuthority
		}
		for _, source := range item.Sources {
			known := false
			for _, previous := range old.Sources {
				if previous.URL == source.URL {
					known = true
				}
			}
			if !known && len(old.Sources) < 12 {
				old.Sources = append(old.Sources, source)
			}
		}
	}
	sort.SliceStable(state.Artifacts, func(i, j int) bool {
		a, b := state.Artifacts[i], state.Artifacts[j]
		if a.Component != b.Component {
			return a.Component < b.Component
		}
		return versionLess(b.Version, a.Version)
	})
	return added
}
func (m *Manager) RefreshCatalog(ctx context.Context) ([]string, error) {
	return m.refreshWithProviders(ctx, defaultProviders(m.Snapshot().Settings))
}
func (m *Manager) refreshWithProviders(parent context.Context, providers []catalogProvider) ([]string, error) {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return nil, fmt.Errorf("应用正在退出")
	}
	if m.refreshCancel != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("版本目录正在更新")
	}
	ctx, cancel := context.WithTimeout(parent, 55*time.Second)
	m.refreshCancel = cancel
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer cancel()
	defer func() { m.mu.Lock(); m.refreshCancel = nil; m.mu.Unlock() }()
	if err := m.change(func(s *State) error { s.Catalog.Refreshing = true; s.Catalog.LastAttempt = timestamp(); return nil }); err != nil {
		return nil, err
	}
	client := m.httpClient()
	defer client.CloseIdleConnections()
	results := make(chan providerResult, len(providers))
	semaphore := make(chan struct{}, 4)
	for _, provider := range providers {
		go func(provider catalogProvider) {
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				results <- providerResult{name: provider.name, err: ctx.Err()}
				return
			}
			defer func() { <-semaphore }()
			items, err := provider.fetch(ctx, client)
			results <- providerResult{name: provider.name, items: items, err: err}
		}(provider)
	}
	warnings := []string{}
	items := []Artifact{}
	successful := false
	for range providers {
		result := <-results
		items = append(items, result.items...)
		if len(result.items) > 0 {
			successful = true
		}
		if result.err != nil {
			warnings = append(warnings, result.name+" 更新失败或不完整，已保留缓存: "+result.err.Error())
		}
	}
	sort.Strings(warnings)
	err := m.change(func(s *State) error {
		s.Catalog.Refreshing = false
		s.Catalog.Warnings = warnings
		s.Catalog.Added = mergeLiveCatalog(s, items)
		if successful {
			s.Catalog.LastSuccess = timestamp()
		}
		return nil
	})
	return warnings, err
}
func catalogDue(settings Settings, status CatalogStatus, now time.Time) bool {
	if !settings.CatalogAutoUpdate || status.Refreshing {
		return false
	}
	last, err := time.Parse(time.RFC3339, status.LastAttempt)
	if err != nil {
		return true
	}
	hours := settings.CatalogUpdateHours
	if hours < 1 {
		hours = 6
	}
	return now.Sub(last) >= time.Duration(hours)*time.Hour
}
func (m *Manager) StartCatalogUpdater(parent context.Context) {
	m.mu.Lock()
	if m.closing || m.updaterStarted {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.catalogCancel = cancel
	m.updaterStarted = true
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		defer cancel()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			state := m.Snapshot()
			if catalogDue(state.Settings, state.Catalog, time.Now()) {
				_, _ = m.RefreshCatalog(ctx)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

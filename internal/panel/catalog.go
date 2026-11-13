package panel

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed catalog.seed.json
var catalogJSON []byte

func seedCatalog() []Artifact {
	var items []Artifact
	_ = json.Unmarshal(catalogJSON, &items)
	return items
}

// New releases of PHPanel may add verified artifacts. Stored user catalogues
// must not replace those additions, and custom sources for existing IDs win.
func mergeCatalog(state *State, artifacts []Artifact) int {
	known := make(map[string]bool, len(state.Artifacts))
	for _, a := range state.Artifacts {
		known[a.ID] = true
	}
	added := 0
	for _, a := range artifacts {
		if known[a.ID] {
			continue
		}
		copy := a
		copy.Sources = append([]Source{}, a.Sources...)
		state.Artifacts = append(state.Artifacts, copy)
		known[a.ID] = true
		added++
	}
	return added
}
func (m *Manager) syncMySQLCatalog() error {
	items := []Artifact{}
	known := map[string]bool{}
	for _, a := range m.Snapshot().Artifacts {
		known[a.ID] = true
	}
	for _, a := range seedCatalog() {
		if a.Component == "mysql" && !known[a.ID] {
			items = append(items, a)
		}
	}
	if len(items) == 0 {
		return nil
	}
	return m.change(func(state *State) error { mergeCatalog(state, items); return nil })
}
func validHash(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && len(s) == 64
}
func validURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Host != "" && u.User == nil && (u.Scheme == "https" || u.Scheme == "http")
}
func signedArtifactID(a Artifact) string {
	sum := sha256.Sum256([]byte(a.SignatureURL))
	return a.Component + "-signed-" + hex.EncodeToString(sum[:])[:24]
}
func artifactID(a Artifact) string {
	if validHash(a.SHA256) {
		if validSignatureMetadata(a) && a.ID == signedArtifactID(a) {
			return a.ID
		}
		return a.Component + "-" + strings.ToLower(a.SHA256[:24])
	}
	if validSignatureMetadata(a) {
		return signedArtifactID(a)
	}
	return ""
}
func validateArtifact(a Artifact) error {
	if !validComponent(a.Component) || !validLabel(a.Version) {
		return fmt.Errorf("请选择支持的组件并填写版本")
	}
	if !validHash(a.SHA256) && !validSignatureMetadata(a) {
		return fmt.Errorf("在线安装需要 SHA-256 摘要或受支持的官方签名")
	}
	if len(a.Sources) == 0 || len(a.Sources) > 12 {
		return fmt.Errorf("请提供 1–12 个相同安装包的下载来源")
	}
	for _, s := range a.Sources {
		if !validLabel(s.Name) || !validURL(s.URL) {
			return fmt.Errorf("来源名称或 HTTP(S) 地址无效（地址中不能包含用户名密码）")
		}
	}
	return nil
}
func (m *Manager) AddArtifact(a Artifact) error {
	a.SHA256 = strings.ToLower(strings.TrimSpace(a.SHA256))
	if err := validateArtifact(a); err != nil {
		return err
	}
	a.ID = artifactID(a)
	if a.Publisher == "" {
		a.Publisher = "用户提供"
	}
	if a.Verification == "" {
		a.Verification = "用户提供 SHA-256"
	}
	if a.Build == "" {
		a.Build = "Windows x64"
	}
	return m.change(func(s *State) error {
		for i := range s.Artifacts {
			if s.Artifacts[i].ID == a.ID {
				s.Artifacts[i] = a
				return nil
			}
		}
		s.Artifacts = append(s.Artifacts, a)
		return nil
	})
}
func (m *Manager) SaveSettings(settings Settings) error {
	if settings.ThemeMode == "" {
		settings.ThemeMode = m.Snapshot().Settings.ThemeMode
		if settings.ThemeMode == "" {
			settings.ThemeMode = "system"
		}
	}
	if !validTheme(settings.ThemeMode) {
		return fmt.Errorf("未知主题模式")
	}
	if settings.CatalogUpdateHours == 0 {
		settings.CatalogUpdateHours = 6
	}
	if settings.CatalogUpdateHours < 1 || settings.CatalogUpdateHours > 168 {
		return fmt.Errorf("目录更新间隔应在 1–168 小时之间")
	}
	if len(settings.CatalogURLs) > 8 {
		return fmt.Errorf("最多配置 8 个额外目录源")
	}
	for _, address := range settings.CatalogURLs {
		if !validCatalogURL(address) {
			return fmt.Errorf("目录源需要 HTTPS 地址（本机测试可使用 HTTP）")
		}
	}
	if settings.ProxyMode != "system" && settings.ProxyMode != "direct" && settings.ProxyMode != "custom" {
		return fmt.Errorf("未知代理模式")
	}
	if settings.SourceMode != "official" && settings.SourceMode != "custom" {
		return fmt.Errorf("未知来源优先级")
	}
	if settings.ProxyMode == "custom" {
		u, err := url.Parse(settings.ProxyURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") {
			return fmt.Errorf("代理地址须为 http://、https:// 或 socks5://")
		}
	}
	return m.change(func(s *State) error { settings.System = s.Settings.System; s.Settings = settings; return nil })
}
func (m *Manager) httpClient() *http.Client {
	m.mu.Lock()
	settings := m.state.Settings
	m.mu.Unlock()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 20 * time.Second
	transport.TLSHandshakeTimeout = 15 * time.Second
	transport.Proxy = systemProxy
	switch settings.ProxyMode {
	case "direct":
		transport.Proxy = nil
	case "custom":
		u, err := url.Parse(settings.ProxyURL)
		if err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Transport: transport}
}
func getJSON(ctx context.Context, client *http.Client, address string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "PHPanel/0.1")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out)
}
func (m *Manager) mergeDiscovered(a Artifact) error {
	a.ID = artifactID(a)
	for _, old := range m.Snapshot().Artifacts {
		if old.ID == a.ID {
			return nil
		}
	}
	return m.AddArtifact(a)
}
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		var x, y int
		_, _ = fmt.Sscanf(as[i], "%d", &x)
		_, _ = fmt.Sscanf(bs[i], "%d", &y)
		if x != y {
			return x < y
		}
	}
	return len(as) < len(bs)
}

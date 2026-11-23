package panel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	detachedPHP             bool
	ownedDetachedPHP        map[string]bool
	phpHostProtected        bool
	phpHostStopping         bool
	nginxLinkMu             sync.Mutex
	linkedNginxStarter      func(string, *runtimeLease) error
	phpRuntime              phpRuntimeBackend
	phpHostOwner            string
	phpHostRun              string
	remoteRestartOwners     map[string]phpRuntimeStatus
	systemServices          systemServiceBackend
	serviceMu               sync.Mutex
	serviceHost             bool
	siteMu                  sync.Mutex
	nginxValidator          func(Instance, InstalledPackage, string) error
	nginxReloader           func(Instance, InstalledPackage) error
	previousRoot            string
	mu                      sync.Mutex
	root                    string
	state                   State
	downloads               map[string]context.CancelFunc
	processes               map[string]*managedProcess
	operations              map[string]bool
	wg                      sync.WaitGroup
	closing                 bool
	stateLock               *os.File
	onChange                func()
	catalogCancel           context.CancelFunc
	refreshCancel           context.CancelFunc
	updaterStarted          bool
	extensionRefresh        map[string]context.CancelFunc
	extensionCatalogFetcher func(context.Context, *http.Client, PHPProbe) ([]PHPExtensionArtifact, []string)
	signatureVerifier       func(context.Context, *http.Client, Artifact, string) error
	userPathReader          func() (string, string, error)
	userPathWriter          func(string) error
	cliHelperPath           string
	composerFetcher         func(context.Context, *http.Client) (ComposerArtifact, error)
}

func New(root string) (*Manager, error) {
	return newManager(root, "")
}
func NewWithRuntimeUserSID(root, sid string) (*Manager, error) {
	if err := validateRuntimeUserSID(sid); err != nil {
		return nil, err
	}
	return newManager(root, sid)
}
func newManager(root, requestedSID string) (*Manager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{"state", "packages", "instances", "sites", "downloads/cache", "downloads/partial"} {
		if err := os.MkdirAll(filepath.Join(abs, dir), 0700); err != nil {
			return nil, err
		}
	}
	abs, err = PhysicalPath(abs)
	if err != nil {
		return nil, fmt.Errorf("无法解析真实数据目录: %w", err)
	}
	lock, err := lockDataRoot(filepath.Join(abs, "state", "panel.lock"))
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = lock.Close()
		}
	}()
	m := &Manager{root: abs, downloads: map[string]context.CancelFunc{}, processes: map[string]*managedProcess{}, operations: map[string]bool{}, stateLock: lock}
	if helper, err := serviceHelperPath(); err == nil {
		if _, err := os.Stat(helper); err == nil {
			m.detachedPHP = true
		}
	}
	m.state = State{Root: abs, Artifacts: seedCatalog(), Packages: []InstalledPackage{}, Instances: []Instance{}, Sites: []Site{}, Tasks: []Task{}, Settings: Settings{ProxyMode: "system", SourceMode: "official", CatalogAutoUpdate: true, CatalogUpdateHours: 6, CatalogURLs: []string{}}, Catalog: CatalogStatus{Warnings: []string{}}}
	content, err := os.ReadFile(m.statePath())
	if err == nil {
		if err = json.Unmarshal(content, &m.state); err != nil {
			return nil, fmt.Errorf("读取元数据失败（保留原文件）: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	mergeCatalog(&m.state, seedCatalog())
	if requestedSID != "" {
		m.state.RuntimeUserSID = requestedSID
	} else if sid, err := currentRuntimeUserSID(); err == nil {
		m.state.RuntimeUserSID = sid
	}
	mergeExtensionArtifacts(&m.state, extensionSeed())
	if m.state.ExtensionPackages == nil {
		m.state.ExtensionPackages = []InstalledPHPExtension{}
	}
	if m.state.PreparedExtensions == nil {
		m.state.PreparedExtensions = []PreparedPHPExtension{}
	}
	if m.state.ExtensionCatalogUpdated == nil {
		m.state.ExtensionCatalogUpdated = map[string]string{}
	}
	m.extensionRefresh = map[string]context.CancelFunc{}
	m.previousRoot = m.state.Root
	rebaseStatePaths(&m.state, m.state.Root, abs)
	m.state.Root = abs
	m.state.Revision = 1
	m.state.Catalog.Refreshing = false
	if m.state.Settings.ThemeMode == "" {
		m.state.Settings.ThemeMode = "system"
	}
	if m.state.Settings.System.CloseBehavior == "" {
		m.state.Settings.System.CloseBehavior = "quit"
	}
	// Preserve legacy website addresses before removing the Nginx instance port.
	for n := range m.state.Sites {
		site := &m.state.Sites[n]
		if site.Port == 0 {
			for _, instance := range m.state.Instances {
				if instance.ID == site.NginxID {
					site.Port = instance.Port
					break
				}
			}
			if site.Port == 0 {
				site.Port = 8080
			}
		}
		if site.Template == "" {
			site.Template = "php"
		}
		site.URL = fmt.Sprintf("http://%s.localhost:%d", site.Name, site.Port)
	}
	if m.state.Settings.CatalogUpdateHours == 0 {
		m.state.Settings.CatalogUpdateHours = 6
	}
	for i := range m.state.Tasks {
		t := &m.state.Tasks[i]
		if t.Status == "downloading" || t.Status == "queued" || t.Status == "installing" || t.Status == "verifying" {
			t.Status = "paused"
			t.Speed = 0
		}
	}
	for i := range m.state.Instances {
		m.state.Instances[i].Status = "stopped"
		m.state.Instances[i].PID = 0
		if m.state.Instances[i].Component == "nginx" {
			m.state.Instances[i].Port = 0
		}
		if m.previousRoot != m.root && within(filepath.Join(m.root, "instances"), m.state.Instances[i].Path) {
			if err := m.updateConfigRoot(m.state.Instances[i]); err != nil {
				return nil, fmt.Errorf("配置中的旧路径修正失败: %w", err)
			}
		}
	}
	// Merged PHP runtimes live on packages; reset their transient status too.
	for i := range m.state.Packages {
		if m.state.Packages[i].Component != "php" {
			continue
		}
		m.state.Packages[i].Status = "stopped"
		m.state.Packages[i].PID = 0
	}
	m.refreshSystemServices()
	if err := m.migrateDirectoryNames(); err != nil {
		return nil, fmt.Errorf("迁移版本目录失败: %w", err)
	}
	if err := m.recoverExtensionPublications(); err != nil {
		return nil, err
	}
	if err := m.migrateFlatExtensions(); err != nil {
		return nil, fmt.Errorf("迁移扩展文件路径失败: %w", err)
	}
	stalePHPDirs, err := m.migratePHPCollapse()
	if err != nil {
		return nil, fmt.Errorf("迁移 PHP 实例到程序版本失败: %w", err)
	}
	if err := m.migrateSiteConfigs(m.Snapshot()); err != nil {
		return nil, fmt.Errorf("迁移共享站点配置失败: %w", err)
	}
	for _, instance := range m.state.Instances {
		if err := formatLegacyRedis(instance); err != nil {
			return nil, err
		}
		if instance.Component == "nginx" {
			if err := m.writeNginxConfig(instance); err != nil {
				return nil, fmt.Errorf("迁移站点独立配置失败: %w", err)
			}
		}
	}
	if err := m.saveLocked(); err != nil {
		return nil, err
	}
	// Only after the collapsed state is safely persisted do we remove the old
	// PHP instance directories; a deletion failure leaves them in place.
	for _, dir := range stalePHPDirs {
		_ = os.RemoveAll(dir)
	}
	success = true
	return m, nil
}
func (m *Manager) statePath() string { return filepath.Join(m.root, "state", "panel.json") }
func (m *Manager) saveLocked() error {
	// The SCM host owns only its child process, never the GUI's metadata file.
	if m.serviceHost {
		return nil
	}
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.statePath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
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
	return os.Rename(tmp, m.statePath())
}
func (m *Manager) Snapshot() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneState(m.state)
}
func cloneState(state State) State {
	state.ExtensionArtifacts = cloneExtensionArtifacts(state.ExtensionArtifacts)
	state.ExtensionPackages = cloneInstalledExtensions(state.ExtensionPackages)
	state.PreparedExtensions = append([]PreparedPHPExtension{}, state.PreparedExtensions...)
	for n := range state.PreparedExtensions {
		if state.PreparedExtensions[n].Record.Files != nil {
			state.PreparedExtensions[n].Record.Files = cloneStringMap(state.PreparedExtensions[n].Record.Files)
		}
	}
	state.ExtensionCatalogUpdated = cloneStringMap(state.ExtensionCatalogUpdated)
	state.Artifacts = append([]Artifact{}, state.Artifacts...)
	for n := range state.Artifacts {
		state.Artifacts[n].Sources = append([]Source{}, state.Artifacts[n].Sources...)
	}
	state.Packages = append([]InstalledPackage{}, state.Packages...)
	for n := range state.Packages {
		if state.Packages[n].NginxAutoPHP != nil {
			enabled := *state.Packages[n].NginxAutoPHP
			state.Packages[n].NginxAutoPHP = &enabled
		}
	}
	state.Instances = append([]Instance{}, state.Instances...)
	for n := range state.Instances {
		if state.Instances[n].NginxAutoPHP != nil {
			enabled := *state.Instances[n].NginxAutoPHP
			state.Instances[n].NginxAutoPHP = &enabled
		}
	}
	state.Sites = append([]Site{}, state.Sites...)
	state.Tasks = append([]Task{}, state.Tasks...)
	for n := range state.Tasks {
		if state.Tasks[n].ComposerArtifact != nil {
			copy := *state.Tasks[n].ComposerArtifact
			state.Tasks[n].ComposerArtifact = &copy
		}
		if state.Tasks[n].ExtensionArtifact != nil {
			copy := *state.Tasks[n].ExtensionArtifact
			copy.Sources = append([]Source{}, copy.Sources...)
			state.Tasks[n].ExtensionArtifact = &copy
		}
	}
	state.Settings.CatalogURLs = append([]string{}, state.Settings.CatalogURLs...)
	state.Catalog.Warnings = append([]string{}, state.Catalog.Warnings...)
	return state
}
func (m *Manager) SnapshotSince(revision uint64) StateUpdate {
	m.mu.Lock()
	defer m.mu.Unlock()
	if revision == m.state.Revision {
		return StateUpdate{Revision: revision}
	}
	copy := cloneState(m.state)
	return StateUpdate{Revision: copy.Revision, Changed: true, State: &copy}
}
func (m *Manager) SetChangeHandler(fn func()) { m.mu.Lock(); m.onChange = fn; m.mu.Unlock() }
func (m *Manager) change(fn func(*State) error) error {
	m.mu.Lock()
	before := cloneState(m.state)
	if err := fn(&m.state); err != nil {
		m.state = before
		m.mu.Unlock()
		return err
	}
	m.state.Revision++
	if err := m.saveLocked(); err != nil {
		m.state = before
		m.mu.Unlock()
		return err
	}
	handler := m.onChange
	m.mu.Unlock()
	if handler != nil {
		handler()
	}
	return nil
}
func id(prefix string) string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + "-" + hex.EncodeToString(b)
}
func timestamp() string            { return time.Now().Format(time.RFC3339) }
func validComponent(c string) bool { return c == "php" || c == "nginx" || c == "mysql" || c == "redis" }
func validLabel(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 100 && !strings.ContainsAny(s, "\x00\r\n")
}
func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closing = true
	if m.catalogCancel != nil {
		m.catalogCancel()
	}
	if m.refreshCancel != nil {
		m.refreshCancel()
	}
	for _, cancel := range m.downloads {
		cancel()
	}
	for _, cancel := range m.extensionRefresh {
		cancel()
	}
	ids := []string{}
	remoteIDs := []string{}
	for key := range m.ownedDetachedPHP {
		remoteIDs = append(remoteIDs, key)
	}
	for key := range m.processes {
		ids = append(ids, key)
	}
	m.mu.Unlock()
	for _, key := range ids {
		_ = m.stopInstance(key, true)
	}
	for _, key := range remoteIDs {
		i, _, err := m.lookupInstance(key)
		if err != nil {
			continue
		}
		status, err := m.phpBackend().Query(i)
		if err == nil && status.Exists && !status.Protected {
			if m.phpBackend().Stop(i, "close-unprotected") == nil {
				_ = m.setInstance(key, "stopped", "", 0)
			}
		}
	}
	m.wg.Wait()
	if m.stateLock != nil {
		_ = m.stateLock.Close()
	}
}

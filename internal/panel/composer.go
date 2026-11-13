package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ComposerArtifact struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	MinPHP  int    `json:"minPhp"`
}

func composerArtifactFromBody(body []byte) (ComposerArtifact, error) {
	var catalog struct {
		Stable []struct {
			Version string `json:"version"`
			Path    string `json:"path"`
			MinPHP  int    `json:"min-php"`
			LTS     bool   `json:"lts"`
			EOL     bool   `json:"eol"`
		} `json:"stable"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		return ComposerArtifact{}, err
	}
	for _, entry := range catalog.Stable {
		if entry.EOL || entry.LTS || !fullVersionPattern.MatchString(entry.Version) || entry.Path != "/download/"+entry.Version+"/composer.phar" {
			continue
		}
		return ComposerArtifact{Version: entry.Version, URL: "https://getcomposer.org" + entry.Path, MinPHP: entry.MinPHP}, nil
	}
	return ComposerArtifact{}, fmt.Errorf("Composer 官方目录没有可用稳定版")
}
func fetchComposerArtifact(ctx context.Context, client *http.Client) (ComposerArtifact, error) {
	body, err := catalogueBody(ctx, client, "https://getcomposer.org/versions", 1<<20)
	if err != nil {
		return ComposerArtifact{}, err
	}
	artifact, err := composerArtifactFromBody(body)
	if err != nil {
		return artifact, err
	}
	checksum, err := catalogueBody(ctx, client, artifact.URL+".sha256sum", 2048)
	if err != nil {
		return artifact, err
	}
	fields := strings.Fields(string(checksum))
	if len(fields) < 1 || !validHash(fields[0]) {
		return artifact, fmt.Errorf("Composer 官方 SHA-256 格式无效")
	}
	artifact.SHA256 = strings.ToLower(fields[0])
	return artifact, nil
}
func phpVersionNumber(version string) int {
	parts := strings.Split(version, ".")
	if len(parts) < 3 {
		return 0
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])
	return major*10000 + minor*100 + patch
}
func (m *Manager) InstallComposer(instanceID string) (string, error) {
	i, p, release, err := m.reservePHPOperation(instanceID)
	if err != nil {
		return "", err
	}
	defer release()
	if _, err = os.Stat(filepath.Join(filepath.Dir(p.Executable), "php.exe")); err != nil {
		return "", fmt.Errorf("请先安装含 php.exe 的 PHP 版本")
	}
	state := m.Snapshot()
	for _, t := range state.Tasks {
		if t.Kind == "composer" && t.Status != "completed" && t.Status != "error" {
			return "", fmt.Errorf("已有 Composer 任务，请在下载中心继续")
		}
	}
	task := Task{ID: id("download"), Kind: "composer", Component: "php", Version: "最新稳定版", InstanceID: i.ID, Source: "Composer 官方", Status: "paused", CreatedAt: timestamp()}
	if err = m.change(func(s *State) error { s.Tasks = append(s.Tasks, task); return nil }); err != nil {
		return "", err
	}
	return task.ID, m.resumeComposerDownload(task.ID)
}
func (m *Manager) resumeComposerDownload(key string) error {
	m.mu.Lock()
	if m.closing || m.downloads[key] != nil {
		m.mu.Unlock()
		return fmt.Errorf("任务正在运行或应用正在退出")
	}
	if len(m.downloads) >= 2 {
		m.mu.Unlock()
		return fmt.Errorf("最多同时下载两个任务")
	}
	var task Task
	for _, t := range m.state.Tasks {
		if t.ID == key {
			task = t
		}
	}
	if task.ID == "" || task.Status == "completed" {
		m.mu.Unlock()
		return fmt.Errorf("Composer 任务不可继续")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.downloads[key] = cancel
	m.wg.Add(1)
	m.mu.Unlock()
	if err := m.updateTask(key, func(t *Task) { t.Status = "downloading"; t.Error = ""; t.Warning = "" }); err != nil {
		cancel()
		m.mu.Lock()
		delete(m.downloads, key)
		m.mu.Unlock()
		m.wg.Done()
		return err
	}
	go func() {
		defer cancel()
		defer m.wg.Done()
		defer func() { m.mu.Lock(); delete(m.downloads, key); m.mu.Unlock() }()
		if err := m.runComposerDownload(ctx, key); err != nil {
			_ = m.updateTask(key, func(t *Task) {
				t.Speed = 0
				t.Error = err.Error()
				t.Status = "error"
				if errors.Is(err, context.Canceled) {
					t.Status = "paused"
					t.Error = ""
				}
			})
		}
	}()
	return nil
}
func (m *Manager) runComposerDownload(ctx context.Context, key string) error {
	task, err := extensionTask(m.Snapshot(), key)
	if err != nil {
		return err
	}
	i, p, err := m.lookupInstance(task.InstanceID)
	if err != nil {
		return err
	}
	if i.Component != "php" {
		return fmt.Errorf("Composer 需要 PHP 实例")
	}
	client := m.httpClient()
	defer client.CloseIdleConnections()
	var a ComposerArtifact
	if task.ComposerArtifact != nil {
		a = *task.ComposerArtifact
	} else {
		fetcher := m.composerFetcher
		if fetcher == nil {
			fetcher = fetchComposerArtifact
		}
		a, err = fetcher(ctx, client)
		if err != nil {
			return err
		}
	}
	if !validHash(a.SHA256) || !fullVersionPattern.MatchString(a.Version) || !validCatalogURL(a.URL) {
		return fmt.Errorf("Composer 制品元数据无效")
	}
	base, _, err := probePHP(p, "", "", i.Path)
	if err != nil {
		return err
	}
	if phpVersionNumber(base.Version) < a.MinPHP {
		return fmt.Errorf("Composer %s 需要 PHP %d 以上，请选择其它实例", a.Version, a.MinPHP)
	}
	if err = m.updateTask(key, func(t *Task) { copy := a; t.ComposerArtifact = &copy; t.Version = a.Version }); err != nil {
		return err
	}
	cache := filepath.Join(m.root, "downloads", "cache", a.SHA256+".phar")
	if hash, _ := fileHash(cache); hash != a.SHA256 {
		partial := filepath.Join(m.root, "downloads", "partial", key+".part")
		if err = m.fetch(ctx, client, key, Source{Name: "Composer 官方", URL: a.URL}, partial); err != nil {
			return err
		}
		hash, err := fileHash(partial)
		if err != nil || hash != a.SHA256 {
			_ = os.Remove(partial)
			return fmt.Errorf("Composer SHA-256 不匹配，拒绝安装")
		}
		if err = os.Rename(partial, cache); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = m.updateTask(key, func(t *Task) { t.Status = "installing"; t.Speed = 0 }); err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, filepath.Join(filepath.Dir(p.Executable), "php.exe"), "-n", cache, "--version", "--no-ansi")
	cmd.Dir = filepath.Dir(p.Executable)
	cmd.Env = phpEnvironment("")
	cmd.WaitDelay = 2 * time.Second
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	hideWindow(cmd)
	if err = cmd.Start(); err != nil {
		return err
	}
	guard, guardErr := guardProcess(cmd)
	if guardErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return guardErr
	}
	err = cmd.Wait()
	guard.close()
	if err != nil || !strings.Contains(output.String(), "Composer version "+a.Version) {
		return fmt.Errorf("Composer 实际版本检查失败: %v；%s", err, output.String())
	}
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	if _, _, err = m.lookupInstance(i.ID); err != nil {
		return fmt.Errorf("用于 Composer 的 PHP 实例已删除")
	}
	dir := filepath.Join(m.root, "tools", "composer")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".composer-")
	if err != nil {
		return err
	}
	path := temp.Name()
	temp.Close()
	_ = os.Remove(path)
	defer os.Remove(path)
	if err = copyExtensionFile(cache, path); err != nil {
		return err
	}
	dest := filepath.Join(dir, "composer.phar")
	previous, readErr := os.ReadFile(dest)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	if err = os.Rename(path, dest); err != nil {
		return fmt.Errorf("Composer 文件正在使用，请等待命令结束后重试: %w", err)
	}
	if err = m.change(func(s *State) error {
		s.Tools.ComposerVersion = a.Version
		s.Tools.ComposerSHA256 = a.SHA256
		if s.Tools.PHPInstanceID == "" {
			s.Tools.PHPInstanceID = i.ID
		}
		return nil
	}); err != nil {
		if previous != nil {
			_ = atomicConfigWrite(dest, previous)
		} else {
			_ = os.Remove(dest)
		}
		return err
	}
	return m.updateTask(key, func(t *Task) {
		t.Status = "completed"
		t.Error = ""
		t.Speed = 0
		if t.Total > 0 {
			t.Received = t.Total
		}
	})
}

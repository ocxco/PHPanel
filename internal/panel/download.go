package panel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (m *Manager) updateTask(key string, fn func(*Task)) error {
	return m.change(func(s *State) error {
		for i := range s.Tasks {
			if s.Tasks[i].ID == key {
				fn(&s.Tasks[i])
				return nil
			}
		}
		return fmt.Errorf("下载任务不存在")
	})
}
func (m *Manager) Install(artifactID string) (string, error) {
	state := m.Snapshot()
	var a Artifact
	for _, item := range state.Artifacts {
		if item.ID == artifactID {
			a = item
			break
		}
	}
	if a.ID == "" {
		return "", fmt.Errorf("版本不存在")
	}
	if err := validateArtifact(a); err != nil {
		return "", err
	}
	for _, p := range state.Packages {
		if p.ID == a.ID || validHash(a.SHA256) && p.Component == a.Component && p.SHA256 == a.SHA256 {
			return "", fmt.Errorf("该版本已经安装")
		}
	}
	task := Task{ID: id("download"), ArtifactID: a.ID, Component: a.Component, Version: a.Version, Status: "paused", CreatedAt: timestamp()}
	if err := m.change(func(s *State) error {
		for _, t := range s.Tasks {
			if t.ArtifactID == a.ID && t.Status != "completed" && t.Status != "error" {
				return fmt.Errorf("已有此版本的任务，请在下载中心继续")
			}
		}
		s.Tasks = append(s.Tasks, task)
		return nil
	}); err != nil {
		return "", err
	}
	return task.ID, m.ResumeDownload(task.ID)
}
func (m *Manager) ResumeDownload(key string) error {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return fmt.Errorf("应用正在退出")
	}
	if _, exists := m.downloads[key]; exists {
		m.mu.Unlock()
		return fmt.Errorf("任务正在运行")
	}
	var task Task
	var artifact Artifact
	for _, t := range m.state.Tasks {
		if t.ID == key {
			task = t
		}
	}
	if task.Kind == "php-extension" {
		m.mu.Unlock()
		return m.resumePHPExtensionDownload(key)
	}
	if task.Kind == "composer" {
		m.mu.Unlock()
		return m.resumeComposerDownload(key)
	}
	for _, a := range m.state.Artifacts {
		if a.ID == task.ArtifactID {
			artifact = a
		}
	}
	if task.ID == "" || artifact.ID == "" {
		m.mu.Unlock()
		return fmt.Errorf("任务或制品不存在")
	}
	if task.Status == "completed" {
		m.mu.Unlock()
		return fmt.Errorf("任务已完成")
	}
	if len(m.downloads) >= 2 {
		m.mu.Unlock()
		return fmt.Errorf("最多同时下载两个任务，请稍后继续")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.downloads[key] = cancel
	m.wg.Add(1)
	m.mu.Unlock()
	if err := m.updateTask(key, func(t *Task) { t.Status = "downloading"; t.Error = "" }); err != nil {
		cancel()
		m.mu.Lock()
		delete(m.downloads, key)
		m.mu.Unlock()
		m.wg.Done()
		return err
	}
	go func() {
		defer m.wg.Done()
		defer cancel()
		defer func() { m.mu.Lock(); delete(m.downloads, key); m.mu.Unlock() }()
		err := m.runDownload(ctx, key, artifact)
		if err != nil {
			_ = m.updateTask(key, func(t *Task) {
				t.Speed = 0
				if errors.Is(err, context.Canceled) {
					t.Status = "paused"
					t.Error = ""
				} else {
					t.Status = "error"
					t.Error = err.Error()
				}
			})
		}
	}()
	return nil
}
func (m *Manager) PauseDownload(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cancel, ok := m.downloads[key]
	if !ok {
		return fmt.Errorf("任务未在运行")
	}
	for _, t := range m.state.Tasks {
		if t.ID == key && t.Status == "installing" {
			return fmt.Errorf("正在发布安装结果，请等待完成")
		}
	}
	cancel()
	return nil
}
func (m *Manager) runDownload(ctx context.Context, key string, a Artifact) error {
	cacheKey := a.SHA256
	if !validHash(cacheKey) {
		cacheKey = a.ID
	}
	cache := filepath.Join(m.root, "downloads", "cache", cacheKey+".zip")
	partial := filepath.Join(m.root, "downloads", "partial", key+".part")
	hash, err := fileHash(cache)
	if err != nil || !validHash(a.SHA256) || hash != a.SHA256 {
		sources := append([]Source{}, a.Sources...)
		state := m.Snapshot()
		if state.Settings.SourceMode == "custom" {
			sort.SliceStable(sources, func(i, j int) bool {
				return strings.HasPrefix(sources[i].Name, "自定义") && !strings.HasPrefix(sources[j].Name, "自定义")
			})
		}
		client := m.httpClient()
		defer client.CloseIdleConnections()
		failures := []string{}
		success := false
		for _, source := range sources {
			if err := ctx.Err(); err != nil {
				return err
			}
			for attempt := 0; attempt < 2; attempt++ {
				err = m.fetch(ctx, client, key, source, partial)
				if err == nil {
					hash, err = fileHash(partial)
					if err == nil && validHash(a.SHA256) && hash != a.SHA256 {
						err = fmt.Errorf("SHA-256 不匹配，拒绝安装")
						_ = os.Remove(partial)
						break
					}
					if err == nil && !validHash(a.SHA256) {
						_ = m.updateTask(key, func(t *Task) { t.Status = "verifying"; t.Speed = 0 })
						verifier := m.signatureVerifier
						if verifier == nil {
							verifier = m.verifyArtifactSignature
						}
						err = verifier(ctx, client, a, partial)
						if err == nil {
							err = m.rememberVerifiedArtifact(a, hash)
							if err == nil {
								a.SHA256 = hash
								a.Verification = "官方 OpenPGP 签名 + SHA-256"
							}
						}
						if err != nil {
							if ctx.Err() != nil {
								return ctx.Err()
							}
							_ = os.Remove(partial)
							_ = m.updateTask(key, func(t *Task) { t.Status = "downloading" })
							break
						}
					}
					if err == nil {
						success = true
						break
					}
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if attempt == 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(time.Second):
					}
				}
			}
			if success {
				break
			}
			failures = append(failures, source.Name+": "+err.Error())
		}
		if !success {
			return fmt.Errorf("下载未完成：%s", strings.Join(failures, "；"))
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		cache = filepath.Join(m.root, "downloads", "cache", a.SHA256+".zip")
		if err := os.Rename(partial, cache); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.updateTask(key, func(t *Task) { t.Status = "installing"; t.Speed = 0 }); err != nil {
		return err
	}
	if _, err := m.installZIPContext(ctx, a, cache); err != nil {
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
func (m *Manager) fetch(ctx context.Context, client *http.Client, key string, source Source, path string) error {
	state := m.Snapshot()
	var old Task
	for _, t := range state.Tasks {
		if t.ID == key {
			old = t
		}
	}
	offset := int64(0)
	if info, err := os.Stat(path); err == nil {
		offset = info.Size()
	}
	if old.SourceURL != source.URL {
		offset = 0
		old.ETag = ""
		if err := os.WriteFile(path, nil, 0600); err != nil {
			return err
		}
	}
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, source.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "PHPanel/0.1")
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		if old.ETag != "" {
			req.Header.Set("If-Range", old.ETag)
		}
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("连接失败或超时")
	}
	defer res.Body.Close()
	limit := int64(4 << 30)
	if old.Kind == "composer" {
		limit = 32 << 20
		if req.URL.Scheme == "https" && res.Request.URL.Scheme != "https" {
			return fmt.Errorf("Composer 下载拒绝 HTTPS 降级")
		}
	}
	if old.Kind == "php-extension" {
		limit = 64 << 20
		if req.URL.Scheme == "https" && res.Request.URL.Scheme != "https" {
			return fmt.Errorf("扩展下载拒绝 HTTPS 降级")
		}
		if old.ExtensionArtifact == nil {
			return fmt.Errorf("扩展任务缺少制品信息")
		}
		if !validHash(old.ExtensionArtifact.SHA256) && !trustedExtensionSource(res.Request.URL.String()) {
			return fmt.Errorf("无摘要的扩展只允许从已知官方 HTTPS 来源下载")
		}
	}
	if res.StatusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0 {
		_ = os.WriteFile(path, nil, 0600)
		return fmt.Errorf("续传范围失效，已重置缓存")
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	total := res.ContentLength
	if res.StatusCode == http.StatusPartialContent {
		var start, end, full int64
		n, _ := fmt.Sscanf(res.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &full)
		if n != 3 || start != offset || end < start || full <= end || full > limit {
			return fmt.Errorf("服务器返回了无效的续传范围")
		}
		total = full
	} else {
		offset = 0
	}
	if total > limit {
		return fmt.Errorf("安装包超过允许的大小")
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := m.updateTask(key, func(t *Task) {
		t.Source = source.Name
		t.SourceURL = source.URL
		t.ETag = res.Header.Get("ETag")
		t.Total = total
		t.Received = offset
		t.Error = ""
	}); err != nil {
		return err
	}
	received := offset
	lastTime := time.Now()
	lastReceived := received
	buffer := make([]byte, 128*1024)
	for {
		n, readErr := res.Body.Read(buffer)
		if n > 0 {
			if received+int64(n) > limit {
				if old.Kind == "composer" {
					return fmt.Errorf("Composer 安装包超过 32 MB 上限")
				}
				if old.Kind == "php-extension" {
					return fmt.Errorf("扩展安装包超过 64 MB 上限")
				}
				return fmt.Errorf("安装包超过 4 GiB 上限")
			}
			if _, err = f.Write(buffer[:n]); err != nil {
				return err
			}
			received += int64(n)
		}
		elapsed := time.Since(lastTime)
		if elapsed >= 500*time.Millisecond || readErr != nil {
			speed := int64(float64(received-lastReceived) / elapsed.Seconds())
			if elapsed.Seconds() == 0 {
				speed = 0
			}
			if err := m.updateTask(key, func(t *Task) { t.Received = received; t.Speed = speed }); err != nil {
				return err
			}
			lastTime = time.Now()
			lastReceived = received
		}
		if readErr == io.EOF {
			if total >= 0 && received != total {
				return fmt.Errorf("响应长度与文件长度不一致")
			}
			return f.Sync()
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("连接中断，可继续下载")
		}
	}
}

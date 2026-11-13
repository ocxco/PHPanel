package panel

import (
	"archive/zip"
	"context"
	"debug/pe"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func validateExtensionArtifact(a PHPExtensionArtifact) error {
	if (a.Name != "redis" && a.Name != "apcu" && a.Name != "xdebug") || !fullVersionPattern.MatchString(a.Version) || a.Kind != "extension" && a.Kind != "zend" || a.Format != "zip" && a.Format != "dll" {
		return fmt.Errorf("扩展制品元数据无效")
	}
	if a.Name == "xdebug" && a.Kind != "zend" || a.Name != "xdebug" && a.Kind != "extension" {
		return fmt.Errorf("扩展加载类型不符")
	}
	if a.SHA256 != "" && !validHash(a.SHA256) {
		return fmt.Errorf("扩展摘要无效")
	}
	if len(a.Sources) > 12 {
		return fmt.Errorf("扩展下载来源过多")
	}
	for _, s := range a.Sources {
		if !validCatalogURL(s.URL) || !validLabel(s.Name) {
			return fmt.Errorf("扩展来源无效")
		}
		if a.SHA256 == "" && !trustedExtensionSource(s.URL) {
			return fmt.Errorf("未提供可信摘要的制品只能使用官方 HTTPS 来源")
		}
	}
	if len(a.Sources) == 0 && a.SHA256 == "" {
		return fmt.Errorf("扩展无下载来源")
	}
	return nil
}
func (m *Manager) ImportPHPExtensionArchive(key, artifactID, revision, archive string, enable bool) (string, error) {
	i, p, release, err := m.reservePHPOperation(key)
	if err != nil {
		return "", err
	}
	defer release()
	set, err := readPHPINI(i)
	if err != nil {
		return "", err
	}
	if enable && set.revision != revision {
		return "", fmt.Errorf("配置已改变，请重新检测后导入")
	}
	base, _, err := probePHP(p, "", "", i.Path)
	if err != nil {
		return "", err
	}
	var a PHPExtensionArtifact
	for _, item := range m.Snapshot().ExtensionArtifacts {
		if item.ID == artifactID {
			a = item
			break
		}
	}
	if a.ID == "" || !extensionCompatible(a, base) {
		return "", fmt.Errorf("此扩展构建不兼容当前 PHP")
	}
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(archive), "."))
	if format != "dll" && format != "zip" {
		return "", fmt.Errorf("只支持 DLL 或 ZIP")
	}
	info, err := os.Stat(archive)
	if err != nil {
		return "", err
	}
	if info.Size() > 64<<20 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("导入文件超过 64 MB 或不是普通文件")
	}
	hash, err := fileHash(archive)
	if err != nil {
		return "", err
	}
	if format == a.Format && a.SHA256 != "" && a.SHA256 != hash {
		return "", fmt.Errorf("本地文件与目录中的 SHA-256 不符")
	}
	verification := "本地导入 · 实际加载校验"
	if format == a.Format && hash == a.SHA256 {
		verification = "本地导入 · " + a.Verification
	}
	a.Format = format
	a.SHA256 = hash
	a.Verification = verification
	a.Publisher = "本地导入"
	a.Sources = []Source{}
	a.ID = "php-extension-local-" + hash[:24]
	cache := filepath.Join(m.root, "downloads", "cache", hash+"."+format)
	if existing, _ := fileHash(cache); existing != hash {
		input, err := os.Open(archive)
		if err != nil {
			return "", err
		}
		defer input.Close()
		out, err := os.CreateTemp(filepath.Dir(cache), ".extension-import-")
		if err != nil {
			return "", err
		}
		temp := out.Name()
		defer os.Remove(temp)
		_, copyErr := io.Copy(out, io.LimitReader(input, (64<<20)+1))
		closeErr := out.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		copyHash, err := fileHash(temp)
		if err != nil || copyHash != hash {
			return "", fmt.Errorf("导入时文件发生变化")
		}
		if err = os.Rename(temp, cache); err != nil {
			return "", err
		}
	}
	task := Task{ID: id("download"), Kind: "php-extension", Component: "php", Version: a.Version, ArtifactID: a.ID, ExtensionArtifact: &a, ExtensionName: a.Name, InstanceID: key, EnableExtension: enable, ConfigRevision: set.revision, Status: "paused", Source: "本地导入", CreatedAt: timestamp()}
	if err = m.change(func(s *State) error {
		for _, t := range s.Tasks {
			if t.Kind == "php-extension" && t.InstanceID == key && t.ExtensionName == a.Name && t.Status != "completed" && t.Status != "error" {
				return fmt.Errorf("已有此扩展的任务")
			}
		}
		s.Tasks = append(s.Tasks, task)
		return nil
	}); err != nil {
		return "", err
	}
	return task.ID, m.resumePHPExtensionDownload(task.ID)
}
func (m *Manager) InstallPHPExtension(key, artifactID, revision string, enable bool) (string, error) {
	i, p, release, err := m.reservePHPOperation(key)
	if err != nil {
		return "", err
	}
	defer release()
	set, err := readPHPINI(i)
	if err != nil {
		return "", err
	}
	if enable && revision != set.revision {
		return "", fmt.Errorf("配置已改变，请重新检测后安装并启用")
	}
	base, _, err := probePHP(p, "", "", i.Path)
	if err != nil {
		return "", err
	}
	state := m.Snapshot()
	var a PHPExtensionArtifact
	for _, item := range state.ExtensionArtifacts {
		if item.ID == artifactID {
			a = item
			break
		}
	}
	if a.ID == "" || !extensionCompatible(a, base) {
		return "", fmt.Errorf("此扩展构建不兼容当前 PHP")
	}
	for _, pending := range state.PreparedExtensions {
		if pending.Record.PackageID == p.ID && pending.Record.Name == a.Name {
			return "", fmt.Errorf("此扩展已有准备好的更新，请停止或重启 PHP 后应用")
		}
	}
	if err = validateExtensionArtifact(a); err != nil {
		return "", err
	}
	if _, builtin := base.Modules[a.Name]; builtin {
		return "", fmt.Errorf("此扩展已内置")
	}
	for _, item := range state.ExtensionPackages {
		if item.PackageID == p.ID && item.ArtifactID == a.ID {
			if _, err := installedExtensionDLL(p, item); err == nil {
				return "", fmt.Errorf("此扩展已经安装，请在本地扩展中启用")
			}
		}
	}
	for _, t := range state.Tasks {
		if t.Kind == "php-extension" && t.ExtensionName == a.Name && t.InstanceID != "" && t.ExtensionArtifact != nil {
			owner, _, err := m.lookupInstance(t.InstanceID)
			if err == nil && owner.PackageID == p.ID && t.Status != "completed" && t.Status != "error" {
				return "", fmt.Errorf("此 PHP 版本已有该扩展的安装任务，请在下载中心继续")
			}
		}
	}
	task := Task{ID: id("download"), Kind: "php-extension", Component: "php", Version: a.Version, ArtifactID: a.ID, ExtensionName: a.Name, ExtensionArtifact: &a, InstanceID: key, EnableExtension: enable, ConfigRevision: set.revision, Status: "paused", CreatedAt: timestamp()}
	if err = m.change(func(s *State) error { s.Tasks = append(s.Tasks, task); return nil }); err != nil {
		return "", err
	}
	return task.ID, m.resumePHPExtensionDownload(task.ID)
}
func (m *Manager) resumePHPExtensionDownload(key string) error {
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
	if task.ExtensionArtifact == nil || task.Status == "completed" || task.Status == "awaiting_restart" {
		m.mu.Unlock()
		return fmt.Errorf("任务无法继续，请应用更新或重新选择扩展")
	}
	a := *task.ExtensionArtifact
	a.Sources = append([]Source{}, a.Sources...)
	if err := validateExtensionArtifact(a); err != nil {
		m.mu.Unlock()
		return err
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
		if err := m.runPHPExtensionDownload(ctx, key, a); err != nil {
			state := m.Snapshot()
			prepared := false
			for _, item := range state.PreparedExtensions {
				if item.TaskID == key {
					prepared = true
				}
			}
			_ = m.updateTask(key, func(t *Task) {
				t.Speed = 0
				if prepared {
					t.Status = "awaiting_restart"
					t.Error = err.Error()
					t.Warning = "准备好的文件已保留，请修正配置后停止或重启 PHP 再应用"
				} else if errors.Is(err, context.Canceled) {
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
func (m *Manager) runPHPExtensionDownload(ctx context.Context, key string, a PHPExtensionArtifact) error {
	cache := filepath.Join(m.root, "downloads", "cache", a.SHA256+"."+a.Format)
	hash, err := fileHash(cache)
	if a.SHA256 == "" || err != nil || hash != a.SHA256 {
		partial := filepath.Join(m.root, "downloads", "partial", key+".part")
		sources := append([]Source{}, a.Sources...)
		if m.Snapshot().Settings.SourceMode == "custom" {
			sort.SliceStable(sources, func(i, j int) bool {
				return strings.HasPrefix(sources[i].Name, "自定义") && !strings.HasPrefix(sources[j].Name, "自定义")
			})
		}
		client := m.httpClient()
		defer client.CloseIdleConnections()
		failures := []string{}
		success := false
		for _, source := range sources {
			for attempt := 0; attempt < 2; attempt++ {
				if err = ctx.Err(); err != nil {
					return err
				}
				err = m.fetch(ctx, client, key, source, partial)
				if err == nil {
					hash, err = fileHash(partial)
					if err == nil && a.SHA256 != "" && hash != a.SHA256 {
						err = fmt.Errorf("SHA-256 不匹配，拒绝安装")
						_ = os.Remove(partial)
					}
				}
				if err == nil {
					success = true
					break
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
			if success {
				break
			}
			failures = append(failures, source.Name+": "+err.Error())
		}
		if !success {
			return fmt.Errorf("扩展下载失败: %s", strings.Join(failures, "；"))
		}
		if a.SHA256 == "" {
			a.SHA256 = hash
			a.Verification = "PHP 官方 HTTPS 下载 · SHA-256 已记录"
		}
		cache = filepath.Join(m.root, "downloads", "cache", a.SHA256+"."+a.Format)
		if existing, err := fileHash(cache); err == nil && existing == a.SHA256 {
			_ = os.Remove(partial)
		} else {
			if err = os.Rename(partial, cache); err != nil {
				return err
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = m.change(func(s *State) error {
		for n := range s.Tasks {
			if s.Tasks[n].ID == key {
				copy := a
				s.Tasks[n].ExtensionArtifact = &copy
				s.Tasks[n].Status = "installing"
				s.Tasks[n].Speed = 0
			}
		}
		mergeExtensionArtifacts(s, []PHPExtensionArtifact{a})
		return nil
	}); err != nil {
		return err
	}
	return m.preparePHPExtension(ctx, key, a, cache)
}
func extensionTask(state State, key string) (Task, error) {
	for _, t := range state.Tasks {
		if t.ID == key {
			return t, nil
		}
	}
	return Task{}, fmt.Errorf("扩展任务不存在")
}
func extensionStageMain(stage, name, format, archive string) (string, error) {
	if format == "dll" {
		input, err := os.Open(archive)
		if err != nil {
			return "", err
		}
		defer input.Close()
		dest := filepath.Join(stage, "php_"+name+".dll")
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(out, io.LimitReader(input, 64<<20))
		closeErr := out.Close()
		if copyErr != nil {
			return "", copyErr
		}
		return dest, closeErr
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return "", err
	}
	total := uint64(0)
	for _, f := range reader.File {
		total += f.UncompressedSize64
	}
	count := len(reader.File)
	reader.Close()
	if count > 256 || total > 128<<20 {
		return "", fmt.Errorf("扩展归档超过文件数量或大小限制")
	}
	raw := filepath.Join(stage, ".unpacked")
	if err = os.Mkdir(raw, 0700); err != nil {
		return "", err
	}
	defer os.RemoveAll(raw)
	if err = extractZIP(archive, raw); err != nil {
		return "", err
	}
	main := ""
	err = filepath.WalkDir(raw, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".dll") {
			return nil
		}
		file := d.Name()
		if phpModuleName(file) == name {
			if main != "" {
				return fmt.Errorf("扩展包有多个目标 DLL")
			}
			file = "php_" + name + ".dll"
			main = filepath.Join(stage, file)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		out, err := os.OpenFile(filepath.Join(stage, file), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, input)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		return "", err
	}
	if main == "" {
		return "", fmt.Errorf("扩展包缺少 php_%s.dll", name)
	}
	return main, nil
}
func (m *Manager) preparePHPExtension(ctx context.Context, key string, a PHPExtensionArtifact, archive string) error {
	task, err := extensionTask(m.Snapshot(), key)
	if err != nil {
		return err
	}
	i, p, err := m.lookupInstance(task.InstanceID)
	if err != nil {
		return fmt.Errorf("目标实例已删除，安装未发布")
	}
	base, _, err := probePHP(p, "", "", i.Path)
	if err != nil {
		return err
	}
	if !extensionCompatible(a, base) {
		return fmt.Errorf("PHP 构建已改变，安装未发布")
	}
	root := filepath.Join(m.root, "downloads", "staged")
	if err = os.MkdirAll(root, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(root, "extension-")
	if err != nil {
		return err
	}
	retained := false
	defer func() {
		if !retained {
			_ = os.RemoveAll(stage)
		}
	}()
	main, err := extensionStageMain(stage, a.Name, a.Format, archive)
	if err != nil {
		return err
	}
	binary, err := pe.Open(main)
	if err != nil {
		return fmt.Errorf("扩展不是有效的 Windows DLL: %w", err)
	}
	machine := binary.Machine
	flags := binary.Characteristics
	binary.Close()
	if flags&pe.IMAGE_FILE_DLL == 0 || base.Bits == 64 && machine != pe.IMAGE_FILE_MACHINE_AMD64 || base.Bits == 32 && machine != pe.IMAGE_FILE_MACHINE_I386 {
		return fmt.Errorf("扩展 DLL 架构不兼容")
	}
	digest, err := fileHash(main)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(m.root, stage)
	if err != nil {
		return err
	}
	relativeDLL := flatExtensionRelativeDLL(p, a.Name)
	prepared := PreparedPHPExtension{TaskID: key, StageRelative: filepath.ToSlash(relative), Record: InstalledPHPExtension{ID: "php-ext-installed-" + digest[:24] + "-" + p.ID, PackageID: p.ID, ArtifactID: a.ID, Name: a.Name, Version: a.Version, Kind: a.Kind, RelativeDLL: filepath.ToSlash(relativeDLL), DLLSHA256: digest, Build: base.Build, ZendBuild: base.ZendBuild, Verification: a.Verification, InstalledAt: timestamp()}}
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = m.validatePreparedExtension(prepared, i, p); err != nil {
		return err
	}
	if err = m.change(func(s *State) error {
		for _, item := range s.Packages {
			if item.ID == p.ID {
				s.PreparedExtensions = append(s.PreparedExtensions, prepared)
				return nil
			}
		}
		return fmt.Errorf("PHP 版本已删除，安装未发布")
	}); err != nil {
		return err
	}
	retained = true
	return m.applyPreparedExtensionsLocked(p.ID)
}
func (m *Manager) validatePreparedExtension(item PreparedPHPExtension, i Instance, p InstalledPackage) error {
	stage, err := m.preparedExtensionStage(item)
	if err != nil {
		return err
	}
	set, err := readPHPINI(i)
	if err != nil {
		return err
	}
	base, _, err := probePHP(p, "", "", i.Path)
	if err != nil {
		return err
	}
	if base.Build != item.Record.Build || item.Record.Kind == "zend" && base.ZendBuild != item.Record.ZendBuild {
		return fmt.Errorf("PHP 构建已改变")
	}
	rows, err := phpInventory(p, base, set, m.Snapshot().ExtensionPackages...)
	if err != nil {
		return err
	}
	found := false
	for n := range rows {
		if rows[n].Name == item.Record.Name {
			if !rows[n].Editable {
				return fmt.Errorf("扩展 %s 已有内置或手工加载项，请先在配置编辑器中处理", item.Record.Name)
			}
			rows[n].DLLPath = filepath.Join(stage, "php_"+item.Record.Name+".dll")
			rows[n].Version = item.Record.Version
			found = true
		}
	}
	if !found {
		rows = append(rows, PHPExtension{Name: item.Record.Name, Kind: item.Record.Kind, Filename: "php_" + item.Record.Name + ".dll", DLLPath: filepath.Join(stage, "php_"+item.Record.Name+".dll"), Editable: true, Dependencies: []string{}})
	}
	main, managed, err := phpCandidate(set, rows, PHPSettingsRequest{Extensions: []PHPSelection{{Name: item.Record.Name, Enabled: true}}})
	if err != nil {
		return err
	}
	check, err := os.MkdirTemp(filepath.Join(i.Path, "temp"), "extension-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(check)
	scan := filepath.Join(check, "conf.d")
	if err = os.Mkdir(scan, 0700); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(check, "php.ini"), main, 0600); err != nil {
		return err
	}
	for name, body := range set.scan {
		if name != phpManagedINI {
			if err = os.WriteFile(filepath.Join(scan, name), body, 0600); err != nil {
				return err
			}
		}
	}
	if err = os.WriteFile(filepath.Join(scan, phpManagedINI), managed, 0600); err != nil {
		return err
	}
	probe, warnings, err := probePHP(p, filepath.Join(check, "php.ini"), scan, i.Path)
	if err != nil {
		return err
	}
	if warnings != "" {
		return fmt.Errorf("扩展候选加载失败，原配置已保留: %s", warnings)
	}
	version, loaded := probe.Modules[item.Record.Name]
	if !loaded || version != item.Record.Version {
		return fmt.Errorf("实际扩展身份/版本与下载制品不符: %s %s", item.Record.Name, version)
	}
	for _, row := range rows {
		if row.Configured {
			if _, ok := probe.Modules[row.Name]; !ok {
				return fmt.Errorf("现有扩展 %s 在候选配置中未能加载", row.Name)
			}
		}
	}
	return nil
}
func (m *Manager) preparedExtensionStage(item PreparedPHPExtension) (string, error) {
	if filepath.IsAbs(item.StageRelative) || strings.Contains(item.StageRelative, ":") {
		return "", fmt.Errorf("临时扩展路径无效")
	}
	path := filepath.Join(m.root, filepath.FromSlash(item.StageRelative))
	root := filepath.Join(m.root, "downloads", "staged")
	if !within(root, path) {
		return "", fmt.Errorf("临时扩展路径越界")
	}
	real, err := PhysicalPath(path)
	if err != nil || !samePHPPath(real, path) {
		return "", fmt.Errorf("临时扩展目录不存在或被重定向")
	}
	hash, err := fileHash(filepath.Join(path, "php_"+item.Record.Name+".dll"))
	if err != nil || hash != item.Record.DLLSHA256 {
		return "", fmt.Errorf("待安装扩展已被修改，请重新下载")
	}
	return path, nil
}
func (m *Manager) packagePHPRunningLocked(packageID string) bool {
	for _, i := range m.state.Instances {
		if i.PackageID == packageID && (m.processes[i.ID] != nil || m.operations[instanceStartKey(i)] || i.Status == "running" || i.Status == "starting" || i.Status == "stopping") {
			return true
		}
	}
	return false
}

// Caller holds siteMu. Publishing is short and reserves the PHP package against
// concurrent starts/removal; network transfers never hold this reservation.
func (m *Manager) applyPreparedExtensionsLocked(packageID string) error {
	state := m.Snapshot()
	items := []PreparedPHPExtension{}
	for _, item := range state.PreparedExtensions {
		if item.Record.PackageID == packageID {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return nil
	}
	m.mu.Lock()
	if m.operations[packageID] || m.closing {
		m.mu.Unlock()
		return fmt.Errorf("PHP 版本正在变更或应用正在退出")
	}
	m.operations[packageID] = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.operations, packageID); m.mu.Unlock() }()
	for _, item := range items {
		task, err := extensionTask(m.Snapshot(), item.TaskID)
		if err != nil {
			return err
		}
		i, p, err := m.lookupInstance(task.InstanceID)
		if err != nil {
			return fmt.Errorf("待安装扩展的目标实例已删除，请重新选择实例")
		}
		old := false
		for _, e := range m.Snapshot().ExtensionPackages {
			if e.PackageID == packageID && e.Name == item.Record.Name {
				old = true
			}
		}
		m.mu.Lock()
		running := m.packagePHPRunningLocked(packageID)
		m.mu.Unlock()
		if old && running {
			if err = m.updateTask(item.TaskID, func(t *Task) {
				t.Status = "awaiting_restart"
				t.Warning = "新文件已准备，停止或重启此 PHP 后替换"
				t.Speed = 0
			}); err != nil {
				return err
			}
			continue
		}
		if err = m.validatePreparedExtension(item, i, p); err != nil {
			_ = m.updateTask(item.TaskID, func(t *Task) {
				t.Status = "awaiting_restart"
				t.Error = err.Error()
				t.Warning = "原扩展和配置已保留"
			})
			return err
		}
		if err = m.publishPreparedExtension(item, p); err != nil {
			return err
		}
		warning := ""
		if task.EnableExtension {
			if _, err = m.savePHPExtensionsReserved(i, p, PHPSettingsRequest{Revision: task.ConfigRevision, Extensions: []PHPSelection{{Name: item.Record.Name, Enabled: true}}}); err != nil {
				warning = "扩展已安装，但未启用：" + err.Error()
			}
		}
		if err = m.updateTask(item.TaskID, func(t *Task) {
			t.Status = "completed"
			t.Warning = warning
			t.Error = ""
			t.Speed = 0
			t.Received = t.Total
		}); err != nil {
			return err
		}
	}
	return nil
}

type extensionPublication struct {
	Prepared   PreparedPHPExtension `json:"prepared"`
	BackupName string               `json:"backupName"`
}

func (m *Manager) publicationPaths(j extensionPublication, p InstalledPackage) (target, backup, journal string, err error) {
	if filepath.Base(p.ID) != p.ID || strings.ContainsAny(p.ID, "/\\:") {
		return "", "", "", fmt.Errorf("PHP 包标识无效")
	}
	name := j.Prepared.Record.Name
	if name != "redis" && name != "apcu" && name != "xdebug" {
		return "", "", "", fmt.Errorf("扩展发布名称无效")
	}
	root := filepath.Join(filepath.Dir(p.Executable), "extensions")
	if !within(p.Path, root) {
		return "", "", "", fmt.Errorf("扩展安装根目录越界")
	}
	target = filepath.Join(root, name)
	if j.BackupName != "" && (filepath.Base(j.BackupName) != j.BackupName || !strings.HasPrefix(j.BackupName, ".before-")) {
		return "", "", "", fmt.Errorf("扩展备份路径无效")
	}
	backup = filepath.Join(root, j.BackupName)
	if err = os.MkdirAll(root, 0700); err != nil {
		return
	}
	physical, e := PhysicalPath(root)
	if e != nil || !samePHPPath(root, physical) {
		err = fmt.Errorf("扩展根目录被重定向")
		return
	}
	journal = filepath.Join(m.root, "state", "extension-publish-"+j.Prepared.Record.Name+"-"+p.ID+".json")
	return
}
func (m *Manager) publishPreparedExtension(item PreparedPHPExtension, p InstalledPackage) error {
	return m.publishFlatExtension(item, p)
}

// Retained to recover publication journals written by earlier releases.
func (m *Manager) publishLegacyPreparedExtension(item PreparedPHPExtension, p InstalledPackage) error {
	stage, err := m.preparedExtensionStage(item)
	if err != nil {
		return err
	}
	j := extensionPublication{Prepared: item, BackupName: ".before-" + item.Record.Name + "-" + item.TaskID}
	target, backup, journal, err := m.publicationPaths(j, p)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(backup); !os.IsNotExist(err) {
		return fmt.Errorf("扩展备份目录已存在，请先完成恢复")
	}
	exists := false
	if _, err = os.Lstat(target); err == nil {
		exists = true
		owned := false
		for _, record := range m.Snapshot().ExtensionPackages {
			if record.PackageID == p.ID && record.Name == item.Record.Name {
				owned = true
			}
		}
		if !owned {
			return fmt.Errorf("目标扩展目录已有手工文件，未覆盖")
		}
		real, err := PhysicalPath(target)
		if err != nil || !samePHPPath(real, target) {
			return fmt.Errorf("目标扩展目录被重定向")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	body, _ := json.MarshalIndent(j, "", "  ")
	if err = atomicConfigWrite(journal, body); err != nil {
		return err
	}
	if exists {
		if err = os.Rename(target, backup); err != nil {
			_ = os.Remove(journal)
			return fmt.Errorf("扩展目录被占用，旧文件已保留: %w", err)
		}
	}
	rollback := func(original error) error {
		if _, err := os.Stat(target); err == nil {
			_ = os.Rename(target, stage)
		}
		if exists {
			if err := os.Rename(backup, target); err != nil {
				return fmt.Errorf("%v；恢复失败: %v，备份保留在 %s", original, err, backup)
			}
		}
		_ = os.Remove(journal)
		return original
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err = renameInstallDirectory(ctx, stage, target); err != nil {
		return rollback(err)
	}
	if err = m.change(func(s *State) error {
		kept := []InstalledPHPExtension{}
		for _, e := range s.ExtensionPackages {
			if e.PackageID != p.ID || e.Name != item.Record.Name {
				kept = append(kept, e)
			}
		}
		s.ExtensionPackages = append(kept, item.Record)
		remaining := []PreparedPHPExtension{}
		for _, e := range s.PreparedExtensions {
			if e.TaskID != item.TaskID {
				remaining = append(remaining, e)
			}
		}
		s.PreparedExtensions = remaining
		return nil
	}); err != nil {
		return rollback(err)
	}
	if exists {
		if err = os.RemoveAll(backup); err != nil {
			return fmt.Errorf("新扩展已安装，但临时备份清理失败: %w", err)
		}
	}
	return os.Remove(journal)
}
func (m *Manager) recoverExtensionPublications() error {
	files, err := filepath.Glob(filepath.Join(m.root, "state", "extension-publish-*.json"))
	if err != nil {
		return err
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if len(body) > 1<<20 {
			return fmt.Errorf("扩展发布记录过大")
		}
		var j extensionPublication
		var layout struct {
			Layout string `json:"layout"`
		}
		if err = json.Unmarshal(body, &layout); err != nil {
			return err
		}
		if layout.Layout == "flat" {
			if err = m.recoverFlatExtension(file, body); err != nil {
				return err
			}
			continue
		}
		if err = json.Unmarshal(body, &j); err != nil {
			return err
		}
		var p InstalledPackage
		for _, candidate := range m.state.Packages {
			if candidate.ID == j.Prepared.Record.PackageID {
				p = candidate
			}
		}
		if p.ID == "" {
			return fmt.Errorf("扩展发布恢复缺少 PHP 包，保留记录 %s", file)
		}
		target, backup, journal, err := m.publicationPaths(j, p)
		if err != nil {
			return err
		}
		if !samePHPPath(journal, file) {
			return fmt.Errorf("扩展发布记录路径无效")
		}
		committed := false
		for _, e := range m.state.ExtensionPackages {
			if e.ID == j.Prepared.Record.ID {
				committed = true
			}
		}
		if committed {
			if _, err := os.Stat(backup); err == nil {
				if err = os.RemoveAll(backup); err != nil {
					return err
				}
			}
			for n := range m.state.Tasks {
				if m.state.Tasks[n].ID == j.Prepared.TaskID && m.state.Tasks[n].Status != "completed" {
					m.state.Tasks[n].Status = "completed"
					m.state.Tasks[n].Warning = "扩展文件已恢复安装，启用状态请重新检测"
					m.state.Tasks[n].Error = ""
				}
			}
		} else {
			stage := filepath.Join(m.root, filepath.FromSlash(j.Prepared.StageRelative))
			if !within(filepath.Join(m.root, "downloads", "staged"), stage) {
				return fmt.Errorf("恢复临时路径越界")
			}
			if _, err := os.Stat(target); err == nil {
				hash, err := fileHash(filepath.Join(target, "php_"+j.Prepared.Record.Name+".dll"))
				if _, backupErr := os.Stat(backup); os.IsNotExist(backupErr) {
					if _, stageErr := os.Stat(stage); stageErr == nil {
						ownedOld := false
						for _, old := range m.state.ExtensionPackages {
							if old.PackageID == p.ID && old.Name == j.Prepared.Record.Name && old.DLLSHA256 == hash {
								ownedOld = true
							}
						}
						if ownedOld {
							if err = os.Remove(file); err != nil {
								return err
							}
							continue
						}
					}
				}
				if err != nil || hash != j.Prepared.Record.DLLSHA256 {
					return fmt.Errorf("恢复时扩展文件发生变化，保留目录 %s", target)
				}
				if _, err := os.Stat(stage); os.IsNotExist(err) {
					if err = os.Rename(target, stage); err != nil {
						return err
					}
				} else {
					return fmt.Errorf("恢复临时目录冲突，保留发布记录")
				}
			}
			if _, err := os.Stat(backup); err == nil {
				if err = os.Rename(backup, target); err != nil {
					return err
				}
			}
			for n := range m.state.Tasks {
				if m.state.Tasks[n].ID == j.Prepared.TaskID {
					m.state.Tasks[n].Status = "awaiting_restart"
					m.state.Tasks[n].Warning = "已恢复原扩展，待重新应用准备好的更新"
				}
			}
		}
		if err = os.Remove(file); err != nil {
			return err
		}
	}
	return nil
}

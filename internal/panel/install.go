package panel

import (
	"archive/zip"
	"context"
	"debug/pe"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func executableName(component string) string {
	switch component {
	case "php":
		return "php-cgi.exe"
	case "nginx":
		return "nginx.exe"
	case "mysql":
		return "mysqld.exe"
	case "redis":
		return "redis-server.exe"
	}
	return ""
}
func extractZIP(archive, dest string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("无法读取 ZIP: %w", err)
	}
	defer reader.Close()
	if len(reader.File) > 40000 {
		return fmt.Errorf("安装包包含过多文件")
	}
	var total uint64
	for _, f := range reader.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.HasPrefix(name, "/") || strings.ContainsAny(name, ":\x00") || f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝不安全的归档条目: %s", f.Name)
		}
		for _, segment := range strings.Split(name, "/") {
			if segment == ".." || strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
				return fmt.Errorf("拒绝越界或无效的归档路径")
			}
			base := strings.ToUpper(strings.Split(segment, ".")[0])
			if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || len(base) == 4 && ((strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
				return fmt.Errorf("拒绝 Windows 保留文件名")
			}
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if !within(dest, target) {
			return fmt.Errorf("归档路径越界")
		}
		total += f.UncompressedSize64
		if total > 4<<30 {
			return fmt.Errorf("解压后超过 4 GiB 上限")
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		input, err := f.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, io.LimitReader(input, int64(f.UncompressedSize64)+1))
		inputErr := input.Close()
		outputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		if outputErr != nil {
			return outputErr
		}
	}
	return nil
}
func findExecutable(root, component string) (string, error) {
	name := executableName(component)
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), name) {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(paths) != 1 {
		return "", fmt.Errorf("安装包需要包含唯一的 %s，实际找到 %d 个", name, len(paths))
	}
	binary, err := pe.Open(paths[0])
	if err != nil {
		return "", fmt.Errorf("%s 不是有效的 Windows 可执行文件", name)
	}
	defer binary.Close()
	if binary.Machine != pe.IMAGE_FILE_MACHINE_AMD64 && !(component == "nginx" && binary.Machine == pe.IMAGE_FILE_MACHINE_I386) {
		return "", fmt.Errorf("首版需要 Windows x64 安装包")
	}
	return paths[0], nil
}
func (m *Manager) installZIP(a Artifact, archive string) (InstalledPackage, error) {
	return m.installZIPContext(context.Background(), a, archive)
}
func (m *Manager) installZIPContext(ctx context.Context, a Artifact, archive string) (InstalledPackage, error) {
	if err := ctx.Err(); err != nil {
		return InstalledPackage{}, err
	}
	if !validComponent(a.Component) || !validLabel(a.Version) {
		return InstalledPackage{}, fmt.Errorf("组件或版本无效")
	}
	hash, err := fileHash(archive)
	if err != nil {
		return InstalledPackage{}, err
	}
	if a.SHA256 != "" && !strings.EqualFold(a.SHA256, hash) {
		return InstalledPackage{}, fmt.Errorf("SHA-256 不匹配，拒绝安装")
	}
	a.SHA256 = hash
	a.ID = artifactID(a)
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return InstalledPackage{}, fmt.Errorf("应用正在退出")
	}
	if m.operations[a.ID] {
		m.mu.Unlock()
		return InstalledPackage{}, fmt.Errorf("该版本正在变更")
	}
	for _, p := range m.state.Packages {
		if p.ID == a.ID || p.Component == a.Component && p.SHA256 == a.SHA256 {
			m.mu.Unlock()
			return p, nil
		}
	}
	m.operations[a.ID] = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer func() { m.mu.Lock(); delete(m.operations, a.ID); m.mu.Unlock() }()
	packagesRoot := filepath.Join(m.root, "packages")
	stage, err := os.MkdirTemp(packagesRoot, ".install-")
	if err != nil {
		return InstalledPackage{}, err
	}
	defer os.RemoveAll(stage)
	if err := extractZIP(archive, stage); err != nil {
		return InstalledPackage{}, err
	}
	exe, err := findExecutable(stage, a.Component)
	if err != nil {
		return InstalledPackage{}, err
	}
	rel, err := filepath.Rel(stage, exe)
	if err != nil {
		return InstalledPackage{}, err
	}
	target, releaseDirectory, err := m.reserveDirectory(packagesRoot, readableDirectoryName("package", a.Component, a.Version))
	if err != nil {
		return InstalledPackage{}, err
	}
	defer releaseDirectory()
	if !within(packagesRoot, target) {
		return InstalledPackage{}, fmt.Errorf("安装目录越界")
	}
	if _, err := os.Lstat(target); err == nil {
		return InstalledPackage{}, fmt.Errorf("安装目录已存在但元数据缺失，请检查 %s", target)
	} else if !os.IsNotExist(err) {
		return InstalledPackage{}, fmt.Errorf("无法检查安装目录 %s: %w", target, err)
	}
	if err := renameInstallDirectory(ctx, stage, target); err != nil {
		return InstalledPackage{}, err
	}
	p := InstalledPackage{DirectoryName: filepath.Base(target), ID: a.ID, Component: a.Component, Version: a.Version, Publisher: a.Publisher, SHA256: hash, Verification: a.Verification, Path: target, Executable: filepath.Join(target, rel), InstalledAt: timestamp()}
	if err := m.change(func(s *State) error { s.Packages = append(s.Packages, p); return nil }); err != nil {
		_ = os.RemoveAll(target)
		return InstalledPackage{}, err
	}
	return p, nil
}
func (m *Manager) ImportArchive(component, version, path, expectedHash string) (InstalledPackage, error) {
	expectedHash = strings.ToLower(strings.TrimSpace(expectedHash))
	if expectedHash != "" && !validHash(expectedHash) {
		return InstalledPackage{}, fmt.Errorf("SHA-256 应为 64 位十六进制")
	}
	verification := "本地导入 · 已记录摘要"
	if expectedHash != "" {
		verification = "本地导入 · 已核对用户摘要"
	}
	return m.installZIP(Artifact{Component: component, Version: version, Publisher: "本地导入", SHA256: expectedHash, Verification: verification}, path)
}
func (m *Manager) RemovePackage(key string) error {
	m.mu.Lock()
	var handler func()
	defer func() {
		m.mu.Unlock()
		if handler != nil {
			handler()
		}
	}()
	if m.operations[key] {
		return fmt.Errorf("此版本正在变更")
	}
	for _, i := range m.state.Instances {
		if i.PackageID == key {
			return fmt.Errorf("版本被实例 %s 引用，不能卸载", i.Name)
		}
	}
	index := -1
	for i, p := range m.state.Packages {
		if p.ID == key {
			index = i
		}
	}
	if index < 0 {
		return fmt.Errorf("版本不存在")
	}
	p := m.state.Packages[index]
	root := filepath.Join(m.root, "packages")
	if !within(root, p.Path) {
		return fmt.Errorf("安装目录越界")
	}
	trash := p.Path + ".removing"
	if err := os.Rename(p.Path, trash); err != nil {
		return err
	}
	before := append([]InstalledPackage{}, m.state.Packages...)
	beforeExtensions := append([]InstalledPHPExtension{}, m.state.ExtensionPackages...)
	beforeRevision := m.state.Revision
	m.state.Packages = append(m.state.Packages[:index], m.state.Packages[index+1:]...)
	kept := []InstalledPHPExtension{}
	for _, e := range m.state.ExtensionPackages {
		if e.PackageID != key {
			kept = append(kept, e)
		}
	}
	m.state.ExtensionPackages = kept
	m.state.Revision++
	if err := m.saveLocked(); err != nil {
		m.state.Packages = before
		m.state.ExtensionPackages = beforeExtensions
		m.state.Revision = beforeRevision
		_ = os.Rename(trash, p.Path)
		return err
	}
	handler = m.onChange
	return os.RemoveAll(trash)
}

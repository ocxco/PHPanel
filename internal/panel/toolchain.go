package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type ToolchainSettings struct {
	PHPPathDisabled bool   `json:"phpPathDisabled,omitempty"`
	PHPEntrySHA256  string `json:"phpEntrySha256,omitempty"`
	// PHPInstanceID is the legacy selector (instance id). Retained only to read
	// pre-migration panel.json; migratePHPCollapse converts it to PHPPackageID.
	PHPInstanceID string `json:"phpInstanceId,omitempty"`
	// PHPPackageID is the merged selector: a PHP package id (package == runtime).
	PHPPackageID    string `json:"phpPackageId,omitempty"`
	ComposerVersion string `json:"composerVersion"`
	ComposerSHA256  string `json:"composerSha256"`
}
type CLIStatus struct {
	PHPPathEnabled      bool   `json:"phpPathEnabled"`
	ComposerPathEnabled bool   `json:"composerPathEnabled"`
	PHPInstanceID       string `json:"phpInstanceId"`
	PHPVersion          string `json:"phpVersion"`
	ComposerVersion     string `json:"composerVersion"`
	ComposerInstalled   bool   `json:"composerInstalled"`
	UserPathEnabled     bool   `json:"userPathEnabled"`
	Directory           string `json:"directory"`
	Warning             string `json:"warning"`
}
type CLIInvocation struct {
	Executable string
	Args       []string
	Env        []string
}

func prependManagedUserPath(existing, dir string) string {
	items := []string{dir}
	for _, part := range strings.Split(existing, ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		if samePHPPath(strings.Trim(strings.TrimSpace(part), "\""), dir) {
			continue
		}
		items = append(items, part)
	}
	return strings.Join(items, ";")
}
func pathHasDirectory(value, dir string) bool {
	for _, part := range strings.Split(value, ";") {
		if samePHPPath(strings.Trim(strings.TrimSpace(part), "\""), dir) {
			return true
		}
	}
	return false
}
func removeManagedUserPath(existing, dir string) string {
	items := []string{}
	for _, part := range strings.Split(existing, ";") {
		if !samePHPPath(strings.Trim(strings.TrimSpace(part), "\""), dir) {
			items = append(items, part)
		}
	}
	return strings.Join(items, ";")
}
func cliEntryExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name+".exe"))
	return err == nil && info.Mode().IsRegular()
}
func (m *Manager) pathAccess() (func() (string, string, error), func(string) error) {
	reader := m.userPathReader
	if reader == nil {
		reader = readUserAndSystemPath
	}
	writer := m.userPathWriter
	if writer == nil {
		writer = writeUserPath
	}
	return reader, writer
}

// activePHPPackageID resolves the selected CLI PHP package id, preferring the
// merged PHPPackageID selector and falling back to the legacy PHPInstanceID
// (which holds a package id after migratePHPCollapse) so pre-migration data
// and tests that write the legacy field keep working.
func activePHPPackageID(state State) string {
	if state.Tools.PHPPackageID != "" {
		return state.Tools.PHPPackageID
	}
	return state.Tools.PHPInstanceID
}

func (m *Manager) GetCLIStatus() (CLIStatus, error) {
	state := m.Snapshot()
	pkgID := activePHPPackageID(state)
	status := CLIStatus{PHPInstanceID: pkgID, ComposerVersion: state.Tools.ComposerVersion, Directory: filepath.Join(m.root, "cli")}
	for _, p := range state.Packages {
		if p.ID == pkgID && p.Component == "php" {
			status.PHPVersion = p.Version
		}
	}
	if state.Tools.ComposerSHA256 != "" {
		hash, err := fileHash(filepath.Join(m.root, "tools", "composer", "composer.phar"))
		status.ComposerInstalled = err == nil && hash == state.Tools.ComposerSHA256
	}
	reader, _ := m.pathAccess()
	user, system, err := reader()
	if err != nil {
		return status, err
	}
	status.UserPathEnabled = pathHasDirectory(user, status.Directory)
	status.PHPPathEnabled = status.UserPathEnabled && cliEntryExists(status.Directory, "php")
	status.ComposerPathEnabled = status.UserPathEnabled && status.ComposerInstalled && cliEntryExists(status.Directory, "composer")
	for _, entry := range strings.Split(system, ";") {
		entry = strings.Trim(strings.TrimSpace(entry), "\"")
		entry = regexp.MustCompile(`%([^%]+)%`).ReplaceAllStringFunc(entry, func(token string) string {
			if value, ok := os.LookupEnv(strings.Trim(token, "%")); ok {
				return value
			}
			return token
		})
		if entry == "" || strings.Contains(entry, "%") {
			continue
		}
		for _, file := range []string{"php.exe", "php.cmd", "php.bat", "composer.exe", "composer.cmd", "composer.bat"} {
			if _, err := os.Stat(filepath.Join(entry, file)); err == nil && !samePHPPath(entry, status.Directory) {
				status.Warning = "系统 PATH 中存在 PHP 或 Composer，它可能优先于用户 PATH。PHPanel 只修改当前用户 PATH；可直接使用命令入口目录中的程序，或自行调整系统级冲突。"
				return status, nil
			}
		}
	}
	return status, nil
}
func (m *Manager) cliSource() (string, error) {
	source := m.cliHelperPath
	if source == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		source = filepath.Join(filepath.Dir(exe), "phpanel-cli.exe")
	}
	info, err := os.Stat(source)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("缺少命令入口程序 phpanel-cli.exe，请使用完整构建包")
	}
	return source, nil
}
func (m *Manager) ensureCLIEntries(names ...string) error {
	source, err := m.cliSource()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		names = []string{"php", "composer"}
	}
	dir := filepath.Join(m.root, "cli")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for _, name := range names {
		if name != "php" && name != "composer" {
			return fmt.Errorf("未知命令入口")
		}
		dest := filepath.Join(dir, name+".exe")
		hash, _ := fileHash(source)
		current, _ := fileHash(dest)
		if current == hash {
			continue
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		out, err := os.CreateTemp(dir, ".cli-entry-")
		if err != nil {
			input.Close()
			return err
		}
		temp := out.Name()
		_, copyErr := out.ReadFrom(input)
		input.Close()
		closeErr := out.Close()
		if copyErr != nil {
			_ = os.Remove(temp)
			return copyErr
		}
		if closeErr != nil {
			_ = os.Remove(temp)
			return closeErr
		}
		if err = os.Rename(temp, dest); err != nil {
			_ = os.Remove(temp)
			return fmt.Errorf("命令入口正在使用，请关闭正在执行的 PHP/Composer 命令后重试: %w", err)
		}
	}
	return nil
}
func (m *Manager) SelectCLIPHP(key string, enablePath bool) (CLIStatus, error) {
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	i, p, release, err := m.reservePHPOperation(key)
	if err != nil {
		return CLIStatus{}, err
	}
	defer release()
	if _, err = os.Stat(filepath.Join(filepath.Dir(p.Executable), "php.exe")); err != nil {
		return CLIStatus{}, fmt.Errorf("此 PHP 包没有 php.exe")
	}
	if _, err = readPHPINI(i); err != nil {
		return CLIStatus{}, err
	}
	commands := []string{"composer"}
	if enablePath || !m.Snapshot().Tools.PHPPathDisabled {
		commands = append(commands, "php")
	}
	phpWasPresent := cliEntryExists(filepath.Join(m.root, "cli"), "php")
	committed := false
	defer func() {
		if !committed && !phpWasPresent {
			_ = os.Remove(filepath.Join(m.root, "cli", "php.exe"))
		}
	}()
	if err = m.ensureCLIEntries(commands...); err != nil {
		return CLIStatus{}, err
	}
	reader, writer := m.pathAccess()
	oldPath, _, err := reader()
	if err != nil {
		return CLIStatus{}, err
	}
	changed := false
	if enablePath {
		next := prependManagedUserPath(oldPath, filepath.Join(m.root, "cli"))
		if next != oldPath {
			if err = writer(next); err != nil {
				return CLIStatus{}, err
			}
			changed = true
		}
	}
	entryHash, _ := fileHash(filepath.Join(m.root, "cli", "php.exe"))
	if err = m.change(func(s *State) error {
		s.Tools.PHPPackageID = key
		s.Tools.PHPInstanceID = key
		if enablePath {
			s.Tools.PHPPathDisabled = false
		}
		if entryHash != "" {
			s.Tools.PHPEntrySHA256 = entryHash
		}
		return nil
	}); err != nil {
		if changed {
			_ = writer(oldPath)
		}
		return CLIStatus{}, err
	}
	committed = true
	return m.GetCLIStatus()
}
func (m *Manager) EnableComposerPath() (CLIStatus, error) {
	finish, err := m.trackServiceAction()
	if err != nil {
		return CLIStatus{}, err
	}
	defer finish()
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	state := m.Snapshot()
	pkgID := activePHPPackageID(state)
	if state.Tools.ComposerVersion == "" || pkgID == "" {
		return CLIStatus{}, fmt.Errorf("请先安装 Composer 并选择命令行 PHP 实例")
	}
	status, err := m.GetCLIStatus()
	if err != nil {
		return status, err
	}
	if !status.ComposerInstalled {
		return status, fmt.Errorf("Composer 文件缺失或校验失败，请重新安装")
	}
	i, p, err := m.lookupInstance(pkgID)
	if err != nil || i.Component != "php" {
		return status, fmt.Errorf("请重新选择 Composer 使用的 PHP 实例")
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(p.Executable), "php.exe")); err != nil {
		return status, fmt.Errorf("Composer 使用的 PHP 文件缺失，请重新安装")
	}
	if _, err = readPHPINI(i); err != nil {
		return status, err
	}
	if err = m.ensureCLIEntries("composer"); err != nil {
		return status, err
	}
	reader, writer := m.pathAccess()
	user, _, err := reader()
	if err != nil {
		return status, err
	}
	next := prependManagedUserPath(user, status.Directory)
	if next != user {
		if err = writer(next); err != nil {
			return status, err
		}
	}
	return m.GetCLIStatus()
}

func (m *Manager) DisableCLIPHPPath() (CLIStatus, error) {
	finish, err := m.trackServiceAction()
	if err != nil {
		return CLIStatus{}, err
	}
	defer finish()
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	status, err := m.GetCLIStatus()
	if err != nil {
		return status, err
	}
	reader, writer := m.pathAccess()
	oldPath, _, err := reader()
	if err != nil {
		return status, err
	}
	entry := filepath.Join(status.Directory, "php.exe")
	backup := ""
	if cliEntryExists(status.Directory, "php") {
		physicalDir, err := PhysicalPath(status.Directory)
		if err != nil || !samePHPPath(physicalDir, status.Directory) {
			return status, fmt.Errorf("命令入口目录被重定向，拒绝取消以保护其它目录")
		}
		hash, err := fileHash(entry)
		if err != nil {
			return status, err
		}
		expected := m.Snapshot().Tools.PHPEntrySHA256
		if expected == "" {
			source, err := m.cliSource()
			if err != nil {
				return status, err
			}
			expected, err = fileHash(source)
			if err != nil {
				return status, err
			}
		}
		if hash != expected {
			return status, fmt.Errorf("PHP 命令入口已被其它程序修改，原文件已保留，请检查 cli/php.exe")
		}
		backup = filepath.Join(status.Directory, ".php-disable-"+id("entry"))
		if err = os.Rename(entry, backup); err != nil {
			return status, fmt.Errorf("PHP 命令入口正在使用，请结束正在执行的 PHP 命令后再取消: %w", err)
		}
	}
	pathChanged := false
	rollback := func(cause error) (CLIStatus, error) {
		if pathChanged {
			if restoreErr := writer(oldPath); restoreErr != nil {
				cause = fmt.Errorf("%v；PATH 恢复失败: %w", cause, restoreErr)
			}
		}
		if backup != "" {
			if restoreErr := os.Rename(backup, entry); restoreErr != nil {
				cause = fmt.Errorf("%v；入口恢复失败，文件保留在 %s: %w", cause, backup, restoreErr)
			}
		}
		return status, cause
	}
	if !status.ComposerPathEnabled {
		next := removeManagedUserPath(oldPath, status.Directory)
		if next != oldPath {
			if err = writer(next); err != nil {
				return rollback(err)
			}
			pathChanged = true
		}
	}
	if err = m.change(func(s *State) error { s.Tools.PHPPathDisabled = true; return nil }); err != nil {
		return rollback(err)
	}
	if backup != "" {
		if err = os.Remove(backup); err != nil {
			status, lookupErr := m.GetCLIStatus()
			if lookupErr != nil {
				return status, lookupErr
			}
			status.Warning = "PHP 环境变量已取消，但旧入口清理失败: " + err.Error()
			return status, nil
		}
	}
	return m.GetCLIStatus()
}

// A console launcher resolves the current instance at each invocation. PHP
// switching needs no repeated PATH edits and never changes unrelated entries.
func ResolveCLIInvocation(root, command string, args []string) (CLIInvocation, error) {
	body, err := os.ReadFile(filepath.Join(root, "state", "panel.json"))
	if err != nil {
		return CLIInvocation{}, err
	}
	var state State
	if err = json.Unmarshal(body, &state); err != nil {
		return CLIInvocation{}, err
	}
	rebaseStatePaths(&state, state.Root, root)
	pkgID := activePHPPackageID(state)
	var p InstalledPackage
	for _, item := range state.Packages {
		if item.ID == pkgID && item.Component == "php" {
			p = item
		}
	}
	if p.ID == "" {
		return CLIInvocation{}, fmt.Errorf("请在 PHPanel 中选择命令行 PHP 实例")
	}
	i := instanceFromPackage(p)
	executable := filepath.Join(filepath.Dir(p.Executable), "php.exe")
	ini := phpINIPath(i)
	scan := phpConfDPath(i)
	if _, err = os.Stat(executable); err != nil {
		return CLIInvocation{}, err
	}
	if _, err = os.Stat(ini); err != nil {
		return CLIInvocation{}, err
	}
	argv := []string{"-c", nativePath(ini)}
	if command == "composer" {
		phar := filepath.Join(root, "tools", "composer", "composer.phar")
		hash, err := fileHash(phar)
		if err != nil || hash != state.Tools.ComposerSHA256 {
			return CLIInvocation{}, fmt.Errorf("Composer 文件缺失或被修改，请重新安装")
		}
		argv = append(argv, phar)
	} else if command != "php" {
		return CLIInvocation{}, fmt.Errorf("未知命令入口")
	}
	argv = append(argv, args...)
	return CLIInvocation{Executable: executable, Args: argv, Env: phpEnvironment(ini, scan, phpLibraryDirs(ini, scan)...)}, nil
}

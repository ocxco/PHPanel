package panel

import (
	"bytes"
	"context"
	"debug/pe"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const phpManagedINI = "50-extensions.ini"

type PHPExtension struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Kind         string   `json:"kind"`
	Filename     string   `json:"filename"`
	Version      string   `json:"version"`
	Configured   bool     `json:"configured"`
	Editable     bool     `json:"editable"`
	Loaded       bool     `json:"loaded"`
	State        string   `json:"state"`
	Source       string   `json:"source"`
	Dependencies []string `json:"dependencies"`
	DLLPath      string   `json:"dllPath,omitempty"`
}
type PHPExtensionReport struct {
	Version      string            `json:"version"`
	Bits         int               `json:"bits"`
	ThreadSafe   bool              `json:"threadSafe"`
	Build        string            `json:"build"`
	Revision     string            `json:"revision"`
	Extensions   []PHPExtension    `json:"extensions"`
	Settings     map[string]string `json:"settings"`
	Running      bool              `json:"running"`
	RuntimeKnown bool              `json:"runtimeKnown"`
	RuntimeError string            `json:"runtimeError"`
	Warnings     string            `json:"warnings"`
	Pending      bool              `json:"pending"`
}
type PHPProbe struct {
	Version    string            `json:"version"`
	Bits       int               `json:"bits"`
	ThreadSafe bool              `json:"threadSafe"`
	Build      string            `json:"build"`
	Modules    map[string]string `json:"modules"`
	Zend       []string          `json:"zend"`
	Settings   map[string]string `json:"settings"`
	INI        string            `json:"ini"`
	Scanned    string            `json:"scanned"`
	ZendBuild  string            `json:"zendBuild"`
}
type PHPSelection struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}
type PHPSettingsRequest struct {
	Revision   string            `json:"revision"`
	Extensions []PHPSelection    `json:"extensions"`
	Settings   map[string]string `json:"settings"`
}

type phpProbeOutput struct{ bytes.Buffer }

func (b *phpProbeOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1024*1024 {
		return 0, fmt.Errorf("PHP 诊断输出超过 1 MB")
	}
	return b.Buffer.Write(p)
}

var phpSettingDefaults = map[string]string{
	"opcache.enable": "1", "opcache.enable_cli": "0", "opcache.memory_consumption": "128",
	"opcache.interned_strings_buffer": "8", "opcache.max_accelerated_files": "10000",
	"opcache.validate_timestamps": "1", "opcache.revalidate_freq": "2",
}

// The same diagnostic runs in CLI for validation and in the owned FastCGI worker
// for actual loaded state. It never reads request parameters or user code.
const phpDiagnostic = `$m=[]; foreach(get_loaded_extensions(false) as $n){$m[strtolower($n)]=(string)phpversion($n);}
$s=[]; foreach(['opcache.enable','opcache.enable_cli','opcache.memory_consumption','opcache.interned_strings_buffer','opcache.max_accelerated_files','opcache.validate_timestamps','opcache.revalidate_freq'] as $k){$s[$k]=(string)ini_get($k);}
ob_start(); phpinfo(INFO_GENERAL); $info=ob_get_clean(); $build='';
if(preg_match('/PHP Extension Build\s*=>\s*([^\r\n]+)/',$info,$v)){$build=trim($v[1]);}
$zendbuild=''; if(preg_match('/Zend Extension Build\s*=>\s*([^\r\n]+)/',$info,$v)){$zendbuild=trim($v[1]);}
echo 'PHPANEL_DIAGNOSTIC:'.json_encode(['version'=>PHP_VERSION,'bits'=>PHP_INT_SIZE*8,'threadSafe'=>(bool)PHP_ZTS,'build'=>$build,'zendBuild'=>$zendbuild,'modules'=>$m,'zend'=>get_loaded_extensions(true),'settings'=>$s,'ini'=>(string)php_ini_loaded_file(),'scanned'=>(string)php_ini_scanned_files()]);`

// phpEnvironment builds the environment for a PHP process. PHPRC names the
// instance php.ini without relying on the command line, so processes PHP
// spawns itself (php_binary() workers, the built-in -S server, proc_open) load
// the same main configuration as the parent instead of falling back to the
// build-time defaults such as C:\php\ext. PHP_INI_SCAN_DIR covers conf.d.
func phpEnvironment(ini, scan string, libraryDirs ...string) []string {
	env := []string{}
	pathValue := ""
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if len(libraryDirs) > 0 && strings.EqualFold(name, "PATH") {
			_, pathValue, _ = strings.Cut(value, "=")
			continue
		}
		if !strings.EqualFold(name, "PHP_INI_SCAN_DIR") && !strings.EqualFold(name, "PHPRC") {
			env = append(env, value)
		}
	}
	if len(libraryDirs) > 0 {
		env = append(env, "PATH="+strings.Join(libraryDirs, string(os.PathListSeparator))+string(os.PathListSeparator)+pathValue)
	}
	if ini != "" {
		env = append(env, "PHPRC="+nativePath(ini))
	}
	return append(env, "PHP_INI_SCAN_DIR="+nativePath(scan))
}
func phpLibraryDirs(ini, scan string) []string {
	files := []string{}
	if ini != "" {
		files = append(files, ini)
	}
	entries, _ := os.ReadDir(scan)
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".ini") {
			files = append(files, filepath.Join(scan, e.Name()))
		}
	}
	out := []string{}
	seen := map[string]bool{}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil || len(body) > 1<<20 {
			continue
		}
		for _, d := range phpDirectives(body) {
			if d.key != "extension" && d.key != "zend_extension" {
				continue
			}
			value := filepath.FromSlash(d.value)
			if !filepath.IsAbs(value) {
				continue
			}
			dir := filepath.Dir(value)
			if !seen[strings.ToLower(dir)] {
				out = append(out, nativePath(dir))
				seen[strings.ToLower(dir)] = true
			}
		}
	}
	return out
}
func parsePHPProbe(output []byte) (PHPProbe, error) {
	var p PHPProbe
	marker := []byte("PHPANEL_DIAGNOSTIC:")
	index := bytes.LastIndex(output, marker)
	if index < 0 {
		return p, fmt.Errorf("PHP 诊断未返回有效结果: %s", strings.TrimSpace(string(output)))
	}
	err := json.Unmarshal(bytes.TrimSpace(output[index+len(marker):]), &p)
	if err != nil || p.Version == "" || p.Modules == nil {
		return p, fmt.Errorf("PHP 诊断结果无效: %v", err)
	}
	return p, nil
}
func probePHP(p InstalledPackage, ini, scan string, workingDirectory ...string) (PHPProbe, string, error) {
	exe := filepath.Join(filepath.Dir(p.Executable), "php.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"-n", "-r", phpDiagnostic}
	if ini != "" {
		args = []string{"-c", nativePath(ini), "-r", phpDiagnostic}
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = nativePath(filepath.Dir(exe))
	if len(workingDirectory) > 0 {
		cmd.Dir = nativePath(workingDirectory[0])
	}
	if ini != "" {
		cmd.Env = phpEnvironment(ini, scan, phpLibraryDirs(ini, scan)...)
	} else {
		cmd.Env = phpEnvironment("", scan)
	}
	var out, warnings phpProbeOutput
	cmd.Stdout = &out
	cmd.Stderr = &warnings
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return PHPProbe{}, "", fmt.Errorf("无法检测 PHP，请检查 php.exe 和 VC++ 运行库: %w", err)
	}
	guard, err := guardProcess(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return PHPProbe{}, "", err
	}
	defer guard.close()
	err = cmd.Wait()
	if err != nil {
		return PHPProbe{}, warnings.String(), fmt.Errorf("PHP 检测失败: %w；%s", err, warnings.String())
	}
	result, err := parsePHPProbe(out.Bytes())
	// display_errors can place startup warnings on stdout as well as stderr.
	if n := bytes.Index(out.Bytes(), []byte("PHPANEL_DIAGNOSTIC:")); n > 0 {
		warnings.Write(out.Bytes()[:n])
	}
	return result, strings.TrimSpace(warnings.String()), err
}

type phpINISet struct {
	main     []byte
	scan     map[string][]byte
	revision string
}

// extractManagedBlock returns the body between the PHPMANAGED markers in a
// php.ini, or nil when no managed block is present.
func extractManagedBlock(main []byte) []byte {
	lines := strings.Split(string(main), "\n")
	start, end := -1, -1
	for n, line := range lines {
		switch strings.TrimSpace(line) {
		case phpManagedBegin:
			start = n
		case phpManagedEnd:
			end = n
		}
		if end >= 0 {
			break
		}
	}
	if start < 0 || end <= start {
		return nil
	}
	return []byte(strings.Join(lines[start+1:end], "\n"))
}

// insertManagedBlock returns main with the managed block replaced by managed
// (wrapped in markers). When no markers exist, the block is appended at the
// end. Everything outside the markers is preserved verbatim, so hand-edits
// outside the block survive an extension toggle.
func insertManagedBlock(main, managed []byte) []byte {
	lines := strings.Split(string(main), "\n")
	start, end := -1, -1
	for n, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == phpManagedBegin {
			start = n
		}
		if trim == phpManagedEnd {
			end = n
			break
		}
	}
	body := strings.Split(strings.TrimRight(string(managed), "\n"), "\n")
	if start < 0 || end <= start {
		out := append([]string{}, lines...)
		out = append(out, phpManagedBegin)
		out = append(out, body...)
		out = append(out, phpManagedEnd)
		return []byte(strings.Join(out, "\n"))
	}
	out := append([]string{}, lines[:start+1]...)
	out = append(out, body...)
	out = append(out, lines[end:]...)
	return []byte(strings.Join(out, "\n"))
}

func readPHPINI(i Instance) (phpINISet, error) {
	set := phpINISet{scan: map[string][]byte{}}
	read := func(path string) ([]byte, error) {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.Size() > 1024*1024 {
			return nil, fmt.Errorf("PHP 配置超过 1 MB")
		}
		body, err := os.ReadFile(path)
		if err == nil && (!utf8.Valid(body) || bytes.ContainsRune(body, 0)) {
			err = fmt.Errorf("PHP 配置须为 UTF-8 文本")
		}
		return body, err
	}
	var err error
	set.main, err = read(phpINIPath(i))
	if err != nil {
		return set, err
	}
	if i.Component == "php" {
		// PHP managed extensions are an inline block inside package/php.ini;
		// there is no conf.d, so grandchild processes (which may not inherit
		// PHP_INI_SCAN_DIR) still load them. Expose the block body as the
		// managed document so the rest of the extension pipeline is unchanged.
		if body := extractManagedBlock(set.main); body != nil {
			set.scan[phpManagedINI] = body
		}
	} else {
		dir := phpConfDPath(i)
		entries, readErr := os.ReadDir(dir)
		if readErr != nil && !os.IsNotExist(readErr) {
			return set, readErr
		}
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".ini") {
				if len(set.scan) >= 64 {
					return set, fmt.Errorf("扩展扫描目录超过 64 个配置文件")
				}
				real, statErr := PhysicalPath(filepath.Join(dir, e.Name()))
				if statErr != nil || !within(i.Path, real) {
					return set, fmt.Errorf("扩展配置路径超出实例目录")
				}
				b, readErr := read(real)
				if readErr != nil {
					return set, readErr
				}
				name := e.Name()
				if strings.EqualFold(name, phpManagedINI) {
					name = phpManagedINI
				}
				set.scan[name] = b
			}
		}
	}
	var all bytes.Buffer
	all.WriteString("php.ini\x00")
	all.Write(set.main)
	for _, name := range sortedINI(set.scan) {
		all.WriteString("\x00" + name + "\x00")
		all.Write(set.scan[name])
	}
	set.revision = configRevision(all.Bytes())
	return set, nil
}
func sortedINI(files map[string][]byte) []string {
	names := []string{}
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

type phpDirective struct {
	key, value string
	line       int
	scoped     bool
}

func phpDirectives(body []byte) []phpDirective {
	result := []phpDirective{}
	scoped := false
	for index, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			upper := strings.ToUpper(line)
			scoped = strings.HasPrefix(upper, "[PATH=") || strings.HasPrefix(upper, "[HOST=")
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		if len(val) > 0 && (val[0] == '\'' || val[0] == '"') {
			q := val[0]
			if end := strings.IndexByte(val[1:], q); end >= 0 {
				val = val[1 : end+1]
			}
		} else {
			if n := strings.IndexByte(val, ';'); n >= 0 {
				val = val[:n]
			}
			val = strings.TrimSpace(val)
		}
		result = append(result, phpDirective{key, val, index, scoped})
	}
	return result
}
func phpModuleName(value string) string {
	value = strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
	value = filepath.Base(value)
	value = strings.TrimSuffix(strings.TrimPrefix(value, "php_"), ".dll")
	switch value {
	case "gd2":
		return "gd"
	case "opcache", "zend opcache":
		return "zend opcache"
	case "oci8_12c", "oci8_19":
		return "oci8"
	}
	return value
}
func extensionDescription(name string) string {
	return map[string]string{"curl": "HTTP 请求与接口调用", "mysqli": "MySQL 数据库连接", "pdo_mysql": "PDO MySQL 驱动", "mbstring": "多字节字符串处理", "openssl": "TLS、证书与加密", "gd": "图片处理与验证码", "intl": "国际化与 Unicode", "fileinfo": "文件类型识别", "zip": "ZIP 压缩与解压", "exif": "图片元数据", "redis": "Redis 客户端", "xdebug": "调试与性能分析", "zend opcache": "缓存 PHP 编译结果", "pdo_sqlite": "PDO SQLite 驱动", "sqlite3": "SQLite 数据库", "sodium": "现代密码学", "sockets": "底层网络通信", "soap": "SOAP 接口", "bcmath": "高精度运算"}[name]
}
func phpInventory(p InstalledPackage, base PHPProbe, set phpINISet, installed ...InstalledPHPExtension) ([]PHPExtension, error) {
	rows := map[string]PHPExtension{}
	for name, version := range base.Modules {
		rows[name] = PHPExtension{Name: name, Description: extensionDescription(name), Kind: "builtin", Configured: true, Version: version, Source: "PHP 内置", Dependencies: []string{}}
	}
	dir := filepath.Join(filepath.Dir(p.Executable), "ext")
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(strings.ToLower(f.Name()), ".dll") {
			continue
		}
		name := phpModuleName(f.Name())
		if name == "dl_test" || name == "zend_test" || name == "phpdbg_webhelper" {
			continue
		}
		if _, builtin := base.Modules[name]; builtin {
			continue
		}
		kind := "extension"
		if name == "zend opcache" || name == "xdebug" {
			kind = "zend"
		}
		deps := []string{}
		if binary, err := pe.Open(filepath.Join(dir, f.Name())); err == nil {
			imports, _ := binary.ImportedLibraries()
			binary.Close()
			for _, lib := range imports {
				if strings.HasPrefix(strings.ToLower(lib), "php_") && !strings.HasPrefix(strings.ToLower(lib), "php_ts") {
					dep := phpModuleName(lib)
					if _, err := os.Stat(filepath.Join(dir, lib)); err == nil && dep != name {
						deps = append(deps, dep)
					}
				}
			}
		}
		rows[name] = PHPExtension{Name: name, Description: extensionDescription(name), Kind: kind, Filename: f.Name(), Editable: true, Source: "随包 / 本地 DLL", Dependencies: deps}
	}
	inspect := func(source string, body []byte, manual bool) {
		for _, d := range phpDirectives(body) {
			if d.key != "extension" && d.key != "zend_extension" {
				continue
			}
			name := phpModuleName(d.value)
			row, ok := rows[name]
			if !ok {
				row = PHPExtension{Name: name, Kind: "manual", Description: "手工加载项，请在配置编辑器中管理", Dependencies: []string{}}
			}
			row.Configured = true
			if d.key == "zend_extension" && row.Kind != "builtin" {
				row.Kind = "zend"
			}
			// Absolute/custom-path declarations remain owned by their original file.
			custom := strings.ContainsAny(d.value, "/\\")
			if custom && row.DLLPath != "" && samePHPPath(row.DLLPath, d.value) {
				custom = false
			}
			if manual || d.scoped || custom || !ok {
				row.Editable = false
				row.Source = source + "（手工管理）"
			}
			rows[name] = row
		}
	}
	// Prefer the configured managed version; otherwise expose the newest local
	// version. Other absolute paths remain under manual configuration ownership.
	for _, item := range selectInstalledExtensions(p, base, set, installed) {
		path, err := installedExtensionDLL(p, item)
		if err != nil {
			continue
		}
		if _, builtin := base.Modules[item.Name]; builtin {
			continue
		}
		rows[item.Name] = PHPExtension{Name: item.Name, Kind: item.Kind, Description: extensionDescription(item.Name), Filename: filepath.Base(path), DLLPath: path, Version: item.Version, Editable: true, Source: "在线安装 · " + item.Verification, Dependencies: []string{}}
	}
	inspect("php.ini", set.main, false)
	for _, name := range sortedINI(set.scan) {
		inspect("conf.d/"+name, set.scan[name], !strings.EqualFold(name, phpManagedINI))
	}
	result := []PHPExtension{}
	for _, row := range rows {
		result = append(result, row)
	}
	sort.Slice(result, func(a, b int) bool {
		if result[a].Kind == "builtin" && result[b].Kind != "builtin" {
			return false
		}
		if result[b].Kind == "builtin" && result[a].Kind != "builtin" {
			return true
		}
		return result[a].Name < result[b].Name
	})
	return result, nil
}

func (m *Manager) reservePHPOperation(key string) (Instance, InstalledPackage, func(), error) {
	m.refreshSystemServices()
	i, p, err := m.lookupInstance(key)
	if err != nil {
		return i, p, nil, err
	}
	if i.Component != "php" {
		return i, p, nil, fmt.Errorf("仅 PHP 实例支持扩展管理")
	}
	m.mu.Lock()
	if m.closing || m.operations[key] || m.operations["php-restart:"+key] {
		m.mu.Unlock()
		return i, p, nil, fmt.Errorf("实例正在变更，请稍后重试")
	}
	m.operations[key] = true
	m.wg.Add(1)
	m.mu.Unlock()
	return i, p, func() { m.mu.Lock(); delete(m.operations, key); m.mu.Unlock(); m.wg.Done() }, nil
}
func (m *Manager) GetPHPExtensions(key string) (PHPExtensionReport, error) {
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	i, p, release, err := m.reservePHPOperation(key)
	if err != nil {
		return PHPExtensionReport{}, err
	}
	defer release()
	return m.phpExtensionReport(i, p)
}
func (m *Manager) phpExtensionReport(i Instance, p InstalledPackage) (PHPExtensionReport, error) {
	report := PHPExtensionReport{}
	set, err := readPHPINI(i)
	if err != nil {
		return report, err
	}
	base, _, err := probePHP(p, "", phpConfDPath(i), i.Path)
	if err != nil {
		return report, err
	}
	rows, err := phpInventory(p, base, set, m.Snapshot().ExtensionPackages...)
	if err != nil {
		return report, err
	}
	configured, warnings, err := probePHP(p, phpINIPath(i), phpConfDPath(i), i.Path)
	if err != nil {
		warnings = err.Error()
	}
	report = PHPExtensionReport{Version: base.Version, Bits: base.Bits, ThreadSafe: base.ThreadSafe, Build: base.Build, Revision: set.revision, Extensions: rows, Settings: map[string]string{}, Warnings: warnings}
	for k, def := range phpSettingDefaults {
		v := configured.Settings[k]
		if v == "" {
			v = def
		}
		report.Settings[k] = v
	}
	for _, d := range phpDirectives(set.main) {
		if _, ok := report.Settings[d.key]; ok && !d.scoped {
			report.Settings[d.key] = canonicalPHPSetting(d.key, d.value)
		}
	}
	for _, file := range sortedINI(set.scan) {
		for _, d := range phpDirectives(set.scan[file]) {
			if _, ok := report.Settings[d.key]; ok && !d.scoped {
				report.Settings[d.key] = canonicalPHPSetting(d.key, d.value)
			}
		}
	}
	m.mu.Lock()
	proc := m.processes[i.ID]
	m.mu.Unlock()
	report.Running = proc != nil || i.Status == "running"
	var actual PHPProbe
	if proc != nil {
		actual, err = m.probeRunningPHP(i, proc)
		if err == nil {
			report.RuntimeKnown = true
		} else {
			report.RuntimeError = err.Error()
		}
	}
	if proc == nil && report.Running {
		actual, err = m.phpBackend().Diagnostic(i)
		if err == nil {
			report.RuntimeKnown = true
		} else {
			report.RuntimeError = err.Error()
		}
	}
	for n := range report.Extensions {
		row := &report.Extensions[n]
		if row.Version == "" {
			row.Version = configured.Modules[row.Name]
		}
		row.State = "stopped"
		if report.RuntimeKnown {
			version, loaded := actual.Modules[row.Name]
			row.Loaded = loaded
			if loaded {
				row.Version = version
			}
			row.State = "disabled"
			if loaded {
				row.State = "loaded"
			}
			if loaded != row.Configured {
				row.State = "pending"
				report.Pending = true
			}
		} else if report.Running {
			row.State = "unknown"
		}
	}
	for _, pending := range m.Snapshot().PreparedExtensions {
		if pending.Record.PackageID == p.ID {
			report.Pending = true
			for n := range report.Extensions {
				if report.Extensions[n].Name == pending.Record.Name {
					report.Extensions[n].State = "update"
				}
			}
		}
	}
	if report.RuntimeKnown {
		for k, v := range report.Settings {
			if _, loaded := actual.Modules["zend opcache"]; loaded && actual.Settings[k] != v {
				report.Pending = true
			}
		}
		// A process launched before scan-directory support needs a restart even
		// when the current selected modules happen to be identical.
		if i.Component != "php" && len(set.scan) > 0 && !strings.Contains(strings.ToLower(strings.ReplaceAll(actual.Scanned, "\\", "/")), strings.ToLower(normalized(phpConfDPath(i)))) {
			report.Pending = true
		}
	}
	return report, nil
}

func canonicalPHPSetting(key, value string) string {
	if key == "opcache.enable" || key == "opcache.enable_cli" || key == "opcache.validate_timestamps" {
		switch strings.ToLower(value) {
		case "on", "true", "yes":
			return "1"
		case "off", "false", "no", "none", "":
			return "0"
		}
	}
	return value
}
func validatePHPSettings(settings map[string]string) error {
	for key, value := range settings {
		if _, ok := phpSettingDefaults[key]; !ok {
			return fmt.Errorf("不支持的 PHP 设置: %s", key)
		}
		n, err := strconv.Atoi(value)
		if err != nil || strconv.Itoa(n) != value {
			return fmt.Errorf("%s 须为整数", key)
		}
		low, high := 0, 1
		switch key {
		case "opcache.memory_consumption":
			low, high = 8, 4096
		case "opcache.interned_strings_buffer":
			low, high = 0, 1024
		case "opcache.max_accelerated_files":
			low, high = 200, 1000000
		case "opcache.revalidate_freq":
			low, high = 0, 86400
		}
		if n < low || n > high {
			return fmt.Errorf("%s 须在 %d–%d 之间", key, low, high)
		}
	}
	return nil
}
func phpCandidate(set phpINISet, rows []PHPExtension, req PHPSettingsRequest) ([]byte, []byte, error) {
	if err := validatePHPSettings(req.Settings); err != nil {
		return nil, nil, err
	}
	byName := map[string]PHPExtension{}
	enabled := map[string]bool{}
	for _, row := range rows {
		byName[row.Name] = row
		enabled[row.Name] = row.Configured
	}
	seen := map[string]bool{}
	for _, choice := range req.Extensions {
		row, ok := byName[choice.Name]
		if !ok || !row.Editable {
			return nil, nil, fmt.Errorf("扩展 %s 不可由界面修改", choice.Name)
		}
		if seen[choice.Name] {
			return nil, nil, fmt.Errorf("扩展选择重复")
		}
		seen[choice.Name] = true
		enabled[choice.Name] = choice.Enabled
	}
	for name, on := range enabled {
		if !on {
			continue
		}
		for _, dep := range byName[name].Dependencies {
			if !enabled[dep] {
				return nil, nil, fmt.Errorf("%s 需要先启用 %s", name, dep)
			}
		}
	}
	// Existing manual settings must stay in place rather than become shadowed.
	for file, body := range set.scan {
		for _, d := range phpDirectives(body) {
			if _, ok := req.Settings[d.key]; ok {
				if strings.EqualFold(file, phpManagedINI) && !d.scoped {
					continue
				}
				return nil, nil, fmt.Errorf("%s 在 conf.d/%s 手工配置，请在配置编辑器中修改", d.key, file)
			}
		}
	}
	remove := map[int]bool{}
	for _, d := range phpDirectives(set.main) {
		if d.key == "extension" || d.key == "zend_extension" {
			if row, ok := byName[phpModuleName(d.value)]; ok && row.Editable && !d.scoped {
				remove[d.line] = true
			}
		}
		if _, ok := req.Settings[d.key]; ok {
			if d.scoped {
				return nil, nil, fmt.Errorf("%s 有按目录/域名配置，请在配置编辑器中修改", d.key)
			}
			remove[d.line] = true
		}
	}
	lines := strings.Split(string(set.main), "\n")
	for line := range remove {
		lines[line] = "; PHPanel moved to managed block: " + strings.TrimRight(lines[line], "\r")
	}
	main := []byte(strings.Join(lines, "\n"))
	var managed strings.Builder
	managed.WriteString("; Managed by PHPanel. Edit through PHP extension management.\n; Changes take effect after restarting this PHP instance.\n\n")
	// Preserve entries manually added to the managed document too.
	if previous := set.scan[phpManagedINI]; len(previous) > 0 {
		kept := strings.Split(string(previous), "\n")
		for _, d := range phpDirectives(previous) {
			owned := false
			if d.key == "extension" || d.key == "zend_extension" {
				owned = byName[phpModuleName(d.value)].Editable
			}
			if _, ok := req.Settings[d.key]; ok {
				owned = true
			}
			if owned {
				kept[d.line] = ""
			}
		}
		for _, line := range kept {
			trim := strings.TrimSpace(line)
			if trim != "" && !strings.HasPrefix(trim, "; Managed by PHPanel") && !strings.HasPrefix(trim, "; Changes take effect") {
				managed.WriteString(line + "\n")
			}
		}
		managed.WriteString("\n")
	}
	visited, visiting := map[string]bool{}, map[string]bool{}
	managed.WriteString("[PHP]\n")
	var emit func(string) error
	emit = func(name string) error {
		if visited[name] || !enabled[name] || !byName[name].Editable {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("扩展依赖循环: %s", name)
		}
		visiting[name] = true
		row := byName[name]
		for _, dep := range row.Dependencies {
			if err := emit(dep); err != nil {
				return err
			}
		}
		if name == "xdebug" {
			if err := emit("zend opcache"); err != nil {
				return err
			}
		}
		key := "extension"
		if row.Kind == "zend" {
			key = "zend_extension"
		}
		filename := row.Filename
		if row.DLLPath != "" {
			filename = normalized(row.DLLPath)
		}
		managed.WriteString(key + "=\"" + filename + "\"\n")
		visited[name] = true
		visiting[name] = false
		return nil
	}
	for _, row := range rows {
		if err := emit(row.Name); err != nil {
			return nil, nil, err
		}
	}
	managed.WriteString("\n[opcache]\n")
	keys := []string{}
	for k := range req.Settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		managed.WriteString(k + "=" + req.Settings[k] + "\n")
	}
	return main, []byte(managed.String()), nil
}
func (m *Manager) SavePHPExtensions(key string, req PHPSettingsRequest) (PHPExtensionReport, error) {
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	i, p, release, err := m.reservePHPOperation(key)
	if err != nil {
		return PHPExtensionReport{}, err
	}
	defer release()
	return m.savePHPExtensionsReserved(i, p, req)
}
func (m *Manager) savePHPExtensionsReserved(i Instance, p InstalledPackage, req PHPSettingsRequest) (PHPExtensionReport, error) {
	set, err := readPHPINI(i)
	if err != nil {
		return PHPExtensionReport{}, err
	}
	if set.revision != req.Revision {
		return PHPExtensionReport{}, fmt.Errorf("文件已被修改，请刷新扩展列表后再保存")
	}
	base, _, err := probePHP(p, "", "", i.Path)
	if err != nil {
		return PHPExtensionReport{}, err
	}
	rows, err := phpInventory(p, base, set, m.Snapshot().ExtensionPackages...)
	if err != nil {
		return PHPExtensionReport{}, err
	}
	main, managed, err := phpCandidate(set, rows, req)
	if err != nil {
		return PHPExtensionReport{}, err
	}
	// For PHP the managed block lives inline in package/php.ini; the candidate
	// is validated with no scan dir so the check matches what grandchild
	// processes (which do not inherit PHP_INI_SCAN_DIR) will actually load.
	inline := i.Component == "php"
	var final []byte
	if inline {
		final = insertManagedBlock(main, managed)
	}
	stage, err := os.MkdirTemp(filepath.Join(i.Path, "temp"), "php-extensions-")
	if err != nil {
		return PHPExtensionReport{}, err
	}
	defer os.RemoveAll(stage)
	var probeIni, probeScan string
	if inline {
		if err = os.WriteFile(filepath.Join(stage, "php.ini"), final, 0600); err != nil {
			return PHPExtensionReport{}, err
		}
		probeIni = filepath.Join(stage, "php.ini")
		probeScan = ""
	} else {
		scan := filepath.Join(stage, "conf.d")
		if err = os.Mkdir(scan, 0700); err != nil {
			return PHPExtensionReport{}, err
		}
		if err = os.WriteFile(filepath.Join(stage, "php.ini"), main, 0600); err != nil {
			return PHPExtensionReport{}, err
		}
		for name, b := range set.scan {
			if !strings.EqualFold(name, phpManagedINI) {
				if err = os.WriteFile(filepath.Join(scan, name), b, 0600); err != nil {
					return PHPExtensionReport{}, err
				}
			}
		}
		if err = os.WriteFile(filepath.Join(scan, phpManagedINI), managed, 0600); err != nil {
			return PHPExtensionReport{}, err
		}
		probeIni = filepath.Join(stage, "php.ini")
		probeScan = scan
	}
	checked, warnings, err := probePHP(p, probeIni, probeScan, i.Path)
	if err != nil {
		return PHPExtensionReport{}, err
	}
	if warnings != "" {
		return PHPExtensionReport{}, fmt.Errorf("候选配置未通过 PHP 启动检查，原配置已保留:\n%s", warnings)
	}
	expected := map[string]bool{}
	for _, row := range rows {
		expected[row.Name] = row.Configured
	}
	for _, choice := range req.Extensions {
		expected[choice.Name] = choice.Enabled
	}
	for _, row := range rows {
		if !row.Editable {
			continue
		}
		_, loaded := checked.Modules[row.Name]
		if loaded != expected[row.Name] {
			return PHPExtensionReport{}, fmt.Errorf("扩展 %s 的实际检测结果与选择不符，原配置已保留", row.Name)
		}
	}
	// Detect external editor writes that happened while the candidate was checked.
	latest, err := readPHPINI(i)
	if err != nil {
		return PHPExtensionReport{}, err
	}
	if latest.revision != set.revision {
		return PHPExtensionReport{}, fmt.Errorf("文件已被修改，请刷新扩展列表后再保存")
	}
	if inline {
		mainPath := phpINIPath(i)
		if err = os.WriteFile(mainPath+".before-extensions", set.main, 0600); err != nil {
			return PHPExtensionReport{}, err
		}
		if err = atomicConfigWrite(mainPath, final); err != nil {
			return PHPExtensionReport{}, err
		}
	} else {
		mainPath := filepath.Join(i.Path, "config", "php.ini")
		managedPath := filepath.Join(i.Path, "config", "conf.d", phpManagedINI)
		if err = os.MkdirAll(filepath.Dir(managedPath), 0700); err != nil {
			return PHPExtensionReport{}, err
		}
		if err = os.WriteFile(mainPath+".before-extensions", set.main, 0600); err != nil {
			return PHPExtensionReport{}, err
		}
		old, exists := set.scan[phpManagedINI]
		if exists {
			if err = os.WriteFile(managedPath+".before-extensions", old, 0600); err != nil {
				return PHPExtensionReport{}, err
			}
		}
		if err = atomicConfigWrite(mainPath, main); err != nil {
			return PHPExtensionReport{}, err
		}
		if err = atomicConfigWrite(managedPath, managed); err != nil {
			rollback := atomicConfigWrite(mainPath, set.main)
			return PHPExtensionReport{}, fmt.Errorf("保存扩展配置失败: %v；恢复主配置: %v", err, rollback)
		}
	}
	return m.phpExtensionReport(i, p)
}

func (m *Manager) RestartPHPWithExtensions(key, revision string) error {
	m.siteMu.Lock()
	defer m.siteMu.Unlock()
	i, p, release, err := m.reservePHPOperation(key)
	if err != nil {
		return err
	}
	// Move the reservation to a token that internal start/stop may bypass.
	m.mu.Lock()
	m.operations["php-restart:"+key] = true
	delete(m.operations, key)
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.operations, "php-restart:"+key); m.mu.Unlock(); release() }()
	set, err := readPHPINI(i)
	if err != nil {
		return err
	}
	if set.revision != revision {
		return fmt.Errorf("配置已变化，请刷新后再重启")
	}
	_, warnings, err := probePHP(p, phpINIPath(i), phpConfDPath(i), i.Path)
	if err != nil {
		return err
	}
	if warnings != "" {
		return fmt.Errorf("配置启动检查失败，服务未重启:\n%s", warnings)
	}
	if err = m.stopInstance(key, true); err != nil {
		return err
	}
	if err = m.applyPreparedExtensionsLocked(i.PackageID); err != nil {
		restore := m.startInstance(key, true)
		return fmt.Errorf("扩展替换失败: %v；恢复 PHP: %v", err, restore)
	}
	return m.startInstance(key, true)
}

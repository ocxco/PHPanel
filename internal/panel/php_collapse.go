package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// migratePHPCollapse merges legacy PHP instance rows into their owning packages.
// After the merge a PHP package IS the runtime: package/php.ini holds the config
// (with the managed extension block inline), the package owns the port and
// runtime state, and no PHP row remains in state.Instances.
//
// It returns the old instance directories that may be removed once the migrated
// state has been persisted successfully, so a save failure never deletes data.
// The function is idempotent: with no PHP instance rows it only backfills a
// baseline for packages that have never been run.
func (m *Manager) migratePHPCollapse() ([]string, error) {
	legacy := make([]Instance, 0, len(m.state.Instances))
	for _, i := range m.state.Instances {
		if i.Component == "php" {
			legacy = append(legacy, i)
		}
	}
	if len(legacy) > 0 {
		if _, err := m.backupStateFile(); err != nil {
			return nil, fmt.Errorf("迁移前备份 panel.json 失败: %w", err)
		}
	}
	stale := []string{}
	removed := map[string]bool{}
	repoint := map[string]string{}
	for _, i := range legacy {
		index := m.findPHPPackage(i)
		if index < 0 {
			// Orphan instance without a package: keep it rather than lose data.
			continue
		}
		if err := m.collapsePHPInstance(i, index); err != nil {
			return nil, err
		}
		repoint[i.ID] = m.state.Packages[index].ID
		removed[i.ID] = true
		if dir := strings.TrimSpace(i.Path); dir != "" && within(filepath.Join(m.root, "instances"), dir) {
			stale = append(stale, dir)
		}
	}
	if len(removed) > 0 {
		kept := m.state.Instances[:0]
		for _, i := range m.state.Instances {
			if !removed[i.ID] {
				kept = append(kept, i)
			}
		}
		m.state.Instances = kept
	}
	if len(repoint) > 0 {
		for n := range m.state.Sites {
			if pkg, ok := repoint[m.state.Sites[n].PHPID]; ok {
				m.state.Sites[n].PHPID = pkg
			}
		}
		if pkg, ok := repoint[m.state.Tools.PHPInstanceID]; ok {
			m.state.Tools.PHPPackageID = pkg
			m.state.Tools.PHPInstanceID = ""
		}
		for n := range m.state.Tasks {
			if pkg, ok := repoint[m.state.Tasks[n].InstanceID]; ok {
				m.state.Tasks[n].InstanceID = pkg
			}
		}
	}
	// Backfill baselines/ports for PHP packages that have never been run
	// (e.g. a package imported without an instance in the legacy model).
	for n := range m.state.Packages {
		p := &m.state.Packages[n]
		if p.Component != "php" {
			continue
		}
		if err := m.ensurePHPPackageBaseline(p); err != nil {
			return nil, err
		}
	}
	return stale, nil
}

// findPHPPackage returns the index of the PHP package owning the legacy
// instance, matching by package id first and by version as a fallback.
func (m *Manager) findPHPPackage(i Instance) int {
	if i.PackageID != "" {
		for n := range m.state.Packages {
			if m.state.Packages[n].Component == "php" && m.state.Packages[n].ID == i.PackageID {
				return n
			}
		}
	}
	if i.Version != "" {
		for n := range m.state.Packages {
			p := m.state.Packages[n]
			if p.Component == "php" && p.Version == i.Version {
				return n
			}
		}
	}
	return -1
}

// collapsePHPInstance writes the legacy instance config into the package and
// transfers the instance's runtime identity onto the package.
//
// While a legacy instance row exists it is authoritative: a package php.ini at
// this point can only be a pre-merge diagnostic artifact (with stale paths), so
// it is rebuilt. Instance rows are removed after a successful migration, which
// keeps this idempotent.
func (m *Manager) collapsePHPInstance(i Instance, index int) error {
	pkg := &m.state.Packages[index]
	pi := instanceFromPackage(*pkg)
	ini := phpINIPath(pi)
	// Regenerate the baseline for the package (correct absolute paths), then
	// preserve user edits from the legacy main config with paths rebased to the
	// package directory, and finally inline the legacy conf.d extensions.
	base := []byte(phpBaseConfigBody(*pkg, pi))
	legacyDir := filepath.Join(i.Path, "config")
	if old, readErr := os.ReadFile(filepath.Join(legacyDir, "php.ini")); readErr == nil {
		rebased := []byte(rebasePaths(string(old), i.Path, pi.Path))
		base = insertManagedBlock(rebased, extractManagedBlock(base))
	}
	if managed, readErr := os.ReadFile(filepath.Join(legacyDir, "conf.d", phpManagedINI)); readErr == nil && len(strings.TrimSpace(string(managed))) > 0 {
		// The legacy block is authoritative for user choices but may predate a
		// baseline extension (e.g. pdo_sqlite). Union it with the baseline so the
		// package keeps the recommended defaults without dropping user extensions.
		base = insertManagedBlock(base, mergeManagedExtensions(managed, extractManagedBlock(base)))
	}
	for _, dir := range []string{phpLogsDir(pi), phpTempDir(pi)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	if err := atomicConfigWrite(ini, base); err != nil {
		return fmt.Errorf("写入 PHP 包配置失败: %w", err)
	}
	if pkg.Port == 0 {
		pkg.Port = i.Port
	}
	pkg.Status = "stopped"
	pkg.PID = 0
	pkg.Error = ""
	if i.LinkedNginxID != "" {
		pkg.LinkedNginxID = i.LinkedNginxID
	}
	if i.LinkedRun != "" {
		pkg.LinkedRun = i.LinkedRun
	}
	if i.ServiceName != "" {
		pkg.ServiceName = i.ServiceName
	}
	pkg.ServiceAutoStart = pkg.ServiceAutoStart || i.ServiceAutoStart
	if pkg.NginxAutoPHP == nil {
		pkg.NginxAutoPHP = i.NginxAutoPHP
	}
	if i.NginxStopPHP {
		pkg.NginxStopPHP = true
	}
	if pkg.DirectoryName == "" {
		pkg.DirectoryName = i.DirectoryName
	}
	return nil
}

// ensurePHPPackageBaseline materializes package/php.ini when missing and
// assigns a free port when the package has none.
func (m *Manager) ensurePHPPackageBaseline(p *InstalledPackage) error {
	i := instanceFromPackage(*p)
	if _, err := os.Stat(phpINIPath(i)); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := writePHPBaseConfig(*p); err != nil {
			return fmt.Errorf("生成 PHP 运行时基线失败: %w", err)
		}
	}
	if p.Port == 0 {
		port, err := m.allocateFreePort("php")
		if err != nil {
			return err
		}
		p.Port = port
	}
	return nil
}

// backupStateFile copies panel.json to panel.json.bak-<timestamp> before a
// destructive migration. A missing panel.json is not an error.
func (m *Manager) backupStateFile() (string, error) {
	src := m.statePath()
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	stamp := time.Now().Format("20060102-150405")
	dst := src + ".bak-" + stamp
	for n := 1; ; n++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = fmt.Sprintf("%s.bak-%s-%d", src, stamp, n)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

// mergeManagedExtensions unions the baseline managed block into the legacy one:
// every extension line the baseline declares but the legacy block lacks is
// appended. Existing legacy lines (and their order) are preserved untouched.
func mergeManagedExtensions(legacy, baseline []byte) []byte {
	have := map[string]bool{}
	for _, line := range strings.Split(string(legacy), "\n") {
		if key := extensionLineKey(line); key != "" {
			have[key] = true
		}
	}
	out := strings.TrimRight(string(legacy), "\r\n")
	for _, line := range strings.Split(string(baseline), "\n") {
		key := extensionLineKey(line)
		if key == "" || have[key] {
			continue
		}
		have[key] = true
		out += "\n" + strings.TrimSpace(line)
	}
	return []byte(out + "\n")
}

// extensionLineKey returns a stable identity for an extension/zend_extension
// directive (directive kind + DLL basename), or "" for any other line.
func extensionLineKey(line string) string {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	kind := ""
	switch {
	case strings.HasPrefix(lower, "zend_extension="):
		kind = "zend"
	case strings.HasPrefix(lower, "extension="):
		kind = "ext"
	default:
		return ""
	}
	open := strings.Index(trimmed, `"`)
	if open < 0 {
		return ""
	}
	closing := strings.Index(trimmed[open+1:], `"`)
	if closing < 0 {
		return ""
	}
	name := trimmed[open+1 : open+1+closing]
	name = strings.ReplaceAll(name, `\`, "/")
	return kind + ":" + strings.ToLower(filepath.Base(name))
}

// rebasePaths rewrites file paths pointing at from so they point at to,
// handling both slash styles (config files may use either).
func rebasePaths(body, from, to string) string {
	if from == "" || from == to {
		return body
	}
	f, t := normalized(from), normalized(to)
	out := strings.ReplaceAll(body, f, t)
	return strings.ReplaceAll(out, strings.ReplaceAll(f, "/", `\`), strings.ReplaceAll(t, "/", `\`))
}

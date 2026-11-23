package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Inline managed-block markers inside package/php.ini. PHPanel owns every line
// between these markers (extension toggles and opcache settings); anything
// outside the block is user-editable and is preserved verbatim on save.
const (
	phpManagedBegin = "; BEGIN PHPMANAGED EXTENSIONS"
	phpManagedEnd   = "; END PHPMANAGED EXTENSIONS"
)

// phpBaseExtensions are enabled by default for a freshly installed PHP package,
// but only when their DLL actually ships in the package ext directory (avoids
// "unable to load dynamic library" startup warnings).
var phpBaseExtensions = []string{"pdo_sqlite", "sqlite3", "mysqli", "pdo_mysql", "mbstring", "openssl"}

// writePHPBaseConfig materializes the baseline runtime layout for a PHP package:
// package/php.ini (with an inline managed extension block) plus logs/ and temp/.
func writePHPBaseConfig(p InstalledPackage) error {
	i := instanceFromPackage(p)
	for _, dir := range []string{phpLogsDir(i), phpTempDir(i)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(phpINIPath(i), []byte(phpBaseConfigBody(p, i)), 0o600)
}

func phpBaseConfigBody(p InstalledPackage, i Instance) string {
	var b strings.Builder
	b.WriteString("[PHP]\n")
	b.WriteString("engine=On\nshort_open_tag=Off\nexpose_php=Off\ndisplay_errors=On\nlog_errors=On\n")
	b.WriteString("error_log=" + quoted(filepath.Join(phpLogsDir(i), "php-error.log")) + "\n")
	b.WriteString("extension_dir=\"ext\"\n")
	b.WriteString("cgi.fix_pathinfo=0\ndate.timezone=Asia/Shanghai\nmemory_limit=256M\n")
	b.WriteString("upload_tmp_dir=" + quoted(phpTempDir(i)) + "\n")
	b.WriteString("session.save_path=" + quoted(phpTempDir(i)) + "\n")
	b.WriteString("\n")
	b.WriteString(phpManagedBegin + "\n")
	b.WriteString("; Managed by PHPanel. Edit through PHP extension management.\n")
	b.WriteString("; Changes take effect after restarting this PHP instance.\n\n")
	b.WriteString("[PHP]\n")
	extDir := phpExtDir(p)
	for _, name := range phpBaseExtensions {
		dll := "php_" + name + ".dll"
		if _, err := os.Stat(filepath.Join(extDir, dll)); err == nil {
			b.WriteString("extension=\"" + dll + "\"\n")
		}
	}
	b.WriteString("\n[opcache]\n")
	b.WriteString("opcache.enable=1\nopcache.enable_cli=0\nopcache.memory_consumption=128\n")
	b.WriteString("opcache.interned_strings_buffer=8\nopcache.max_accelerated_files=10000\n")
	b.WriteString("opcache.validate_timestamps=1\nopcache.revalidate_freq=2\n")
	b.WriteString(phpManagedEnd + "\n")
	return b.String()
}

// allocateFreePort picks the first free port for a component, checking both the
// current state (packages/instances/sites) and an actual localhost bind so a
// port held by an external process is not selected.
func (m *Manager) allocateFreePort(component string) (int, error) {
	m.mu.Lock()
	used := map[int]bool{}
	for _, p := range m.state.Packages {
		if p.Port != 0 {
			used[p.Port] = true
		}
	}
	for _, i := range m.state.Instances {
		if i.Port != 0 {
			used[i.Port] = true
		}
	}
	for _, s := range m.state.Sites {
		if s.Port != 0 {
			used[s.Port] = true
		}
	}
	m.mu.Unlock()
	base := 9000
	for port := base; port < base+1000; port++ {
		if used[port] {
			continue
		}
		if err := checkPort(port); err == nil {
			return port, nil
		}
	}
	return 0, fmt.Errorf("无法为 %s 分配空闲端口", component)
}

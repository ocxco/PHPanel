package panel

import "path/filepath"

// PHP path helpers centralize the layout differences between the merged PHP
// runtime (package == runtime: config lives at the package root so a bare
// php.exe auto-loads <package>/php.ini) and the other components, which keep
// their instance-relative <path>/config layout unchanged.
//
// For PHP, i.Path is the package directory (set by lookupInstance when it
// synthesizes an Instance from an InstalledPackage). For other components
// i.Path is the instance directory.

// phpConfigDir returns the directory holding the component's main config file.
func phpConfigDir(i Instance) string {
	if i.Component == "php" {
		return i.Path
	}
	return filepath.Join(i.Path, "config")
}

// phpINIPath returns the absolute path to the component's main config file.
// For PHP this is <package>/php.ini (auto-loaded by a bare php.exe with no
// -c/PHPRC, e.g. artisan serve grandchildren).
func phpINIPath(i Instance) string {
	return filepath.Join(phpConfigDir(i), "php.ini")
}

// phpConfDPath returns the scan directory for extra .ini overrides, or "" when
// the component has none. PHP has no conf.d: extensions are managed inline in
// php.ini so they reach processes that do not inherit PHP_INI_SCAN_DIR.
func phpConfDPath(i Instance) string {
	if i.Component == "php" {
		return ""
	}
	return filepath.Join(phpConfigDir(i), "conf.d")
}

// phpLogsDir returns the runtime log directory (component-neutral layout).
func phpLogsDir(i Instance) string { return filepath.Join(i.Path, "logs") }

// phpTempDir returns the runtime temp directory (component-neutral layout).
func phpTempDir(i Instance) string { return filepath.Join(i.Path, "temp") }

// phpExtDir returns the directory containing a PHP package's extension DLLs.
func phpExtDir(p InstalledPackage) string {
	return filepath.Join(filepath.Dir(p.Executable), "ext")
}

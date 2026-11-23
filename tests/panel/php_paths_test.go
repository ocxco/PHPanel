package panel

import (
	"path/filepath"
	"testing"
)

func TestPHPPathHelpers(t *testing.T) {
	php := Instance{Component: "php", Path: `C:\pkg\package-php8.5`}
	if got := phpConfigDir(php); got != php.Path {
		t.Fatalf("phpConfigDir(php)=%q want %q", got, php.Path)
	}
	if got := phpINIPath(php); got != filepath.Join(php.Path, "php.ini") {
		t.Fatalf("phpINIPath(php)=%q", got)
	}
	if got := phpConfDPath(php); got != "" {
		t.Fatalf("phpConfDPath(php)=%q want empty", got)
	}
	if got := phpLogsDir(php); got != filepath.Join(php.Path, "logs") {
		t.Fatalf("phpLogsDir(php)=%q", got)
	}
	if got := phpTempDir(php); got != filepath.Join(php.Path, "temp") {
		t.Fatalf("phpTempDir(php)=%q", got)
	}

	nginx := Instance{Component: "nginx", Path: `C:\inst\instance-nginx`}
	if got := phpConfigDir(nginx); got != filepath.Join(nginx.Path, "config") {
		t.Fatalf("phpConfigDir(nginx)=%q", got)
	}
	if got := phpINIPath(nginx); got != filepath.Join(nginx.Path, "config", "php.ini") {
		t.Fatalf("phpINIPath(nginx)=%q", got)
	}
	if got := phpConfDPath(nginx); got != filepath.Join(nginx.Path, "config", "conf.d") {
		t.Fatalf("phpConfDPath(nginx)=%q", got)
	}
	if got := phpLogsDir(nginx); got != filepath.Join(nginx.Path, "logs") {
		t.Fatalf("phpLogsDir(nginx)=%q", got)
	}
}

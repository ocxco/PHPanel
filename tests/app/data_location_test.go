package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDataRootIsExecutableDirectory(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "PHPanel.exe")
	old := filepath.Join(t.TempDir(), "legacy-data")
	if err := saveDataRoot(exe, old); err != nil {
		t.Fatal(err)
	}
	root, err := resolveDataRoot(exe, "")
	if err != nil || root != dir {
		t.Fatalf("portable default: %q %v", root, err)
	}
	override := filepath.Join(t.TempDir(), "explicit")
	root, err = resolveDataRoot(exe, override)
	if err != nil || root != override {
		t.Fatal("explicit override ignored")
	}
	legacy, err := legacyDataRoot(exe)
	if err != nil || legacy != old {
		t.Fatal("legacy migration source lost")
	}
}

func TestPortableWebviewProfileUsesDataDirectoryAndHonorsOverride(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "PHPanel.exe")
	root := filepath.Join(dir, "data")
	if resolveWebviewProfile(exe, root, "") != "" {
		t.Fatal("ordinary profile changed")
	}
	if err := os.WriteFile(filepath.Join(dir, "portable.flag"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if got := resolveWebviewProfile(exe, root, ""); got != filepath.Join(root, "state", "webview2") {
		t.Fatal(got)
	}
	if resolveWebviewProfile(exe, root, "explicit-profile") != "explicit-profile" {
		t.Fatal("profile override ignored")
	}
}
func TestLegacyRecordErrorsDoNotAlterPortableDefault(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "PHPanel.exe")
	if err := os.WriteFile(dataLocationFile(exe), []byte(`{"schemaVersion":1,"path":"relative"}`), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := resolveDataRoot(exe, "")
	if err != nil || root != dir {
		t.Fatal("legacy record overrode portable default")
	}
	if _, err := legacyDataRoot(exe); err == nil {
		t.Fatal("unsafe migration source accepted")
	}
}

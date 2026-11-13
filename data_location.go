package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type dataLocationConfig struct {
	SchemaVersion int    `json:"schemaVersion"`
	Path          string `json:"path"`
}

func dataLocationFile(executable string) string {
	return filepath.Join(filepath.Dir(executable), "phpanel-data-location.json")
}
func resolveDataRoot(executable, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return filepath.Abs(filepath.Dir(executable))
}
func resolveWebviewProfile(executable, root, override string) string {
	if override != "" {
		return override
	}
	if info, err := os.Stat(filepath.Join(filepath.Dir(executable), "portable.flag")); err == nil && info.Mode().IsRegular() {
		return filepath.Join(root, "state", "webview2")
	}
	return ""
}
func legacyDataRoot(executable string) (string, error) {
	body, err := os.ReadFile(dataLocationFile(executable))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var config dataLocationConfig
	if json.Unmarshal(body, &config) != nil || config.SchemaVersion != 1 || !filepath.IsAbs(config.Path) {
		return "", fmt.Errorf("旧目录记录无效，请检查 phpanel-data-location.json")
	}
	return config.Path, nil
}
func saveDataRoot(executable, root string) error {
	body, err := json.MarshalIndent(dataLocationConfig{SchemaVersion: 1, Path: root}, "", "  ")
	if err != nil {
		return err
	}
	path := dataLocationFile(executable)
	if err := os.WriteFile(path+".tmp", body, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

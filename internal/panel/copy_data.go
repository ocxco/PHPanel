package panel

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Copy the managed data, keeping the original root as a backup. Locks prevent
// copying a running managed database, and panel.json is published last.
func CopyDataRoot(source, target string) error {
	source, err := PhysicalPath(source)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	if source == target || within(source, target) || within(target, source) {
		return fmt.Errorf("新旧目录不能相同或相互包含")
	}
	if _, err := os.Stat(filepath.Join(source, "state", "panel.json")); err != nil {
		return fmt.Errorf("旧目录中没有环境记录: %w", err)
	}
	if _, err := os.Stat(filepath.Join(target, "state", "panel.json")); err == nil {
		return fmt.Errorf("目标目录已有环境记录，拒绝覆盖")
	}
	sourceLock, err := lockDataRoot(filepath.Join(source, "state", "panel.lock"))
	if err != nil {
		return fmt.Errorf("旧环境正在使用，请退出旧版后重试: %w", err)
	}
	defer sourceLock.Close()
	if err := os.MkdirAll(target, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(target, ".phpanel-copy-")
	if err != nil {
		return err
	}
	if !within(target, stage) {
		return fmt.Errorf("临时复制目录越界")
	}
	defer os.RemoveAll(stage)
	names := []string{"downloads", "packages", "instances", "sites", "tools", "cli", "state"}
	for _, name := range names {
		destination := filepath.Join(target, name)
		if _, err := os.Stat(destination); err == nil {
			return fmt.Errorf("目标已有 %s 目录，拒绝覆盖", name)
		}
		from := filepath.Join(source, name)
		if _, err := os.Stat(from); os.IsNotExist(err) {
			continue
		}
		if err := copyTree(from, filepath.Join(stage, name)); err != nil {
			return err
		}
	}
	published := []string{}
	rollback := func() {
		for n := len(published) - 1; n >= 0; n-- {
			name := published[n]
			_ = os.Rename(filepath.Join(target, name), filepath.Join(stage, name))
		}
	}
	for _, name := range names {
		staged := filepath.Join(stage, name)
		if _, err := os.Stat(staged); os.IsNotExist(err) {
			continue
		}
		if err := os.Rename(staged, filepath.Join(target, name)); err != nil {
			rollback()
			return err
		}
		published = append(published, name)
	}
	return nil
}
func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("旧环境包含链接，停止复制以避免越界: %s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		if entry.Name() == "panel.lock" || entry.Name() == "panel.json.tmp" {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			input.Close()
			return err
		}
		_, err = io.Copy(output, input)
		if err == nil {
			err = output.Sync()
		}
		inputErr := input.Close()
		outputErr := output.Close()
		if err != nil {
			return err
		}
		if inputErr != nil {
			return inputErr
		}
		return outputErr
	})
}

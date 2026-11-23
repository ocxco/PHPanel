package panel

import (
	"fmt"
	"os"
	"path/filepath"
)

type Location struct {
	Path   string `json:"path"`
	IsFile bool   `json:"isFile"`
}

// Resolve only known app locations instead of accepting arbitrary shell text.
func (m *Manager) ResolveLocation(kind, key string) (Location, error) {
	state := m.Snapshot()
	target := ""
	file := false
	switch kind {
	case "root":
		target = state.Root
	case "downloads":
		target = filepath.Join(state.Root, "downloads", "cache")
	case "packages":
		target = filepath.Join(state.Root, "packages")
	case "instances":
		target = filepath.Join(state.Root, "instances")
	case "sites":
		target = filepath.Join(state.Root, "sites")
	case "package":
		for _, p := range state.Packages {
			if p.ID == key {
				target = p.Path
				break
			}
		}
		if target != "" && !within(filepath.Join(state.Root, "packages"), target) {
			return Location{}, fmt.Errorf("程序目录不属于当前数据目录")
		}
	case "instance", "config", "data", "logs":
		var i Instance
		found := false
		for _, inst := range state.Instances {
			if inst.ID == key {
				i = inst
				found = true
				break
			}
		}
		if !found {
			// Merged PHP: a package id is the runtime id (no PHP Instance rows).
			for _, p := range state.Packages {
				if p.ID == key && p.Component == "php" {
					i = instanceFromPackage(p)
					found = true
					break
				}
			}
		}
		if found {
			base := i.Path
			allowedRoot := filepath.Join(state.Root, "instances")
			if i.Component == "php" {
				allowedRoot = filepath.Join(state.Root, "packages")
			}
			if !within(allowedRoot, base) {
				return Location{}, fmt.Errorf("实例目录不属于当前数据目录")
			}
			target = base
			switch kind {
			case "config":
				target = phpConfigDir(i)
			case "logs":
				target = phpLogsDir(i)
			case "data":
				target = filepath.Join(base, "data")
			}
		}
	case "archive":
		artifactID := ""
		for _, task := range state.Tasks {
			if task.ID == key {
				if task.Kind == "composer" && task.ComposerArtifact != nil && validHash(task.ComposerArtifact.SHA256) {
					target = filepath.Join(state.Root, "downloads", "cache", task.ComposerArtifact.SHA256+".phar")
					file = true
				}
				if task.Kind == "php-extension" && task.ExtensionArtifact != nil && validHash(task.ExtensionArtifact.SHA256) {
					target = filepath.Join(state.Root, "downloads", "cache", task.ExtensionArtifact.SHA256+"."+task.ExtensionArtifact.Format)
					file = true
				}
				artifactID = task.ArtifactID
				break
			}
		}
		for _, a := range state.Artifacts {
			if target != "" {
				break
			}
			if a.ID == artifactID && validHash(a.SHA256) {
				target = filepath.Join(state.Root, "downloads", "cache", a.SHA256+".zip")
				file = true
				break
			}
		}
	default:
		return Location{}, fmt.Errorf("未知目录类型")
	}
	if target == "" {
		return Location{}, fmt.Errorf("未找到对应的程序、实例或安装包")
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return Location{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Location{}, fmt.Errorf("位置不存在: %s", abs)
	}
	if file && info.IsDir() || !file && !info.IsDir() {
		return Location{}, fmt.Errorf("位置类型与预期不一致")
	}
	physical, err := PhysicalPath(abs)
	if err != nil {
		return Location{}, fmt.Errorf("无法解析资源管理器可见的位置: %w", err)
	}
	return Location{Path: physical, IsFile: file}, nil
}
func rebaseStatePaths(state *State, oldRoot, newRoot string) {
	if oldRoot == "" || oldRoot == newRoot {
		return
	}
	rebase := func(path string) string {
		if path == oldRoot {
			return newRoot
		}
		if within(oldRoot, path) {
			relative, err := filepath.Rel(oldRoot, path)
			if err == nil {
				return filepath.Join(newRoot, relative)
			}
		}
		return path
	}
	for n := range state.Packages {
		state.Packages[n].Path = rebase(state.Packages[n].Path)
		state.Packages[n].Executable = rebase(state.Packages[n].Executable)
	}
	for n := range state.Instances {
		state.Instances[n].Path = rebase(state.Instances[n].Path)
	}
	for n := range state.Sites {
		state.Sites[n].Root = rebase(state.Sites[n].Root)
	}
}

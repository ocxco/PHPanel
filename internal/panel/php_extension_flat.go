package panel

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

type extensionFileMove struct {
	Name   string `json:"name"`
	Before string `json:"before"`
	After  string `json:"after"`
}
type flatExtensionPublication struct {
	Layout     string               `json:"layout"`
	Prepared   PreparedPHPExtension `json:"prepared"`
	BackupName string               `json:"backupName"`
	Moves      []extensionFileMove  `json:"moves"`
}

func extensionDirectoryFiles(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	if len(entries) > 256 {
		return nil, fmt.Errorf("扩展文件数量过多")
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".dll") {
			return nil, fmt.Errorf("扩展暂存目录含非 DLL 文件: %s", e.Name())
		}
		path := filepath.Join(dir, e.Name())
		real, err := PhysicalPath(path)
		if err != nil || !samePHPPath(path, real) {
			return nil, fmt.Errorf("扩展文件路径被重定向")
		}
		hash, err := fileHash(path)
		if err != nil {
			return nil, err
		}
		out[e.Name()] = hash
	}
	return out, nil
}
func extensionOwnedFiles(p InstalledPackage, record InstalledPHPExtension) map[string]string {
	if !samePHPPath(record.RelativeDLL, flatExtensionRelativeDLL(p, record.Name)) {
		return map[string]string{}
	}
	out := cloneStringMap(record.Files)
	out["php_"+record.Name+".dll"] = record.DLLSHA256
	return out
}
func (m *Manager) flatPublicationPaths(j flatExtensionPublication, p InstalledPackage) (root, backup, journal string, err error) {
	_, backup, journal, err = m.publicationPaths(extensionPublication{Prepared: j.Prepared, BackupName: j.BackupName}, p)
	root = filepath.Join(filepath.Dir(p.Executable), "extensions")
	return
}
func (m *Manager) planFlatPublication(item PreparedPHPExtension, p InstalledPackage) (flatExtensionPublication, error) {
	stage, err := m.preparedExtensionStage(item)
	if err != nil {
		return flatExtensionPublication{}, err
	}
	files, err := extensionDirectoryFiles(stage)
	if err != nil {
		return flatExtensionPublication{}, err
	}
	item.Record.RelativeDLL = flatExtensionRelativeDLL(p, item.Record.Name)
	item.Record.Files = files
	j := flatExtensionPublication{Layout: "flat", Prepared: item, BackupName: ".before-" + item.Record.Name + "-" + item.TaskID, Moves: []extensionFileMove{}}
	root, backup, _, err := m.flatPublicationPaths(j, p)
	if err != nil {
		return j, err
	}
	if _, err = os.Lstat(backup); !os.IsNotExist(err) {
		return j, fmt.Errorf("扩展备份目录已存在，请先完成恢复")
	}
	state := m.Snapshot()
	owned := map[string]string{}
	shared := map[string]bool{}
	for _, r := range state.ExtensionPackages {
		if r.PackageID != p.ID {
			continue
		}
		if r.Name == item.Record.Name {
			owned = extensionOwnedFiles(p, r)
		} else {
			for name := range extensionOwnedFiles(p, r) {
				shared[strings.ToLower(name)] = true
			}
		}
	}
	names := map[string]bool{}
	for name := range files {
		names[name] = true
	}
	for name := range owned {
		names[name] = true
	}
	keys := []string{}
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if filepath.Base(name) != name || strings.ContainsAny(name, "/\\:") || !strings.EqualFold(filepath.Ext(name), ".dll") {
			return j, fmt.Errorf("扩展文件名无效")
		}
		path := filepath.Join(root, name)
		before := ""
		if info, err := os.Lstat(path); err == nil {
			if !info.Mode().IsRegular() {
				return j, fmt.Errorf("扩展目标不是普通文件")
			}
			real, err := PhysicalPath(path)
			if err != nil || !samePHPPath(real, path) {
				return j, fmt.Errorf("扩展目标被重定向")
			}
			before, err = fileHash(path)
			if err != nil {
				return j, err
			}
		} else if !os.IsNotExist(err) {
			return j, err
		}
		after := files[name]
		if before == after {
			continue
		}
		if before != "" {
			if shared[strings.ToLower(name)] {
				if after == "" {
					continue
				}
				return j, fmt.Errorf("依赖 DLL %s 被其它扩展使用且内容不同，未覆盖", name)
			}
			if owned[name] == "" || owned[name] != before {
				return j, fmt.Errorf("目标 DLL %s 已有手工文件或被修改，未覆盖", name)
			}
		}
		j.Moves = append(j.Moves, extensionFileMove{Name: name, Before: before, After: after})
	}
	return j, nil
}
func copyExtensionFile(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dest), ".extension-copy-")
	if err != nil {
		return err
	}
	temp := out.Name()
	defer os.Remove(temp)
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if _, err = os.Lstat(dest); !os.IsNotExist(err) {
		return fmt.Errorf("目标文件已存在，未覆盖: %s", dest)
	}
	return os.Rename(temp, dest)
}
func checkedExtensionRemoval(path, root string, recursive bool) error {
	if !within(root, path) {
		return fmt.Errorf("扩展清理路径越界")
	}
	real, err := PhysicalPath(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !samePHPPath(real, path) {
		return fmt.Errorf("扩展清理路径被重定向")
	}
	if recursive {
		return os.RemoveAll(real)
	}
	return os.Remove(real)
}
func (m *Manager) publishFlatExtension(item PreparedPHPExtension, p InstalledPackage) error {
	j, err := m.planFlatPublication(item, p)
	if err != nil {
		return err
	}
	root, backup, journal, err := m.flatPublicationPaths(j, p)
	if err != nil {
		return err
	}
	stage, err := m.preparedExtensionStage(item)
	if err != nil {
		return err
	}
	if err = os.Mkdir(backup, 0700); err != nil {
		return err
	}
	body, _ := json.MarshalIndent(j, "", "  ")
	if err = atomicConfigWrite(journal, body); err != nil {
		_ = os.Remove(backup)
		return err
	}
	rollback := func(original error) error {
		if err := m.rollbackFlatFiles(j, p); err != nil {
			return fmt.Errorf("%v；文件恢复失败: %v，发布记录已保留", original, err)
		}
		_ = os.Remove(journal)
		return original
	}
	for _, move := range j.Moves {
		target := filepath.Join(root, move.Name)
		if move.Before != "" {
			if hash, err := fileHash(target); err != nil || hash != move.Before {
				return rollback(fmt.Errorf("发布期间 DLL %s 已变化", move.Name))
			}
			if err = os.Rename(target, filepath.Join(backup, move.Name)); err != nil {
				return rollback(err)
			}
		}
		if move.After != "" {
			if err = copyExtensionFile(filepath.Join(stage, move.Name), target); err != nil {
				return rollback(err)
			}
		}
	}
	if err = m.change(func(s *State) error {
		kept := []InstalledPHPExtension{}
		for _, r := range s.ExtensionPackages {
			if r.PackageID != p.ID || r.Name != item.Record.Name {
				kept = append(kept, r)
			}
		}
		s.ExtensionPackages = append(kept, j.Prepared.Record)
		remaining := []PreparedPHPExtension{}
		for _, r := range s.PreparedExtensions {
			if r.TaskID != item.TaskID {
				remaining = append(remaining, r)
			}
		}
		s.PreparedExtensions = remaining
		return nil
	}); err != nil {
		return rollback(err)
	}
	if err = checkedExtensionRemoval(backup, root, true); err != nil {
		return err
	}
	if err = checkedExtensionRemoval(stage, filepath.Join(m.root, "downloads", "staged"), true); err != nil {
		return err
	}
	return os.Remove(journal)
}
func (m *Manager) rollbackFlatFiles(j flatExtensionPublication, p InstalledPackage) error {
	root, backup, _, err := m.flatPublicationPaths(j, p)
	if err != nil {
		return err
	}
	for n := len(j.Moves) - 1; n >= 0; n-- {
		move := j.Moves[n]
		target := filepath.Join(root, move.Name)
		saved := filepath.Join(backup, move.Name)
		savedHash, savedErr := fileHash(saved)
		if savedErr == nil && savedHash != move.Before {
			return fmt.Errorf("备份 DLL %s 已变化", move.Name)
		}
		current, currentErr := fileHash(target)
		if savedErr == nil {
			if currentErr == nil {
				if current != move.After {
					return fmt.Errorf("目标 DLL %s 已变化，未覆盖", move.Name)
				}
				if err = checkedExtensionRemoval(target, root, false); err != nil {
					return err
				}
			} else if !os.IsNotExist(currentErr) {
				return currentErr
			}
			if err = os.Rename(saved, target); err != nil {
				return err
			}
		} else if !os.IsNotExist(savedErr) {
			return savedErr
		} else if move.Before == "" && currentErr == nil {
			if current != move.After {
				return fmt.Errorf("新 DLL %s 已变化，未删除", move.Name)
			}
			if err = checkedExtensionRemoval(target, root, false); err != nil {
				return err
			}
		}
	}
	if _, err = os.Stat(backup); os.IsNotExist(err) {
		return nil
	}
	return checkedExtensionRemoval(backup, root, true)
}
func (m *Manager) recoverFlatExtension(file string, body []byte) error {
	var j flatExtensionPublication
	if err := json.Unmarshal(body, &j); err != nil {
		return err
	}
	var p InstalledPackage
	for _, candidate := range m.state.Packages {
		if candidate.ID == j.Prepared.Record.PackageID {
			p = candidate
		}
	}
	if p.ID == "" {
		return fmt.Errorf("扩展恢复缺少 PHP 包")
	}
	root, backup, journal, err := m.flatPublicationPaths(j, p)
	if err != nil {
		return err
	}
	if !samePHPPath(file, journal) {
		return fmt.Errorf("扩展恢复记录路径无效")
	}
	for _, move := range j.Moves {
		if filepath.Base(move.Name) != move.Name || strings.ContainsAny(move.Name, "/\\:") || !strings.EqualFold(filepath.Ext(move.Name), ".dll") {
			return fmt.Errorf("扩展恢复文件名无效")
		}
	}
	committed := false
	for _, r := range m.state.ExtensionPackages {
		if reflect.DeepEqual(r, j.Prepared.Record) {
			committed = true
		}
	}
	if !committed {
		if err = m.rollbackFlatFiles(j, p); err != nil {
			return err
		}
	} else {
		if _, err = os.Stat(backup); err == nil {
			if err = checkedExtensionRemoval(backup, root, true); err != nil {
				return err
			}
		}
		stage := filepath.Join(m.root, filepath.FromSlash(j.Prepared.StageRelative))
		if _, err = os.Stat(stage); err == nil {
			if err = checkedExtensionRemoval(stage, filepath.Join(m.root, "downloads", "staged"), true); err != nil {
				return err
			}
		}
	}
	return os.Remove(file)
}

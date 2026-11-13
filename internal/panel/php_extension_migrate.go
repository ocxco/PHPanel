package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type extensionConfigMove struct {
	Path   string `json:"path"`
	Before []byte `json:"before"`
	After  []byte `json:"after"`
}
type extensionLayoutMigration struct {
	Before        InstalledPHPExtension `json:"before"`
	After         InstalledPHPExtension `json:"after"`
	StageRelative string                `json:"stageRelative"`
	Configs       []extensionConfigMove `json:"configs"`
	Revisions     map[string]string     `json:"revisions"`
}

func rewriteExtensionDirectivePath(body []byte, oldPath, newPath string) []byte {
	lines := strings.Split(string(body), "\n")
	for _, d := range phpDirectives(body) {
		if (d.key == "extension" || d.key == "zend_extension") && samePHPPath(d.value, oldPath) {
			line := lines[d.line]
			for _, value := range []string{d.value, normalized(oldPath), nativePath(oldPath)} {
				line = strings.ReplaceAll(line, value, normalized(newPath))
			}
			lines[d.line] = line
		}
	}
	return []byte(strings.Join(lines, "\n"))
}
func (m *Manager) migrationConfigPath(relative string) (string, error) {
	path := filepath.Join(m.root, filepath.FromSlash(relative))
	if filepath.IsAbs(relative) || strings.Contains(relative, ":") || !within(filepath.Join(m.root, "instances"), path) {
		return "", fmt.Errorf("扩展配置迁移路径越界")
	}
	real, err := PhysicalPath(path)
	if err != nil || !samePHPPath(path, real) {
		return "", fmt.Errorf("扩展配置迁移路径被重定向")
	}
	return real, nil
}
func (m *Manager) completeExtensionMigration(j extensionLayoutMigration, p InstalledPackage, journal string) error {
	// Once files and metadata are published, a crash recovery finishes only
	// known before/after configuration contents; external edits remain intact.
	for _, change := range j.Configs {
		path, err := m.migrationConfigPath(change.Path)
		if err != nil {
			return err
		}
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(current) == string(change.After) {
			continue
		}
		if string(current) != string(change.Before) {
			return fmt.Errorf("迁移期间配置 %s 已被编辑，原文件已保留", path)
		}
		if err = os.WriteFile(path+".before-extension-layout", change.Before, 0600); err != nil {
			return err
		}
		if err = atomicConfigWrite(path, change.After); err != nil {
			return err
		}
	}
	newRevisions := map[string]string{}
	for _, i := range m.Snapshot().Instances {
		if i.PackageID == p.ID {
			set, err := readPHPINI(i)
			if err != nil {
				return err
			}
			newRevisions[i.ID] = set.revision
		}
	}
	if err := m.change(func(s *State) error {
		for n := range s.PreparedExtensions {
			r := &s.PreparedExtensions[n].Record
			if r.PackageID == p.ID {
				r.RelativeDLL = flatExtensionRelativeDLL(p, r.Name)
			}
		}
		for n := range s.Tasks {
			t := &s.Tasks[n]
			if before, ok := j.Revisions[t.InstanceID]; ok && t.Kind == "php-extension" && t.ConfigRevision == before {
				t.ConfigRevision = newRevisions[t.InstanceID]
			}
		}
		return nil
	}); err != nil {
		return err
	}
	oldDir := filepath.Dir(filepath.Join(p.Path, filepath.FromSlash(j.Before.RelativeDLL)))
	root := filepath.Join(filepath.Dir(p.Executable), "extensions")
	if !within(root, oldDir) || filepath.Base(oldDir) != j.Before.Name {
		return fmt.Errorf("旧扩展目录与安装记录不符")
	}
	// Remove only matching copied DLLs. Leave any unrelated manual files alone.
	for name, hash := range j.After.Files {
		path := filepath.Join(oldDir, name)
		if actual, err := fileHash(path); err == nil && actual == hash {
			if err = checkedExtensionRemoval(path, root, false); err != nil {
				return err
			}
		}
	}
	if entries, err := os.ReadDir(oldDir); err == nil && len(entries) == 0 {
		if err = checkedExtensionRemoval(oldDir, root, false); err != nil {
			return err
		}
	}
	stage := filepath.Join(m.root, filepath.FromSlash(j.StageRelative))
	if _, err := os.Stat(stage); err == nil {
		if err = checkedExtensionRemoval(stage, filepath.Join(m.root, "downloads", "staged"), true); err != nil {
			return err
		}
	}
	return os.Remove(journal)
}
func (m *Manager) migrateFlatExtensions() error {
	files, err := filepath.Glob(filepath.Join(m.root, "state", "extension-flatten-*.json"))
	if err != nil {
		return err
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if len(body) > 8<<20 {
			return fmt.Errorf("扩展迁移记录过大")
		}
		var j extensionLayoutMigration
		if err = json.Unmarshal(body, &j); err != nil {
			return err
		}
		var p InstalledPackage
		for _, candidate := range m.state.Packages {
			if candidate.ID == j.After.PackageID {
				p = candidate
			}
		}
		if p.ID == "" {
			return fmt.Errorf("扩展迁移缺少 PHP 包")
		}
		expected := filepath.Join(m.root, "state", "extension-flatten-"+j.After.Name+"-"+p.ID+".json")
		if !samePHPPath(expected, file) || !validFlatMigration(j, p) {
			return fmt.Errorf("扩展迁移记录无效")
		}
		committed := false
		for _, record := range m.state.ExtensionPackages {
			if record.PackageID == p.ID && record.Name == j.After.Name && samePHPPath(record.RelativeDLL, j.After.RelativeDLL) {
				committed = true
			}
		}
		if committed {
			if err = m.completeExtensionMigration(j, p, file); err != nil {
				return err
			}
		} else {
			stage := filepath.Join(m.root, filepath.FromSlash(j.StageRelative))
			if _, err := os.Stat(stage); err == nil {
				if err = checkedExtensionRemoval(stage, filepath.Join(m.root, "downloads", "staged"), true); err != nil {
					return err
				}
			}
			if err = os.Remove(file); err != nil {
				return err
			}
		}
	}
	for _, old := range m.Snapshot().ExtensionPackages {
		var p InstalledPackage
		for _, candidate := range m.Snapshot().Packages {
			if candidate.ID == old.PackageID {
				p = candidate
			}
		}
		if p.ID == "" {
			continue
		}
		flat := flatExtensionRelativeDLL(p, old.Name)
		if samePHPPath(old.RelativeDLL, flat) {
			continue
		}
		if old.Name != "redis" && old.Name != "apcu" && old.Name != "xdebug" {
			return fmt.Errorf("扩展迁移名称无效")
		}
		oldPath, err := installedExtensionDLL(p, old)
		if err != nil {
			return err
		}
		oldDir := filepath.Dir(oldPath)
		root := filepath.Join(filepath.Dir(p.Executable), "extensions")
		if filepath.Dir(oldDir) != root || filepath.Base(oldDir) != old.Name {
			return fmt.Errorf("旧扩展路径不符合预期")
		}
		if err = os.MkdirAll(filepath.Join(m.root, "downloads", "staged"), 0700); err != nil {
			return err
		}
		stage, err := os.MkdirTemp(filepath.Join(m.root, "downloads", "staged"), "flatten-")
		if err != nil {
			return err
		}
		retained := false
		err = func() error {
			defer func() {
				if !retained {
					_ = os.RemoveAll(stage)
				}
			}()
			entries, err := os.ReadDir(oldDir)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".dll") {
					source := filepath.Join(oldDir, entry.Name())
					real, err := PhysicalPath(source)
					if err != nil || !samePHPPath(real, source) {
						return fmt.Errorf("旧扩展 DLL 被重定向")
					}
					if err = copyExtensionFile(source, filepath.Join(stage, entry.Name())); err != nil {
						return err
					}
				}
			}
			manifest, err := extensionDirectoryFiles(stage)
			if err != nil {
				return err
			}
			next := old
			next.RelativeDLL = flat
			next.Files = manifest
			stageRelative, _ := filepath.Rel(m.root, stage)
			j := extensionLayoutMigration{Before: old, After: next, StageRelative: filepath.ToSlash(stageRelative), Configs: []extensionConfigMove{}, Revisions: map[string]string{}}
			newPath := filepath.Join(p.Path, filepath.FromSlash(flat))
			for _, i := range m.Snapshot().Instances {
				if i.PackageID != p.ID {
					continue
				}
				set, err := readPHPINI(i)
				if err != nil {
					return err
				}
				j.Revisions[i.ID] = set.revision
				all := map[string][]byte{"php.ini": set.main}
				for name, body := range set.scan {
					all[filepath.Join("conf.d", name)] = body
				}
				for name, body := range all {
					updated := rewriteExtensionDirectivePath(body, oldPath, newPath)
					if string(updated) != string(body) {
						path := filepath.Join(i.Path, "config", name)
						relative, _ := filepath.Rel(m.root, path)
						j.Configs = append(j.Configs, extensionConfigMove{Path: filepath.ToSlash(relative), Before: body, After: updated})
					}
				}
			}
			journal := filepath.Join(m.root, "state", "extension-flatten-"+old.Name+"-"+p.ID+".json")
			body, _ := json.MarshalIndent(j, "", "  ")
			if err = atomicConfigWrite(journal, body); err != nil {
				return err
			}
			retained = true
			prepared := PreparedPHPExtension{TaskID: id("flatten"), StageRelative: j.StageRelative, Record: next}
			if err = m.publishFlatExtension(prepared, p); err != nil {
				return err
			}
			return m.completeExtensionMigration(j, p, journal)
		}()
		if err != nil {
			return err
		}
	}
	changed := false
	for _, r := range m.Snapshot().PreparedExtensions {
		for _, p := range m.Snapshot().Packages {
			if r.Record.PackageID == p.ID && !samePHPPath(r.Record.RelativeDLL, flatExtensionRelativeDLL(p, r.Record.Name)) {
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	return m.change(func(s *State) error {
		for n := range s.PreparedExtensions {
			r := &s.PreparedExtensions[n].Record
			for _, p := range s.Packages {
				if r.PackageID == p.ID {
					r.RelativeDLL = flatExtensionRelativeDLL(p, r.Name)
				}
			}
		}
		return nil
	})
}
func validFlatMigration(j extensionLayoutMigration, p InstalledPackage) bool {
	if filepath.Base(p.ID) != p.ID || strings.ContainsAny(p.ID, "/\\:") || (j.After.Name != "redis" && j.After.Name != "apcu" && j.After.Name != "xdebug") {
		return false
	}
	if j.Before.PackageID != p.ID || j.Before.Name != j.After.Name || !samePHPPath(j.After.RelativeDLL, flatExtensionRelativeDLL(p, j.After.Name)) {
		return false
	}
	for name, hash := range j.After.Files {
		if filepath.Base(name) != name || strings.ContainsAny(name, "/\\:") || !validHash(hash) {
			return false
		}
	}
	return true
}

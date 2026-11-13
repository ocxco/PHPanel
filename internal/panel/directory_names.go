package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func readableDirectoryName(kind, component, version string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(version) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	v := strings.Trim(b.String(), ".-_")
	if v == "" {
		v = "custom"
	}
	return kind + "-" + component + v
}
func (m *Manager) reserveDirectory(base, name string) (string, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for n := 1; n < 10000; n++ {
		candidate := name
		if n > 1 {
			candidate += fmt.Sprintf("-%d", n)
		}
		path := filepath.Join(base, candidate)
		if !within(base, path) {
			return "", nil, fmt.Errorf("目录名称无效")
		}
		if m.operations["directory:"+path] {
			continue
		}
		if _, err := os.Lstat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", nil, err
		}
		m.operations["directory:"+path] = true
		return path, func() { m.mu.Lock(); delete(m.operations, "directory:"+path); m.mu.Unlock() }, nil
	}
	return "", nil, fmt.Errorf("无法分配唯一目录")
}

type directoryMove struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}
type directoryConfigBackup struct {
	Path string `json:"path"`
	Body []byte `json:"body"`
}
type directoryJournal struct {
	Moves   []directoryMove         `json:"moves"`
	Configs []directoryConfigBackup `json:"configs"`
}

func (m *Manager) directoryJournalPath() string {
	return filepath.Join(m.root, "state", "directory-migration.json")
}
func (m *Manager) journalPath(relative string) (string, error) {
	path := filepath.Join(m.root, filepath.FromSlash(relative))
	if filepath.IsAbs(relative) || !within(m.root, path) {
		return "", fmt.Errorf("目录迁移记录越界")
	}
	return path, nil
}
func (m *Manager) rollbackDirectories(j directoryJournal) error {
	for n := len(j.Moves) - 1; n >= 0; n-- {
		move := j.Moves[n]
		from, err := m.journalPath(move.From)
		if err != nil {
			return err
		}
		to, err := m.journalPath(move.To)
		if err != nil {
			return err
		}
		_, oldErr := os.Lstat(from)
		_, newErr := os.Lstat(to)
		if oldErr == nil && newErr == nil {
			return fmt.Errorf("迁移恢复时发现同名目录，已保留文件: %s", to)
		}
		if os.IsNotExist(oldErr) && newErr == nil {
			if err := renameInstallDirectory(context.Background(), to, from); err != nil {
				return err
			}
		}
	}
	for _, backup := range j.Configs {
		path, err := m.journalPath(backup.Path)
		if err != nil {
			return err
		}
		if err := atomicConfigWrite(path, backup.Body); err != nil {
			return err
		}
	}
	return os.Remove(m.directoryJournalPath())
}
func (m *Manager) recoverDirectoryMigration() error {
	body, err := os.ReadFile(m.directoryJournalPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var j directoryJournal
	if err := json.Unmarshal(body, &j); err != nil {
		return err
	}
	committed := true
	for _, move := range j.Moves {
		path, err := m.journalPath(move.To)
		if err != nil {
			return err
		}
		found := false
		if move.Kind == "packages" {
			for _, p := range m.state.Packages {
				if p.ID == move.ID && p.Path == path {
					found = true
				}
			}
		} else {
			for _, i := range m.state.Instances {
				if i.ID == move.ID && i.Path == path {
					found = true
				}
			}
		}
		if !found {
			committed = false
		}
	}
	if committed {
		return os.Remove(m.directoryJournalPath())
	}
	return m.rollbackDirectories(j)
}
func (m *Manager) migrateDirectoryNames() error {
	if err := m.recoverDirectoryMigration(); err != nil {
		return fmt.Errorf("恢复目录迁移失败: %w", err)
	}
	before := cloneState(m.state)
	candidate := cloneState(m.state)
	j := directoryJournal{}
	reserved := map[string]bool{}
	allocate := func(kind, id, component, version, path, folder string) (string, string, error) {
		if folder != "" {
			return path, folder, nil
		}
		base := filepath.Join(m.root, kind)
		if filepath.Dir(path) != base || !within(base, path) {
			return path, "", nil
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path, "", nil
		} else if err != nil {
			return "", "", err
		}
		actual, err := PhysicalPath(path)
		if err != nil {
			return "", "", err
		}
		rel, err := filepath.Rel(path, actual)
		if err != nil || rel != "." {
			return "", "", fmt.Errorf("目录被重定向，拒绝迁移: %s", path)
		}
		prefix := "package"
		if kind == "instances" {
			prefix = "instance"
		}
		name := readableDirectoryName(prefix, component, version)
		if filepath.Base(path) == name {
			return path, name, nil
		}
		for n := 1; n < 10000; n++ {
			folder := name
			if n > 1 {
				folder += fmt.Sprintf("-%d", n)
			}
			to := filepath.Join(base, folder)
			if reserved[to] {
				continue
			}
			if _, err := os.Lstat(to); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				return "", "", err
			}
			reserved[to] = true
			fromRel, _ := filepath.Rel(m.root, path)
			toRel, _ := filepath.Rel(m.root, to)
			j.Moves = append(j.Moves, directoryMove{Kind: kind, ID: id, From: filepath.ToSlash(fromRel), To: filepath.ToSlash(toRel)})
			return to, folder, nil
		}
		return "", "", fmt.Errorf("目录名称冲突过多")
	}
	for n, p := range candidate.Packages {
		path, folder, err := allocate("packages", p.ID, p.Component, p.Version, p.Path, p.DirectoryName)
		if err != nil {
			return err
		}
		if path != p.Path {
			relative, err := filepath.Rel(p.Path, p.Executable)
			if err != nil || !within(p.Path, p.Executable) {
				return fmt.Errorf("程序路径越界")
			}
			candidate.Packages[n].Executable = filepath.Join(path, relative)
		}
		candidate.Packages[n].Path = path
		candidate.Packages[n].DirectoryName = folder
	}
	for n, i := range candidate.Instances {
		path, folder, err := allocate("instances", i.ID, i.Component, i.Version, i.Path, i.DirectoryName)
		if err != nil {
			return err
		}
		candidate.Instances[n].Path = path
		candidate.Instances[n].DirectoryName = folder
	}
	if len(j.Moves) == 0 {
		m.state = candidate
		return nil
	}
	type replacement struct{ old, new string }
	replacements := []replacement{}
	for _, move := range j.Moves {
		old, _ := m.journalPath(move.From)
		next, _ := m.journalPath(move.To)
		for _, from := range []string{old, strings.ReplaceAll(old, "\\", "/"), nativePath(old), normalized(old)} {
			to := next
			if strings.Contains(from, "/") {
				to = strings.ReplaceAll(next, "\\", "/")
			}
			replacements = append(replacements, replacement{from, to})
		}
	}
	sort.SliceStable(replacements, func(a, b int) bool { return len(replacements[a].old) > len(replacements[b].old) })
	rewrite := func(body []byte) []byte {
		text := string(m.rebaseConfigBody(body))
		for _, r := range replacements {
			text = strings.ReplaceAll(text, r.old, r.new)
		}
		return []byte(text)
	}
	updated := map[string][]byte{}
	collect := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("配置目录包含链接，拒绝迁移")
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".ini" && ext != ".conf" && ext != ".sql" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		next := rewrite(body)
		if string(next) == string(body) {
			return nil
		}
		rel, _ := filepath.Rel(m.root, path)
		j.Configs = append(j.Configs, directoryConfigBackup{Path: filepath.ToSlash(rel), Body: body})
		updated[path] = next
		return nil
	}
	for _, i := range before.Instances {
		dir := filepath.Join(i.Path, "config")
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		if err := filepath.WalkDir(dir, collect); err != nil {
			return err
		}
	}
	if err := filepath.WalkDir(filepath.Join(m.root, "sites"), func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() && path != filepath.Join(m.root, "sites") {
			return filepath.SkipDir
		}
		return collect(path, e, err)
	}); err != nil {
		return err
	}
	for n, site := range candidate.Sites {
		candidate.Sites[n].Root = string(rewrite([]byte(site.Root)))
	}
	journalBody, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicConfigWrite(m.directoryJournalPath(), journalBody); err != nil {
		return err
	}
	rollback := func(cause error) error {
		m.state = before
		if err := m.rollbackDirectories(j); err != nil {
			return fmt.Errorf("%w；目录恢复失败，迁移记录已保留: %v", cause, err)
		}
		return cause
	}
	for _, move := range j.Moves {
		from, _ := m.journalPath(move.From)
		to, _ := m.journalPath(move.To)
		if err := renameInstallDirectory(context.Background(), from, to); err != nil {
			return rollback(err)
		}
	}
	for original, body := range updated {
		path := original
		for _, move := range j.Moves {
			from, _ := m.journalPath(move.From)
			to, _ := m.journalPath(move.To)
			if within(from, original) {
				relative, _ := filepath.Rel(from, original)
				path = filepath.Join(to, relative)
				break
			}
		}
		if err := atomicConfigWrite(path, body); err != nil {
			return rollback(err)
		}
	}
	backup, err := json.MarshalIndent(before, "", "  ")
	if err != nil {
		return rollback(err)
	}
	if err := atomicConfigWrite(m.statePath()+".before-directory-rename", backup); err != nil {
		return rollback(err)
	}
	m.state = candidate
	if err := m.saveLocked(); err != nil {
		return rollback(err)
	}
	return os.Remove(m.directoryJournalPath())
}

package panel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PHPExtensionArtifact struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	PHPBranch    string   `json:"phpBranch"`
	ThreadSafe   bool     `json:"threadSafe"`
	Bits         int      `json:"bits"`
	Compiler     string   `json:"compiler"`
	Kind         string   `json:"kind"`
	Format       string   `json:"format"`
	Publisher    string   `json:"publisher"`
	SHA256       string   `json:"sha256"`
	Verification string   `json:"verification"`
	Sources      []Source `json:"sources"`
}
type InstalledPHPExtension struct {
	Files        map[string]string `json:"files,omitempty"`
	ID           string            `json:"id"`
	PackageID    string            `json:"packageId"`
	ArtifactID   string            `json:"artifactId"`
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Kind         string            `json:"kind"`
	RelativeDLL  string            `json:"relativeDll"`
	DLLSHA256    string            `json:"dllSha256"`
	Build        string            `json:"build"`
	ZendBuild    string            `json:"zendBuild"`
	Verification string            `json:"verification"`
	InstalledAt  string            `json:"installedAt"`
}

func cloneInstalledExtensions(items []InstalledPHPExtension) []InstalledPHPExtension {
	out := append([]InstalledPHPExtension{}, items...)
	for n := range out {
		if out[n].Files != nil {
			out[n].Files = cloneStringMap(out[n].Files)
		}
	}
	return out
}
func flatExtensionRelativeDLL(p InstalledPackage, name string) string {
	path := filepath.Join(filepath.Dir(p.Executable), "extensions", "php_"+name+".dll")
	relative, _ := filepath.Rel(p.Path, path)
	return filepath.ToSlash(relative)
}

type PreparedPHPExtension struct {
	TaskID        string                `json:"taskId"`
	StageRelative string                `json:"stageRelative"`
	Record        InstalledPHPExtension `json:"record"`
}

func cloneStringMap(input map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range input {
		out[k] = v
	}
	return out
}
func cloneExtensionArtifacts(input []PHPExtensionArtifact) []PHPExtensionArtifact {
	out := append([]PHPExtensionArtifact{}, input...)
	for n := range out {
		out[n].Sources = append([]Source{}, out[n].Sources...)
	}
	return out
}
func extensionArtifactID(a PHPExtensionArtifact) string {
	identity := fmt.Sprintf("%s|%s|%s|%t|%d|%s|%s|%s", a.Name, a.Version, a.PHPBranch, a.ThreadSafe, a.Bits, a.Compiler, a.Kind, a.Format)
	sum := sha256.Sum256([]byte(identity))
	return "php-extension-" + hex.EncodeToString(sum[:12])
}
func phpBranch(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}
func phpCompiler(build string) string {
	parts := strings.Split(strings.ToLower(build), ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "vs") || strings.HasPrefix(part, "vc") {
			return part
		}
	}
	return ""
}
func extensionBuildKey(p PHPProbe) string {
	return fmt.Sprintf("%s/%d/%t/%s/%s", phpBranch(p.Version), p.Bits, p.ThreadSafe, p.Build, p.ZendBuild)
}
func extensionCompatible(a PHPExtensionArtifact, p PHPProbe) bool {
	return a.PHPBranch == phpBranch(p.Version) && a.Bits == p.Bits && a.ThreadSafe == p.ThreadSafe && a.Compiler == phpCompiler(p.Build) && !strings.Contains(strings.ToLower(p.Build), "debug")
}
func samePHPPath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(filepath.FromSlash(strings.ReplaceAll(a, "\\", "/"))), filepath.Clean(filepath.FromSlash(strings.ReplaceAll(b, "\\", "/"))))
}
func installedExtensionDLL(p InstalledPackage, item InstalledPHPExtension) (string, error) {
	if item.PackageID != p.ID || filepath.IsAbs(item.RelativeDLL) || strings.Contains(item.RelativeDLL, ":") || !validHash(item.DLLSHA256) {
		return "", fmt.Errorf("扩展安装记录无效")
	}
	path := filepath.Join(p.Path, filepath.FromSlash(item.RelativeDLL))
	root := filepath.Join(filepath.Dir(p.Executable), "extensions")
	if !within(root, path) {
		return "", fmt.Errorf("扩展路径不属于 PHP 包的 extensions 目录")
	}
	real, err := PhysicalPath(path)
	if err != nil || !samePHPPath(real, path) {
		return "", fmt.Errorf("扩展路径不存在或被重定向")
	}
	hash, err := fileHash(real)
	if err != nil || hash != item.DLLSHA256 {
		return "", fmt.Errorf("扩展 DLL 已被修改，请重新安装")
	}
	return real, nil
}
func selectInstalledExtensions(p InstalledPackage, base PHPProbe, set phpINISet, items []InstalledPHPExtension) []InstalledPHPExtension {
	configured := map[string]string{}
	for _, file := range sortedINI(set.scan) {
		if !strings.EqualFold(file, phpManagedINI) {
			continue
		}
		for _, d := range phpDirectives(set.scan[file]) {
			if d.key == "extension" || d.key == "zend_extension" {
				configured[phpModuleName(d.value)] = d.value
			}
		}
	}
	selected := map[string]InstalledPHPExtension{}
	for _, item := range items {
		if item.PackageID != p.ID || item.Build != base.Build || item.Kind == "zend" && item.ZendBuild != base.ZendBuild {
			continue
		}
		path, err := installedExtensionDLL(p, item)
		if err != nil {
			continue
		}
		old, ok := selected[item.Name]
		oldPath, _ := installedExtensionDLL(p, old)
		if !ok || samePHPPath(path, configured[item.Name]) || !samePHPPath(oldPath, configured[item.Name]) && versionLess(old.Version, item.Version) {
			selected[item.Name] = item
		}
	}
	out := []InstalledPHPExtension{}
	for _, item := range selected {
		out = append(out, item)
	}
	return out
}
func mergeExtensionArtifacts(state *State, items []PHPExtensionArtifact) {
	if state.ExtensionArtifacts == nil {
		state.ExtensionArtifacts = []PHPExtensionArtifact{}
	}
	for _, a := range items {
		found := false
		for n, old := range state.ExtensionArtifacts {
			if old.ID == a.ID {
				found = true
				// Keep trusted pinned hashes and user mirror URLs across metadata refreshes.
				if a.SHA256 == "" && old.SHA256 != "" {
					a.SHA256 = old.SHA256
					a.Verification = old.Verification
				}
				for _, s := range old.Sources {
					exists := false
					for _, ns := range a.Sources {
						if s.URL == ns.URL {
							exists = true
						}
					}
					if !exists && len(a.Sources) < 12 {
						a.Sources = append(a.Sources, s)
					}
				}
				state.ExtensionArtifacts[n] = a
				break
			}
		}
		if !found {
			state.ExtensionArtifacts = append(state.ExtensionArtifacts, a)
		}
	}
}
func (m *Manager) fixManagedExtensionPaths(i Instance) error {
	if i.Component != "php" || m.previousRoot == "" || m.previousRoot == m.root {
		return nil
	}
	dir := filepath.Join(i.Path, "config", "conf.d")
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, f := range files {
		if !f.IsDir() && strings.EqualFold(filepath.Ext(f.Name()), ".ini") {
			if err := m.updateConfigFileRoot(i, filepath.Join("conf.d", f.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

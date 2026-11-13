package release

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type serviceRecord struct {
	Name string `json:"serviceName"`
}

func registeredServices(root string) ([]string, error) {
	body, err := os.ReadFile(filepath.Join(root, "state", "panel.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state struct {
		Instances []serviceRecord `json:"instances"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, fmt.Errorf("无法读取环境记录，已阻止卸载以保护数据: %w", err)
	}
	var result []string
	for _, i := range state.Instances {
		if i.Name != "" {
			if !strings.HasPrefix(i.Name, "PHPanel-") {
				return nil, fmt.Errorf("系统服务名称异常")
			}
			result = append(result, i.Name)
		}
	}
	return result, nil
}
func ValidateInstallRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if absolute == filepath.VolumeName(absolute)+string(os.PathSeparator) {
		return "", fmt.Errorf("安装目录不能是磁盘根目录")
	}
	return filepath.Clean(absolute), nil
}
func Check(root, mode string) error {
	root, err := ValidateInstallRoot(root)
	if err != nil {
		return err
	}
	if mode != "update" && mode != "uninstall" {
		return fmt.Errorf("无效的安装检查模式")
	}
	if err := checkProgramFiles(root); err != nil {
		return err
	}
	if mode == "uninstall" {
		if err := checkRegisteredPrograms(root); err != nil {
			return err
		}
		services, err := registeredServices(root)
		if err != nil {
			return err
		}
		for _, name := range services {
			exists, err := serviceExists(name)
			if err != nil {
				return fmt.Errorf("无法核对 Windows 服务 %s，请先在面板中注销系统服务: %w", name, err)
			}
			if exists {
				return fmt.Errorf("请先在 PHPanel 中停止并注销系统服务 %s，再卸载程序。环境与数据库会保留。", name)
			}
		}
	}
	return nil
}
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(strings.ReplaceAll(a, "/", string(os.PathSeparator))), filepath.Clean(strings.ReplaceAll(b, "/", string(os.PathSeparator))))
}
func RemoveManagedPath(value, dir string) string {
	var parts []string
	for _, part := range strings.Split(value, ";") {
		if !samePath(strings.Trim(strings.TrimSpace(part), "\""), dir) {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ";")
}
func Cleanup(root string) error {
	root, err := ValidateInstallRoot(root)
	if err != nil {
		return err
	}
	if err := Check(root, "uninstall"); err != nil {
		return err
	}
	return cleanupUserSettings(root)
}

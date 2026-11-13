package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func loadRuntimeState(root string) (*Manager, error) {
	abs, err := PhysicalPath(root)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(filepath.Join(abs, "state", "panel.json"))
	if err != nil {
		return nil, err
	}
	var state State
	if err = json.Unmarshal(body, &state); err != nil {
		return nil, err
	}
	if !strings.EqualFold(filepath.Clean(state.Root), abs) {
		return nil, fmt.Errorf("软件或数据目录已移动，请注销并重新注册系统服务")
	}
	m := &Manager{root: abs, state: state, serviceHost: true, processes: map[string]*managedProcess{}, operations: map[string]bool{}, downloads: map[string]context.CancelFunc{}}
	for n := range m.state.Instances {
		m.state.Instances[n].Status, m.state.Instances[n].PID = "stopped", 0
	}
	return m, nil
}
func loadServiceHost(root, key, name string) (*Manager, Instance, error) {
	m, err := loadRuntimeState(root)
	if err != nil {
		return nil, Instance{}, err
	}
	i, p, err := m.lookupInstance(key)
	if err != nil {
		return nil, Instance{}, err
	}
	if !supportsSystemService(i.Component) || i.ServiceName != name {
		return nil, Instance{}, fmt.Errorf("实例系统服务注册记录不匹配")
	}
	if !within(filepath.Join(m.root, "instances"), i.Path) || !within(filepath.Join(m.root, "packages"), p.Executable) {
		return nil, Instance{}, fmt.Errorf("实例或程序路径越界")
	}
	return m, i, nil
}
func appendServiceHostLog(i Instance, err error) error {
	f, openErr := os.OpenFile(filepath.Join(i.Path, "logs", "service-host.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if openErr != nil {
		return openErr
	}
	defer f.Close()
	_, writeErr := fmt.Fprintf(f, "%s %v\n", timestamp(), err)
	return writeErr
}

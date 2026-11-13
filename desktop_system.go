package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"phpanel/internal/panel"
)

const startupArgument = "--phpanel-startup"
const dataRootArgument = "--phpanel-data-root="

type autorunEntry struct {
	Exists bool
	Value  string
	Kind   uint32
}
type startupRegistry interface {
	Read() (autorunEntry, error)
	Write(autorunEntry) error
}
type trayIcon interface {
	Ready() bool
	Close()
}
type SystemStatus struct {
	Settings      panel.SystemSettings `json:"settings"`
	TrayAvailable bool                 `json:"trayAvailable"`
	Warning       string               `json:"warning"`
}
type desktopController struct {
	mu               sync.Mutex
	forceExit        atomic.Bool
	registry         startupRegistry
	tray             trayIcon
	trayError        string
	hide, show, quit func()
}

func parseDesktopLaunch(args []string) (string, error) {
	root := ""
	for _, arg := range args {
		if strings.HasPrefix(arg, dataRootArgument) {
			if root != "" {
				return "", fmt.Errorf("重复的数据目录参数")
			}
			root = strings.TrimPrefix(arg, dataRootArgument)
			if !filepath.IsAbs(root) || strings.ContainsAny(root, "\x00\r\n") {
				return "", fmt.Errorf("数据目录参数无效")
			}
		}
	}
	return root, nil
}
func readDesktopPreferences(root string) panel.SystemSettings {
	settings := panel.SystemSettings{CloseBehavior: "quit"}
	body, err := os.ReadFile(filepath.Join(root, "state", "panel.json"))
	if err != nil {
		return settings
	}
	var state struct {
		Settings struct {
			System panel.SystemSettings `json:"system"`
		} `json:"settings"`
	}
	if json.Unmarshal(body, &state) != nil {
		return settings
	}
	result, err := panel.NormalizeSystemSettings(state.Settings.System)
	if err != nil {
		return settings
	}
	return result
}
func desktopStartState(settings panel.SystemSettings, elevatedRestart bool) options.WindowStartState {
	if settings.StartMinimized && !elevatedRestart {
		return options.Minimised
	}
	return options.Normal
}
func desktopInstanceID(root string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(root))))
	return fmt.Sprintf("PHPanel-%x", hash[:16])
}
func (d *desktopController) beforeClose(settings panel.SystemSettings) bool {
	if d.forceExit.Load() || settings.CloseBehavior != "tray" {
		return false
	}
	d.mu.Lock()
	ready := d.tray != nil && d.tray.Ready()
	d.mu.Unlock()
	if !ready {
		return false
	}
	d.hide()
	return true
}
func (d *desktopController) exit() { d.forceExit.Store(true); d.quit() }
func (d *desktopController) restore() {
	if !d.forceExit.Load() {
		d.show()
	}
}
func (d *desktopController) save(settings panel.SystemSettings, command string, persist func(panel.SystemSettings) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	settings, err := panel.NormalizeSystemSettings(settings)
	if err != nil {
		return err
	}
	if settings.CloseBehavior == "tray" && (d.tray == nil || !d.tray.Ready()) {
		return fmt.Errorf("系统托盘不可用，请使用关闭程序行为")
	}
	previous, err := d.registry.Read()
	if err != nil {
		return err
	}
	next := previous
	if settings.AutoStart {
		next = autorunEntry{Exists: true, Value: command, Kind: 1}
	} else if previous.Value == command {
		next = autorunEntry{}
	}
	changed := next != previous
	if changed {
		if err := d.registry.Write(next); err != nil {
			return fmt.Errorf("保存开机启动失败: %w", err)
		}
	}
	if err := persist(settings); err != nil {
		if changed {
			if rollback := d.registry.Write(previous); rollback != nil {
				return fmt.Errorf("%v；开机启动恢复失败: %w", err, rollback)
			}
		}
		return err
	}
	return nil
}
func (a *App) configureDesktop() {
	a.desktop = &desktopController{registry: newStartupRegistry(), hide: func() { runtime.WindowHide(a.ctx) }, show: func() { runtime.WindowShow(a.ctx); runtime.WindowUnminimise(a.ctx) }, quit: func() { runtime.Quit(a.ctx) }}
}
func (a *App) startDesktop() {
	tray, err := newTrayIcon(a.desktop.restore, a.desktop.exit)
	a.desktop.mu.Lock()
	a.desktop.tray = tray
	if err != nil {
		a.desktop.trayError = err.Error()
	}
	a.desktop.mu.Unlock()
}
func (a *App) beforeClose(context.Context) bool {
	if a.manager == nil || a.initErr != nil {
		return false
	}
	return a.desktop.beforeClose(a.manager.Snapshot().Settings.System)
}
func (a *App) GetSystemStatus() (SystemStatus, error) {
	if err := a.ready(); err != nil {
		return SystemStatus{}, err
	}
	settings := a.manager.Snapshot().Settings.System
	a.desktop.mu.Lock()
	defer a.desktop.mu.Unlock()
	status := SystemStatus{Settings: settings, TrayAvailable: a.desktop.tray != nil && a.desktop.tray.Ready(), Warning: a.desktop.trayError}
	exe, err := os.Executable()
	if err != nil {
		return status, err
	}
	entry, err := a.desktop.registry.Read()
	if err != nil {
		return status, err
	}
	status.Settings.AutoStart = entry.Exists && entry.Value == startupCommand(exe, a.manager.Snapshot().Root)
	return status, nil
}
func (a *App) SaveSystemSettings(settings panel.SystemSettings) error {
	if err := a.ready(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return a.desktop.save(settings, startupCommand(exe, a.manager.Snapshot().Root), a.manager.SaveSystemSettings)
}
func (a *App) ExitApplication() { a.desktop.exit() }

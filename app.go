package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"phpanel/internal/panel"
)

type App struct {
	desktop          *desktopController
	explicitDataRoot string
	elevation        *elevationController
	elevationResume  *elevationRequest
	ctx              context.Context
	manager          *panel.Manager
	initErr          error
}

func NewApp() *App { a := &App{}; a.configureDesktop(); a.configureElevation(); return a }
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	executable, err := os.Executable()
	if err != nil {
		a.initErr = err
		return
	}
	root, err := resolveDataRoot(executable, os.Getenv("PHPANEL_DATA_DIR"))
	if a.explicitDataRoot != "" {
		root = a.explicitDataRoot
		err = nil
	}
	if a.elevationResume != nil {
		root = a.elevationResume.DataRoot
		err = nil
	}
	if err != nil {
		a.initErr = err
		return
	}
	if os.Getenv("PHPANEL_DATA_DIR") == "" {
		if _, stateErr := os.Stat(filepath.Join(root, "state", "panel.json")); os.IsNotExist(stateErr) {
			previous, previousErr := legacyDataRoot(executable)
			if previousErr != nil {
				a.initErr = previousErr
				return
			}
			if previous != "" && !strings.EqualFold(filepath.Clean(previous), filepath.Clean(root)) {
				if err := panel.CopyDataRoot(previous, root); err != nil {
					a.initErr = fmt.Errorf("复制旧环境失败，原目录已保留: %w", err)
					return
				}
			}
		}
	}
	if a.elevationResume != nil && a.elevationResume.OriginalUserSID != "" {
		a.manager, a.initErr = panel.NewWithRuntimeUserSID(root, a.elevationResume.OriginalUserSID)
	} else {
		a.manager, a.initErr = panel.New(root)
	}
	if a.initErr == nil {
		a.startDesktop()
		a.applyNativeTheme(a.manager.Snapshot().Settings.ThemeMode)
		if os.Getenv("PHPANEL_DATA_DIR") == "" {
			_ = saveDataRoot(executable, a.manager.Snapshot().Root)
		}
		a.manager.SetChangeHandler(func() { runtime.EventsEmit(a.ctx, "panel:changed") })
		a.manager.StartCatalogUpdater(ctx)
	}
}
func (a *App) shutdown(context.Context) {
	a.desktop.forceExit.Store(true)
	a.desktop.mu.Lock()
	tray := a.desktop.tray
	a.desktop.tray = nil
	a.desktop.mu.Unlock()
	if tray != nil {
		tray.Close()
	}
	if a.manager != nil {
		a.manager.SetChangeHandler(nil)
		a.manager.Close()
	}
}
func (a *App) SnapshotSince(revision uint64) (panel.StateUpdate, error) {
	if err := a.ready(); err != nil {
		return panel.StateUpdate{}, err
	}
	a.manager.RefreshSystemServices()
	return a.manager.SnapshotSince(revision), nil
}
func (a *App) ready() error {
	if a.initErr != nil {
		return a.initErr
	}
	if a.manager == nil {
		return fmt.Errorf("应用尚未完成初始化")
	}
	return nil
}
func (a *App) Snapshot() (panel.State, error) {
	if err := a.ready(); err != nil {
		return panel.State{}, err
	}
	a.manager.RefreshSystemServices()
	return a.manager.Snapshot(), nil
}

func (a *App) RegisterSystemService(id string, autoStart bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.RegisterSystemService(id, autoStart)
}
func (a *App) SetSystemServiceAutoStart(id string, enabled bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.SetSystemServiceAutoStart(id, enabled)
}
func (a *App) UnregisterSystemService(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.UnregisterSystemService(id)
}
func (a *App) RefreshCatalog() ([]string, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.manager.RefreshCatalog(a.ctx)
}
func (a *App) AddArtifact(item panel.Artifact) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.AddArtifact(item)
}
func (a *App) Install(id string) (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	return a.manager.Install(id)
}
func (a *App) PauseDownload(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.PauseDownload(id)
}
func (a *App) ResumeDownload(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.ResumeDownload(id)
}
func (a *App) SaveSettings(settings panel.Settings) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.SaveSettings(settings)
}
func (a *App) SaveTheme(mode string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.manager.SaveTheme(mode); err != nil {
		return err
	}
	a.applyNativeTheme(mode)
	return nil
}
func (a *App) applyNativeTheme(mode string) {
	switch mode {
	case "dark":
		runtime.WindowSetDarkTheme(a.ctx)
	case "light":
		runtime.WindowSetLightTheme(a.ctx)
	default:
		runtime.WindowSetSystemDefaultTheme(a.ctx)
	}
}
func (a *App) ImportArchive(component, version, expectedHash string) (panel.InstalledPackage, error) {
	if err := a.ready(); err != nil {
		return panel.InstalledPackage{}, err
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "导入 Windows ZIP 安装包", Filters: []runtime.FileFilter{{DisplayName: "ZIP 安装包", Pattern: "*.zip"}}})
	if err != nil {
		return panel.InstalledPackage{}, err
	}
	if path == "" {
		return panel.InstalledPackage{}, fmt.Errorf("已取消导入")
	}
	return a.manager.ImportArchive(component, version, path, expectedHash)
}
func (a *App) CreateInstance(packageID, name string, port int) (panel.Instance, error) {
	if err := a.ready(); err != nil {
		return panel.Instance{}, err
	}
	return a.manager.CreateInstance(packageID, name, port)
}
func (a *App) StartInstance(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.StartInstance(id)
}
func (a *App) SaveNginxLinkOptions(id string, autoPHP, stopPHP bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.SaveNginxLinkOptions(id, autoPHP, stopPHP)
}
func (a *App) StopNginxWithPHP(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.StopNginxWithPHP(id)
}
func (a *App) UpdateInstancePort(id string, port int) (panel.Instance, error) {
	if err := a.ready(); err != nil {
		return panel.Instance{}, err
	}
	return a.manager.UpdateInstancePort(id, port)
}
func (a *App) GetCLIStatus() (panel.CLIStatus, error) {
	if err := a.ready(); err != nil {
		return panel.CLIStatus{}, err
	}
	return a.manager.GetCLIStatus()
}
func (a *App) SelectCLIPHP(id string, enablePath bool) (panel.CLIStatus, error) {
	if err := a.ready(); err != nil {
		return panel.CLIStatus{}, err
	}
	return a.manager.SelectCLIPHP(id, enablePath)
}
func (a *App) DisableCLIPHPPath() (panel.CLIStatus, error) {
	if err := a.ready(); err != nil {
		return panel.CLIStatus{}, err
	}
	return a.manager.DisableCLIPHPPath()
}
func (a *App) InstallComposer(id string) (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	return a.manager.InstallComposer(id)
}
func (a *App) EnableComposerPath() (panel.CLIStatus, error) {
	if err := a.ready(); err != nil {
		return panel.CLIStatus{}, err
	}
	return a.manager.EnableComposerPath()
}
func (a *App) StopInstance(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.StopInstance(id)
}
func (a *App) DeleteInstance(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.DeleteInstance(id)
}
func (a *App) RemovePackage(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.RemovePackage(id)
}
func (a *App) CreateSite(name, root, nginxID, phpID string, port int, template string) (panel.Site, error) {
	if err := a.ready(); err != nil {
		return panel.Site{}, err
	}
	return a.manager.CreateSiteWithOptions(name, root, nginxID, phpID, port, template)
}
func (a *App) UpdateSite(id, name, root, nginxID, phpID string, port int, template string) (panel.Site, error) {
	if err := a.ready(); err != nil {
		return panel.Site{}, err
	}
	return a.manager.UpdateSiteWithOptions(id, name, root, nginxID, phpID, port, template)
}
func (a *App) ListConfigs(id string) ([]string, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.manager.ListConfigs(id)
}
func (a *App) ReadConfig(id, name string) (panel.ConfigDocument, error) {
	if err := a.ready(); err != nil {
		return panel.ConfigDocument{}, err
	}
	return a.manager.ReadConfig(id, name)
}
func (a *App) SaveConfig(id, name, content, revision string) (panel.ConfigDocument, error) {
	if err := a.ready(); err != nil {
		return panel.ConfigDocument{}, err
	}
	return a.manager.SaveConfig(id, name, content, revision)
}
func (a *App) DeleteSite(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.DeleteSite(id)
}
func (a *App) GetPHPExtensions(id string) (panel.PHPExtensionReport, error) {
	if err := a.ready(); err != nil {
		return panel.PHPExtensionReport{}, err
	}
	return a.manager.GetPHPExtensions(id)
}
func (a *App) GetPHPOnlineExtensions(id string, refresh bool) (panel.PHPOnlineExtensions, error) {
	if err := a.ready(); err != nil {
		return panel.PHPOnlineExtensions{}, err
	}
	return a.manager.GetPHPOnlineExtensions(id, refresh)
}
func (a *App) InstallPHPExtension(id, artifactID, revision string, enable bool) (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	return a.manager.InstallPHPExtension(id, artifactID, revision, enable)
}
func (a *App) AddPHPExtensionMirror(id, name, url, hash string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.AddPHPExtensionMirror(id, name, url, hash)
}
func (a *App) ImportPHPExtension(id, artifactID, revision string, enable bool) (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "导入 PHP 扩展 DLL 或 ZIP", Filters: []runtime.FileFilter{{DisplayName: "PHP 扩展", Pattern: "*.dll;*.zip"}}})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("已取消导入")
	}
	return a.manager.ImportPHPExtensionArchive(id, artifactID, revision, path, enable)
}
func (a *App) SavePHPExtensions(id string, request panel.PHPSettingsRequest) (panel.PHPExtensionReport, error) {
	if err := a.ready(); err != nil {
		return panel.PHPExtensionReport{}, err
	}
	return a.manager.SavePHPExtensions(id, request)
}
func (a *App) RestartPHPWithExtensions(id, revision string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.RestartPHPWithExtensions(id, revision)
}
func (a *App) PickDirectory() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择网站目录"})
}
func (a *App) OpenSite(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	url, err := a.manager.SiteURL(id)
	if err == nil {
		runtime.BrowserOpenURL(a.ctx, url)
	}
	return err
}
func (a *App) ReadLog(id string) (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	return a.manager.ReadLog(id)
}
func (a *App) Credentials(id string) (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	return a.manager.Credentials(id)
}
func (a *App) GetLocation(kind, id string) (panel.Location, error) {
	if err := a.ready(); err != nil {
		return panel.Location{}, err
	}
	return a.manager.ResolveLocation(kind, id)
}
func (a *App) OpenLocation(kind, id string) error {
	location, err := a.GetLocation(kind, id)
	if err != nil {
		return err
	}
	return openLocation(location)
}
func (a *App) CopyLocation(kind, id string) error {
	location, err := a.GetLocation(kind, id)
	if err != nil {
		return err
	}
	return runtime.ClipboardSetText(a.ctx, location.Path)
}

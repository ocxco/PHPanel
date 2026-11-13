package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const elevationArgument = "--phpanel-elevated-restart="

var errElevationCancelled = errors.New("已取消 Windows 管理员授权")

type elevationRequest struct {
	Version           int    `json:"version"`
	ParentPID         int    `json:"parentPid"`
	Nonce             string `json:"nonce"`
	DataRoot          string `json:"dataRoot"`
	ReturnView        string `json:"returnView"`
	InstanceID        string `json:"instanceId,omitempty"`
	ServiceAutoStart  *bool  `json:"serviceAutoStart,omitempty"`
	OriginalUserSID   string `json:"originalUserSid,omitempty"`
	SoftwareRendering string `json:"softwareRendering,omitempty"`
	WebviewProfile    string `json:"webviewProfile,omitempty"`
}
type PrivilegeStatus struct {
	Elevated         bool   `json:"elevated"`
	ReturnView       string `json:"returnView,omitempty"`
	InstanceID       string `json:"instanceId,omitempty"`
	ServiceAutoStart *bool  `json:"serviceAutoStart,omitempty"`
}
type ElevationResult struct {
	Cancelled bool `json:"cancelled"`
}
type elevationController struct {
	mu         sync.Mutex
	launch     func(string, elevationRequest) error
	quit       func()
	isElevated func() bool
}

func validRestartView(view string) bool {
	switch view {
	case "sites", "instances", "environments", "downloads", "settings":
		return true
	}
	return false
}
func (r elevationRequest) validate() error {
	if r.Version != 1 || r.ParentPID < 1 || !filepath.IsAbs(r.DataRoot) || !validRestartView(r.ReturnView) {
		return fmt.Errorf("管理员重启参数无效")
	}
	nonce, err := hex.DecodeString(r.Nonce)
	if err != nil || len(nonce) != 32 {
		return fmt.Errorf("管理员重启握手标识无效")
	}
	if strings.ContainsAny(r.DataRoot+r.InstanceID+r.WebviewProfile+r.OriginalUserSID, "\x00\r\n") {
		return fmt.Errorf("管理员重启参数包含无效字符")
	}
	return nil
}
func encodeElevationRequest(r elevationRequest) (string, error) {
	if err := r.validate(); err != nil {
		return "", err
	}
	body, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return elevationArgument + base64.RawURLEncoding.EncodeToString(body), nil
}
func verifyElevationDataRoot(root string) error {
	stateDir := filepath.Join(root, "state")
	if _, err := os.Stat(filepath.Join(stateDir, "panel.json")); err != nil {
		return fmt.Errorf("无法访问原数据目录: %w", err)
	}
	probe, err := os.CreateTemp(stateDir, ".elevation-probe-*")
	if err != nil {
		return fmt.Errorf("原数据目录不可写: %w", err)
	}
	name := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}
func parseElevationRequest(args []string) (*elevationRequest, error) {
	var result *elevationRequest
	for _, arg := range args {
		if strings.HasPrefix(arg, elevationArgument) {
			if result != nil || len(arg) > 16384 {
				return nil, fmt.Errorf("管理员重启参数重复或过长")
			}
			body, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(arg, elevationArgument))
			if err != nil {
				return nil, err
			}
			var request elevationRequest
			if err := json.Unmarshal(body, &request); err != nil {
				return nil, err
			}
			if err := request.validate(); err != nil {
				return nil, err
			}
			result = &request
		}
	}
	return result, nil
}
func (c *elevationController) restart(executable string, request elevationRequest) (ElevationResult, error) {
	if !c.mu.TryLock() {
		return ElevationResult{}, fmt.Errorf("正在请求管理员授权，请稍候")
	}
	defer c.mu.Unlock()
	if c.isElevated() {
		return ElevationResult{}, fmt.Errorf("当前已以管理员身份运行，请检查目标服务的权限设置")
	}
	if err := request.validate(); err != nil {
		return ElevationResult{}, err
	}
	if err := c.launch(executable, request); err != nil {
		if errors.Is(err, errElevationCancelled) {
			return ElevationResult{Cancelled: true}, nil
		}
		return ElevationResult{}, fmt.Errorf("管理员重启失败，当前面板已保留: %w", err)
	}
	c.quit()
	return ElevationResult{}, nil
}

func (a *App) GetPrivilegeStatus() PrivilegeStatus {
	status := PrivilegeStatus{Elevated: isProcessElevated()}
	if a.elevationResume != nil {
		status.ReturnView = a.elevationResume.ReturnView
		status.InstanceID = a.elevationResume.InstanceID
		status.ServiceAutoStart = a.elevationResume.ServiceAutoStart
	}
	return status
}
func (a *App) RestartAsAdministrator(view, instanceID string, autoStart *bool) (ElevationResult, error) {
	if err := a.ready(); err != nil {
		return ElevationResult{}, err
	}
	if !validRestartView(view) {
		return ElevationResult{}, fmt.Errorf("重启返回页面无效")
	}
	state := a.manager.Snapshot()
	if instanceID != "" {
		found := false
		for _, i := range state.Instances {
			if i.ID == instanceID {
				found = true
				break
			}
		}
		if !found {
			return ElevationResult{}, fmt.Errorf("实例已不存在，请刷新后重试")
		}
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return ElevationResult{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return ElevationResult{}, err
	}
	request := elevationRequest{Version: 1, ParentPID: os.Getpid(), Nonce: hex.EncodeToString(nonce), DataRoot: state.Root, ReturnView: view, InstanceID: instanceID, OriginalUserSID: state.RuntimeUserSID, SoftwareRendering: os.Getenv("PHPANEL_SOFTWARE_RENDERING"), WebviewProfile: os.Getenv("PHPANEL_WEBVIEW_PROFILE_DIR")}
	request.ServiceAutoStart = autoStart
	return a.elevation.restart(exe, request)
}
func (a *App) configureElevation() {
	a.elevation = &elevationController{launch: launchElevatedRestart, isElevated: isProcessElevated, quit: a.desktop.exit}
}

package main

import (
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	resume, err := parseElevationRequest(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	if resume != nil {
		if err := prepareElevatedStartup(*resume); err != nil {
			log.Fatal(err)
		}
		_ = os.Setenv("PHPANEL_SOFTWARE_RENDERING", resume.SoftwareRendering)
		_ = os.Setenv("PHPANEL_WEBVIEW_PROFILE_DIR", resume.WebviewProfile)
	}
	app := NewApp()
	app.elevationResume = resume
	exe, exeErr := os.Executable()
	if exeErr != nil {
		log.Fatal(exeErr)
	}
	root, rootErr := resolveDataRoot(exe, os.Getenv("PHPANEL_DATA_DIR"))
	if rootErr != nil {
		log.Fatal(rootErr)
	}
	explicitRoot, rootErr := parseDesktopLaunch(os.Args[1:])
	if rootErr != nil {
		log.Fatal(rootErr)
	}
	app.explicitDataRoot = explicitRoot
	if explicitRoot != "" {
		root = explicitRoot
	}
	if resume != nil {
		root = resume.DataRoot
	}
	desktopSettings := readDesktopPreferences(root)
	webviewProfile := resolveWebviewProfile(exe, root, os.Getenv("PHPANEL_WEBVIEW_PROFILE_DIR"))
	err = wails.Run(&options.App{
		Title: "PHPanel · 本地开发环境", Width: 1280, Height: 840,
		MinWidth: 1060, MinHeight: 680,
		BackgroundColour: &options.RGBA{R: 246, G: 247, B: 245, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup, OnShutdown: app.shutdown, OnBeforeClose: app.beforeClose,
		WindowStartState:   desktopStartState(desktopSettings, resume != nil),
		SingleInstanceLock: &options.SingleInstanceLock{UniqueId: desktopInstanceID(root), OnSecondInstanceLaunch: func(options.SecondInstanceData) { app.desktop.restore() }},
		Bind:               []interface{}{app},
		Windows:            &windows.Options{WebviewGpuIsDisabled: os.Getenv("PHPANEL_SOFTWARE_RENDERING") != "0", WebviewUserDataPath: webviewProfile},
	})
	if err != nil {
		log.Fatal(err)
	}
}

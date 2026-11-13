# Windows 发行包

在 Windows x64 项目目录执行 `scripts/package.ps1`。默认从源码重新构建，并在独立临时工程中编译，不覆盖 `build/bin` 内正在运行的面板、辅助程序或环境数据。

## 命令

同时生成安装程序、Portable 和 SHA-256 校验文件：

```powershell
.\scripts\package.ps1 -Version 0.1.0
```

只生成 Portable：

```powershell
.\scripts\package.ps1 -Version 0.1.0 -Target Portable
```

只生成安装程序，显式指定 NSIS 编译器：

```powershell
.\scripts\package.ps1 -Version 0.1.0 -Target Installer -NSISPath 'C:\Program Files (x86)\NSIS\makensis.exe'
```

复用已构建程序，不重新编译前端和主程序：

```powershell
.\scripts\package.ps1 -Version 0.1.0 -SkipBuild
```

此方式使用 `build/bin` 的三个 EXE，只复制程序文件；不会复制其中的数据库、实例、网站、下载缓存、状态文件或本机目录记录。`-SkipBuild` 不更改已有 EXE 的版本信息。

`-OutputDirectory` 默认 `dist`，支持绝对路径；`-BinariesDirectory` 可指定已构建的三个 EXE 所在目录。版本使用 `主版本.次版本.修订版本` 格式。

## 构建依赖

- Go 1.24+、Node.js 20+，以及项目已安装的前端依赖（首次在 `frontend` 运行 `npm.cmd ci`）。
- Wails CLI 2.11.0。脚本优先使用 PATH 中的 `wails`；没有时，可从已缓存的 Wails 模块编译临时 CLI。缓存不存在时，先执行 `go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0`。
- 安装程序需要 NSIS 3。优先使用 `-NSISPath`，然后检查 PATH、项目内 `.tools/nsis/Bin/makensis.exe` 和常见安装目录；缺失时明确失败，Portable 不需要 NSIS。

脚本不会自动下载或安装 NSIS，不会运行生成的安装包。临时工程结束后自动清理，Go 编译缓存位于 `.build/go-cache`，发行产物保存在 `dist`。

当前工作区已保留项目本地的 NSIS 3.04 编译器，默认命令可直接使用。它来自 Electron 构建工具发布的 `nsis-3.0.4.1` 工具包，仅作为本地构建依赖，不包含在发行包或 Git 仓库中。换电脑或新检出项目时需自行安装 NSIS 3，或通过 `-NSISPath` 指定编译器。

## 产物

应用图标使用 `build/windows/icon.ico`，主程序、任务栏、托盘以及安装和卸载程序共用这套图标。`build/appicon.png` 是 Wails 的高清图标源，`build/appicon.svg` 保留矢量原稿。更新图标后需要重新构建；`-SkipBuild` 会继续使用已有 EXE 中的图标。

```text
dist/
  PHPanel-0.1.0-windows-x64-setup.exe
  PHPanel-0.1.0-windows-x64-portable.zip
  PHPanel-0.1.0-windows-x64-SHA256SUMS.txt
```

Portable 包含三个 EXE、README、系统说明文档以及 `portable.flag`。解压后运行 PHPanel.exe，环境数据默认位于 EXE 旁；有便携标记时，WebView2 浏览器缓存也默认位于数据目录的 `state/webview2`。显式的 `PHPANEL_DATA_DIR` 和 `PHPANEL_WEBVIEW_PROFILE_DIR` 设置仍优先。

## 安装与卸载

安装程序默认安装到当前用户 `%LOCALAPPDATA%\Programs\PHPanel`，使用 HKCU 卸载注册和当前用户快捷方式，不要求整机管理员安装。系统服务管理仍使用面板内的 UAC 授权流程。

升级前检查目标目录中的程序是否仍在运行或不可写；需要用户退出面板，并停止使用该目录的系统服务和 PHP 托管程序。安装器不直接结束进程。

卸载前检查是否仍有使用该程序目录的 PHPanel 系统服务，存在时要求先在面板中停止并注销。卸载只删除明确列出的程序文件及快捷方式，保留环境、数据库、网站、缓存和说明文档。当前用户的安装目录 CLI PATH 条目，以及指向此 EXE 的登录启动项会清理，其它条目保留。

## WebView2 与国内网络

默认安装程序内含 Wails SDK 的 Microsoft WebView2 在线引导程序。已安装 Runtime 时跳过；缺失时调用引导程序下载并安装，失败会停止安装并提示。

网络受限或需要离线发行时，下载 Microsoft 官方 x64 Evergreen Standalone Installer，传给 `-WebView2InstallerPath`：

```powershell
.\scripts\package.ps1 -Version 0.1.0 -WebView2InstallerPath 'D:\Installers\MicrosoftEdgeWebView2RuntimeInstallerX64.exe'
```

Portable 依赖本机 WebView2 Runtime，不自动执行依赖安装程序。

## 验证范围

生成过程验证构建命令与编译器返回码，输出版本对应的 SHA-256 清单。单元测试覆盖便携缓存目录、卸载保护与 PATH 保留。`tests/release/package.ps1` 验证发布文件、运行数据隔离，以及重复打包后的校验清单刷新：

```powershell
.\tests\release\package.ps1
```

发行验证可以只读解压 ZIP/NSIS 检查文件内容及版本；完整的安装、升级、卸载和 WebView2 依赖安装需在干净的 Windows 测试环境中验证。

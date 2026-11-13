# 测试

正式单元测试和集成测试源码集中保存在本目录。Go 的同包单元测试需要访问未导出的实现，因此运行脚本使用 Go 官方 `-overlay` 功能，把这里的源码映射到对应包的虚拟测试文件位置；不会复制或修改产品源码。

- `app/`：桌面程序的数据位置及管理员重启测试。
- `panel/`：服务管理、下载与校验、配置、扩展、网站、端口和命令行工具测试。
- `go.mod` 仅将测试源目录隔离出父模块的默认包遍历，实际测试仍编译父项目模块。

运行全部单元测试与静态检查：

```powershell
.\tests\run.ps1 -Vet
```

按名称筛选：

```powershell
.\tests\run.ps1 -Filter 'TestManagedUserPath|TestInstancePort'
```

可选真实组件集成测试使用隔离目录，禁止使用用户实例做删除/改端口测试。设置环境变量指向相应可信安装包或本地测试文件后运行：

```powershell
$env:PHPANEL_NATIVE_PACKAGES_DIR = 'D:\workspace\PHPanel\build\bin\packages'
$env:PHPANEL_SMOKE_ARCHIVES_DIR = 'D:\test-artifacts'
$env:PHPANEL_COMPOSER_FIXTURE = 'D:\test-artifacts\composer.phar'
$env:PHPANEL_CLI_HELPER = 'D:\workspace\PHPanel\build\bin\phpanel-cli.exe'
.\tests\run.ps1 -Integration -Filter 'TestNativeComposerInstallAndCLISelection|TestNativePHPPortChangeWithNginx' -Timeout '3m'
```

测试文件缺失时相应测试会跳过。仓库不保留历史测试下载、解压目录、测量快照或临时验证程序。

管理员重启测试验证 Windows `runas` 参数、握手事件、等待旧进程退出、授权取消与失败时保留旧面板、重复请求限制和数据目录探测。测试不会实际调用 UAC、关闭当前应用或创建管理员进程。

系统服务托管程序的真实组件测试使用一个外部 JSON 安装包清单，数组必须包含 Nginx、Redis、MySQL 三个对象，每个对象包含 `Component`、`Version`、`Archive`（ZIP 绝对路径）和 `SHA256`（可信文件摘要）。设置 `PHPANEL_SERVICE_FIXTURES` 指向此清单，再运行 `tests/run.ps1 -Integration -Filter '^TestNativeSystemServiceHostLifecycle$' -Timeout '5m'`。测试会验证实际 SCM 服务处理器的启动与停止行为，并确认组件在面板退出后仍运行；不会向 Windows 注册真实系统服务，也不需要管理员权限。

PHP 联动测试使用相同格式的安装包清单，但依次包含 Nginx、PHP 第一版本、PHP 第二版本。设置 `PHPANEL_LINK_FIXTURES` 指向该清单，`PHPANEL_LINK_HELPER` 指向已编译的 `phpanel-service.exe`，运行 `tests/run.ps1 -Integration -Filter '^TestNativeNginx(PHPLinkAndReuse|ServicePHPLinkSurvivesGUIAndNginxStop)$' -Timeout '4m'`。测试使用隔离目录，验证多版本网站 HTTP 200、原进程复用、后台 PHP 扩展重启、面板退出及停止 Nginx 后保留 PHP、按启动归属一起停止。

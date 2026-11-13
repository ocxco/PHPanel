# PHPanel

Windows 本地开发环境管理工具，使用 Wails、React、TypeScript 和 Go。完整发行包包含 build/bin/PHPanel.exe、命令入口辅助程序 phpanel-cli.exe 和系统服务托管程序 phpanel-service.exe。

## 功能

- PHP、Nginx、MySQL 和社区 Windows Redis 多版本安装，运行时自动发现版本；支持代理、自定义镜像、缓存、续传及 ZIP 离线导入。
- 在线制品经过可信摘要或受支持的官方签名验证后发布，安装失败不覆盖已有程序和实例数据。
- 独立服务实例、配置、日志和数据。PHP/MySQL/Redis 每个已安装版本同时只能运行一个实例；Nginx 所有版本合计只能运行一个实例。
- PHP、Redis、MySQL 等实例停止后可修改端口，保存后由用户手动启动。PHP 端口变更同步修改引用它的站点 FastCGI 地址，并检查及应用 Nginx 配置。
- 网站创建、编辑、删除，支持通用 PHP、Laravel、ThinkPHP、WordPress 和静态站点。站点配置独立存放于 sites。
- 内置 CodeMirror 配置编辑器及按实例管理的 PHP 扩展。Redis、Xdebug、APCu 支持在线安装、镜像和本地导入，DLL 直接放在对应 PHP 包的 extensions 下。
- Composer 官方稳定版下载、SHA-256 验证和实际版本检查，可一键添加命令入口到当前用户 PATH。
- 环境管理提供命令行 PHP 版本选择、设置及取消环境变量的唯一入口。命令入口每次执行时读取当前选择，继承实例的 php.ini 和扩展扫描目录；取消 PHP 环境变量时保留可用的 Composer 入口。
- Windows Job Object 管理应用启动的服务，应用退出时停止其服务；数据目录使用独占锁。
- Nginx、Redis、MySQL 可以注册为 Windows 系统服务，独立于面板运行，支持开机自启动、切换启动方式及停止后注销。普通用户可查看状态，服务管理操作需要管理员权限。
- 管理员权限不足时可在应用内确认重启，再通过 Windows UAC 授权；取消时保留旧面板，成功后返回原页面和实例设置。
- 系统设置支持当前用户登录时打开面板、启动时最小化，以及关闭程序或最小化到托盘。托盘提供打开和退出入口。
- 启动 Nginx 默认联动启动站点所需的 PHP，复用已运行的实例；默认停止 Nginx 时保留 PHP，可一键停止本次联动的新实例。Nginx 系统服务联动或复用的 PHP 在面板退出后继续运行。

## 使用

1. 打开 PHPanel.exe，在环境管理中安装组件并创建实例。
2. 在服务实例中启动、停止，或停止后修改端口。
3. 在网站管理中连接 PHP 和 Nginx，设置网站目录及端口；使用 <名称>.localhost:<端口> 访问。
4. 在 PHP 卡片的扩展管理中调整加载配置，保存后明确启动或重启实例应用。
5. 在环境管理的“工具与命令行”中选择 PHP 版本，点击“设置环境变量”添加或切换，点击“取消环境变量”移除本软件的 PHP 命令入口；同一页面支持 Composer 安装与 PATH 设置。
6. 需要开机启动时，停止目标实例，在 Nginx 的“启动设置”或其它组件的“系统服务”中勾选“开机自动启动”并注册。权限不足时可确认 **授权并重启**，在 Windows UAC 中授权后返回继续操作。注册后可点击实例的“启动”按钮立即运行。

PATH 修改只针对当前用户，不要求管理员。重新打开终端后使用 php -v、composer --version。如果系统级 PATH 中已有 PHP/Composer，系统路径可能优先，软件会明确提示；不会自动改动系统 PATH。完整命令入口位于数据根目录的 cli。

默认数据根目录是 EXE 所在目录，可用 PHPANEL_DATA_DIR 指定其它位置。详见 [本地存储](docs/local-storage.md)、[网站管理](docs/site-management.md)、[实例删除](docs/instance-deletion.md)、[目录与主题](docs/readable-directories-and-theme.md)、[命令行工具](docs/command-line-tools.md) 和 [Windows 系统服务](docs/windows-services.md)。

面板启动与关闭行为见 [系统设置](docs/system-settings.md)。

## 下载与网络

代理和来源设置位于偏好设置。软件尚未提供自建国内 CDN，可使用自定义镜像、代理和本地导入。镜像文件必须符合固定制品的摘要或签名。

Composer 从 getcomposer.org/versions 发现稳定版，读取该版本 .sha256sum 后验证 PHAR。下载使用应用代理，不自动更改用户项目的 Packagist 仓库设置。

运行时目录和额外 JSON 源格式见 [自动目录](docs/automatic-catalogs.md)。普通版本发布无需重新编译客户端，上游页面结构、安装协议变化可能需要更新适配器。

## 开发、构建与测试

需要 Go 1.24+、Node.js 20+、Windows 10/11 x64 和 WebView2 Runtime。PHP/MySQL 可能需要相应 VC++ 运行库。

首次在 frontend 目录执行 npm.cmd ci，然后运行 scripts/build.ps1。统一脚本会编译前端、命令入口、系统服务托管程序和桌面程序。

生成发行包使用 `scripts/package.ps1 -Version 0.1.0`，同时输出当前用户安装程序、Portable ZIP 和 SHA-256 清单。构建采用独立临时工程，保留当前 `build/bin` 及环境数据。只生成便携包可加 `-Target Portable`；安装程序需要 NSIS 3。详见 [打包说明](docs/packaging.md)。

正式测试源码集中在 tests；Go 包内测试通过官方 overlay 映射到对应包，不复制产品代码。运行 tests/run.ps1 -Vet，真实组件集成测试的设置方法见 [测试说明](tests/README.md)。

历史探测程序、计划文档、测量快照、旧构建及测试下载不属于发行内容，不保存在仓库。

## 限制

Redis 使用经过校验的社区 Windows 发行版。当前构建针对 Windows x64，部分上游组件不支持中文数据路径。PHP 以应用管理的普通进程运行；Nginx、Redis、MySQL 默认同样由面板管理，可以按需注册为系统服务。

默认使用软件渲染减少面板 GPU 开销，可用 PHPANEL_SOFTWARE_RENDERING=0 恢复硬件加速。WebView2 内存包含多个进程，不能只用主进程数字代表整个应用。

# PHP 与 Composer 命令入口

环境管理的“工具与命令行”是命令行 PHP 环境变量管理的唯一入口，提供 PHP 版本下拉框、**设置环境变量**、**取消环境变量**。服务实例的 PHP 卡片保留服务、配置和扩展操作。

## 当前用户 PATH

软件生成 `cli/php.exe` 与 `cli/composer.exe`，把 `cli` 目录放到当前用户 PATH 的前端，并去重本目录已有条目。其它软件的路径及系统 PATH 保留。修改后会通知 Windows 环境变量变化，但已打开终端通常需要重新打开。

PHP 切换更新的是面板中保存的实例 ID；两个命令入口每次执行时读取该选择，因此无需反复修改 PATH。命令使用对应 PHP 包的 `php.exe`，通过 `-c` 指定实例 `php.ini`，同时以 `PHPRC` 传递同一份 `php.ini`、以进程专属 `PHP_INI_SCAN_DIR` 指定实例 `conf.d`，并保留终端当前工作目录、参数、输入输出及退出码。`PHPRC` 与 `PHP_INI_SCAN_DIR` 会被 PHP 派生的子进程（如 `queue:work`、`artisan serve` 的 `-S` 服务器、`proc_open`）继承，使这些子进程在缺少 `-c` 时仍加载实例主配置，而不是回落到编译期默认的 `C:\php\ext`。

选择版本后点击“设置环境变量”即可添加或切换；选中的版本已经生效时，该按钮置灰。“取消环境变量”移除 PHPanel 生成的全局 PHP 命令入口，但保留已选的 PHP 配置，供 Composer 继续使用。如果 Composer 命令已可用，保留其 PATH 与入口；否则移除本软件的 CLI 目录 PATH 项。其它 PHP 安装、PATH 条目、PHP 服务和扩展不随取消操作修改。取消后可以重新设置。

设置及取消会校验命令入口归属，并在 PATH 或元数据写入失败时回滚。入口正在使用或被其它程序改写时会明确提示，保留原文件。

系统级 PATH 位于用户 PATH 之前。如果系统路径已有 PHP/Composer，界面会提示潜在优先级冲突。用户可以直接执行完整入口路径，或自行调整系统路径；软件不会尝试管理员权限修改。删除当前命令行 PHP 实例后，需要重新选择一个实例。

## Composer

从官方版本目录选择非 EOL、非 LTS 的最新稳定版，读取同版本官方 `.sha256sum`。下载 PHAR 后验证 SHA-256，并使用选定的 PHP 实际运行版本检查，再安装到 `tools/composer/composer.phar`。

下载、暂停、继续及重试位于下载中心，下载使用应用的代理设置。安装不会自动修改 PATH；安装完成后点击“添加 Composer 到 PATH”。安装及配置记录保存在数据根目录的 `state/panel.json`。

命令入口执行 Composer 前检查 PHAR 摘要，文件缺失或被修改时拒绝运行并提示重新安装。Composer 使用当前选择的 PHP，PHP 实例无需启动为 FastCGI 服务。

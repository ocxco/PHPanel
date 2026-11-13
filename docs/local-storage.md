# 本地文件、缓存与服务位置

设置中的数据目录来自实际后端工作目录，不是固定显示的示例路径。正常启动默认使用 `PHPanel.exe` 所在目录；设置 `PHPANEL_DATA_DIR` 时使用该启动环境变量指定的目录。

Windows 打包进程可能重定向 AppData，后端使用文件句柄解析物理路径，设置和打开入口均返回资源管理器可见的位置。默认便携目录为 EXE 所在目录。

程序目录旁的 `phpanel-data-location.json` 仅作为旧环境复制的来源记录，不覆盖新的便携默认目录。首次升级时若 EXE 旁没有环境记录，可在旧目录未被使用时复制数据，原目录保留；已有目标环境不会被覆盖。显式 `PHPANEL_DATA_DIR` 优先级最高。更正已有配置中的根路径时保存 `.before-path-fix` 备份，不搬动数据库文件。

| 根目录下的位置 | 内容 |
| --- | --- |
| `downloads/cache` | 完整安装包，按 SHA-256 加 `.zip`、`.dll`、`.phar` 缓存 |
| `downloads/partial` | 尚未完成下载的续传文件 |
| `packages` | 已解压的 PHP、Nginx、MySQL、Redis 程序，按制品分开 |
| `instances/instance-<组件><版本>/config` | 每个实例自己的配置文件 |
| `instances/instance-<组件><版本>/data` | MySQL/Redis 等实例的数据 |
| `instances/instance-<组件><版本>/logs` | 启动输出、错误与检查日志 |
| `packages/package-php<版本>/extensions` | 在线扩展 DLL 直接存放的位置 |
| `tools/composer` | Composer PHAR |
| `cli` | 当前 PHP、Composer 的命令入口 |
| `sites` | 共享的站点 .conf 与自动创建的欢迎网站子目录；已有项目可位于其它目录 |
| `state` | 面板元数据、目录缓存和公开签名证书缓存 |

界面入口：
- 偏好设置：打开数据目录、复制路径、打开程序目录/实例目录/下载缓存。
- 环境管理的已安装版本：打开该版本的程序目录。
- 服务实例：提供实例、数据和日志文件夹入口；“编辑配置”在内置 CodeMirror 6 中打开配置。Nginx 主配置通过 include 加载 EXE 旁 sites/*.conf；这些站点配置不随 Nginx 实例删除。
- 下载中心：完成后“定位安装包”，在资源管理器中选中对应 ZIP。

入口先通过后端解析当前记录并检查位置存在，再使用 Windows 资源管理器打开。安装包按摘要命名以便去重和校验，不依靠肉眼从文件名判断版本。

默认实例以普通用户进程运行，可在任务管理器中看到 `php-cgi.exe`、`nginx.exe`、`mysqld.exe` 或 `redis-server.exe`。Nginx、Redis、MySQL 可以在实例卡片的“系统服务”入口注册；注册后会出现在 `services.msc`，由独立的 `phpanel-service.exe` 托管，面板退出后仍可运行。详见 [Windows 系统服务](windows-services.md)。

如果逐级查找困难，可以把完整数据目录粘贴到资源管理器地址栏，或使用面板的打开按钮。目录不存在或记录异常时会显示实际路径和错误，不会静默打开其它位置。

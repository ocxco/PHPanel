package panel

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type managedProcess struct {
	runtimeServer runtimeServer
	lease         *runtimeLease
	cmd           *exec.Cmd
	guard         *processGuard
	done          chan struct{}
	log           *os.File
}

// instanceFromPackage synthesizes an Instance view of a PHP package. After the
// PHP package==runtime merge there are no PHP Instance rows; this lets the large
// body of runtime/extension code (written against Instance) keep working. The
// synthesized Instance.Path is the package directory and its runtime fields are
// copied from the package's embedded PHPRuntime.
func instanceFromPackage(p InstalledPackage) Instance {
	return Instance{
		ID:               p.ID,
		PackageID:        p.ID,
		Name:             p.Version,
		Component:        p.Component,
		Version:          p.Version,
		Port:             p.Port,
		Path:             p.Path,
		Status:           p.Status,
		PID:              p.PID,
		Error:            p.Error,
		LinkedNginxID:    p.LinkedNginxID,
		LinkedRun:        p.LinkedRun,
		ServiceName:      p.ServiceName,
		ServiceAutoStart: p.ServiceAutoStart,
		NginxAutoPHP:     p.NginxAutoPHP,
		NginxStopPHP:     p.NginxStopPHP,
		DirectoryName:    p.DirectoryName,
	}
}

func (m *Manager) lookupInstance(key string) (Instance, InstalledPackage, error) {
	state := m.Snapshot()
	// Merged PHP: a package id is the runtime id (no PHP Instance rows).
	for _, p := range state.Packages {
		if p.ID == key && p.Component == "php" {
			return instanceFromPackage(p), p, nil
		}
	}
	// Legacy instances: nginx / mysql / redis (and pre-migration PHP).
	for _, i := range state.Instances {
		if i.ID == key {
			for _, p := range state.Packages {
				if p.ID == i.PackageID {
					return i, p, nil
				}
			}
		}
	}
	return Instance{}, InstalledPackage{}, fmt.Errorf("实例或程序版本不存在")
}
func (m *Manager) CreateInstance(packageID, name string, port int) (Instance, error) {
	if !validLabel(name) {
		return Instance{}, fmt.Errorf("名称不能为空，端口须在 1–65535 之间")
	}
	m.mu.Lock()
	if m.closing || m.operations[packageID] {
		m.mu.Unlock()
		return Instance{}, fmt.Errorf("此版本正在变更")
	}
	var p InstalledPackage
	for _, item := range m.state.Packages {
		if item.ID == packageID {
			p = item
		}
	}
	if p.ID == "" {
		m.mu.Unlock()
		return Instance{}, fmt.Errorf("请先安装此版本")
	}
	if p.Component == "php" {
		m.mu.Unlock()
		return Instance{}, fmt.Errorf("PHP 包即为运行时，无需创建实例")
	}
	if p.Component == "nginx" {
		port = 0
	} else if port < 1 || port > 65535 {
		m.mu.Unlock()
		return Instance{}, fmt.Errorf("端口须在 1–65535 之间")
	}
	if p.Component != "php" && strings.IndexFunc(nativePath(m.root), func(r rune) bool { return r > 127 }) >= 0 {
		m.mu.Unlock()
		return Instance{}, fmt.Errorf("该 Windows 发行包的路径兼容性有限，请将 PHPANEL_DATA_DIR 设置为英文目录（可以包含空格），例如 D:\\PHPanelData")
	}
	m.operations[packageID] = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer func() { m.mu.Lock(); delete(m.operations, packageID); m.mu.Unlock() }()
	if p.Component == "nginx" {
		m.siteMu.Lock()
		defer m.siteMu.Unlock()
	}
	i := Instance{ID: id("instance"), PackageID: p.ID, Name: name, Component: p.Component, Version: p.Version, Port: port, Status: "stopped"}
	instancePath, releaseDirectory, err := m.reserveDirectory(filepath.Join(m.root, "instances"), readableDirectoryName("instance", i.Component, i.Version))
	if err != nil {
		return Instance{}, err
	}
	defer releaseDirectory()
	i.Path = instancePath
	i.DirectoryName = filepath.Base(i.Path)
	if err := os.Mkdir(i.Path, 0700); err != nil {
		return Instance{}, err
	}
	for _, dir := range []string{"config", "data", "logs", "temp"} {
		if err := os.MkdirAll(filepath.Join(i.Path, dir), 0700); err != nil {
			_ = os.RemoveAll(i.Path)
			return Instance{}, err
		}
	}
	if p.Component == "mysql" {
		password := strings.TrimPrefix(id("password"), "password-") + strings.TrimPrefix(id("password"), "password-")
		if err := os.WriteFile(filepath.Join(i.Path, "config", "credentials.txt"), []byte("用户名: root\n密码: "+password+"\n"), 0600); err != nil {
			return Instance{}, err
		}
		sql := "ALTER USER 'root'@'localhost' IDENTIFIED BY '" + password + "';\n"
		if err := os.WriteFile(filepath.Join(i.Path, "config", "init.sql"), []byte(sql), 0600); err != nil {
			return Instance{}, err
		}
	}
	if err := m.writeInstanceConfig(i, p); err != nil {
		_ = os.RemoveAll(i.Path)
		return Instance{}, err
	}
	if err := m.change(func(s *State) error {
		for _, old := range s.Instances {
			if port != 0 && old.Port == port {
				return fmt.Errorf("端口已被实例 %s 使用", old.Name)
			}
		}
		for _, site := range s.Sites {
			if port != 0 && site.Port == port {
				return fmt.Errorf("端口已被网站 %s 使用", site.Name)
			}
		}
		s.Instances = append(s.Instances, i)
		return nil
	}); err != nil {
		_ = os.RemoveAll(i.Path)
		return Instance{}, err
	}
	return i, nil
}
func normalized(path string) string { return strings.ReplaceAll(nativePath(path), "\\", "/") }
func quoted(path string) string     { return "\"" + normalized(path) + "\"" }
func (m *Manager) writeInstanceConfig(i Instance, p InstalledPackage) error {
	var file, body string
	switch i.Component {
	case "php":
		file = "php.ini"
		body = "[PHP]\nengine=On\nshort_open_tag=Off\nexpose_php=Off\ndisplay_errors=On\nlog_errors=On\nerror_log=" + quoted(filepath.Join(i.Path, "logs", "php-error.log")) + "\nextension_dir=" + quoted(filepath.Join(filepath.Dir(p.Executable), "ext")) + "\ncgi.fix_pathinfo=0\ndate.timezone=Asia/Shanghai\nmemory_limit=256M\nupload_tmp_dir=" + quoted(filepath.Join(i.Path, "temp")) + "\nsession.save_path=" + quoted(filepath.Join(i.Path, "temp")) + "\nextension=mysqli\nextension=pdo_mysql\nextension=mbstring\nextension=openssl\n"
	case "mysql":
		file = "my.ini"
		body = "[mysqld]\nbasedir=" + quoted(filepath.Dir(filepath.Dir(p.Executable))) + "\ndatadir=" + quoted(filepath.Join(i.Path, "data")) + "\nport=" + strconv.Itoa(i.Port) + "\nbind-address=127.0.0.1\nloose-mysqlx=0\nlog-error=" + quoted(filepath.Join(i.Path, "logs", "mysql-error.log")) + "\n"
	case "redis":
		file = "redis.conf"
		body = redisConfig(i.Port)
	case "nginx":
		return m.writeNginxConfig(i)
	default:
		return fmt.Errorf("不支持此组件")
	}
	return os.WriteFile(filepath.Join(i.Path, "config", file), []byte(body), 0600)
}
func (m *Manager) setInstance(key, status, detail string, pid int) error {
	return m.change(func(s *State) error {
		for n := range s.Packages {
			if s.Packages[n].ID == key && s.Packages[n].Component == "php" {
				s.Packages[n].Status = status
				s.Packages[n].Error = detail
				s.Packages[n].PID = pid
				return nil
			}
		}
		for n := range s.Instances {
			if s.Instances[n].ID == key {
				s.Instances[n].Status = status
				s.Instances[n].Error = detail
				s.Instances[n].PID = pid
				return nil
			}
		}
		return fmt.Errorf("实例或运行时不存在")
	})
}
func checkPort(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("端口 %d 被占用或无法监听", port)
	}
	return listener.Close()
}
func (m *Manager) StartInstance(key string) error {
	m.refreshSystemServices()
	finish, err := m.trackServiceAction()
	if err != nil {
		return err
	}
	defer finish()
	i, _, err := m.lookupInstance(key)
	if err != nil {
		return err
	}
	if i.ServiceName != "" && !m.serviceHost {
		return m.controlSystemService(key, true)
	}
	if i.Component == "php" {
		m.siteMu.Lock()
		err = m.applyPreparedExtensionsLocked(i.PackageID)
		m.siteMu.Unlock()
		if err != nil {
			return err
		}
	}
	if i.Component == "nginx" {
		return m.startNginxLinked(key)
	}
	return m.startInstance(key, false)
}
func (m *Manager) startInstance(key string, restarting bool) error {
	if restarting {
		m.mu.Lock()
		owner, remote := m.remoteRestartOwners[key]
		delete(m.remoteRestartOwners, key)
		m.mu.Unlock()
		if remote {
			i, _, err := m.lookupInstance(key)
			if err != nil {
				return err
			}
			status, err := m.phpBackend().Start(i, owner.Owner, owner.Run)
			if err != nil {
				return err
			}
			if owner.Protected {
				if err := m.phpBackend().Protect(i, true); err != nil {
					return err
				}
			}
			return m.syncRemotePHP(i, status)
		}
	}
	if !m.serviceHost && m.detachedPHP {
		i, _, err := m.lookupInstance(key)
		if err != nil {
			return err
		}
		if i.Component == "php" {
			return m.startDetachedPHP(i, restarting, "", "")
		}
	}
	return m.startInstanceWithLease(key, restarting, nil)
}
func (m *Manager) startInstanceWithLease(key string, restarting bool, supplied *runtimeLease) error {
	i, p, err := m.lookupInstance(key)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.closing || m.operations[key] || (!restarting && m.operations["php-restart:"+key]) || m.processes[key] != nil {
		m.mu.Unlock()
		return fmt.Errorf("实例正在运行或变更")
	}
	if err := m.canStartLocked(i); err != nil {
		m.mu.Unlock()
		return err
	}
	startKey := instanceStartKey(i)
	m.operations[startKey] = true
	m.operations[key] = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer func() { m.mu.Lock(); delete(m.operations, key); delete(m.operations, startKey); m.mu.Unlock() }()
	fail := func(err error) error { _ = m.setInstance(key, "error", err.Error(), 0); return err }
	lease := supplied
	if lease == nil {
		lease, err = acquireRuntimeLease(m.root, i)
	}
	if err != nil {
		return fail(err)
	}
	transferred := false
	defer func() {
		if !transferred {
			lease.release()
		}
	}()
	if i.Component != "nginx" {
		if err := checkPort(i.Port); err != nil {
			return fail(err)
		}
	}
	if err := m.setInstance(key, "starting", "", 0); err != nil {
		return err
	}
	// Validate existing configuration, keeping user edits when starting again.
	if i.Component == "nginx" {
		_ = os.Remove(filepath.Join(i.Path, "nginx.pid"))
	}
	if err := func() error {
		if i.Component == "nginx" {
			m.siteMu.Lock()
			defer m.siteMu.Unlock()
		}
		if m.serviceHost {
			return nil
		}
		if err := m.updateConfigRoot(i); err != nil {
			return err
		}
		if i.Component == "nginx" {
			return m.writeNginxConfig(i)
		}
		return nil
	}(); err != nil {
		return fail(err)
	}
	cfg := filepath.Join(i.Path, "config")
	if i.Component == "php" {
		cfg = phpConfigDir(i)
	}
	args := []string{}
	switch i.Component {
	case "php":
		args = []string{"-c", nativePath(phpINIPath(i)), "-b", fmt.Sprintf("127.0.0.1:%d", i.Port)}
	case "redis":
		args = []string{nativePath(filepath.Join(cfg, "redis.conf"))}
		if _, err := os.Stat(filepath.Join(filepath.Dir(p.Executable), "cygwin1.dll")); err == nil {
			args[0] = posixWindowsPath(args[0], true)
		} else if _, err := os.Stat(filepath.Join(filepath.Dir(p.Executable), "msys-2.0.dll")); err == nil {
			args[0] = posixWindowsPath(args[0], false)
		}
	case "nginx":
		args = []string{"-p", normalized(i.Path) + "/", "-c", normalized(filepath.Join(cfg, "nginx.conf"))}
		testArgs := append(append([]string{}, args...), "-t")
		if err := runLogged(p.Executable, testArgs, i.Path, filepath.Join(i.Path, "logs", "config-check.log"), 20*time.Second); err != nil {
			return fail(fmt.Errorf("Nginx 配置检查失败，请查看日志: %w", err))
		}
	case "mysql":
		args = []string{"--defaults-file=" + nativePath(filepath.Join(cfg, "my.ini")), "--console"}
		if _, err := os.Stat(filepath.Join(i.Path, "data", "mysql")); os.IsNotExist(err) {
			if err := runLogged(p.Executable, []string{"--defaults-file=" + nativePath(filepath.Join(cfg, "my.ini")), "--initialize-insecure"}, i.Path, filepath.Join(i.Path, "logs", "initialize.log"), 2*time.Minute); err != nil {
				return fail(fmt.Errorf("MySQL 数据目录初始化失败，请查看日志: %w", err))
			}
		}
		initSQL := filepath.Join(cfg, "init.sql")
		if _, err := os.Stat(initSQL); err == nil {
			args = append(args, "--init-file="+normalized(initSQL))
		}
	}
	logfile, err := os.OpenFile(filepath.Join(i.Path, "logs", "console.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fail(err)
	}
	cmd := exec.Command(p.Executable, args...)
	cmd.Dir = nativePath(i.Path)
	if i.Component == "php" {
		ini := phpINIPath(i)
		scan := phpConfDPath(i)
		cmd.Env = phpEnvironment(ini, scan, phpLibraryDirs(ini, scan)...)
	}
	cmd.Stdout = logfile
	cmd.Stderr = logfile
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		logfile.Close()
		return fail(fmt.Errorf("启动失败（检查 VC++ 运行库和日志）: %w", err))
	}
	guard, err := guardProcess(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		logfile.Close()
		return fail(err)
	}
	proc := &managedProcess{cmd: cmd, guard: guard, done: make(chan struct{}), log: logfile, lease: lease}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		_ = guard.kill()
		_ = cmd.Wait()
		guard.close()
		logfile.Close()
		return fail(fmt.Errorf("应用正在退出"))
	}
	m.processes[key] = proc
	if i.Component == "php" && m.phpHostOwner == "" {
		for n := range m.state.Instances {
			if m.state.Instances[n].ID == key {
				m.state.Instances[n].LinkedNginxID = ""
				m.state.Instances[n].LinkedRun = ""
			}
		}
	}
	if i.Component == "php" && m.phpHostOwner != "" {
		for n := range m.state.Instances {
			if m.state.Instances[n].ID == key {
				m.state.Instances[n].LinkedNginxID, m.state.Instances[n].LinkedRun = m.phpHostOwner, m.phpHostRun
			}
		}
	}
	transferred = true
	m.mu.Unlock()
	if i.Component == "php" {
		proc.runtimeServer, err = servePHPRuntime(m, i, proc)
	} else if i.Component == "nginx" && m.serviceHost {
		proc.runtimeServer, err = serveNginxRuntime(m, i, proc)
	}
	if err != nil {
		m.mu.Lock()
		delete(m.processes, key)
		m.mu.Unlock()
		_ = guard.kill()
		_ = cmd.Wait()
		guard.close()
		logfile.Close()
		lease.release()
		return fail(err)
	}
	go func() {
		waitErr := cmd.Wait()
		logfile.Close()
		guard.close()
		lease.release()
		if proc.runtimeServer != nil {
			proc.runtimeServer.Close()
		}
		m.mu.Lock()
		owned := m.processes[key] == proc
		if owned {
			delete(m.processes, key)
		}
		m.mu.Unlock()
		if owned {
			detail := ""
			status := "stopped"
			if waitErr != nil {
				status = "error"
				detail = "服务退出: " + waitErr.Error() + "；请查看日志"
			}
			_ = m.setInstance(key, status, detail, 0)
		}
		close(proc.done)
	}()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-proc.done:
			return fmt.Errorf("服务启动后退出，请查看实例日志")
		default:
		}
		ready := false
		if i.Component == "nginx" {
			pid, readErr := os.ReadFile(filepath.Join(i.Path, "nginx.pid"))
			ready = readErr == nil && strings.TrimSpace(string(pid)) == strconv.Itoa(cmd.Process.Pid)
			// The master writes its PID before workers finish binding sockets.
			for _, site := range m.Snapshot().Sites {
				conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", site.Port), 300*time.Millisecond)
				if dialErr != nil {
					ready = false
					break
				}
				conn.Close()
			}
		} else {
			conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", i.Port), 300*time.Millisecond)
			if dialErr == nil {
				conn.Close()
				ready = true
			}
		}
		if ready {
			select {
			case <-proc.done:
				return fmt.Errorf("服务启动后退出，请查看实例日志")
			case <-time.After(150 * time.Millisecond):
			}
			if i.Component == "mysql" {
				_ = os.Remove(filepath.Join(cfg, "init.sql"))
			}
			return m.setInstance(key, "running", "", cmd.Process.Pid)
		}
		time.Sleep(150 * time.Millisecond)
	}
	_ = m.stopInstance(key, true)
	return fail(fmt.Errorf("服务未在 25 秒内监听端口，请查看日志"))
}
func posixWindowsPath(path string, cygwin bool) string {
	path = strings.ReplaceAll(path, "\\", "/")
	if len(path) > 2 && path[1] == ':' {
		drive := strings.ToLower(path[:1])
		if cygwin {
			return "/cygdrive/" + drive + path[2:]
		}
		return "/" + drive + path[2:]
	}
	return path
}
func runLogged(exe string, args []string, dir, path string, timeout time.Duration) error {
	log, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = nativePath(dir)
	cmd.Stdout = log
	cmd.Stderr = log
	hideWindow(cmd)
	return cmd.Run()
}
func (m *Manager) updateConfigRoot(i Instance) error {
	names := map[string]string{"php": "php.ini", "mysql": "my.ini", "redis": "redis.conf", "nginx": "nginx.conf"}
	if err := m.updateConfigFileRoot(i, names[i.Component]); err != nil {
		return err
	}
	if i.Component == "nginx" {
		for _, site := range m.Snapshot().Sites {
			if site.NginxID == i.ID {
				if err := m.updateConfigFileRoot(i, filepath.FromSlash(siteConfigName(site))); err != nil {
					return err
				}
			}
		}
	}
	if err := m.fixManagedExtensionPaths(i); err != nil {
		return err
	}
	return nil
}
func (m *Manager) updateConfigFileRoot(i Instance, name string) error {
	if m.previousRoot == "" || m.previousRoot == m.root {
		return nil
	}
	if name == "" {
		return nil
	}
	path := filepath.Join(i.Path, "config", name)
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	updated := strings.ReplaceAll(string(body), strings.ReplaceAll(m.previousRoot, "\\", "/"), strings.ReplaceAll(m.root, "\\", "/"))
	updated = strings.ReplaceAll(updated, m.previousRoot, m.root)
	if updated == string(body) {
		return nil
	}
	backup := path + ".before-path-fix"
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		if err := os.WriteFile(backup, body, 0600); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(updated), 0600)
}
func (m *Manager) StopInstance(key string) error {
	m.refreshSystemServices()
	finish, err := m.trackServiceAction()
	if err != nil {
		return err
	}
	defer finish()
	instance, _, lookupErr := m.lookupInstance(key)
	if lookupErr != nil {
		return lookupErr
	}
	var stopErr error
	if instance.ServiceName != "" && !m.serviceHost {
		stopErr = m.controlSystemService(key, false)
	} else {
		stopErr = m.stopInstance(key, false)
	}
	if stopErr != nil {
		return stopErr
	}
	if instance.Component == "nginx" && instance.NginxStopPHP {
		return m.stopLinkedPHP(key)
	}
	i, _, err := m.lookupInstance(key)
	if err != nil {
		return err
	}
	if i.Component == "php" {
		m.siteMu.Lock()
		defer m.siteMu.Unlock()
		return m.applyPreparedExtensionsLocked(i.PackageID)
	}
	return nil
}
func (m *Manager) trackServiceAction() (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil, fmt.Errorf("应用正在退出")
	}
	m.wg.Add(1)
	return m.wg.Done, nil
}
func (m *Manager) stopInstance(key string, internal bool) error {
	i, p, err := m.lookupInstance(key)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if !internal && (m.operations[key] || m.operations["php-restart:"+key]) {
		m.mu.Unlock()
		return fmt.Errorf("实例正在变更，请稍后重试")
	}
	proc := m.processes[key]
	if proc != nil {
		delete(m.processes, key)
	}
	m.mu.Unlock()
	if proc == nil {
		if i.Component == "php" && !m.serviceHost {
			remote, queryErr := m.phpBackend().Query(i)
			if queryErr != nil {
				return queryErr
			}
			if remote.Exists {
				if internal && remote.Persistent {
					m.mu.Lock()
					if m.remoteRestartOwners == nil {
						m.remoteRestartOwners = map[string]phpRuntimeStatus{}
					}
					m.remoteRestartOwners[key] = remote
					m.mu.Unlock()
				}
				if err := m.phpBackend().Stop(i, ""); err != nil {
					return err
				}
			}
		}
		return m.setInstance(key, "stopped", "", 0)
	}
	_ = m.setInstance(key, "stopping", "", proc.cmd.Process.Pid)
	// Graceful shutdown first; only the owned Job Object may be terminated.
	if i.Component == "nginx" {
		_ = runLogged(p.Executable, []string{"-p", normalized(i.Path) + "/", "-c", normalized(filepath.Join(i.Path, "config", "nginx.conf")), "-s", "quit"}, i.Path, filepath.Join(i.Path, "logs", "shutdown.log"), 3*time.Second)
	}
	if i.Component == "redis" {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", i.Port), time.Second)
		if err == nil {
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			_, _ = conn.Write([]byte("*2\r\n$8\r\nSHUTDOWN\r\n$4\r\nSAVE\r\n"))
			conn.Close()
		}
	}
	if i.Component == "mysql" {
		client := filepath.Join(filepath.Dir(p.Executable), "mysqladmin.exe")
		credentials, readErr := os.ReadFile(filepath.Join(i.Path, "config", "credentials.txt"))
		if readErr == nil {
			lines := strings.Split(string(credentials), "\n")
			if len(lines) > 1 {
				password := strings.TrimPrefix(lines[1], "密码: ")
				secret := filepath.Join(i.Path, "config", "shutdown-client.ini")
				body := "[client]\nuser=root\npassword=" + password + "\nhost=127.0.0.1\nport=" + strconv.Itoa(i.Port) + "\n"
				if os.WriteFile(secret, []byte(body), 0600) == nil {
					_ = runLogged(client, []string{"--defaults-extra-file=" + nativePath(secret), "shutdown"}, i.Path, filepath.Join(i.Path, "logs", "shutdown.log"), 5*time.Second)
					_ = os.Remove(secret)
				}
			}
		}
	}
	select {
	case <-proc.done:
	case <-time.After(3 * time.Second):
		_ = proc.guard.kill()
		select {
		case <-proc.done:
		case <-time.After(5 * time.Second):
			return fmt.Errorf("进程仍在退出，请查看日志")
		}
	}
	return m.setInstance(key, "stopped", "", 0)
}
func (m *Manager) ReadLog(key string) (string, error) {
	i, _, err := m.lookupInstance(key)
	if err != nil {
		return "", err
	}
	out := strings.Builder{}
	for _, name := range []string{"console.log", "config-check.log", "site-reload.log", "nginx-error.log", "php-error.log", "mysql-error.log", "initialize.log", "shutdown.log"} {
		data, err := os.ReadFile(filepath.Join(i.Path, "logs", name))
		if err == nil && len(data) > 0 {
			if len(data) > 24*1024 {
				data = data[len(data)-24*1024:]
			}
			out.WriteString("── " + name + " ──\n" + string(data) + "\n")
		}
	}
	if out.Len() == 0 {
		if current, _, lookupErr := m.lookupInstance(key); lookupErr == nil && current.Error != "" {
			return current.Error, nil
		}
		return "暂无日志。启动实例后会在这里显示输出。", nil
	}
	return out.String(), nil
}
func (m *Manager) Credentials(key string) (string, error) {
	i, _, err := m.lookupInstance(key)
	if err != nil {
		return "", err
	}
	if i.Component != "mysql" {
		return "", fmt.Errorf("此实例没有数据库凭据")
	}
	b, err := os.ReadFile(filepath.Join(i.Path, "config", "credentials.txt"))
	return string(b), err
}

// Caller holds mu: the reservation closes the gap before starting status is saved.
func instanceStartKey(i Instance) string {
	if i.Component == "nginx" {
		return "start:nginx"
	}
	return "start:" + i.PackageID
}
func (m *Manager) canStartLocked(i Instance) error {
	if m.operations[i.PackageID] {
		return fmt.Errorf("此版本正在安装扩展或变更，请稍后启动")
	}
	if i.Component == "nginx" && m.operations["nginx-config"] {
		return fmt.Errorf("Nginx 配置正在变更，请稍后启动")
	}
	if m.operations[instanceStartKey(i)] {
		if i.Component == "nginx" {
			return fmt.Errorf("Nginx 只能同时启动一个实例，已有实例正在启动")
		}
		return fmt.Errorf("此已安装版本已有实例正在启动")
	}
	// Runtime views: legacy instances plus synthesized PHP packages (PHP
	// package == runtime after the merge; there are no PHP Instance rows).
	runtimes := make([]Instance, 0, len(m.state.Instances)+len(m.state.Packages))
	runtimes = append(runtimes, m.state.Instances...)
	for _, p := range m.state.Packages {
		if p.Component == "php" {
			runtimes = append(runtimes, instanceFromPackage(p))
		}
	}
	for _, other := range runtimes {
		conflict := other.PackageID == i.PackageID
		if i.Component == "nginx" {
			conflict = other.Component == "nginx"
		}
		if other.ID != i.ID && conflict && (m.processes[other.ID] != nil || other.Status == "starting" || other.Status == "running" || other.Status == "stopping") {
			if i.Component == "nginx" {
				return fmt.Errorf("Nginx 只能同时运行一个实例，请先停止 %s（%s）", other.Name, other.Version)
			}
			return fmt.Errorf("此已安装版本已有实例 %s 正在运行或变更，请先停止它", other.Name)
		}
	}
	return nil
}

func redisConfig(port int) string {
	return "# Network\nbind 127.0.0.1\nprotected-mode yes\nport " + strconv.Itoa(port) + "\n\n# Working directory and logging\ndir ./data\nlogfile \"\"\n\n# Persistence\nappendonly yes\nsave 60 1000\n"
}
func formatLegacyRedis(i Instance) error {
	if i.Component != "redis" {
		return nil
	}
	path := filepath.Join(i.Path, "config", "redis.conf")
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	legacy := "bind 127.0.0.1\nprotected-mode yes\nport " + strconv.Itoa(i.Port) + "\ndir ./data\nlogfile \"\"\nappendonly yes\nsave 60 1000\n"
	if string(body) != legacy {
		return nil
	}
	if err := os.WriteFile(path+".before-format", body, 0600); err != nil {
		return err
	}
	return atomicConfigWrite(path, []byte(redisConfig(i.Port)))
}

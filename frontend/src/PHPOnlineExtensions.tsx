import { useEffect, useState } from "react";
import { ArrowDownToLine, ChevronDown, CircleAlert, Download, LoaderCircle, RefreshCw, Search } from "lucide-react";
import { call, type Instance, type State, type PHPExtensionReport, type PHPOnlineExtensions as OnlineReport, type PHPExtensionArtifact } from "./api";

const extensions = [
  { name: "redis", title: "Redis", description: "PHP Redis 客户端，连接已有 Redis 服务。", mark: "R" },
  { name: "xdebug", title: "Xdebug", description: "断点调试、错误追踪与性能分析。", mark: "X" },
  { name: "apcu", title: "APCu", description: "缓存应用数据，减少重复计算和数据库读取。", mark: "A" },
];
export default function PHPOnlineExtensions({ instance, state, report, disabled }: {
  instance: Instance; state: State; report: PHPExtensionReport; disabled: boolean;
}) {
  const [catalog, setCatalog] = useState<OnlineReport>();
  const [loading, setLoading] = useState(false);
  const [work, setWork] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [query, setQuery] = useState("");
  const [details, setDetails] = useState("");
  const [mirrorName, setMirrorName] = useState("国内镜像");
  const [mirrorURL, setMirrorURL] = useState("");
  const [mirrorHash, setMirrorHash] = useState("");
  async function load(refresh: boolean) {
    setLoading(true); setError("");
    try { setCatalog(await call<OnlineReport>("GetPHPOnlineExtensions", instance.id, refresh)); }
    catch(e) { setError(String(e)); }
    finally { setLoading(false); }
  }
  useEffect(() => { void (async () => {
    setLoading(true);
    try {
      const cached = await call<OnlineReport>("GetPHPOnlineExtensions", instance.id, false); setCatalog(cached);
      if (state.settings.catalogAutoUpdate && (!cached.updatedAt || Date.now() - Date.parse(cached.updatedAt) > (state.settings.catalogUpdateHours || 6) * 3600000)) await load(true);
    } catch(e) { setError(String(e)); } finally { setLoading(false); }
  })(); }, [instance.id]);
  async function install(a: PHPExtensionArtifact, enable: boolean, local = false) {
    setWork(a.name); setError(""); setMessage("");
    try { await call<string>(local ? "ImportPHPExtension" : "InstallPHPExtension", instance.id, a.id, report.revision, enable); setMessage(local ? "已导入安装文件，正在进行实际加载检查。" : "已提交下载任务。安装完成后可在本地扩展中检查状态。服务重启由你操作。"); }
    catch(e) { setError(String(e)); }
    finally { setWork(""); }
  }
  async function addMirror(a: PHPExtensionArtifact) {
    setWork(a.name); setError("");
    try { await call("AddPHPExtensionMirror", a.id, mirrorName, mirrorURL, mirrorHash); setMirrorURL(""); setMirrorHash(""); await load(false); setMessage("镜像已保存，使用与原制品相同的摘要进行校验。"); }
    catch(e) { setError(String(e)); } finally { setWork(""); }
  }
  return <div className="online-extension-pane">
    <div className="online-extension-intro"><div><strong>为 PHP {catalog?.version || report.version} 安装扩展</strong><p>自动选择最新兼容稳定版。DLL 直接保存在此 PHP 包的 extensions 目录，每个扩展只保留当前一份。</p></div>
      <button className="button secondary compact" disabled={loading || !!work || disabled} onClick={() => void load(true)}>{loading ? <LoaderCircle className="spin" size={14} /> : <RefreshCw size={14} />}刷新目录</button></div>
    <label className="extension-search online-extension-search"><Search size={16} /><input aria-label="搜索在线扩展" value={query} placeholder="搜索 Redis、调试、缓存…" onChange={e => setQuery(e.target.value)} /></label>
    {disabled && <p className="field-note">请先保存或放弃本地扩展修改，再安装在线扩展。</p>}
    {error && <div className="extension-notice error" role="alert"><CircleAlert size={16} /><span>{error}</span></div>}
    {message && <div className="extension-notice success" role="status">{message}</div>}
    {catalog?.warnings.map((warning, n) => <div className="extension-notice warning" key={n}>{warning}，已保留缓存目录。</div>)}
    {extensions.filter(item => `${item.title} ${item.description}`.toLowerCase().includes(query.toLowerCase())).map(item => {
      const a = catalog?.items.find(a => a.name === item.name && a.sources.length > 0);
      const installed = state.extensionPackages.find(e => e.packageId === instance.packageId && e.name === item.name);
      const task = state.tasks.slice().reverse().find(t => t.kind === "php-extension" && t.extensionName === item.name && t.instanceId === instance.id);
      const pending = state.preparedExtensions.some(e => e.record.packageId === instance.packageId && e.record.name === item.name);
      const active = !!task && ["paused", "queued", "downloading", "installing", "verifying", "awaiting_restart"].includes(task.status);
      const local = report.extensions.find(e => e.name === item.name);
      const current = !!installed && !!a && installed.version.localeCompare(a.version, undefined, {numeric:true}) >= 0 && !!local?.editable;
      return <article className="online-extension-card" key={item.name}>
        <div className={`online-extension-symbol ${item.name}`}>{item.mark}</div>
        <div className="online-extension-info"><div className="online-extension-name"><strong>{item.title}</strong><span className="mono">{a?.version || "—"}</span>{installed && <span className="online-installed">已安装 {installed.version}</span>}</div>
          <p>{item.description}</p><small>{a ? `${a.publisher} · ${a.kind === "zend" ? "Zend 扩展" : "普通扩展"}` : loading ? "正在读取兼容目录…" : "上游暂未提供匹配此 PHP 构建的发行包"}</small>
          {active && <div className="online-task-state"><LoaderCircle size={13} className={task.status === "downloading" || task.status === "installing" ? "spin" : ""} /><span>{pending ? "文件已准备，停止或重启 PHP 后更新" : task.status === "paused" ? "下载已暂停，可在下载中心继续" : task.status === "installing" ? "正在检查加载并安装" : `下载中 ${task.total > 0 ? Math.min(100, Math.round(task.received / task.total * 100)) + "%" : ""}`}</span></div>}
          {task?.warning && <small className="online-warning">{task.warning}</small>}
          {task?.error && <small className="inline-error">{task.error}</small>}
          {current && !active && <small>{local?.configured ? local.loaded ? "当前 PHP 已加载" : "已启用，启动或重启后生效" : "可在本地扩展中启用"}</small>}
        </div>
        <div className="online-extension-actions">
          <button className="button primary compact" disabled={!a || disabled || !!work || active || current} onClick={() => a && void install(a, true)}>{work === item.name ? <LoaderCircle className="spin" size={14} /> : <Download size={14} />}{current ? "已安装最新版" : installed ? "更新并启用" : "安装并启用"}</button>
          <button className="text-button" disabled={!a || disabled || !!work || active || current} onClick={() => a && void install(a, false)}><ArrowDownToLine size={12} />仅安装</button>
          <button className="text-button" disabled={!a} onClick={() => setDetails(details === item.name ? "" : item.name)}>来源与导入 <ChevronDown size={12} /></button>
        </div>
        {details === item.name && a && <div className="online-extension-details">
          <p className="mono">PHP {a.phpBranch} · {a.threadSafe ? "TS" : "NTS"} · {a.bits} 位 · {a.compiler.toUpperCase()}</p>
          <p>{a.verification}</p><div className="online-source-list">{a.sources.map(source => <div key={source.url}><strong>{source.name}</strong><span className="mono">{source.url}</span></div>)}</div>
          <div className="online-mirror-form"><input aria-label={`${item.title} 镜像名称`} value={mirrorName} onChange={e => setMirrorName(e.target.value)} /><input aria-label={`${item.title} 镜像地址`} placeholder="同一制品的 HTTPS 镜像地址" value={mirrorURL} onChange={e => setMirrorURL(e.target.value)} />
            {!a.sha256 && <input aria-label={`${item.title} 可信摘要`} placeholder="可信 SHA-256，必填" value={mirrorHash} onChange={e => setMirrorHash(e.target.value)} />}
            <button className="button secondary compact" disabled={!!work || !mirrorURL || !a.sha256 && !mirrorHash} onClick={() => void addMirror(a)}>添加镜像</button></div>
          <div className="online-import-line"><span>可导入当前推荐版本的 DLL 或 ZIP，仍会进行实际加载校验。</span><button className="button secondary compact" disabled={disabled || !!work || active} onClick={() => void install(a, true, true)}>导入并启用</button></div>
        </div>}
      </article>;
    })}
    <p className="extension-help">下载、暂停和重试统一在下载中心管理。目录刷新不会自动更新已安装的扩展。</p>
  </div>;
}

import { useEffect, useMemo, useRef, useState } from "react";
import { Check, CircleAlert, LoaderCircle, LockKeyhole, Puzzle, RefreshCw, Search, X } from "lucide-react";
import { call, type Instance, type PHPExtensionReport, type State } from "./api";
import PHPOnlineExtensions from "./PHPOnlineExtensions";

const opcacheFields = [
  { key: "opcache.enable", label: "启用 OPcache", help: "在网站请求中缓存 PHP 编译结果。", bool: true },
  { key: "opcache.enable_cli", label: "CLI 缓存", help: "对使用此配置的命令行进程启用缓存。", bool: true },
  { key: "opcache.validate_timestamps", label: "检查文件修改", help: "本地开发建议开启，代码修改后可自动更新缓存。", bool: true },
  { key: "opcache.memory_consumption", label: "缓存内存", help: "8–4096 MB", min: 8, max: 4096, unit: "MB" },
  { key: "opcache.interned_strings_buffer", label: "字符串缓存", help: "0–1024 MB", min: 0, max: 1024, unit: "MB" },
  { key: "opcache.max_accelerated_files", label: "最大缓存文件数", help: "200–1000000，PHP 会选择临近的容量档位。", min: 200, max: 1000000 },
  { key: "opcache.revalidate_freq", label: "修改检查间隔", help: "0 表示每次请求都检查；最多 86400 秒。", min: 0, max: 86400, unit: "秒" },
];

export default function PHPExtensions({ instance, state, close, editConfig }: {
  instance: Instance; state: State; close: () => void; editConfig: () => void;
}) {
  const dialog = useRef<HTMLElement>(null);
  const [report, setReport] = useState<PHPExtensionReport>();
  const [enabled, setEnabled] = useState<Record<string, boolean>>({});
  const [settings, setSettings] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [tab, setTab] = useState<"extensions" | "opcache" | "online">("extensions");
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("all");
  const [discard, setDiscard] = useState<"close" | "refresh" | "config">();
  const dirty = !!report && (report.extensions.some(row => row.editable && enabled[row.name] !== row.configured)
    || Object.keys(settings).some(key => settings[key] !== report.settings[key]));
  const changedCount = report?.extensions.filter(row => row.editable && enabled[row.name] !== row.configured).length ?? 0;
  const opcache = report?.extensions.find(row => row.name === "zend opcache");
  const taskChanges = state.tasks.filter(t => t.kind === "php-extension" && t.instanceId === instance.id && ["completed", "awaiting_restart"].includes(t.status))
    .map(t => `${t.id}:${t.status}:${t.warning}`).join("|");
  const lastTasks = useRef(taskChanges);
  const rows = useMemo(() => report?.extensions.filter(row => {
    const matches = `${row.name} ${row.description} ${row.filename}`.toLowerCase().includes(query.toLowerCase());
    return matches && (filter === "all" || filter === "enabled" && enabled[row.name]
      || filter === "disabled" && row.editable && !enabled[row.name]
      || filter === "builtin" && row.kind === "builtin"
      || filter === "pending" && (row.state === "pending" || row.state === "update" || enabled[row.name] !== row.configured));
  }) ?? [], [report, enabled, query, filter]);

  function adopt(data: PHPExtensionReport) {
    setReport(data);
    setEnabled(Object.fromEntries(data.extensions.map(row => [row.name, row.configured])));
    setSettings({ ...data.settings });
  }
  async function load() {
    setBusy(true); setError(""); setMessage("");
    try { adopt(await call<PHPExtensionReport>("GetPHPExtensions", instance.id)); }
    catch (e) { setError(String(e)); }
    finally { setBusy(false); }
  }
  useEffect(() => { void load(); }, [instance.id]);
  useEffect(() => {
    if (lastTasks.current === taskChanges) return;
    lastTasks.current = taskChanges;
    if (state.tasks.some(t => t.instanceId === instance.id && ["completed", "awaiting_restart"].includes(t.status))) {
      if (!dirty) void load(); else setMessage("安装状态已更新。保存或放弃本地修改后，可重新检测扩展。");
    }
  }, [taskChanges]);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    dialog.current?.focus();
    return () => previous?.focus();
  }, []);
  function leave(action: "close" | "refresh" | "config") {
    if (busy) return;
    if (dirty) { setDiscard(action); return; }
    if (action === "close") close();
    else if (action === "config") editConfig();
    else void load();
  }
  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.defaultPrevented) { event.preventDefault(); if (discard) setDiscard(undefined); else leave("close"); }
      if (event.key === "Tab") {
        const root = discard ? dialog.current?.querySelector(".extension-discard") : dialog.current;
        const targets = Array.from(root?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled)') ?? []);
        if (!targets.length) { event.preventDefault(); return; }
        const first = targets[0], last = targets[targets.length - 1];
        if (event.shiftKey && (document.activeElement === first || !targets.includes(document.activeElement as HTMLElement))) { event.preventDefault(); last.focus(); }
        else if (!event.shiftKey && (document.activeElement === last || !targets.includes(document.activeElement as HTMLElement))) { event.preventDefault(); first.focus(); }
      }
    };
    window.addEventListener("keydown", listener);
    return () => window.removeEventListener("keydown", listener);
  }, [dirty, busy, discard]);

  function select(names: string[], value: boolean) {
    if (!report) return;
    const next = { ...enabled };
    const include = (name: string) => {
      const row = report.extensions.find(row => row.name === name);
      if (!row?.editable || next[name]) return;
      next[name] = true;
      row.dependencies.forEach(include);
    };
    names.forEach(name => value ? include(name) : next[name] = false);
    if (!value) {
      let changed = true;
      while (changed) {
        changed = false;
        report.extensions.forEach(row => {
          if (row.editable && next[row.name] && row.dependencies.some(dep => !next[dep])) { next[row.name] = false; changed = true; }
        });
      }
    }
    setEnabled(next); setMessage("");
  }
  async function apply(restart: boolean) {
    if (!report) return;
    setBusy(true); setError(""); setMessage("");
    try {
      let saved = report;
      if (dirty) {
        saved = await call<PHPExtensionReport>("SavePHPExtensions", instance.id, {
          revision: report.revision,
          extensions: report.extensions.filter(row => row.editable).map(row => ({ name: row.name, enabled: enabled[row.name] })),
          settings: Object.fromEntries(Object.entries(settings).filter(([key, value]) => value !== report.settings[key])),
        });
        adopt(saved);
      }
      if (restart) {
        await call("RestartPHPWithExtensions", instance.id, saved.revision);
        const fresh = await call<PHPExtensionReport>("GetPHPExtensions", instance.id);
        adopt(fresh);
        if (!fresh.runtimeKnown || fresh.pending || fresh.warnings) {
          setError("PHP 已启动，但实际加载检查仍有异常。" + (fresh.runtimeError || fresh.warnings || "请检查扩展状态和实例日志。"));
        } else setMessage("PHP 已启动，扩展的实际加载状态已核对。");
      } else setMessage(saved.running ? "配置已校验并保存。重启此 PHP 实例后生效。" : "配置已校验并保存。下次启动此 PHP 实例时生效。");
    } catch (e) { setError(String(e)); }
    finally { setBusy(false); }
  }

  return <div className="modal-backdrop">
    <section ref={dialog} tabIndex={-1} className="modal php-extensions-modal" role="dialog" aria-modal="true" aria-labelledby="php-extensions-title">
      <div className="extension-heading">
        <div className="extension-title-icon"><Puzzle size={23} /></div>
        <div><h2 id="php-extensions-title">PHP 扩展管理</h2>
          <p>{instance.name} <span className="mono" title={report?.build}>· PHP {report?.version || instance.version}</span>
            {report && ` · ${report.bits} 位 · ${report.threadSafe ? "TS" : "NTS"}`}</p></div>
        <button className="icon-button" aria-label="关闭扩展管理" disabled={busy} onClick={() => leave("close")}><X size={20} /></button>
      </div>
      <div className="extension-toolbar">
        <div className="extension-tabs" role="tablist" aria-label="扩展管理页面">
          <button role="tab" aria-selected={tab === "extensions"} className={tab === "extensions" ? "active" : ""} onClick={() => setTab("extensions")}>本地扩展 <span>{report?.extensions.length ?? "—"}</span></button>
          <button role="tab" aria-selected={tab === "online"} className={tab === "online" ? "active" : ""} onClick={() => setTab("online")}>在线扩展</button>
          <button role="tab" aria-selected={tab === "opcache"} className={tab === "opcache" ? "active" : ""} onClick={() => setTab("opcache")}>OPcache 设置</button>
        </div>
        <button className="text-button" disabled={busy} onClick={() => leave("config")}>编辑配置文件</button>
        <button className="subtle-button" disabled={busy} onClick={() => leave("refresh")}><RefreshCw size={14} />重新检测</button>
      </div>
      <div className="extension-runtime-line">
        <span className={`extension-dot ${report?.runtimeKnown ? "live" : ""}`} />
        <strong>{!report ? "正在检测 PHP" : report.running ? report.runtimeKnown ? "已核对运行中的 PHP" : "运行状态未确认" : "PHP 未运行"}</strong>
        <span>{!report ? "检测本地扩展及实例配置" : report.running ? !report.runtimeKnown ? "请查看诊断提示，或重新检测" : report.pending ? "有配置等待重启生效" : "实际状态来自当前 FastCGI 进程" : "保存配置后，启动此实例即可应用"}</span>
        {busy && <LoaderCircle className="spin" size={15} />}
      </div>
      {error && <div className="extension-notice error" role="alert"><CircleAlert size={16} /><span>{error}</span></div>}
      {message && <div className="extension-notice success" role="status"><Check size={16} /><span>{message}</span></div>}
      {report?.warnings && <div className="extension-notice warning"><CircleAlert size={16} /><span>当前配置检测有告警：{report.warnings}</span></div>}
      {report?.runtimeError && <div className="extension-notice warning"><CircleAlert size={16} /><span>{report.runtimeError}</span></div>}
      {!report ? <div className="extension-empty">{busy ? "正在扫描本地扩展并检测 PHP…" : "检测失败，可重新检测或检查实例配置。"}</div> : tab === "extensions" ? <>
        <div className="extension-search-row">
          <label className="extension-search"><Search size={16} /><input aria-label="搜索 PHP 扩展" placeholder="搜索扩展名或用途，例如 curl、图片" value={query} onChange={e => setQuery(e.target.value)} /></label>
          <select aria-label="筛选扩展状态" value={filter} onChange={e => setFilter(e.target.value)}>
            <option value="all">全部扩展</option><option value="enabled">配置已启用</option><option value="disabled">可启用</option><option value="pending">有待应用的修改</option><option value="builtin">内置扩展</option>
          </select>
        </div>
        <div className="extension-batch-row"><span>{rows.length} 项 · {changedCount} 项开关修改</span>
          <button className="text-button" disabled={busy || !rows.some(row => row.editable)} onClick={() => select(rows.filter(row => row.editable).map(row => row.name), true)}>启用当前列表</button>
          <button className="text-button" disabled={busy || !rows.some(row => row.editable)} onClick={() => select(rows.filter(row => row.editable).map(row => row.name), false)}>禁用当前列表</button></div>
        <div className="extension-table-scroll"><table className="extension-table"><thead><tr><th>扩展与用途</th><th>类型</th><th>配置开关</th><th>当前运行</th></tr></thead><tbody>
          {rows.map(row => <tr key={row.name} className={enabled[row.name] !== row.configured ? "draft-row" : ""}>
            <td><strong className="mono">{row.name}</strong>{row.description && <p>{row.description}</p>}
              <small title={row.source}>{row.filename || row.source}{row.version && ` · v${row.version}`}{row.dependencies.length > 0 && ` · 依赖 ${row.dependencies.join("、")}`}</small></td>
            <td><span className="extension-kind">{row.kind === "builtin" ? "内置" : !row.editable ? "手工管理" : row.kind === "zend" ? "Zend" : "普通扩展"}</span></td>
            <td><label className="extension-toggle"><input type="checkbox" aria-label={`${row.name} 配置启用`} disabled={busy || !row.editable} checked={!!enabled[row.name]} onChange={e => select([row.name], e.target.checked)} />
              <span>{enabled[row.name] ? "已启用" : "已禁用"}</span>{!row.editable && <LockKeyhole size={12} />}</label>
              {enabled[row.name] !== row.configured && <small className="extension-draft">未保存</small>}</td>
            <td><span className={`extension-status ${row.state}`}>{row.state === "update" ? "文件待更新" : row.state === "stopped" ? "未运行" : row.state === "unknown" ? "未确认" : row.state === "pending" ? row.loaded ? "已加载 · 待停用" : "未加载 · 待启用" : row.loaded ? "已加载" : "未加载"}</span></td>
          </tr>)}
        </tbody></table>{rows.length === 0 && <div className="extension-empty">没有匹配的扩展。</div>}</div>
        <p className="extension-help">内置扩展随 PHP 加载；手工加载项保留在原配置中。开关表示保存后的加载配置，当前运行状态需要重启后才会同步。</p>
      </> : tab === "online" ? <PHPOnlineExtensions instance={instance} state={state} report={report} disabled={busy || dirty} /> : <div className="extension-opcache">
        {!opcache ? <div className="extension-empty">当前安装包没有检测到 OPcache。</div> : <>
          <div className="extension-opcache-summary"><strong>{opcache.kind === "builtin" ? "此版本已内置 OPcache" : "OPcache 使用 Zend 扩展加载"}</strong>
            <p>{opcache.kind === "builtin" ? "通过下面的参数控制缓存行为。" : enabled[opcache.name] ? "已选择加载 OPcache，可调整下面的参数。" : "请先在本地扩展中启用 zend opcache；参数会随配置一起保存。"}</p></div>
          <div className="extension-settings-grid">{opcacheFields.map(field => <label className="extension-setting" key={field.key}>
            <span><strong>{field.label}</strong><small>{field.help}</small></span>
            {field.bool ? <select aria-label={field.label} disabled={busy} value={settings[field.key] ?? "0"} onChange={e => setSettings({ ...settings, [field.key]: e.target.value })}><option value="1">开启</option><option value="0">关闭</option></select>
              : <div className="extension-number"><input type="number" aria-label={field.label} min={field.min} max={field.max} disabled={busy} value={settings[field.key] ?? ""} onChange={e => setSettings({ ...settings, [field.key]: e.target.value })} />{field.unit && <span>{field.unit}</span>}</div>}
          </label>)}</div>
          {settings["opcache.validate_timestamps"] === "0" && <div className="extension-notice warning"><CircleAlert size={16} /><span>已关闭文件修改检查。修改站点代码后，需要重启 PHP 才能更新缓存。</span></div>}
        </>}
      </div>}
      <footer className="extension-footer"><span>{dirty ? "有未保存的修改" : report?.pending ? "配置已保存，等待应用" : "每个 PHP 实例独立管理"}</span>
        <button className="subtle-button" disabled={busy} onClick={() => leave("close")}>关闭</button>
        <button className="subtle-button" disabled={busy || !dirty} onClick={() => void apply(false)}><Check size={14} />校验并保存</button>
        <button className="button primary" disabled={busy || !report} onClick={() => void apply(true)}>{busy ? <LoaderCircle className="spin" size={14} /> : <RefreshCw size={14} />}{dirty ? report?.running ? "保存并重启" : "保存并启动" : report?.running ? "重启并应用" : "启动并应用"}</button>
      </footer>
      {discard && <div className="extension-discard-backdrop"><div className="extension-discard" role="alertdialog" aria-modal="true" aria-label="放弃未保存的扩展修改">
        <strong>有未保存的扩展修改</strong><p>继续会放弃本次开关和参数修改。</p>
        <div><button className="button secondary" onClick={() => setDiscard(undefined)}>继续编辑</button><button className="button secondary" onClick={() => { const action = discard; setDiscard(undefined); if (action === "close") close(); else if (action === "config") editConfig(); else void load(); }}>放弃修改并继续</button></div>
      </div></div>}
    </section>
  </div>;
}

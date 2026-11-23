import { useEffect, useState } from "react";
import { Download, LoaderCircle, Terminal, Check, X } from "lucide-react";
import { call, type CLIStatus, type State } from "./api";

export default function ToolchainPanel({ state, disabled, changed }: { state: State; disabled: boolean; changed: () => Promise<void> }) {
  const [status, setStatus] = useState<CLIStatus>();
  const [php, setPHP] = useState(state.tools.phpPackageId || state.tools.phpInstanceId || state.packages.find(p => p.component === "php")?.id || "");
  const [busy, setBusy] = useState("");
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const phpPackages = state.packages.filter(p => p.component === "php");

  async function refresh() {
    try { setStatus(await call<CLIStatus>("GetCLIStatus")); }
    catch (e) { setError(String(e)); }
  }
  useEffect(() => {
    void refresh();
    if (!state.packages.some(p => p.id === php && p.component === "php")) {
      setPHP(state.tools.phpPackageId || state.tools.phpInstanceId || state.packages.find(p => p.component === "php")?.id || "");
    }
  }, [state.tools.phpPackageId, state.tools.phpInstanceId, state.tools.composerVersion, state.packages.length]);

  async function perform(kind: string) {
    setBusy(kind); setError(""); setMessage("");
    try {
      if (kind === "install") {
        await call("InstallComposer", php);
        setMessage("Composer 安装任务已加入下载中心。");
      } else {
        const result = kind === "php-remove"
          ? await call<CLIStatus>("DisableCLIPHPPath")
          : kind === "composer-path"
            ? await call<CLIStatus>("EnableComposerPath")
            : await call<CLIStatus>("SelectCLIPHP", php, true);
        setStatus(result);
        setMessage(result.warning || (kind === "php-remove"
          ? `PHP 环境变量已取消${result.composerPathEnabled ? "，Composer 命令仍可用" : ""}，请重新打开终端。`
          : "环境变量已设置，请重新打开终端。"));
      }
      await changed(); await refresh();
    } catch (e) { setError(String(e)); }
    finally { setBusy(""); }
  }

  const task = state.tasks.slice().reverse().find(t => t.kind === "composer");
  const installing = !!task && ["downloading", "installing", "verifying"].includes(task.status);
  const currentPHP = php === (status?.phpPackageId || status?.phpInstanceId) && status?.phpPathEnabled;
  const canRemovePHP = !!status && (status.phpPathEnabled || (status.userPathEnabled && !status.composerPathEnabled));
  const locked = disabled || !!busy;

  return <section className="toolchain-panel">
    <div className="toolchain-title"><Terminal size={18} /><strong>工具与命令行</strong><span>当前用户 PATH · 重新打开终端后生效</span></div>
    <div className="toolchain-content">
      <div className="toolchain-php">
        <strong className="toolchain-label">命令行 PHP</strong>
        <div className="toolchain-controls">
          <select aria-label="PHP 版本" disabled={locked} value={php} onChange={e => setPHP(e.target.value)}>
            <option value="">选择 PHP 版本</option>
            {phpPackages.map(p => <option key={p.id} value={p.id}>PHP {p.version}{phpPackages.filter(other => other.version === p.version).length > 1 ? ` · ${p.id.slice(0, 8)}` : ""}</option>)}
          </select>
          <button className="button secondary compact" disabled={locked || !php || !status || !!currentPHP} onClick={() => void perform("php-path")}>
            {busy === "php-path" ? <LoaderCircle className="spin" size={14} /> : <Check size={14} />}设置环境变量
          </button>
          <button className="button secondary compact" disabled={locked || !canRemovePHP} onClick={() => void perform("php-remove")}>
            {busy === "php-remove" ? <LoaderCircle className="spin" size={14} /> : <X size={14} />}取消环境变量
          </button>
        </div>
        <small>{status?.phpPathEnabled ? status.phpVersion ? `当前：PHP ${status.phpVersion}` : "请选择有效的 PHP 版本" : "未设置 PHP 环境变量"}</small>
      </div>
      <div className="toolchain-composer">
        <div><strong>Composer</strong><span className="mono">{status?.composerInstalled ? status.composerVersion : "尚未安装"}</span></div>
        <p>从官方稳定版目录下载并校验，运行时使用选定的 PHP。</p>
        <div className="toolchain-actions">
          <button className="button secondary compact" disabled={locked || installing || !php} onClick={() => void perform("install")}>
            {installing || busy === "install" ? <LoaderCircle className="spin" size={14} /> : <Download size={14} />}{installing ? "安装中…" : status?.composerInstalled ? "更新 Composer" : "安装 Composer"}
          </button>
          <button className="button secondary compact" disabled={locked || !status?.composerInstalled || !status?.phpInstanceId || status.composerPathEnabled} onClick={() => void perform("composer-path")}><Check size={14} />{status?.composerPathEnabled ? "Composer 已加入 PATH" : "添加 Composer 到 PATH"}</button>
        </div>
        {task?.error && <small className="inline-error">{task.error}</small>}
      </div>
    </div>
    {message && <p className="field-note" role="status">{message}</p>}
    {error && <p className="inline-error" role="alert">{error}</p>}
    {status?.warning && status.warning !== message && <p className="field-note">{status.warning}</p>}
  </section>;
}

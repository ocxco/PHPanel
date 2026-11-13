import { useEffect, useRef, useState } from "react";
import { LoaderCircle, ShieldCheck, X } from "lucide-react";
import { call, type Instance } from "./api";

export default function SystemServicePanel({instance, restoreAutoStart, close, changed}: {instance: Instance; restoreAutoStart?: boolean; close: () => void; changed: () => Promise<void>}) {
  const [autoStart, setAutoStart] = useState(restoreAutoStart??(instance.serviceName ? !!instance.serviceAutoStart : true));
  const previousService=useRef({name:instance.serviceName,autoStart:instance.serviceAutoStart});
  const [autoPHP, setAutoPHP] = useState(instance.nginxAutoPHP !== false);
  const [stopPHP, setStopPHP] = useState(!!instance.nginxStopPHP);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  useEffect(() => {const previous=previousService.current;if(instance.serviceName&&(previous.name!==instance.serviceName||previous.autoStart!==instance.serviceAutoStart))setAutoStart(!!instance.serviceAutoStart);previousService.current={name:instance.serviceName,autoStart:instance.serviceAutoStart};}, [instance.serviceName, instance.serviceAutoStart]);
  useEffect(() => {setAutoPHP(instance.nginxAutoPHP !== false);setStopPHP(!!instance.nginxStopPHP);},[instance.nginxAutoPHP,instance.nginxStopPHP]);
  const stopped = instance.status === "stopped" || (!instance.serviceName && instance.status === "error");
  async function perform(method: string) {
    setBusy(true); setError(""); setMessage("");
    try {
      await call(method, instance.id, ...(method === "SaveNginxLinkOptions" ? [autoPHP, stopPHP] : method === "UnregisterSystemService" ? [] : [autoStart]));
      await changed();
      setMessage(method === "SaveNginxLinkOptions" ? "联动设置已保存，下次启动 Nginx 时生效。" : method === "RegisterSystemService" ? "已注册。可手动启动；开机启动将在下次启动 Windows 时生效。" : method === "UnregisterSystemService" ? "已注销。实例配置和数据已保留。" : "启动方式已保存。");
    } catch(e) { setError(String(e)); await changed(); }
    finally { setBusy(false); }
  }
  return <div className="modal-backdrop"><section className="modal system-service-modal" role="dialog" aria-modal="true" aria-labelledby="system-service-title">
    <div className="modal-heading"><h2 id="system-service-title"><ShieldCheck size={20}/>{instance.component === "nginx" ? "Nginx 启动设置" : "系统服务"}</h2><button className="icon-button" disabled={busy} aria-label="关闭" onClick={close}><X size={18}/></button></div>
    <p>{instance.name} · {instance.serviceName ? "已注册" : "尚未注册"}</p>
    {instance.component === "nginx" && <div className="system-service-options nginx-link-options"><strong>PHP 联动</strong><label><input type="checkbox" disabled={busy || !stopped} checked={autoPHP} onChange={e => setAutoPHP(e.target.checked)}/>自动启动站点所需 PHP</label><label><input type="checkbox" disabled={busy || !stopped} checked={stopPHP} onChange={e => setStopPHP(e.target.checked)}/>停止 Nginx 时同时停止联动 PHP</label><p className="field-note">已运行的 PHP 会直接复用。仅停止本次联动新启动的实例。请先停止 Nginx 再修改设置。</p><button className="button secondary compact" disabled={busy || !stopped || (autoPHP === (instance.nginxAutoPHP !== false) && stopPHP === !!instance.nginxStopPHP)} onClick={() => void perform("SaveNginxLinkOptions")}>保存联动设置</button></div>}
    <p className="field-note">注册后由 Windows 托管，退出 PHPanel 后仍可运行。权限不足时可通过授权提示，以管理员身份重启后继续。</p>
    <div className="system-service-options">
      {instance.serviceName && <label className="field">Windows 服务名称<input readOnly value={instance.serviceName}/></label>}
      <label><input type="checkbox" disabled={busy} checked={autoStart} onChange={e => setAutoStart(e.target.checked)}/>开机自动启动</label>
      {!stopped && <p className="field-note">请先在实例卡片中停止实例，再注册或注销系统服务。</p>}
      <p className="field-note">注册后请保持软件和数据目录的位置。移动目录前先停止并注销服务。Nginx 全部版本只允许注册一个实例。</p>
    </div>
    {error && <p className="inline-error" role="alert">{error}</p>}
    {message && <p className="field-note" role="status">{message}</p>}
    <div className="system-service-actions">
      {instance.serviceName ? <><button className="button secondary" disabled={busy || !stopped} onClick={() => void perform("UnregisterSystemService")}>注销服务</button><button className="button primary" disabled={busy || autoStart === !!instance.serviceAutoStart} onClick={() => void perform("SetSystemServiceAutoStart")}>{busy && <LoaderCircle size={14} className="spin"/>}保存</button></> : <button className="button primary" disabled={busy || !stopped} onClick={() => void perform("RegisterSystemService")}>{busy && <LoaderCircle size={14} className="spin"/>}注册服务</button>}
    </div>
  </section></div>;
}

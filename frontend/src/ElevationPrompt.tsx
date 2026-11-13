import { useState } from "react";
import { LoaderCircle, ShieldCheck, X } from "lucide-react";
import { call } from "./api";

export default function ElevationPrompt({view,instanceId,serviceAutoStart,elevated,close}:{view:string;instanceId:string;serviceAutoStart?:boolean;elevated:boolean;close:()=>void}) {
  const [busy,setBusy]=useState(false);
  const [error,setError]=useState("");
  async function restart(){
    setBusy(true);setError("");
    try {
      const result=await call<{cancelled:boolean}>("RestartAsAdministrator",view,instanceId,serviceAutoStart??null);
      if(result.cancelled){setError("已取消 Windows 授权，当前面板和实例已保留。");setBusy(false);}
    }catch(e){setError(String(e));setBusy(false);}
  }
  return <div className="modal-backdrop elevation-backdrop"><section className="modal elevation-modal" role="dialog" aria-modal="true" aria-labelledby="elevation-title">
    <div className="modal-heading"><h2 id="elevation-title"><ShieldCheck size={20}/>{elevated?"服务权限不足":"需要管理员权限"}</h2><button className="icon-button" disabled={busy} aria-label="关闭授权提示" onClick={close}><X size={18}/></button></div>
    {elevated?<p className="modal-intro">当前已以管理员身份运行。请检查目标 Windows 服务的权限设置后重试。</p>:<><p className="modal-intro">授权后将以管理员身份重新启动 PHPanel，并返回当前页面和实例设置。重启后可继续刚才的操作。</p><p className="field-note">重启会停止由当前面板管理的普通实例，并暂停下载。Windows 系统服务继续运行。未保存的其它编辑不会保留。</p><p className="field-note">接下来由 Windows 显示 UAC 授权窗口。取消授权时，当前面板与实例保持运行。</p></>}
    {error&&<p className="inline-error" role="alert">{error}</p>}
    <div className="modal-actions"><button className="button secondary" disabled={busy} onClick={close}>{elevated?"关闭":"暂不重启"}</button>{!elevated&&<button className="button primary" disabled={busy} onClick={()=>void restart()}>{busy?<LoaderCircle size={15} className="spin"/>:<ShieldCheck size={15}/>} {busy?"等待 Windows 授权…":"授权并重启"}</button>}</div>
  </section></div>;
}

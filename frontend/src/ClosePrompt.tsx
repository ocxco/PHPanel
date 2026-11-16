import { useEffect, useRef, useState } from "react";
import { LoaderCircle, Monitor, X } from "lucide-react";
import { call, type SystemStatus } from "./api";

export default function ClosePrompt({ close }: { close: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [trayAvailable, setTrayAvailable] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const element = dialog.current!;
    const previousFocus = document.activeElement as HTMLElement | null;
    element.showModal();
    let active = true;
    void call<SystemStatus>("GetSystemStatus").then(status => {
      if (active) setTrayAvailable(status.trayAvailable);
    }).catch(e => {
      if (active) setError(String(e));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; element.close(); previousFocus?.focus(); };
  }, []);

  async function cancel() {
    if (busy) return;
    setBusy(true);
    try { await call("CancelCloseChoice"); close(); }
    catch (e) { setError(String(e)); setBusy(false); }
  }

  async function choose(behavior: "quit" | "tray") {
    if (busy) return;
    setBusy(true);
    setError("");
    try { await call("SelectCloseBehavior", behavior); close(); }
    catch (e) { setError(String(e)); setBusy(false); }
  }

  return (
    <dialog ref={dialog} className="close-prompt-dialog" aria-labelledby="close-prompt-title" aria-describedby="close-prompt-description" onCancel={event => { event.preventDefault(); void cancel(); }}>
      <section className="modal">
        <div className="modal-heading">
          <h2 id="close-prompt-title">关闭 PHPanel</h2>
          <button type="button" className="icon-button" disabled={busy} aria-label="取消关闭" onClick={() => void cancel()}><X size={18} /></button>
        </div>
        <p id="close-prompt-description" className="modal-intro">请选择点击窗口关闭按钮后的操作。我们会记住你的选择，之后可在“偏好设置 → 系统设置”中修改。</p>
        <p className="field-note">退出程序会停止面板管理的普通实例；最小化到托盘后，面板、下载和实例继续运行，点击托盘图标可重新打开。</p>
        {!loading && !trayAvailable && <p className="field-note">系统托盘当前不可用，请选择退出程序或取消关闭。</p>}
        {error && <p className="inline-error" role="alert">{error}</p>}
        <div className="modal-actions">
          <button type="button" className="button secondary" disabled={busy} autoFocus onClick={() => void cancel()}>取消</button>
          <button type="button" className="button secondary" disabled={busy} onClick={() => void choose("quit")}>退出程序</button>
          <button type="button" className="button primary" disabled={busy || loading || !trayAvailable} onClick={() => void choose("tray")}>
            {busy || loading ? <LoaderCircle size={16} className="spin" /> : <Monitor size={16} />}最小化到托盘
          </button>
        </div>
      </section>
    </dialog>
  );
}

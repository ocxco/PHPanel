import { useEffect, useRef, useState } from "react";
import { Check, LoaderCircle, X } from "lucide-react";
import { basicSetup } from "codemirror";
import { Compartment, EditorState } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import {
  StreamLanguage,
  HighlightStyle,
  syntaxHighlighting,
} from "@codemirror/language";
import { tags } from "@lezer/highlight";
import { nginx } from "@codemirror/legacy-modes/mode/nginx";
import { properties } from "@codemirror/legacy-modes/mode/properties";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { call, type ConfigDocument, type Instance } from "./api";

function CodeMirror({
  document,
  disabled,
  change,
}: {
  document: ConfigDocument;
  disabled: boolean;
  change: (text: string) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<EditorView | undefined>(undefined);
  const readOnly = useRef(new Compartment());
  const callback = useRef(change);
  callback.current = change;
  useEffect(() => {
    const mode = document.name.endsWith(".ini")
      ? properties
      : document.name === "redis.conf"
        ? shell
        : nginx;
    const view = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: document.content,
        extensions: [
          basicSetup,
          syntaxHighlighting(
            HighlightStyle.define([
              { tag: tags.keyword, color: "var(--syntax-keyword)" },
              { tag: tags.string, color: "var(--syntax-string)" },
              { tag: tags.comment, color: "var(--muted)" },
              { tag: tags.number, color: "var(--syntax-number)" },
              { tag: tags.variableName, color: "var(--text)" },
            ]),
          ),
          StreamLanguage.define(mode),
          readOnly.current.of(EditorState.readOnly.of(disabled)),
          EditorView.contentAttributes.of({
            "aria-label": "配置代码编辑器",
            spellcheck: "false",
          }),
          EditorView.updateListener.of((update) => {
            if (update.docChanged)
              callback.current(update.state.doc.toString());
          }),
          EditorView.theme({
            "&": { height: "440px", fontSize: "13px" },
            ".cm-content": { color: "var(--text)", caretColor: "var(--text)" },
            ".cm-activeLine, .cm-activeLineGutter": {
              backgroundColor: "var(--surface-alt)",
            },
            ".cm-scroller": {
              overflow: "auto",
              fontFamily: "Consolas, monospace",
            },
            "&.cm-focused": { outline: "none" },
          }),
        ],
      }),
    });
    editor.current = view;
    return () => {
      view.destroy();
      editor.current = undefined;
    };
  }, [document.name]);
  useEffect(() => {
    editor.current?.dispatch({
      effects: readOnly.current.reconfigure(EditorState.readOnly.of(disabled)),
    });
  }, [disabled]);
  return <div className="config-code" ref={host} />;
}
export default function ConfigEditor({
  instance,
  initialFile,
  close,
}: {
  instance: Instance;
  initialFile?: string;
  close: () => void;
}) {
  const [names, setNames] = useState<string[]>([]);
  const [document, setDocument] = useState<ConfigDocument>();
  const [content, setContent] = useState("");
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [discard, setDiscard] = useState<{ file?: string } | null>(null);
  const dirty = !!document && content !== document.content;
  async function load(file: string) {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const doc = await call<ConfigDocument>("ReadConfig", instance.id, file);
      setDocument(doc);
      setContent(doc.content);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    let active = true;
    void call<string[]>("ListConfigs", instance.id)
      .then(async (files) => {
        if (!active) return;
        setNames(files);
        await load(initialFile || files[0]);
      })
      .catch((e) => {
        if (active) {
          setError(String(e));
          setBusy(false);
        }
      });
    return () => {
      active = false;
    };
  }, [instance.id]);
  function leave(file?: string) {
    if (busy) return;
    if (dirty) {
      setDiscard({ file });
      return;
    }
    if (file) void load(file);
    else close();
  }
  useEffect(() => {
    const listener = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !e.defaultPrevented) {
        e.preventDefault();
        leave();
      }
    };
    window.addEventListener("keydown", listener);
    return () => window.removeEventListener("keydown", listener);
  }, [busy, dirty]);
  async function save() {
    if (!document) return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const saved = await call<ConfigDocument>(
        "SaveConfig",
        instance.id,
        document.name,
        content,
        document.revision,
      );
      setDocument(saved);
      setMessage(
        instance.component === "nginx"
          ? document.name === "nginx.conf"
            ? "主配置已检查并保存，运行时会自动重载对应实例。"
            : "站点配置已检查并保存，当前运行的 Nginx 会自动重载。"
          : "配置已保存，重启此实例后生效。",
      );
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="modal-backdrop">
      <section
        className="modal config-modal"
        role="dialog"
        aria-modal="true"
        aria-label="内置配置编辑器"
      >
        <div className="modal-heading">
          <h2>{instance.name} · 配置编辑</h2>
          <button
            className="icon-button"
            disabled={busy}
            aria-label="关闭编辑器"
            onClick={() => leave()}
          >
            <X size={20} />
          </button>
        </div>
        <div className="config-toolbar">
          <label className="field">
            配置文件
            <select
              disabled={busy}
              value={document?.name || ""}
              onChange={(e) => leave(e.target.value)}
            >
              {names.map((name) => (
                <option key={name}>{name}</option>
              ))}
            </select>
          </label>
          <span className="muted">
            {dirty ? "有未保存的修改" : "CodeMirror 6 · UTF-8"}
          </span>
        </div>
        {document?.path && (
          <div
            className="instance-path"
            style={{ marginBottom: 10 }}
            title={document.path}
          >
            {document.path}
          </div>
        )}
        {document && (
          <CodeMirror
            key={document.name}
            document={document}
            disabled={busy || !!discard}
            change={setContent}
          />
        )}
        {busy && (
          <p className="field-note">
            <LoaderCircle size={14} className="spin" /> 正在处理配置…
          </p>
        )}
        <p className="field-note">
          {instance.component === "nginx"
            ? "保存前检查整套配置。主配置须保留站点 include 指令；编辑网站表单会重新生成该站点配置并备份旧文件。"
            : "保存后重启实例应用配置，旧文件保存在 .before-edit 备份中。"}{" "}
          Ctrl+F 搜索，Ctrl+Z 撤销。
        </p>
        {error && (
          <p className="inline-error" role="alert">
            {error}
          </p>
        )}
        {message && (
          <p className="field-note" role="status">
            {message}
          </p>
        )}
        {discard ? (
          <div className="config-discard">
            <p>当前修改尚未保存，是否放弃？</p>
            <button
              className="button secondary"
              onClick={() => setDiscard(null)}
            >
              继续编辑
            </button>
            <button
              className="button secondary"
              onClick={() => {
                const next = discard.file;
                setDiscard(null);
                if (next) void load(next);
                else close();
              }}
            >
              放弃修改
            </button>
          </div>
        ) : (
          <div className="modal-actions">
            <button
              className="button secondary"
              disabled={busy}
              onClick={() => leave()}
            >
              关闭
            </button>
            <button
              className="button primary"
              disabled={busy || !dirty}
              onClick={() => void save()}
            >
              <Check size={15} />
              保存配置
            </button>
          </div>
        )}
      </section>
    </div>
  );
}

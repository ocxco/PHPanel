import {
  lazy,
  Suspense,
  useEffect,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import {
  ArrowDownToLine,
  ArrowUpRight,
  Boxes,
  Check,
  CheckCircle2,
  Copy,
  ChevronRight,
  ChevronDown,
  CircleHelp,
  Download,
  FolderOpen,
  Globe2,
  HardDrive,
  Layers3,
  LoaderCircle,
  Network,
  PackagePlus,
  Pause,
  Play,
  Plus,
  Pencil,
  RefreshCw,
  Search,
  Settings2,
  SlidersHorizontal,
  Sun,
  Moon,
  Monitor,
  ShieldCheck,
  Square,
  Terminal,
  Trash2,
  X,
} from "lucide-react";
import {
  call,
  desktop,
  type Artifact,
  type Component,
  type Instance,
  type Package,
  type Settings,
  type Site,
  type CatalogStatus,
  type State,
  type PrivilegeStatus,
  type AdminRequiredDetail,
  type SystemSettings,
  type SystemStatus,
} from "./api";
import { usePanelState } from "./usePanelState";
import ClosePrompt from "./ClosePrompt";
import logoLight from "./assets/logo-light.png";
import logoDark from "./assets/logo-dark.png";

const ConfigEditor = lazy(() => import("./ConfigEditor"));
const PHPExtensions = lazy(() => import("./PHPExtensions"));
const ToolchainPanel = lazy(() => import("./ToolchainPanel"));
const SystemServicePanel = lazy(() => import("./SystemServicePanel"));
const ElevationPrompt = lazy(() => import("./ElevationPrompt"));
const templates: Record<string, string> = {
  php: "通用 PHP",
  laravel: "Laravel",
  thinkphp: "ThinkPHP",
  wordpress: "WordPress",
  static: "静态站点",
};

const components: Component[] = ["php", "nginx", "mysql", "redis"];
const label: Record<Component, string> = {
  php: "PHP",
  nginx: "Nginx",
  mysql: "MySQL",
  redis: "Redis",
};
const subtitle: Record<Component, string> = {
  php: "语言运行时",
  nginx: "Web 服务器",
  mysql: "关系型数据库",
  redis: "内存数据库",
};
const defaults: Record<Component, number> = {
  php: 9000,
  nginx: 0,
  mysql: 3306,
  redis: 6379,
};
const statusLabel: Record<string, string> = {
  stopped: "已停止",
  running: "运行中",
  starting: "启动中",
  stopping: "停止中",
  error: "异常",
	awaiting_restart: "待替换文件",
  downloading: "下载中",
  paused: "已暂停",
  queued: "等待中",
  installing: "安装中",
  verifying: "验证签名中",
  completed: "已完成",
};
type View = "environments" | "instances" | "sites" | "downloads" | "settings";
const nav = [
  { id: "sites" as View, label: "网站管理", icon: Globe2 },
  { id: "instances" as View, label: "服务实例", icon: Layers3 },
  { id: "environments" as View, label: "环境管理", icon: Boxes },
  { id: "downloads" as View, label: "下载中心", icon: Download },
  { id: "settings" as View, label: "偏好设置", icon: Settings2 },
];
type Modal =
  | { kind: "import" }
  | { kind: "custom" }
  | { kind: "instance"; pkg: Package }
  | { kind: "edit-instance"; instance: Instance }
  | { kind: "site"; site?: Site }
  | { kind: "delete-site"; site: Site }
  | { kind: "delete-instance"; instance: Instance }
  | { kind: "sources"; artifact: Artifact }
  | { kind: "config"; instance: Instance; file?: string }
  | { kind: "php-extensions"; instance: Instance }
  | { kind: "system-service"; instance: Instance }
  | { kind: "text"; title: string; text: string }
  | { kind: "remove"; pkg: Package };
function bytes(n: number) {
  if (n <= 0) return "0 B";
  const unit = Math.min(3, Math.floor(Math.log(n) / Math.log(1024)));
  return `${(n / 1024 ** unit).toFixed(unit ? 1 : 0)} ${["B", "KB", "MB", "GB"][unit]}`;
}
function instanceLabel(i: Instance) {
  const version = i.version;
  return i.name.includes(version) ? i.name : `${i.name} · ${version}`;
}
// Merged PHP: a PHP package IS the runtime. View a package as an Instance so
// existing runtime handlers (start/stop, config, extensions, logs) and modals
// can consume it; the Go side resolves the package id via lookupInstance.
function phpPackageAsInstance(p: Package): Instance {
  return {
    id: p.id,
    packageId: p.id,
    name: `PHP ${p.version}`,
    component: "php",
    version: p.version,
    port: p.port,
    path: p.path,
    status: p.status,
    pid: p.pid,
    error: p.error,
    linkedNginxId: p.linkedNginxId,
    linkedRun: p.linkedRun,
    serviceName: p.serviceName,
    serviceAutoStart: p.serviceAutoStart,
    nginxAutoPHP: p.nginxAutoPHP,
    nginxStopPHP: p.nginxStopPHP,
  };
}
function ComponentMark({
  type,
  small = false,
}: {
  type: Component;
  small?: boolean;
}) {
  return (
    <span className={`component-mark ${type} ${small ? "small" : ""}`}>
      {type === "php" ? (
        "php"
      ) : type === "nginx" ? (
        "N"
      ) : type === "mysql" ? (
        "SQL"
      ) : (
        "R"
      )}
    </span>
  );
}
function Badge({ status }: { status: string }) {
  return (
    <span className={`badge ${status}`}>
      <i />
      {statusLabel[status] || status}
    </span>
  );
}
function Empty({
  icon,
  title,
  text,
  action,
}: {
  icon: ReactNode;
  title: string;
  text: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty">
      <div className="empty-icon">{icon}</div>
      <h3>{title}</h3>
      <p>{text}</p>
      {action}
    </div>
  );
}

export default function App() {
  const { state, connectionError, refresh: sync } = usePanelState();
  const [themeMode, setThemeMode] = useState<Settings["themeMode"]>(() => {
    const value = localStorage.getItem("phpanel-theme");
    return value === "light" || value === "dark" ? value : "system";
  });
  useEffect(() => {
    if (state.root) setThemeMode(state.settings.themeMode || "system");
  }, [state.root, state.settings.themeMode]);
  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      const dark =
        themeMode === "dark" || (themeMode === "system" && media.matches);
      document.documentElement.dataset.theme = dark ? "dark" : "light";
      document.documentElement.style.colorScheme = dark ? "dark" : "light";
    };
    apply();
    localStorage.setItem("phpanel-theme", themeMode);
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [themeMode]);
  const [view, setView] = useState<View>("sites"),
    [filter, setFilter] = useState<Component | "all">("all"),
    [tab, setTab] = useState<"catalog" | "installed">("catalog"),
    [search, setSearch] = useState(""),
    [page, setPage] = useState(0);
  const [privilege,setPrivilege]=useState<PrivilegeStatus>({elevated:false});
  const [restartResume,setRestartResume]=useState<PrivilegeStatus>();
  const [restoredDraft,setRestoredDraft]=useState<AdminRequiredDetail>();
  const [elevationPrompt,setElevationPrompt]=useState<(AdminRequiredDetail & {view:View;elevated:boolean})>();
  const [closePrompt, setClosePrompt] = useState(false);
  useEffect(() => {
    if (!desktop()) return;
    let active = true;
    const unsubscribe = window.runtime?.EventsOnMultiple?.(
      "desktop:close-choice", () => { if (active) setClosePrompt(true); }, -1,
    );
    void call<boolean>("GetPendingCloseChoice").then(pending => {
      if (active && pending) setClosePrompt(true);
    }).catch(() => {});
    return () => { active = false; unsubscribe?.(); };
  }, []);
  useEffect(()=>{
    if(!desktop())return;
    void call<PrivilegeStatus>("GetPrivilegeStatus").then(status=>{setPrivilege(status);if(status.returnView)setRestartResume(status);}).catch(()=>{});
  },[]);
  useEffect(()=>{
    if(!restartResume || !state.root)return;
    if(nav.some(n=>n.id===restartResume.returnView))setView(restartResume.returnView as View);
    const instance=state.instances.find(i=>i.id===restartResume.instanceId);
    if(instance&&instance.component!=="php"){setModal({kind:"system-service",instance});setRestoredDraft({instanceId:instance.id,serviceAutoStart:restartResume.serviceAutoStart});}
    setRestartResume(undefined);
  },[restartResume,state.root,state.instances]);
  useEffect(()=>{
    const required=(event:Event)=>{const detail=(event as CustomEvent<AdminRequiredDetail>).detail;
      setElevationPrompt({...detail,view,elevated:privilege.elevated});
      void call<PrivilegeStatus>("GetPrivilegeStatus").then(status=>{setPrivilege(status);setElevationPrompt(current=>current?{...current,elevated:status.elevated}:current);}).catch(()=>{});
    };
    window.addEventListener("phpanel:admin-required",required);
    return()=>window.removeEventListener("phpanel:admin-required",required);
  },[view,privilege.elevated]);
  useEffect(() => setPage(0), [filter, search, tab]);
  const [modal, setModal] = useState<Modal | null>(null),
    [busy, setBusy] = useState(""),
    [notice, setNotice] = useState<{ text: string; error: boolean } | null>(
      null,
    );
  useEffect(() => {
    if (!notice) return;
    const timer = setTimeout(
      () => setNotice(null),
      notice.error ? 12000 : 6000,
    );
    return () => clearTimeout(timer);
  }, [notice]);
  async function action(
    key: string,
    work: () => Promise<unknown>,
    success?: string,
  ) {
    if (busy) return;
    setBusy(key);
    try {
      await work();
      await sync();
      if (success) setNotice({ text: success, error: false });
    } catch (e) {
      setNotice({ text: String(e), error: true });
    } finally {
      setBusy("");
    }
  }
  const phpRuntimes = state.packages.filter((p) => p.component === "php");
  const serviceInstances = state.instances.filter(
    (i) => i.component !== "php",
  );
  // All "运行实例": legacy instances + PHP packages (package == runtime).
  // The UI treats them uniformly in the 实例管理 view.
  const allInstances = [
    ...serviceInstances,
    ...phpRuntimes.map(phpPackageAsInstance),
  ];
  const running = allInstances.filter((i) => i.status === "running").length;
  const activeDownloads = state.tasks.filter((t) =>
    ["downloading", "installing", "verifying"].includes(t.status),
  ).length;
  const findPackage = (a: Artifact) =>
    state.packages.find(
      (p) =>
        p.id === a.id ||
        (Boolean(a.sha256) &&
          p.component === a.component &&
          p.sha256 === a.sha256),
    );
  const artifacts = state.artifacts
    .filter(
      (a) =>
        (filter === "all" || a.component === filter) &&
        `${a.component} ${a.version}`
          .toLowerCase()
          .includes(search.toLowerCase()),
    )
    .sort(
      (a, b) =>
        Number(Boolean(findPackage(b))) - Number(Boolean(findPackage(a))) ||
        a.component.localeCompare(b.component) ||
        b.version.localeCompare(a.version, undefined, { numeric: true }),
    );
  const packages = state.packages.filter(
    (p) =>
      (filter === "all" || p.component === filter) &&
      `${p.component} ${p.version}`
        .toLowerCase()
        .includes(search.toLowerCase()),
  );
  const currentNav = nav.find((n) => n.id === view)!;
  const pageCount = Math.max(1, Math.ceil(artifacts.length / 30));
  function install(a: Artifact) {
    void action(
      `install-${a.id}`,
      async () => {
        await call("Install", a.id);
        setView("downloads");
      },
      "下载任务已创建",
    );
  }
  function instanceButton(pkg: Package) {
    if (pkg.component === "php") {
      return (
        <button
          className="button compact secondary"
          onClick={() => {
            setView("instances");
          }}
        >
          <Layers3 size={14} /> 管理实例
        </button>
      );
    }
    const hasInstance = state.instances.some((i) => i.packageId === pkg.id);
    return (
      <button
        className="button compact secondary"
        disabled={!!busy}
        onClick={() =>
          hasInstance
            ? setView("instances")
            : setModal({ kind: "instance", pkg })
        }
      >
        {hasInstance ? <Layers3 size={14} /> : <Plus size={14} />}
        {hasInstance ? "查看实例" : "创建实例"}
      </button>
    );
  }
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <a
          className="brand"
          href="#"
          aria-label="PHPanel · 环境管理"
          onClick={(e) => {
            e.preventDefault();
            setView("environments");
          }}
        >
          <img className="brand-logo brand-logo-light" src={logoLight} alt="PHPanel" width={300} height={132} draggable={false} />
          <img className="brand-logo brand-logo-dark" src={logoDark} alt="PHPanel" width={300} height={132} draggable={false} />
          <span className="brand-version">v{__APP_VERSION__}</span>
        </a>
        <div className="workspace-card" aria-label="当前工作空间">
          <span><Globe2 size={15} /></span>
          <div>
            <strong>本地工作空间</strong>
            <small>{desktop() ? "Windows · x64" : "浏览器预览"}</small>
          </div>
          <ChevronDown size={14} />
        </div>
        <div className="workspace-label">
          工作空间 <span>01</span>
        </div>
        <nav>
          {nav.map((n) => (
            <button
              key={n.id}
              className={view === n.id ? "active" : ""}
              onClick={() => setView(n.id)}
            >
              <n.icon size={18} />
              {n.label}
              {n.id === "downloads" && activeDownloads > 0 && (
                <span className="nav-count">{activeDownloads}</span>
              )}
              {view === n.id && <span className="nav-active-dot" />}
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <div className="local-status">
            <span className="pulse-dot" />
            <div>
              本地工作空间
              <small>{desktop() ? "Windows · x64" : "浏览器预览"}</small>
            </div>
            <HardDrive size={16} />
          </div>
          <div className="sidebar-foot">
            <span>PHPanel</span>
            <span>v{__APP_VERSION__}</span>
          </div>
        </div>
      </aside>
      <main>
        <header className="topbar">
          <div>
            <span>工作空间</span>
            <ChevronRight size={14} />
            <strong>{currentNav.label}</strong>
          </div>
          <div className="topbar-right">
            <button className="button secondary compact" disabled={!!busy || privilege.elevated} title={privilege.elevated?"当前以管理员身份运行":"通过 Windows UAC 授权并重启"} onClick={()=>setElevationPrompt({view,instanceId:modal?.kind==="system-service"?modal.instance.id:"",elevated:false})}><ShieldCheck size={14}/>{privilege.elevated?"管理员":"以管理员重启"}</button>
            <span className="platform">
              <span className="windows-icon">⊞</span> Windows x64
            </span>
            <button
              className="icon-button"
              aria-label="查看帮助"
              onClick={() =>
                setModal({
                  kind: "text",
                  title: "开始使用 PHPanel",
                  text: "1. 在环境管理中下载安装包，或导入本地 ZIP。\n2. 为安装的版本创建实例，配置独立端口。\n3. 启动 PHP 和 Nginx，创建网站后启动对应 Nginx。\n\n国内网络：可在偏好设置中配置代理，在版本的“下载来源”中添加同一文件的镜像。所有在线下载都需要匹配 SHA-256。\n\nMySQL 数据与程序分开保存，不提供跨大版本原地替换。Redis 使用 redis-windows 社区构建。",
                })
              }
            >
              <CircleHelp size={18} />
            </button>
          </div>
        </header>
        <div className="content">
          {!desktop() && (
            <div className="preview-banner">
              <Network size={16} /> 浏览器界面预览 ·
              运行桌面程序后可管理本机环境。这里没有模拟服务数据。
            </div>
          )}
          {connectionError && (
            <div className="error-banner">{connectionError}</div>
          )}
          <section className="page-heading">
            <div>
              <div className="eyebrow">YOUR LOCAL WORKSPACE</div>
              <h1>
                {view === "environments" ? "环境管理" : currentNav.label}
                <span className="heading-dot">.</span>
              </h1>
              <p>
                {
                  {
                    environments:
                      "版本自由选择，环境各自独立。让开发从容开始。",
                    instances:
                      "Nginx 所有版本合计只能运行一个实例；其它组件每个已安装版本只能运行一个实例。",
                    sites: "把项目连接到合适的环境，一键打开本地网站。",
                    downloads: "查看每一次下载的来源、进度和校验结果。",
                    settings: "为你的网络和工作习惯，调整本地环境。",
                  }[view]
                }
              </p>
            </div>
            <div className="heading-actions">
              {view === "environments" && (
                <>
                  <button
                    className="button secondary"
                    disabled={!!busy}
                    onClick={() => setModal({ kind: "import" })}
                  >
                    <FolderOpen size={16} />
                    离线导入
                  </button>
                  <button
                    className="button primary"
                    onClick={() => setModal({ kind: "custom" })}
                  >
                    <Plus size={16} />
                    添加版本
                  </button>
                </>
              )}
              {view === "sites" && (
                <button
                  className="button primary"
                  onClick={() => setModal({ kind: "site" })}
                >
                  <Plus size={16} />
                  创建网站
                </button>
              )}
            </div>
          </section>
          {view === "environments" && (
            <>
              <Suspense fallback={null}><ToolchainPanel state={state} disabled={!!busy} changed={sync} /></Suspense>
              <div className="catalog-status">
                <RefreshCw
                  size={14}
                  className={state.catalog.refreshing ? "spin" : ""}
                />
                <span>
                  {state.catalog.refreshing
                    ? "正在检查各组件的新版本…"
                    : state.catalog.lastSuccess
                      ? `目录更新于 ${new Date(state.catalog.lastSuccess).toLocaleString("zh-CN")}`
                      : "显示本地缓存目录"}
                </span>
                {state.catalog.warnings.length > 0 && (
                  <button
                    className="text-button"
                    onClick={() =>
                      setModal({
                        kind: "text",
                        title: "目录更新信息",
                        text: state.catalog.warnings.join("\n"),
                      })
                    }
                  >
                    部分来源暂不可用 · 查看详情
                  </button>
                )}
              </div>
              <div className="overview-grid">
                {components.map((c) => (
                  <button
                    key={c}
                    className={`overview-card ${filter === c ? "selected" : ""}`}
                    onClick={() => setFilter(filter === c ? "all" : c)}
                  >
                    <div className="overview-card-top">
                      <ComponentMark type={c} small />
                      <ArrowUpRight size={17} />
                    </div>
                    <div className="overview-title">
                      {label[c]}
                      <span>{subtitle[c]}</span>
                    </div>
                    <div className="overview-card-bottom">
                      <span>
                        <b>
                          {
                            state.packages.filter((p) => p.component === c)
                              .length
                          }
                        </b>{" "}
                        个已安装版本
                      </span>
                      <span
                        className={
                          allInstances.some(
                            (i) => i.component === c && i.status === "running",
                          )
                            ? "running-note"
                            : "quiet-note"
                        }
                      >
                        <i />
                        {
                          allInstances.filter(
                            (i) => i.component === c && i.status === "running",
                          ).length
                        }{" "}
                        运行中
                      </span>
                    </div>
                  </button>
                ))}
              </div>
              <section className="panel">
                <div className="panel-toolbar">
                  <div className="tabs">
                    <button
                      className={tab === "catalog" ? "selected" : ""}
                      onClick={() => setTab("catalog")}
                    >
                      可用版本 <span>{state.artifacts.length}</span>
                    </button>
                    <button
                      className={tab === "installed" ? "selected" : ""}
                      onClick={() => setTab("installed")}
                    >
                      已安装 <span>{state.packages.length}</span>
                    </button>
                  </div>
                  <button
                    className="subtle-button"
                    disabled={!!busy || state.catalog.refreshing}
                    onClick={() =>
                      void action(
                        "refresh",
                        async () => {
                          const warnings =
                            await call<string[]>("RefreshCatalog");
                          if (warnings?.length)
                            throw new Error(warnings.join("\n"));
                        },
                        "各组件版本目录已更新",
                      )
                    }
                  >
                    <RefreshCw
                      size={14}
                      className={
                        busy === "refresh" || state.catalog.refreshing
                          ? "spin"
                          : ""
                      }
                    />
                    刷新目录
                  </button>
                </div>
                <div className="filter-row">
                  <div className="filter-chips">
                    {(["all", ...components] as const).map((c) => (
                      <button
                        key={c}
                        className={filter === c ? "selected" : ""}
                        onClick={() => setFilter(c)}
                      >
                        {c === "all" ? "全部组件" : label[c]}
                      </button>
                    ))}
                  </div>
                  <label className="search">
                    <Search size={15} />
                    <input
                      aria-label="搜索版本"
                      placeholder="搜索组件或版本…"
                      value={search}
                      onChange={(e) => setSearch(e.target.value)}
                    />
                  </label>
                </div>
                {tab === "catalog" ? (
                  <>
                    <div className="table-scroll">
                      <table>
                        <thead>
                          <tr>
                            <th>组件 / 版本</th>
                            <th>发行来源</th>
                            <th>完整性校验</th>
                            <th>状态</th>
                            <th className="align-right">操作</th>
                          </tr>
                        </thead>
                        <tbody>
                          {artifacts
                            .slice(page * 30, (page + 1) * 30)
                            .map((a) => {
                              const installed = Boolean(findPackage(a)),
                                task = state.tasks.find(
                                  (t) =>
                                    t.artifactId === a.id &&
                                    [
                                      "downloading",
                                      "installing",
                                      "paused",
                                      "verifying",
                                    ].includes(t.status),
                                );
                              return (
                                <tr key={a.id}>
                                  <td>
                                    <div className="version-cell">
                                      <ComponentMark type={a.component} small />
                                      <div>
                                        <strong>
                                          {label[a.component]}{" "}
                                          <span className="mono">
                                            {a.version}
                                          </span>
                                        </strong>
                                        <small>{a.build}</small>
                                      </div>
                                    </div>
                                  </td>
                                  <td>
                                    <span className="publisher">
                                      {a.publisher}
                                    </span>
                                    <button
                                      className="text-button source-button"
                                      onClick={() =>
                                        setModal({
                                          kind: "sources",
                                          artifact: a,
                                        })
                                      }
                                    >
                                      下载来源 <ChevronRight size={12} />
                                    </button>
                                  </td>
                                  <td>
                                    <span className="verified">
                                      <ShieldCheck size={14} />
                                      {a.sha256 ? "SHA-256" : "官方签名"}
                                    </span>
                                    <small className="cell-note">
                                      {a.verification}
                                    </small>
                                  </td>
                                  <td>
                                    {installed ? (
                                      <span className="installed-status">
                                        <CheckCircle2 size={14} />
                                        已安装
                                      </span>
                                    ) : task ? (
                                      <Badge status={task.status} />
                                    ) : (
                                      <span className="muted">可安装</span>
                                    )}
                                  </td>
                                  <td className="align-right">
                                    {installed ? (
                                      instanceButton(findPackage(a)!)
                                    ) : task ? (
                                      <button
                                        className="button compact secondary"
                                        onClick={() => setView("downloads")}
                                      >
                                        查看任务
                                      </button>
                                    ) : (
                                      <button
                                        className="button compact install"
                                        disabled={!!busy}
                                        onClick={() => install(a)}
                                      >
                                        <ArrowDownToLine size={14} />
                                        安装
                                      </button>
                                    )}
                                  </td>
                                </tr>
                              );
                            })}
                        </tbody>
                      </table>
                    </div>
                    {artifacts.length === 0 && (
                      <Empty
                        icon={<Boxes size={25} />}
                        title={
                          desktop()
                            ? "未找到匹配版本"
                            : "在桌面程序中加载版本目录"
                        }
                        text="支持在线目录、自定义下载地址和本地 ZIP 安装包。"
                      />
                    )}
                  </>
                ) : (
                  <>
                    {packages.map((p) => (
                        <div className="package-row" key={p.id}>
                          <ComponentMark type={p.component} />
                          <div className="package-details">
                            <strong>
                              {label[p.component]}{" "}
                              <span className="mono">{p.version}</span>
                            </strong>
                            <span className="package-path">{p.path}</span>
                          </div>
                          <span className="verified">
                            <ShieldCheck size={14} />
                            {p.verification}
                          </span>
                          <button
                            className="subtle-button"
                            disabled={!!busy}
                            onClick={() =>
                              void action(`package-dir-${p.id}`, () =>
                                call("OpenLocation", "package", p.id),
                              )
                            }
                          >
                            <FolderOpen size={15} />
                            目录
                          </button>
                          {instanceButton(p)}
                          <button
                            className="icon-button danger"
                            aria-label={`卸载 ${label[p.component]} ${p.version}`}
                            onClick={() => setModal({ kind: "remove", pkg: p })}
                          >
                            <Trash2 size={16} />
                          </button>
                        </div>
                      ))}
                    {packages.length === 0 && (
                      <Empty
                        icon={<PackagePlus size={25} />}
                        title="从第一套环境开始"
                        text="在可用版本中安装所需组件，也可以导入已有的安装包。"
                        action={
                          <button
                            className="button secondary"
                            onClick={() => setTab("catalog")}
                          >
                            浏览可用版本
                            <ChevronRight size={14} />
                          </button>
                        }
                      />
                    )}
                  </>
                )}
                <div className="panel-foot">
                  <ShieldCheck size={14} />
                  <span>下载完成后核对摘要，再安装到独立目录</span>
                  <span className="foot-right">
                    {tab === "catalog" ? artifacts.length : packages.length}{" "}
                    个版本
                  </span>
                  {tab === "catalog" && pageCount > 1 && (
                    <>
                      <button
                        className="text-button"
                        disabled={page === 0}
                        onClick={() => setPage((p) => p - 1)}
                      >
                        上一页
                      </button>
                      <span>
                        {page + 1}/{pageCount}
                      </span>
                      <button
                        className="text-button"
                        disabled={page + 1 >= pageCount}
                        onClick={() => setPage((p) => p + 1)}
                      >
                        下一页
                      </button>
                    </>
                  )}
                </div>
              </section>
              <div className="network-note">
                <span className="network-note-icon">
                  <Network size={20} />
                </span>
                <div>
                  <strong>为不同的网络环境，保留更多选择。</strong>
                  <p>配置代理、添加同一安装包的镜像，或直接离线导入。</p>
                </div>
                <button
                  className="text-button"
                  onClick={() => setView("settings")}
                >
                  网络设置
                  <ArrowUpRight size={14} />
                </button>
              </div>
            </>
          )}
          {view === "instances" && (
            <section className="panel">
              <div className="section-label">
                <h2>
                  服务实例 <span>{allInstances.length}</span>
                </h2>
                <span className="running-note">
                  <i />
                  {running} 个正在运行
                </span>
              </div>
              {allInstances.length === 0 ? (
                <Empty
                  icon={<Layers3 size={26} />}
                  title="还没有运行实例"
                  text="下载并安装组件版本后，会自动出现在这里。"
                  action={
                    <button
                      className="button primary"
                      onClick={() => {
                        setView("environments");
                        setTab("installed");
                      }}
                    >
                      管理已安装版本
                      <ChevronRight size={14} />
                    </button>
                  }
                />
              ) : (
                components.map((component) => {
                  const group = allInstances
                    .filter((i) => i.component === component)
                    .sort((a, b) =>
                      b.version.localeCompare(a.version, undefined, {
                        numeric: true,
                      }),
                    );
                  if (!group.length) return null;
                  return (
                    <section className="service-group" key={component}>
                      <div className="service-group-heading">
                        <h3>
                          <ComponentMark type={component} small />
                          {label[component]}
                        </h3>
                        <span>
                          {group.length} 个实例 ·{" "}
                          {group.filter((i) => i.status === "running").length}{" "}
                          运行中
                        </span>
                      </div>
                      <div className="instance-grid">
                        {group.map((i) => (
                          <article className="instance-card" key={i.id}>
                            <div className="instance-card-top">
                              <ComponentMark type={i.component} small />
                              <div className="instance-info">
                                <strong>
                                  {i.name}
                                  <Badge status={i.status} />
                                  {i.name !==
                                    `${label[i.component]} ${i.version}` && (
                                    <span className="mono">
                                      {label[i.component]} {i.version}
                                    </span>
                                  )}
                                </strong>
                                <small>
                                  {i.component === "nginx" ? (
                                    "监听端口由站点管理"
                                  ) : (
                                    <>
                                      127.0.0.1:
                                      <span className="mono">{i.port}</span>
                                    </>
                                  )}
                                  {i.pid > 0 && ` · PID ${i.pid}`}
                                  {i.serviceName && ` · 系统服务${i.serviceAutoStart ? " · 开机启动" : ""}`}
                                  {i.linkedNginxId && " · Nginx 联动"}
                                </small>
                              </div>
                              <div className="instance-card-power">
                                <button
                                  className={`button compact ${i.status === "running" ? "stop" : "start"}`}
                                  disabled={
                                    !!busy ||
                                    ["starting", "stopping"].includes(i.status) ||
                                    (i.status !== "running" &&
                                      i.component !== "php" &&
                                      state.instances.some(
                                        (other) =>
                                          other.id !== i.id &&
                                          (i.component === "nginx"
                                            ? other.component === "nginx"
                                            : other.packageId === i.packageId) &&
                                          [
                                            "running",
                                            "starting",
                                            "stopping",
                                          ].includes(other.status),
                                      ))
                                  }
                                  onClick={() =>
                                    void action(
                                      i.id,
                                      () =>
                                        call(
                                          i.status === "running"
                                            ? "StopInstance"
                                            : "StartInstance",
                                          i.id,
                                        ),
                                      i.status === "running"
                                        ? "实例已停止"
                                        : "实例已启动",
                                    )
                                  }
                                >
                                  {busy === i.id ? (
                                    <LoaderCircle size={12} className="spin" />
                                  ) : null}
                                  {i.status === "running" ? "停止" : "启动"}
                                </button>
                              </div>
                            </div>
                            <div className="folder-links">
                              {i.component !== "php" && <button className="text-button" disabled={!!busy} onClick={() => setModal({kind:"system-service",instance:i})}>{i.component === "nginx" ? "启动设置" : "系统服务"}</button>}
                              {i.component !== "nginx" && <button className="text-button" disabled={!!busy || ["running", "starting", "stopping"].includes(i.status)} onClick={() => setModal({kind:"edit-instance",instance:i})} title="停止实例后修改端口">修改端口</button>}
                              {i.component === "php" && (
                                <button className="text-button" disabled={!!busy || i.status === "starting" || i.status === "stopping"}
                                  onClick={() => setModal({ kind: "php-extensions", instance: i })}>
                                  扩展管理
                                </button>
                              )}
                              <button
                                className="text-button"
                                disabled={!!busy}
                                onClick={() =>
                                  setModal({ kind: "config", instance: i })
                                }
                              >
                                编辑配置
                              </button>
                              {[
                                { kind: "instance", label: "实例目录" },
                                { kind: "data", label: "数据" },
                                { kind: "logs", label: "日志文件夹" },
                              ].map((location) => (
                                <button
                                  key={location.kind}
                                  className="text-button"
                                  disabled={!!busy}
                                  onClick={() =>
                                    void action(
                                      `${location.kind}-${i.id}`,
                                      () =>
                                        call(
                                          "OpenLocation",
                                          location.kind,
                                          i.id,
                                        ),
                                    )
                                  }
                                >
                                  {location.label}
                                </button>
                              ))}
                            </div>
                            {i.error && (
                              <span className="inline-error">{i.error.replace("[ADMIN_REQUIRED]", "").trim()}</span>
                            )}
                            <div className="instance-footer">
                              <button
                                className="subtle-button"
                                disabled={!!busy}
                                onClick={() =>
                                  void action("logs", async () =>
                                    setModal({
                                      kind: "text",
                                      title: `${i.name} · 日志`,
                                      text: await call<string>("ReadLog", i.id),
                                    }),
                                  )
                                }
                              >
                                <Terminal size={12} />
                                日志
                              </button>
                              {i.component === "mysql" && (
                                <button
                                  className="subtle-button"
                                  onClick={() =>
                                    void action("credentials", async () =>
                                      setModal({
                                        kind: "text",
                                        title: `${i.name} · 数据库凭据`,
                                        text: await call<string>(
                                          "Credentials",
                                          i.id,
                                        ),
                                      }),
                                    )
                                  }
                                >
                                  凭据
                                </button>
                              )}
                              {i.component === "nginx" && state.sites.some(site => site.template !== "static" && site.phpId) && <button className="subtle-button" disabled={!!busy || ["starting", "stopping"].includes(i.status)} title="停止 Nginx 和本次联动启动的 PHP；复用的 PHP 保留" onClick={() => void action(`stop-linked-${i.id}`, () => call("StopNginxWithPHP", i.id), "Nginx 和本次联动 PHP 已停止，复用的 PHP 已保留")}>{i.status === "running" ? "一起停止" : "停止联动 PHP"}</button>}
                              <div className="instance-card-tail">
                                <span className="instance-status">
                                  {["running", "starting"].includes(i.status)
                                    ? "响应正常"
                                    : "未在运行"}
                                </span>
                                {i.component !== "php" ? (
                                  <button
                                    className="icon-button danger"
                                    disabled={
                                      !!busy ||
                                      !!i.serviceName ||
                                      ["running", "starting", "stopping"].includes(
                                        i.status,
                                      )
                                    }
                                    aria-label={`删除实例 ${i.name}`}
                                    title={
                                      i.serviceName ? "请先停止并注销系统服务" : ["running", "starting", "stopping"].includes(
                                        i.status,
                                      )
                                        ? "请先停止实例"
                                        : "删除实例及其配置、日志和数据"
                                    }
                                    onClick={() =>
                                      setModal({
                                        kind: "delete-instance",
                                        instance: i,
                                      })
                                    }
                                  >
                                    <Trash2 size={14} />
                                  </button>
                                ) : (
                                  <button
                                    className="icon-button danger"
                                    disabled={
                                      !!busy ||
                                      ["running", "starting", "stopping"].includes(
                                        i.status,
                                      )
                                    }
                                    aria-label={`卸载 PHP ${i.version}`}
                                    title={
                                      ["running", "starting", "stopping"].includes(i.status)
                                        ? "请先停止 PHP"
                                        : "卸载此 PHP 版本，所有配置和扩展一并移除"
                                    }
                                    onClick={() => {
                                      const pkg = state.packages.find((p) => p.id === i.id);
                                      if (pkg) setModal({ kind: "remove", pkg });
                                    }}
                                  >
                                    <Trash2 size={14} />
                                  </button>
                                )}
                              </div>
                            </div>
                          </article>
                        ))}
                      </div>
                    </section>
                  );
                })
              )}
            </section>
          )}
          {view === "sites" && (
            <>
              <div className="hint">
                <Globe2 size={17} /> 使用 .localhost 域名，无需修改
                hosts。网站变更会先检查配置，运行中的 Nginx 会自动重载。
              </div>
              <section className={`panel${state.sites.length ? " site-list" : ""}`}>
                {state.sites.length === 0 ? (
                  <Empty
                    icon={<Globe2 size={26} />}
                    title="让第一个项目在本地运行"
                    text="选择 Nginx 和 PHP 实例，连接已有目录或创建欢迎页面。"
                    action={
                      <button
                        className="button primary"
                        onClick={() => setModal({ kind: "site" })}
                      >
                        <Plus size={15} />
                        创建网站
                      </button>
                    }
                  />
                ) : (
                  state.sites.map((s) => (
                    <div className="site-row" key={s.id}>
                      <span className="site-icon">
                        <Globe2 size={23} />
                      </span>
                      <div className="site-info">
                        <strong>
                          {s.name}
                          <small>{s.url}</small>
                        </strong>
                        <span>{s.root}</span>
                      </div>
                      <span className="site-tags">
                        <span className="tag">
                          {templates[s.template] || "通用 PHP"}
                        </span>
                        {s.template !== "static" && (
                          <span className="tag php">
                            PHP{" "}
                            {
                              state.packages.find((p) => p.id === s.phpId)
                                ?.version ||
                              state.instances.find((i) => i.id === s.phpId)
                                ?.version
                            }
                          </span>
                        )}
                      </span>
                      <button
                        className="button compact ghost"
                        disabled={!!busy}
                        onClick={() => {
                          const instance =
                            state.instances.find(
                              (i) =>
                                i.component === "nginx" &&
                                i.status === "running",
                            ) ||
                            state.instances.find(
                              (i) =>
                                i.id === s.nginxId && i.component === "nginx",
                            ) ||
                            state.instances.find(
                              (i) => i.component === "nginx",
                            );
                          if (instance)
                            setModal({
                              kind: "config",
                              instance,
                              file: `sites/${s.id}.conf`,
                            });
                        }}
                      >
                        <SlidersHorizontal size={13} />
                        配置
                      </button>
                      <button
                        className="button compact secondary"
                        disabled={!!busy}
                        onClick={() => setModal({ kind: "site", site: s })}
                      >
                        <Pencil size={14} />
                        编辑
                      </button>
                      <button
                        className="icon-button danger"
                        disabled={!!busy}
                        aria-label={`删除网站 ${s.name}`}
                        title="删除网站记录，保留目录文件"
                        onClick={() =>
                          setModal({ kind: "delete-site", site: s })
                        }
                      >
                        <Trash2 size={16} />
                      </button>
                      <button
                        className="button compact site-open"
                        onClick={() =>
                          void action(s.id, () => call("OpenSite", s.id))
                        }
                      >
                        打开网站
                        <ArrowUpRight size={14} />
                      </button>
                    </div>
                  ))
                )}
              </section>
            </>
          )}
          {view === "downloads" && (
            <section className="panel">
              <div className="section-label">
                <h2>
                  下载任务 <span>{state.tasks.length}</span>
                </h2>
                <span className="muted">最多同时下载 2 个任务</span>
              </div>
              {state.tasks.length === 0 ? (
                <Empty
                  icon={<Download size={26} />}
                  title="下载中心很安静"
                  text="安装版本后，来源、进度和校验结果会显示在这里。"
                  action={
                    <button
                      className="button secondary"
                      onClick={() => setView("environments")}
                    >
                      选择版本
                      <ChevronRight size={14} />
                    </button>
                  }
                />
              ) : (
                state.tasks
                  .slice()
                  .reverse()
                  .map((t) => (
                    <div className="download-row" key={t.id}>
                      <ComponentMark type={t.component} />
                      <div className="download-info">
                        <div>
                          <strong>
                            {t.kind === "composer" ? "Composer" : t.kind === "php-extension" ? `PHP 扩展 · ${t.extensionName}` : label[t.component]}{" "}
                            <span className="mono">{t.version}</span>
                          </strong>
                          <Badge status={t.status} />
                        </div>
                        <p>
                          {t.source || "等待选择来源"}
                          <span>
                            {bytes(t.received)}
                            {t.total > 0 && ` / ${bytes(t.total)}`}
                            {t.status === "downloading" &&
                              ` · ${bytes(t.speed)}/s`}
                          </span>
                        </p>
                        <div className={`progress-track ${t.status}`}>
                          <span
                            style={{
                              width: `${t.status === "completed" ? 100 : t.total > 0 ? Math.min(100, (t.received / t.total) * 100) : 0}%`,
                            }}
                          />
                        </div>
                        {t.error && (
                          <span className="inline-error">{t.error}</span>
                        )}
						{t.warning && <span className="field-note">{t.warning}</span>}
                      </div>
                      {t.status === "downloading" ? (
                        <button
                          className="icon-button"
                          disabled={!!busy}
                          aria-label="暂停下载"
                          onClick={() =>
                            void action(t.id, () => call("PauseDownload", t.id))
                          }
                        >
                          <Pause size={18} />
                        </button>
                      ) : ["paused", "error"].includes(t.status) ? (
                        <button
                          className="button compact secondary"
                          disabled={!!busy}
                          onClick={() =>
                            void action(t.id, () =>
                              call("ResumeDownload", t.id),
                            )
                          }
                        >
                          <Play size={14} />
                          {t.status === "error" ? "重试" : "继续"}
                        </button>
                      ) : t.status === "awaiting_restart" ? (
                        <button className="button compact secondary" disabled={!!busy}
                          onClick={() => { const i = state.instances.find(x => x.id === t.instanceId); const pkg = state.packages.find(p => p.id === t.instanceId); const target = i || (pkg ? phpPackageAsInstance(pkg) : undefined); if (target) setModal({kind:"php-extensions",instance:target}); }}>
                          管理并应用
                        </button>
                      ) : t.status === "completed" ? (
                        <>
                          <button
                            className="button compact secondary"
                            disabled={!!busy}
                            onClick={() =>
                              void action(`archive-${t.id}`, () =>
                                call("OpenLocation", "archive", t.id),
                              )
                            }
                          >
                            <FolderOpen size={14} />
                            定位安装包
                          </button>
                          <CheckCircle2 size={21} className="success-icon" />
                        </>
                      ) : (
                        <LoaderCircle size={21} className="spin" />
                      )}
                    </div>
                  ))
              )}
            </section>
          )}
          {view === "settings" && (
            <SettingsPanel
              settings={state.settings}
              themeMode={themeMode}
              changeTheme={(mode) => {
                setThemeMode(mode);
                if (!desktop()) return;
                void action(
                  "theme",
                  async () => {
                    try {
                      await call("SaveTheme", mode);
                    } catch (e) {
                      setThemeMode(state.settings.themeMode || "system");
                      throw e;
                    }
                  },
                  "外观设置已保存",
                );
              }}
              catalog={state.catalog}
              root={state.root}
              disabled={!!busy}
              openLocation={(kind) =>
                void action(`open-${kind}`, () =>
                  call("OpenLocation", kind, ""),
                )
              }
              copyRoot={() =>
                void action(
                  "copy-root",
                  () => call("CopyLocation", "root", ""),
                  "数据目录路径已复制",
                )
              }
              save={(s) =>
                void action(
                  "settings",
                  () => call("SaveSettings", s),
                  "设置已保存",
                )
              }
              saveSystem={(settings)=>void action("system-settings",()=>call("SaveSystemSettings",settings),"系统设置已保存")}
            />
          )}
          <footer className="content-footer">
            <span>
              <span className="tiny-dot" /> 所有环境与数据保存在本机
            </span>
            <span>
              {state.packages.length} 个版本 · {state.instances.length} 个实例
            </span>
          </footer>
        </div>
      </main>
      {notice && (
        <div role="status" className={`toast ${notice.error ? "error" : ""}`}>
          {notice.error ? <CircleHelp size={18} /> : <CheckCircle2 size={18} />}
          <span>{notice.text}</span>
          <button aria-label="关闭提示" onClick={() => setNotice(null)}>
            <X size={16} />
          </button>
        </div>
      )}
      {modal?.kind === "config" ? (
        <Suspense
          fallback={
            <div className="modal-backdrop">
              <div className="modal">正在加载配置编辑器…</div>
            </div>
          }
        >
          <ConfigEditor
            instance={modal.instance}
            initialFile={modal.file}
            close={() => setModal(null)}
          />
        </Suspense>
      ) : modal?.kind === "system-service" ? (
        <Suspense fallback={<div className="modal-backdrop"><div className="modal">正在加载系统服务…</div></div>}>
          <SystemServicePanel instance={state.instances.find(i => i.id === modal.instance.id) || modal.instance} restoreAutoStart={restoredDraft?.instanceId===modal.instance.id?restoredDraft.serviceAutoStart:undefined} close={() => {setModal(null);setRestoredDraft(undefined);}} changed={sync} />
        </Suspense>
      ) : modal?.kind === "php-extensions" ? (
        <Suspense fallback={<div className="modal-backdrop"><div className="modal">正在加载扩展管理…</div></div>}>
          <PHPExtensions instance={modal.instance} state={state} close={() => setModal(null)}
            editConfig={() => setModal({ kind: "config", instance: modal.instance })} />
        </Suspense>
      ) : (
        modal && (
          <ModalView
            modal={modal}
            state={state}
            busy={!!busy}
            close={() => setModal(null)}
            submit={(key, work, success) =>
              void action(
                key,
                async () => {
                  await work();
                  setModal(null);
                },
                success,
              )
            }
          />
        )
      )}
      {elevationPrompt&&<Suspense fallback={<div className="modal-backdrop elevation-backdrop"><div className="modal">正在准备授权提示…</div></div>}><ElevationPrompt {...elevationPrompt} close={()=>setElevationPrompt(undefined)}/></Suspense>}
      {closePrompt && <ClosePrompt close={() => setClosePrompt(false)} />}
    </div>
  );
}

function SettingsPanel({
  settings,
  themeMode,
  changeTheme,
  catalog,
  root,
  disabled,
  openLocation,
  copyRoot,
  save,
  saveSystem,
}: {
  settings: Settings;
  themeMode: Settings["themeMode"];
  changeTheme: (mode: Settings["themeMode"]) => void;
  catalog: CatalogStatus;
  root: string;
  disabled: boolean;
  openLocation: (kind: string) => void;
  copyRoot: () => void;
  save: (s: Settings) => void;
  saveSystem:(s:SystemSettings)=>void;
}) {
  const [draft, setDraft] = useState(settings);
  const [settingsTab, setSettingsTab] = useState<
    "appearance" | "system" | "network" | "storage"
  >("appearance");
  const [systemDraft,setSystemDraft]=useState<SystemSettings>(settings.system||{autoStart:false,startMinimized:false,closeBehavior:"quit"});
  const [systemStatus,setSystemStatus]=useState<SystemStatus>();
  const [systemError,setSystemError]=useState("");
  useEffect(()=>{setSystemDraft(settings.system||{autoStart:false,startMinimized:false,closeBehavior:"quit"});},[settings.system?.autoStart,settings.system?.startMinimized,settings.system?.closeBehavior,settings.system?.closeBehaviorConfirmed]);
  useEffect(()=>{if(settingsTab!=="system"||!desktop())return;let active=true;void call<SystemStatus>("GetSystemStatus").then(status=>{if(active){setSystemStatus(status);setSystemDraft(status.settings);setSystemError("");}}).catch(error=>{if(active)setSystemError(String(error));});return()=>{active=false};},[settingsTab,settings.system?.autoStart]);
  useEffect(
    () => setDraft(settings),
    [
      settings.proxyMode,
      settings.proxyUrl,
      settings.sourceMode,
      settings.catalogAutoUpdate,
      settings.catalogUpdateHours,
      settings.catalogUrls.join("\n"),
    ],
  );
  return (
    <div className="settings-shell">
      <div className="settings-tabs" role="tablist" aria-label="偏好设置分类">
        {(
          [
            { id: "appearance", text: "外观", icon: Sun },
            { id: "system", text: "系统设置", icon: Monitor },
            { id: "network", text: "网络与下载", icon: Network },
            { id: "storage", text: "本地存储", icon: HardDrive },
          ] as const
        ).map((tab) => (
          <button
            key={tab.id}
            id={`settings-tab-${tab.id}`}
            role="tab"
            aria-selected={settingsTab === tab.id}
            aria-controls={`settings-pane-${tab.id}`}
            onClick={() => setSettingsTab(tab.id)}
            className={settingsTab === tab.id ? "selected" : ""}
          >
            <tab.icon size={16} />
            {tab.text}
          </button>
        ))}
      </div>
      <section
        className="panel settings-panel"
        role="tabpanel"
        id="settings-pane-appearance"
        aria-labelledby="settings-tab-appearance"
        hidden={settingsTab !== "appearance"}
      >
        <div className="settings-title">
          <Sun size={20} />
          <div>
            <h2>主题模式</h2>
            <p>选择适合当前环境的界面，修改后立即生效。</p>
          </div>
        </div>
        <div className="theme-options" role="radiogroup" aria-label="主题模式">
          {(
            [
              {
                mode: "light",
                text: "日间模式",
                description: "明亮、清晰的浅色界面",
                icon: Sun,
              },
              {
                mode: "dark",
                text: "夜间模式",
                description: "深色背景，减少暗处眩光",
                icon: Moon,
              },
              {
                mode: "system",
                text: "跟随系统",
                description: "随 Windows 外观自动切换",
                icon: Monitor,
              },
            ] as const
          ).map((option) => (
            <button
              key={option.mode}
              type="button"
              role="radio"
              aria-checked={themeMode === option.mode}
              disabled={disabled}
              className={`theme-option ${themeMode === option.mode ? "selected" : ""}`}
              onClick={() => changeTheme(option.mode)}
            >
              {themeMode === option.mode && (
                <span className="theme-current">当前使用</span>
              )}
              <div className={`theme-preview ${option.mode}`}>
                <i />
                <span />
                <span />
              </div>
              <div className="theme-option-label">
                <option.icon size={16} />
                <strong>{option.text}</strong>
              </div>
              <small>{option.description}</small>
            </button>
          ))}
        </div>
      </section>
      <section className="panel settings-panel" role="tabpanel" id="settings-pane-system" aria-labelledby="settings-tab-system" hidden={settingsTab!=="system"}>
        <div className="settings-title"><Monitor size={20}/><div><h2>系统设置</h2><p>设置面板的启动与关闭方式。</p></div></div>
        <div className="system-preference-row"><div><strong>开机自动启动</strong><p>当前用户登录 Windows 后自动打开 PHPanel，系统服务的开机启动由各实例独立设置。</p></div><label className="checkbox-field"><input aria-label="开机自动启动" type="checkbox" disabled={disabled||!systemStatus} checked={systemDraft.autoStart} onChange={event=>setSystemDraft({...systemDraft,autoStart:event.target.checked})}/>启用</label></div>
        <div className="system-preference-row"><div><strong>静默启动</strong><p>下次启动后自动隐藏到托盘，点击托盘图标可打开面板；管理员授权重启时会显示窗口。</p></div><label className="checkbox-field"><input aria-label="静默启动" type="checkbox" disabled={disabled||(systemStatus?.trayAvailable===false&&!systemDraft.startMinimized)} checked={systemDraft.startMinimized} onChange={event=>setSystemDraft({...systemDraft,startMinimized:event.target.checked})}/>启用</label></div>
        <div className="system-preference-row"><div><strong>关闭按钮行为</strong><p>首次关闭时选择并记住关闭方式，也可在这里修改。最小化到托盘后，面板与实例继续运行。</p></div><select aria-label="关闭按钮行为" disabled={disabled} value={systemDraft.closeBehaviorConfirmed?systemDraft.closeBehavior:"ask"} onChange={event=>setSystemDraft({...systemDraft,closeBehavior:event.target.value==="ask"?systemDraft.closeBehavior:event.target.value as "quit"|"tray",closeBehaviorConfirmed:event.target.value!=="ask"})}><option value="ask">首次关闭时询问</option><option value="quit">退出程序</option><option value="tray" disabled={systemStatus?.trayAvailable===false}>最小化到托盘</option></select></div>
        {systemStatus?.warning&&<p className="field-note">{systemStatus.warning}。当前无法隐藏到托盘，请使用退出程序行为。</p>}
        {systemError&&<p className="inline-error" role="alert">{systemError}</p>}
        <div className="settings-actions"><button className="button primary" disabled={disabled||!systemStatus||!!systemError} onClick={()=>saveSystem(systemDraft)}><Check size={16}/>保存系统设置</button></div>
      </section>
      <section
        className="panel settings-panel"
        role="tabpanel"
        id="settings-pane-network"
        aria-labelledby="settings-tab-network"
        hidden={settingsTab !== "network"}
      >
        <div className="settings-title">
          <Network size={20} />
          <div>
            <h2>网络与下载</h2>
            <p>仅影响 PHPanel，不修改系统网络设置。</p>
          </div>
        </div>
        <label className="field">
          代理模式
          <select
            value={draft.proxyMode}
            onChange={(e) => setDraft({ ...draft, proxyMode: e.target.value })}
          >
            <option value="system">使用系统 / 环境代理</option>
            <option value="direct">直接连接</option>
            <option value="custom">自定义代理</option>
          </select>
        </label>
        {draft.proxyMode === "custom" && (
          <label className="field">
            代理地址
            <input
              placeholder="http://127.0.0.1:7890 或 socks5://127.0.0.1:1080"
              value={draft.proxyUrl}
              onChange={(e) => setDraft({ ...draft, proxyUrl: e.target.value })}
            />
          </label>
        )}
        <label className="field">
          下载来源优先级
          <select
            value={draft.sourceMode}
            onChange={(e) => setDraft({ ...draft, sourceMode: e.target.value })}
          >
            <option value="official">官方来源优先，失败后尝试其他地址</option>
            <option value="custom">自定义镜像优先，失败后尝试官方地址</option>
          </select>
        </label>
        <p className="field-note">
          在环境管理的“下载来源”中配置每个安装包的镜像地址。不同来源必须提供
          同一份文件，并通过摘要或官方签名校验。
        </p>
        <div className="settings-divider" />
        <h2 className="settings-subtitle">版本目录自动更新</h2>
        <label className="checkbox-field">
          <input
            type="checkbox"
            checked={draft.catalogAutoUpdate}
            onChange={(e) =>
              setDraft({ ...draft, catalogAutoUpdate: e.target.checked })
            }
          />
          启动时检查，并定期发现新版本
        </label>
        <label className="field">
          检查间隔（小时）
          <input
            type="number"
            min={1}
            max={168}
            value={draft.catalogUpdateHours}
            onChange={(e) =>
              setDraft({ ...draft, catalogUpdateHours: Number(e.target.value) })
            }
          />
        </label>
        <label className="field">
          额外目录源（可选）
          <textarea
            rows={3}
            placeholder="一行一个你信任的 HTTPS 目录地址"
            value={draft.catalogUrls.join("\n")}
            onChange={(e) =>
              setDraft({
                ...draft,
                catalogUrls: e.target.value
                  .split("\n")
                  .map((s) => s.trim())
                  .filter(Boolean),
              })
            }
          />
        </label>
        <p className="field-note">
          默认读取各组件上游目录。这里可接入国内或组织内部目录，来源数据更新后会自动同步版本。
          {catalog.lastSuccess &&
            ` 最近成功更新：${new Date(catalog.lastSuccess).toLocaleString("zh-CN")}`}
        </p>
        <div className="settings-actions"><button
          className="button primary"
          disabled={disabled}
          onClick={() => save({ ...draft, themeMode })}
        >
          <Check size={16} />
          保存设置
        </button></div>
      </section>
      <section
        className="panel settings-panel"
        role="tabpanel"
        id="settings-pane-storage"
        aria-labelledby="settings-tab-storage"
        hidden={settingsTab !== "storage"}
      >
        <div className="settings-title">
          <HardDrive size={20} />
          <div>
            <h2>本地存储</h2>
            <p>程序、配置与数据分别保存。</p>
          </div>
        </div>
        <label className="field">
          数据目录
          <div className="path-display">{root || "桌面程序运行后显示"}</div>
        </label>
        <div className="storage-actions">
          <button
            className="button secondary"
            disabled={disabled || !root}
            onClick={() => openLocation("root")}
          >
            <FolderOpen size={15} />
            打开数据目录
          </button>
          <button
            className="button secondary"
            disabled={disabled || !root}
            onClick={copyRoot}
          >
            <Copy size={14} />
            复制路径
          </button>
        </div>
        <div className="storage-list">
          <div>
            <Boxes size={16} />
            <span>packages</span>
            <small>已安装的版本</small>
            <button
              className="text-button"
              disabled={disabled || !root}
              onClick={() => openLocation("packages")}
            >
              打开
            </button>
          </div>
          <div>
            <Layers3 size={16} />
            <span>instances</span>
            <small>配置、数据库和日志</small>
            <button
              className="text-button"
              disabled={disabled || !root}
              onClick={() => openLocation("instances")}
            >
              打开
            </button>
          </div>
          <div>
            <Download size={16} />
            <span>downloads</span>
            <small>续传片段和已校验缓存</small>
            <button
              className="text-button"
              disabled={disabled || !root}
              onClick={() => openLocation("downloads")}
            >
              打开缓存
            </button>
          </div>
        </div>
        <p className="field-note">
          可直接用上方按钮打开位置；文件夹的显示方式不会影响实际保存路径。
          <br />
          默认保存在 PHPanel.exe 所在目录，可通过 PHPANEL_DATA_DIR
          指定其它位置。卸载程序版本不会删除实例数据。
        </p>
      </section>
    </div>
  );
}

function ModalView({
  modal,
  state,
  busy,
  close,
  submit,
}: {
  modal: Exclude<Modal, { kind: "config" | "php-extensions" }>;
  state: State;
  busy: boolean;
  close: () => void;
  submit: (key: string, work: () => Promise<unknown>, success?: string) => void;
}) {
  const phpRuntimes = state.packages.filter((p) => p.component === "php");
  const [component, setComponent] = useState<Component>("php"),
    [version, setVersion] = useState(""),
    [hash, setHash] = useState(""),
    [urls, setURLs] = useState(
      modal.kind === "sources"
        ? modal.artifact.sources
            .filter((s) => s.name.startsWith("自定义镜像"))
            .map((s) => s.url)
            .join("\n")
        : "",
    ),
    [name, setName] = useState(
      modal.kind === "site" && modal.site
        ? modal.site.name
        : modal.kind === "instance"
          ? `${label[modal.pkg.component]} ${modal.pkg.version}`
          : "",
    ),
    [port, setPort] = useState(
      modal.kind === "edit-instance" ? modal.instance.port : modal.kind === "instance"
        ? defaults[modal.pkg.component]
        : modal.kind === "site" && modal.site
          ? modal.site.port
          : 8080,
    ),
    [template, setTemplate] = useState(
      modal.kind === "site" && modal.site ? modal.site.template : "php",
    ),
    [root, setRoot] = useState(
      modal.kind === "site" && modal.site ? modal.site.root : "",
    ),
    [nginxID, setNginxID] = useState(
      modal.kind === "site" && modal.site
        ? modal.site.nginxId
        : state.instances.find(
            (i) => i.component === "nginx" && i.status === "running",
          )?.id ||
            state.instances.find((i) => i.component === "nginx")?.id ||
            "",
    ),
    [phpID, setPHPID] = useState(
      modal.kind === "site" && modal.site
        ? modal.site.phpId
        : state.tools.phpPackageId ||
          state.tools.phpInstanceId ||
          phpRuntimes[0]?.id ||
          "",
    );
  const title =
    modal.kind === "edit-instance" ? "修改实例端口" : modal.kind === "import"
      ? "离线导入安装包"
      : modal.kind === "custom"
        ? "添加自定义版本"
        : modal.kind === "instance"
          ? "创建服务实例"
          : modal.kind === "site"
            ? modal.site
              ? "编辑网站"
              : "创建本地网站"
            : modal.kind === "sources"
              ? "下载来源"
              : modal.kind === "delete-site"
                ? "删除网站"
                : modal.kind === "delete-instance"
                  ? "删除服务实例"
                  : modal.kind === "remove"
                    ? "卸载程序版本"
                    : modal.kind === "text" ? modal.title : "系统服务";
  const deletingInstance =
    modal.kind === "delete-instance"
      ? state.instances.find((i) => i.id === modal.instance.id)
      : undefined;
  const linkedSites =
    modal.kind === "delete-instance"
      ? state.sites.filter((site) => site.phpId === modal.instance.id)
      : [];
  const cannotDeleteInstance =
    modal.kind === "delete-instance" &&
    (!deletingInstance ||
      ["running", "starting", "stopping"].includes(deletingInstance.status) ||
      linkedSites.length > 0);
  useEffect(() => {
    function handle(e: KeyboardEvent) {
      if (e.key === "Escape" && !busy) close();
    }
    window.addEventListener("keydown", handle);
    return () => window.removeEventListener("keydown", handle);
  }, [busy]);
  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (modal.kind === "edit-instance") submit("instance-port",()=>call("UpdateInstancePort",modal.instance.id,port),"端口已保存，请手动启动实例");
    if (modal.kind === "delete-instance")
      submit(
        "delete-instance",
        () => call("DeleteInstance", modal.instance.id),
        "实例及其配置、日志和数据已删除",
      );
    if (modal.kind === "import")
      submit(
        "import",
        () => call("ImportArchive", component, version, hash),
        "安装包已导入",
      );
    if (modal.kind === "custom")
      submit(
        "custom",
        () =>
          call("AddArtifact", {
            id: "",
            component,
            version,
            build: "Windows x64",
            publisher: "用户提供",
            sha256: hash,
            verification: "用户提供 SHA-256",
            sources: urls
              .split("\n")
              .map((s) => s.trim())
              .filter(Boolean)
              .map((url, i) => ({ name: `自定义来源 ${i + 1}`, url })),
          }),
        "版本已加入目录",
      );
    if (modal.kind === "instance")
      submit(
        "instance",
        () => call("CreateInstance", modal.pkg.id, name, port),
        "实例已创建，可在服务实例中启动",
      );
    if (modal.kind === "site")
      submit(
        "site",
        () =>
          modal.site
            ? call(
                "UpdateSite",
                modal.site.id,
                name,
                root,
                nginxID,
                phpID,
                port,
                template,
              )
            : call("CreateSite", name, root, nginxID, phpID, port, template),
        modal.site
          ? "网站已更新，相关配置已检查并应用"
          : "网站已创建，相关配置已检查并应用",
      );
    if (modal.kind === "sources")
      submit(
        "sources",
        () =>
          call("AddArtifact", {
            ...modal.artifact,
            sources: [
              ...modal.artifact.sources.filter(
                (s) => !s.name.startsWith("自定义镜像"),
              ),
              ...urls
                .split("\n")
                .map((s) => s.trim())
                .filter(Boolean)
                .map((url, i) => ({ name: `自定义镜像 ${i + 1}`, url })),
            ],
          }),
        "下载来源已更新",
      );
    if (modal.kind === "remove")
      submit(
        "remove",
        () => call("RemovePackage", modal.pkg.id),
        "程序版本已卸载",
      );
    if (modal.kind === "delete-site")
      submit(
        "delete-site",
        () => call("DeleteSite", modal.site.id),
        "网站记录已删除，目录文件已保留",
      );
  }
  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) close();
      }}
    >
      <section
        className={`modal ${modal.kind === "text" ? "wide" : ""}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby="modal-title"
      >
        <div className="modal-heading">
          <h2 id="modal-title">{title}</h2>
          <button
            className="icon-button"
            disabled={busy}
            aria-label="关闭对话框"
            onClick={close}
          >
            <X size={20} />
          </button>
        </div>
        {modal.kind === "text" ? (
          <pre className="log-text">{modal.text}</pre>
        ) : (
          <form onSubmit={onSubmit}>
            {(modal.kind === "import" || modal.kind === "custom") && (
              <>
                <p className="modal-intro">
                  导入可解压的 Windows x64 ZIP 包。PHP 推荐 NTS 构建；Redis
                  支持含 redis-server.exe 的社区构建。
                </p>
                <div className="form-columns">
                  <label className="field">
                    组件
                    <select
                      value={component}
                      onChange={(e) =>
                        setComponent(e.target.value as Component)
                      }
                    >
                      {components.map((c) => (
                        <option key={c} value={c}>
                          {label[c]}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="field">
                    版本
                    <input
                      required
                      placeholder="例如 8.4.0"
                      value={version}
                      onChange={(e) => setVersion(e.target.value)}
                    />
                  </label>
                </div>
                <label className="field">
                  SHA-256{" "}
                  {modal.kind === "import" && (
                    <span className="muted">（可选）</span>
                  )}
                  <input
                    className="mono"
                    required={modal.kind === "custom"}
                    pattern="[a-fA-F0-9]{64}"
                    placeholder={
                      modal.kind === "import"
                        ? "填写后核对；留空则记录文件摘要"
                        : "填写可信来源的 64 位摘要"
                    }
                    value={hash}
                    onChange={(e) => setHash(e.target.value.trim())}
                  />
                </label>
                {modal.kind === "custom" && (
                  <label className="field">
                    下载地址
                    <textarea
                      required
                      rows={3}
                      placeholder="https://…/package.zip\n一行一个，相同文件可配置多个来源"
                      value={urls}
                      onChange={(e) => setURLs(e.target.value)}
                    />
                  </label>
                )}
              </>
            )}
            {modal.kind === "edit-instance" && <><div className="modal-component"><ComponentMark type={modal.instance.component} /><div><strong>{instanceLabel(modal.instance)}</strong><small>实例配置、数据和名称保留</small></div></div><label className="field">监听端口<input required type="number" min={1} max={65535} value={port} onChange={e=>setPort(Number(e.target.value))} /></label><p className="field-note">仅允许停止后修改。保存后请手动启动此实例。{modal.instance.component === "php" && "引用此 PHP 的站点会同步更新并检查 Nginx 配置。"}</p></>}
            {modal.kind === "instance" && (
              <>
                <div className="modal-component">
                  <ComponentMark type={modal.pkg.component} />
                  <div>
                    <strong>
                      {label[modal.pkg.component]} {modal.pkg.version}
                    </strong>
                    <small>配置和数据保存到新的独立实例目录</small>
                  </div>
                </div>
                <label className="field">
                  实例名称
                  <input
                    required
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                  />
                </label>
                {modal.pkg.component !== "nginx" && (
                  <label className="field">
                    监听端口
                    <input
                      required
                      type="number"
                      min={1}
                      max={65535}
                      value={port}
                      onChange={(e) => setPort(Number(e.target.value))}
                    />
                  </label>
                )}
                <p className="field-note">
                  {modal.pkg.component === "nginx"
                    ? "监听端口在网站管理中设置。"
                    : "默认仅监听 127.0.0.1。"}{" "}
                  {modal.pkg.component === "nginx"
                    ? "所有 Nginx 版本合计只能运行一个实例。"
                    : "同一已安装版本只能运行一个实例。"}
                </p>
              </>
            )}
            {modal.kind === "site" && (
              <>
                <label className="field">
                  站点名称
                  <input
                    required
                    pattern="[a-z0-9][a-z0-9-]*"
                    placeholder="my-project"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                  />
                  <small>
                    访问地址：http://{name || "my-project"}.localhost:{port}
                  </small>
                </label>
                <div className="form-columns">
                  <label className="field">
                    网站模板
                    <select
                      value={template}
                      onChange={(e) => setTemplate(e.target.value)}
                    >
                      {Object.entries(templates).map(([value, text]) => (
                        <option key={value} value={value}>
                          {text}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="field">
                    网站端口
                    <input
                      required
                      type="number"
                      min={1}
                      max={65535}
                      value={port}
                      onChange={(e) => setPort(Number(e.target.value))}
                    />
                  </label>
                </div>
                <div className="form-columns">
                  <label className="field">
                    Nginx 配置校验实例
                    <select
                      value={nginxID}
                      onChange={(e) => setNginxID(e.target.value)}
                    >
                      <option value="">自动选择 Nginx</option>
                      {state.instances
                        .filter((i) => i.component === "nginx")
                        .map((i) => (
                          <option key={i.id} value={i.id}>
                            {instanceLabel(i)}
                          </option>
                        ))}
                    </select>
                  </label>
                  <label className="field">
                    PHP 实例{template === "static" && "（无需 PHP）"}
                    <select
                      disabled={template === "static"}
                      required={template !== "static"}
                      value={phpID}
                      onChange={(e) => setPHPID(e.target.value)}
                    >
                      <option value="">选择 PHP 版本</option>
                      {phpRuntimes.map((p) => (
                        <option key={p.id} value={p.id}>
                          PHP {p.version}
                          {p.port ? ` · 127.0.0.1:${p.port}` : ""}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
                <label className="field">
                  网站目录
                  <div className="input-with-button">
                    <input
                      placeholder={
                        modal.site ? "网站项目目录" : "留空则创建欢迎页面"
                      }
                      value={root}
                      onChange={(e) => setRoot(e.target.value)}
                    />
                    <button
                      type="button"
                      aria-label="选择目录"
                      onClick={() =>
                        void call<string>("PickDirectory")
                          .then((p) => {
                            if (p) setRoot(p);
                          })
                          .catch(() => {})
                      }
                    >
                      <FolderOpen size={17} />
                    </button>
                  </div>
                </label>
              </>
            )}
            {modal.kind === "site" && (
              <p className="field-note">
                {template === "laravel" || template === "thinkphp"
                  ? "Laravel / ThinkPHP 请将网站目录选为项目的 public 目录。"
                  : "不同域名可以共享端口。"}{" "}
                所有 Nginx 版本共用 sites
                目录中的站点配置，实际运行版本在服务实例中选择。模板不下载框架；修改站点会备份并重新生成配置。
              </p>
            )}
            {modal.kind === "delete-site" && (
              <>
                <p className="modal-intro">
                  确定删除网站 <strong>{modal.site.name}</strong>
                  ？此操作会移除面板记录和对应的 Nginx 站点配置。
                </p>
                <div className="path-display">{modal.site.root}</div>
                <p className="field-note">
                  该目录中的代码、上传文件和其它内容都会保留。相关 Nginx
                  配置检查或重载失败时，删除不会保存。
                </p>
              </>
            )}
            {modal.kind === "delete-instance" && (
              <>
                <p className="modal-intro">
                  确定删除实例 <strong>{modal.instance.name}</strong>？
                </p>
                <p className="field-note">
                  {label[modal.instance.component]} {modal.instance.version}
                  {modal.instance.component !== "nginx" &&
                    ` · 127.0.0.1:${modal.instance.port}`}
                </p>
                <div className="path-display">{modal.instance.path}</div>
                <p className="field-note">
                  该目录中的配置、日志和数据会被永久删除，已安装的程序版本保留。
                </p>
                {modal.instance.component === "nginx" && (
                  <p className="field-note">
                    独立 sites 目录中的站点记录和配置会保留，新 Nginx
                    实例可以直接加载。
                  </p>
                )}
                {(modal.instance.component === "mysql" ||
                  modal.instance.component === "redis") && (
                  <p className="inline-error">
                    {modal.instance.component === "mysql"
                      ? "MySQL 数据库文件"
                      : "Redis 持久化数据"}
                    也会删除，请先完成需要的数据备份。
                  </p>
                )}
                {linkedSites.length > 0 && (
                  <p className="inline-error">
                    此实例仍被网站{" "}
                    {linkedSites.map((site) => site.name).join("、")}{" "}
                    引用，请先修改网站使用的实例或删除对应网站。
                  </p>
                )}
              </>
            )}
            {modal.kind === "sources" && (
              <>
                <p className="modal-intro">
                  {label[modal.artifact.component]} {modal.artifact.version} ·
                  所有来源必须提供相同的 ZIP 文件。
                </p>
                <div className="source-list">
                  {modal.artifact.sources.map((s) => (
                    <div key={s.url}>
                      <strong>{s.name}</strong>
                      <span>{s.url}</span>
                    </div>
                  ))}
                </div>
                <label className="field">
                  自定义镜像地址
                  <textarea
                    rows={3}
                    placeholder="一行一个完整 ZIP 地址，保存会替换此前的自定义镜像"
                    value={urls}
                    onChange={(e) => setURLs(e.target.value)}
                  />
                </label>
                <div className="hash-note">
                  <ShieldCheck size={15} />
                  <span>
                    {modal.artifact.sha256 ? (
                      <>
                        预期 SHA-256<code>{modal.artifact.sha256}</code>
                      </>
                    ) : (
                      <>
                        官方 OpenPGP 签名
                        <code>{modal.artifact.signatureUrl}</code>
                        安装前核验签名并记录 SHA-256。
                      </>
                    )}
                  </span>
                </div>
              </>
            )}
            {modal.kind === "remove" && (
              <p className="modal-intro">
                卸载 {label[modal.pkg.component]} {modal.pkg.version}{" "}
                的程序文件。被实例引用的版本会拒绝卸载，实例数据不受影响。
              </p>
            )}
            <div className="modal-actions">
              <button
                type="button"
                className="button secondary"
                disabled={busy}
                onClick={close}
              >
                取消
              </button>
              <button
                className={`button ${modal.kind === "delete-instance" ? "danger-delete" : "primary"}`}
                disabled={busy || cannotDeleteInstance}
                type="submit"
              >
                {busy ? (
                  <LoaderCircle size={15} className="spin" />
                ) : modal.kind === "delete-site" ||
                  modal.kind === "delete-instance" ? (
                  <Trash2 size={15} />
                ) : modal.kind === "site" && modal.site ? (
                  <Check size={15} />
                ) : (
                  <Plus size={15} />
                )}{" "}
                {modal.kind === "delete-site" ||
                modal.kind === "delete-instance"
                  ? "确认删除"
                  : modal.kind === "site" && modal.site
                    ? "保存修改"
                    : modal.kind === "import"
                      ? "选择文件并导入"
                      : modal.kind === "sources"
                        ? "保存来源"
                        : modal.kind === "remove"
                          ? "确认卸载"
                          : "确认"}
              </button>
            </div>
          </form>
        )}
      </section>
    </div>
  );
}

export type Component = "php" | "nginx" | "mysql" | "redis";
export type Source = { name: string; url: string };
export type Artifact = {
  id: string;
  component: Component;
  version: string;
  build: string;
  publisher: string;
  sha256: string;
  verification: string;
  sources: Source[];
  signatureUrl?: string;
  signatureAuthority?: string;
};
export type Package = {
  id: string;
  component: Component;
  version: string;
  publisher: string;
  sha256: string;
  verification: string;
  path: string;
  executable: string;
  installedAt: string;
};
export type Instance = {
  nginxAutoPHP?: boolean;
  nginxStopPHP?: boolean;
  linkedNginxId?: string;
  linkedRun?: string;
  serviceName?: string;
  serviceAutoStart?: boolean;
  id: string;
  packageId: string;
  name: string;
  component: Component;
  version: string;
  port: number;
  path: string;
  status: string;
  pid: number;
  error: string;
};
export type Site = {
  id: string;
  name: string;
  root: string;
  port: number;
  template: string;
  nginxId: string;
  phpId: string;
  url: string;
};
export type Task = {
     kind?: "runtime" | "php-extension" | "composer";
	 extensionName?: string;
	 instanceId?: string;
	 warning?: string;
  id: string;
  artifactId: string;
  component: Component;
  version: string;
  status: string;
  source: string;
  sourceUrl: string;
  received: number;
  total: number;
  speed: number;
  error: string;
  createdAt: string;
};
export type Settings = {
	 system?: SystemSettings;
  themeMode: "light" | "dark" | "system";
  proxyMode: string;
  proxyUrl: string;
  sourceMode: string;
  catalogAutoUpdate: boolean;
  catalogUpdateHours: number;
  catalogUrls: string[];
};
export type SystemSettings={autoStart:boolean;startMinimized:boolean;closeBehavior:"quit"|"tray";closeBehaviorConfirmed?:boolean};
export type SystemStatus={settings:SystemSettings;trayAvailable:boolean;warning:string};
export type CatalogStatus = {
  refreshing: boolean;
  lastAttempt: string;
  lastSuccess: string;
  added: number;
  warnings: string[];
};
export type State = {
	 tools: {phpInstanceId: string; composerVersion: string; composerSha256: string};
	 extensionPackages: InstalledPHPExtension[];
	 preparedExtensions: {taskId: string; record: InstalledPHPExtension}[];
  revision: number;
  root: string;
  artifacts: Artifact[];
  packages: Package[];
  instances: Instance[];
  sites: Site[];
  tasks: Task[];
  settings: Settings;
  catalog: CatalogStatus;
};
export type StateUpdate = { revision: number; changed: boolean; state?: State };
declare global {
  interface Window {
    runtime?: {
      EventsOnMultiple?: (
        event: string,
        callback: () => void,
        max: number,
      ) => () => void;
    };
    go?: {
      main?: { App?: Record<string, (...args: unknown[]) => Promise<unknown>> };
    };
  }
}
export const desktop = () => Boolean(window.go?.main?.App);
export type PrivilegeStatus = {elevated: boolean; returnView?: string; instanceId?: string; serviceAutoStart?: boolean};
export type AdminRequiredDetail = {instanceId: string; serviceAutoStart?: boolean};
export async function call<T = void>(
  method: string,
  ...args: unknown[]
): Promise<T> {
  const fn = window.go?.main?.App?.[method];
  if (!fn)
    throw new Error(
      "请在 PHPanel 桌面程序中执行此操作。浏览器仅用于界面预览。",
    );
  try {return (await fn(...args)) as T;} catch(error) {
    const text=String(error);
    if(text.includes("[ADMIN_REQUIRED]")) {
      window.dispatchEvent(new CustomEvent<AdminRequiredDetail>("phpanel:admin-required",{detail:{instanceId:typeof args[0]==="string"?args[0]:"",serviceAutoStart:["RegisterSystemService","SetSystemServiceAutoStart"].includes(method)&&typeof args[1]==="boolean"?args[1]:undefined}}));
      throw new Error(text.replace("[ADMIN_REQUIRED]", "").trim());
    }
    throw error;
  }
}
export const emptyState: State = {
	 tools: {phpInstanceId:"",composerVersion:"",composerSha256:""},
	 extensionPackages: [],
	 preparedExtensions: [],
  revision: 0,
  root: "",
  artifacts: [],
  packages: [],
  instances: [],
  sites: [],
  tasks: [],
  settings: {
    themeMode: "system",
    proxyMode: "system",
    proxyUrl: "",
    sourceMode: "official",
    catalogAutoUpdate: true,
    catalogUpdateHours: 6,
    catalogUrls: [],
  },
  catalog: {
    refreshing: false,
    lastAttempt: "",
    lastSuccess: "",
    added: 0,
    warnings: [],
  },
};

export type ConfigDocument = {
  name: string;
  content: string;
  revision: string;
  path: string;
};
export type PHPExtension = {
  name: string;
  description: string;
  kind: "builtin" | "extension" | "zend" | "manual";
  filename: string;
  version: string;
  configured: boolean;
  editable: boolean;
  loaded: boolean;
  state: "stopped" | "unknown" | "disabled" | "loaded" | "pending" | "update";
  source: string;
  dependencies: string[];
};
export type PHPExtensionReport = {
  version: string;
  bits: number;
  threadSafe: boolean;
  build: string;
  revision: string;
  extensions: PHPExtension[];
  settings: Record<string, string>;
  running: boolean;
  runtimeKnown: boolean;
  runtimeError: string;
  warnings: string;
  pending: boolean;
};
export type PHPExtensionArtifact = {
  id: string; name: string; version: string; phpBranch: string; threadSafe: boolean;
  bits: number; compiler: string; kind: "extension" | "zend"; format: "zip" | "dll";
  publisher: string; sha256: string; verification: string; sources: Source[];
};
export type InstalledPHPExtension = {
  id: string; packageId: string; artifactId: string; name: string; version: string;
  relativeDll: string; verification: string;
};
export type PHPOnlineExtensions = {
  version: string; build: string; zendBuild: string; items: PHPExtensionArtifact[];
  updatedAt: string; warnings: string[];
};
export type CLIStatus = {phpInstanceId:string;phpVersion:string;composerVersion:string;composerInstalled:boolean;userPathEnabled:boolean;phpPathEnabled:boolean;composerPathEnabled:boolean;directory:string;warning:string};

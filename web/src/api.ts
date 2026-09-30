// Typed client for the rendimiento API. Types mirror the Go JSON structs.

export type Size = "small" | "medium" | "large";

/** A need: "postgres", "redis", or another service to call. */
export type Need = string | { service: string; env?: string } | { postgres: { env: string } } | { redis: { env: string } };

export interface Service {
  name: string;
  path?: string;
  language?: string;
  port?: number;
  build?: { dockerfile?: string; args?: Record<string, string> };
  test?: { image: string; command: string };
  size?: Size;
  replicas?: number;
  domain?: string;
  health?: { path: string };
  env?: Record<string, string>;
  secrets?: string[];
  needs?: Need[];
  secretEnv?: Record<string, string>;
  secretFiles?: { secret: string; mount: string }[];
  volume?: { size: string; mount: string };
  streaming?: boolean;
  tlsSecret?: string;
  lan?: { ip?: string; port?: number };
}

/** Every secret a service reads (envFrom, single keys, or files). */
export function secretNames(s: Service): string[] {
  const names = new Set<string>(s.secrets ?? []);
  Object.values(s.secretEnv ?? {}).forEach((ref) => names.add(ref.split("/")[0]));
  (s.secretFiles ?? []).forEach((f) => names.add(f.secret));
  return [...names].sort();
}

export interface Job {
  name: string;
  schedule: string;
  timeZone?: string;
  service?: string;
  path?: string;
  image?: string;
  command?: string[];
  secrets?: string[];
  secretEnv?: Record<string, string>;
}

export interface Spec {
  services: Service[];
  jobs?: Job[];
  tasks?: Task[];
}

/** A command run as a CI step (mobile builds, smoke tests, release scripts). */
export interface Task {
  name: string;
  image: string;
  command: string;
  path?: string;
  watch?: string[];
  after?: string[];
  when?: "deploy" | "always";
  optional?: boolean;
  env?: Record<string, string>;
  secrets?: string[];
  secretEnv?: Record<string, string>;
}

/** Every secret an app reads: its services', jobs' and tasks'. */
export function appSecretNames(s: Spec): string[] {
  const names = new Set<string>(s.services.flatMap(secretNames));
  for (const x of [...(s.jobs ?? []), ...(s.tasks ?? [])]) {
    (x.secrets ?? []).forEach((n) => names.add(n));
    Object.values(x.secretEnv ?? {}).forEach((ref) => names.add(ref.split("/")[0]));
  }
  return [...names].sort();
}

export interface Detected {
  path: string;
  language: string;
  framework?: string;
  version?: string;
  port: number;
  dockerfile: boolean;
  testCommand?: string;
  reasons: string[];
}

export interface Proposal {
  repo: string;
  defaultBranch: string;
  detected: Detected[];
  spec: Spec;
  files: Record<string, string>;
  existing: boolean;
  suggestedName?: string;
  railpack: boolean;
  migration?: {
    namespace: string;
    managed: boolean;
    argocd?: string;
    deployments?: string[];
    hosts?: string[];
    secrets?: string[];
  };
}

export interface Step {
  id: string;
  service: string;
  kind: "test" | "build" | "task";
  dependsOn: string[];
  status: "pending" | "running" | "succeeded" | "failed" | "skipped" | "reused";
  digest?: string;
  message?: string;
  startedAt?: string;
  finishedAt?: string;
}

export interface Run {
  id: number;
  appId: number;
  sha: string;
  branch: string;
  event: string;
  deploy: boolean;
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled";
  message: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  steps?: Step[];
}

export interface ServiceStatus {
  name: string;
  replicas: number;
  readyReplicas: number;
  image?: string;
  url?: string;
  lanURL?: string;
  certReady: boolean;
  dnsReady: boolean;
  message?: string;
}

export interface AppStatus {
  phase?: "WaitingForBuild" | "Progressing" | "Healthy" | "Degraded" | "Suspended" | "Error";
  message?: string;
  release?: number;
  services?: ServiceStatus[];
}

export interface App {
  id: number;
  name: string;
  repo: string;
  installationId: number;
  defaultBranch: string;
  spec: Spec;
  createdAt: string;
  status: AppStatus;
  lastRun?: Run;
}

export interface Release {
  id: number;
  number: number;
  runId?: number;
  sha: string;
  images: Record<string, string>;
  rollbackOf?: number;
  createdAt: string;
}

export interface ResourceNode {
  kind: string;
  name: string;
  health: "Healthy" | "Progressing" | "Degraded" | "Missing";
  info?: string[];
  children?: ResourceNode[];
}

export interface Installation {
  id: number;
  account: { login: string };
}

export interface Repo {
  id: number;
  full_name: string;
  name: string;
  default_branch: string;
  private: boolean;
  pushed_at: string;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: "same-origin",
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error ?? msg;
    } catch {
      /* not JSON */
    }
    throw new ApiError(res.status, msg);
  }
  if (res.status === 204) return undefined as T;
  const type = res.headers.get("Content-Type") ?? "";
  return (type.includes("json") ? res.json() : res.text()) as Promise<T>;
}

export const api = {
  me: () => request<{ login: string }>("GET", "/api/me"),
  logout: () => request<void>("POST", "/api/auth/logout"),
  setupStatus: () => request<{ configured: boolean; appSlug?: string; installURL?: string }>("GET", "/api/setup/status"),
  installations: () => request<{ installations: Installation[]; installURL: string }>("GET", "/api/installations"),
  repos: (installation: number) => request<Repo[]>("GET", `/api/installations/${installation}/repos`),
  zones: () => request<string[]>("GET", "/api/zones"),
  migration: (name: string) => request<{ migration: Proposal["migration"] | null }>("GET", `/api/namespaces/${encodeURIComponent(name)}/migration`),
  propose: (installation: number, repo: string, branch: string) =>
    request<Proposal>("POST", "/api/propose", { installation, repo, branch }),
  createApp: (req: {
    installation: number;
    repo: string;
    defaultBranch: string;
    name: string;
    spec: Spec;
    files: Record<string, string>;
    existing: boolean;
    adopt: boolean;
  }) => request<{ app: App; pr?: { number: number; html_url: string }; run?: Run }>("POST", "/api/apps", req),
  apps: () => request<App[]>("GET", "/api/apps"),
  app: (name: string) => request<App>("GET", `/api/apps/${name}`),
  deleteImpact: (name: string) =>
    request<{ namespace: string; volumes: string[]; secrets: string[]; adopted: boolean; sharedNamespace: boolean }>("GET", `/api/apps/${name}/impact`),
  disconnectApp: (name: string) => request<void>("POST", `/api/apps/${name}/disconnect?confirm=${encodeURIComponent(name)}`),
  deleteApp: (name: string) => request<void>("DELETE", `/api/apps/${name}?confirm=${encodeURIComponent(name)}`),
  runs: (app: string) => request<Run[]>("GET", `/api/apps/${app}/runs`),
  triggerRun: (app: string, branch?: string) => request<Run>("POST", `/api/apps/${app}/runs`, { branch }),
  releases: (app: string) => request<Release[]>("GET", `/api/apps/${app}/releases`),
  rollback: (app: string, release: number) => request<Release>("POST", `/api/apps/${app}/rollback`, { release }),
  resources: (app: string) => request<ResourceNode>("GET", `/api/apps/${app}/resources`),
  putSecret: (app: string, secret: string, data: Record<string, string>) =>
    request<void>("PUT", `/api/apps/${app}/secrets/${secret}`, data),
  run: (id: number) => request<Run>("GET", `/api/runs/${id}`),
  cancelRun: (id: number) => request<void>("POST", `/api/runs/${id}/cancel`),
  stepLog: (id: number, step: string) => request<string>("GET", `/api/runs/${id}/steps/${encodeURIComponent(step)}/log`),
};

/** Subscribes to server-sent events; returns an unsubscribe function. */
export function subscribe(path: string, handlers: Record<string, (data: any) => void>): () => void {
  const es = new EventSource(path, { withCredentials: true });
  for (const [type, fn] of Object.entries(handlers)) {
    es.addEventListener(type, (e) => fn(JSON.parse((e as MessageEvent).data)));
  }
  return () => es.close();
}

export function timeAgo(iso?: string): string {
  if (!iso) return "";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return `${Math.floor(s)}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export function duration(start?: string, end?: string): string {
  if (!start) return "";
  const s = Math.round(((end ? new Date(end) : new Date()).getTime() - new Date(start).getTime()) / 1000);
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

// ---- environment ----

export type EnvStatus = "ok" | "warning" | "missing" | "error";

export interface EnvCheck {
  id: string;
  name: string;
  category: string;
  required: boolean;
  status: EnvStatus;
  summary: string;
  details?: string[];
  version?: string;
  fix?: string;
}

export interface EnvProvider {
  slot: string;
  title: string;
  active: string;
  name: string;
  status: EnvStatus;
  detail: string;
  options: { id: string; name: string; available: boolean }[];
}

export interface EnvNode {
  name: string;
  roles?: string[];
  ready: boolean;
  arch: string;
  os: string;
  kubelet: string;
  pressure?: string[];
  pods: number;
  cpuCores: number;
  cpuUsed?: number;
  memBytes: number;
  memUsed?: number;
  unschedulable?: boolean;
}

export interface EnvProblem {
  kind: string;
  namespace: string;
  name: string;
  reason: string;
  since?: string;
}

export interface EnvReport {
  generatedAt: string;
  overall: EnvStatus;
  summary: string;
  cluster: { distribution: string; name: string; version: string; nodes: number; nodesReady: number; pods: number; namespaces: number };
  providers: EnvProvider[];
  checks: EnvCheck[];
  nodes: EnvNode[];
  problems: EnvProblem[] | null;
  ddns?: { enabled: boolean; ip?: string; checkedAt?: string; changedAt?: string; error?: string };
}

export const envApi = {
  report: (refresh = false) => request<EnvReport>("GET", `/api/environment${refresh ? "?refresh=1" : ""}`),
  dnsSync: () => request<{ ip: string; changed: boolean; updated: string[] | null }>("POST", "/api/dns/sync"),
};

// ---- services catalog ----

export type CatalogGroup = "apps" | "other" | "infrastructure";

export interface CatalogEntry {
  id: string;
  name: string;
  namespace: string;
  group: CatalogGroup;
  category: string;
  title: string;
  description?: string;
  origin: {
    kind: "rendimiento" | "addon" | "argocd" | "helm" | "manual";
    summary: string;
    app?: string;
    repo?: string;
    repoUrl?: string;
    release?: number;
    argoApp?: string;
    helmRelease?: string;
    images?: { ref: string; link?: string; built?: boolean }[];
  };
  protocol: string;
  url: string;
  shortUrl: string;
  ports: { name?: string; port: number; target: string; protocol: string }[];
  public?: string[];
  lan?: string;
  health?: string;
  docs?: string;
  endpoints?: string[];
  env: string;
  snippet: string;
  usedBy: { namespace: string; workload: string; kind: string; env: string; app?: string }[];
  pods: number;
  ready: number;
  networkPolicies: number;
}

export interface Catalog {
  entries: CatalogEntry[];
  generated: string;
}

export const catalogApi = {
  list: (refresh = false) => request<Catalog>("GET", `/api/services${refresh ? "?refresh=1" : ""}`),
};

// ---- add-ons ----

export interface RenovateSettings {
  schedule: string;
  image: string;
  extraRepos: string[];
  config: string;
  logLevel?: "info" | "debug";
}

export interface RenovateRepoResult {
  result?: string;
  prs?: string[];
  automerged?: string[];
  errors?: string[];
}

export interface AddonRun {
  id: number;
  addon: string;
  trigger: "schedule" | "manual";
  status: "running" | "succeeded" | "failed";
  message: string;
  repos: string[];
  results: Record<string, RenovateRepoResult>;
  log?: string;
  startedAt: string;
  finishedAt?: string;
}

export interface Addon {
  name: string;
  title: string;
  description: string;
  enabled: boolean;
  settings: RenovateSettings;
  running: boolean;
  nextRun?: string;
  repos: string[];
  apps: { name: string; repo: string; enabled: boolean }[];
  lastRun?: AddonRun;
  defaults?: RenovateSettings;
  problems: string[];
  permissionsUrl: string;
}

export interface AppAddons {
  renovate: {
    enabled: boolean;
    addonEnabled: boolean;
    prsUrl: string;
    lastRun?: { id: number; status: AddonRun["status"]; startedAt: string; result: RenovateRepoResult };
  };
}

export const addonsApi = {
  list: () => request<Addon[]>("GET", "/api/addons"),
  get: (name: string) => request<Addon>("GET", `/api/addons/${name}`),
  save: (name: string, enabled: boolean, settings: RenovateSettings) => request<Addon>("PUT", `/api/addons/${name}`, { enabled, settings }),
  run: (name: string) => request<AddonRun>("POST", `/api/addons/${name}/runs`),
  runs: (name: string) => request<AddonRun[]>("GET", `/api/addons/${name}/runs`),
  runLog: (name: string, id: number) => request<AddonRun>("GET", `/api/addons/${name}/runs/${id}`),
  forApp: (app: string) => request<AppAddons>("GET", `/api/apps/${encodeURIComponent(app)}/addons`),
  setForApp: (app: string, addon: string, enabled: boolean) =>
    request<AppAddons>("PUT", `/api/apps/${encodeURIComponent(app)}/addons/${addon}`, { enabled }),
};

// ---- installed add-ons (Helm charts / git manifests) ----

export interface AddonObjectRef { group?: string; version: string; kind: string; namespace?: string; name: string }
export interface AddonPreview {
  create: number;
  update: number;
  unchanged: number;
  prune: number;
  items?: (AddonObjectRef & { action: "create" | "update" | "prune"; diff?: string })[];
}
export interface AddonSource {
  helm?: { repo: string; chart: string; version: string };
  git?: { repo: string; path: string; revision?: string };
}
export type AddonPhase = "Pending" | "Synced" | "OutOfSync" | "Blocked" | "Error" | "Suspended";
export interface InstalledAddon {
  name: string;
  title: string;
  category: string;
  description: string;
  namespace: string;
  source: AddonSource;
  manualSync: boolean;
  suspend: boolean;
  adopt: boolean;
  fromGit: boolean;
  sourceFile?: string;
  status: {
    phase?: AddonPhase;
    message?: string;
    revision?: string;
    objects?: AddonObjectRef[];
    preview?: AddonPreview;
    hooks?: { name: string; kind: string; events: string[] }[];
    hookRuns?: { event: string; name: string; kind: string; status: "succeeded" | "failed"; message?: string; finished: string }[];
    lastSynced?: string;
    adopted?: boolean;
  };
  definition?: string;
}
export interface CatalogField { key: string; label: string; type: "string" | "number" | "boolean"; default?: unknown; description?: string }
export interface AddonCatalogEntry {
  id: string;
  title: string;
  category: string;
  description: string;
  homepage?: string;
  kind: "helm" | "custom-helm" | "custom-git";
  helm?: { repo: string; chart: string; version: string };
  namespace?: string;
  fields?: CatalogField[];
  manualSync?: boolean;
  notes?: string;
}
export interface AddonDefinition {
  name: string;
  title?: string;
  category?: string;
  description?: string;
  namespace: string;
  createNamespace?: boolean;
  helm?: { repo: string; chart: string; version: string };
  git?: { repo?: string; path: string };
  releaseName?: string;
  values?: Record<string, unknown>;
  adopt?: boolean;
  allowAdoptChanges?: boolean;
  prune?: boolean;
  manualSync?: boolean;
  suspend?: boolean;
}

export const installedApi = {
  catalog: () => request<AddonCatalogEntry[]>("GET", "/api/addon-catalog"),
  list: () => request<{ addons: InstalledAddon[]; repo?: string; sync?: { at: string; commit?: string; error?: string; invalid?: Record<string, string> } }>("GET", "/api/installed"),
  get: (name: string) => request<InstalledAddon>("GET", `/api/installed/${name}`),
  save: (name: string, body: { definition?: AddonDefinition; valuesYaml?: string; yaml?: string }) =>
    request<{ commit: string; repo: string }>("PUT", `/api/installed/${name}`, body),
  sync: (name: string) => request<void>("POST", `/api/installed/${name}/sync`),
  patch: (name: string, body: { suspend?: boolean; allowAdoptChanges?: boolean; manualSync?: boolean }) =>
    request<{ commit: string }>("PATCH", `/api/installed/${name}`, body),
  remove: (name: string, uninstall: boolean) => request<{ commit: string }>("DELETE", `/api/installed/${name}?uninstall=${uninstall}`),
};

import { useEffect, useMemo, useState, type ReactNode } from "react";
import {
  Activity,
  Archive,
  Bell,
  CheckCircle2,
  Clock3,
  Database,
  HardDrive,
  KeyRound,
  Play,
  RotateCcw,
  Search,
  ShieldCheck,
  TriangleAlert,
} from "lucide-react";
import { Button } from "./components/ui/button";

const navItems = [
  { label: "Dashboard", icon: Activity, active: true },
  { label: "Targets", icon: Database },
  { label: "Backups", icon: Archive },
  { label: "Storage", icon: HardDrive },
  { label: "Keys", icon: KeyRound },
];

const statusClass: Record<string, string> = {
  running: "bg-warning/15 text-warning",
  succeeded: "bg-success/15 text-success",
  finalizing: "bg-indigo/20 text-indigo-light",
  queued: "bg-indigo/20 text-indigo-light",
  failed: "bg-danger/15 text-danger-light",
};

const metricTone = {
  success: "bg-success/15 text-success",
  warning: "bg-warning/15 text-warning",
  bronze: "bg-bronze/15 text-bronze",
  danger: "bg-danger/15 text-danger-light",
};

const healthTone = {
  success: "text-success",
  warning: "text-warning",
  bronze: "text-bronze",
  indigo: "text-indigo-light",
};

const tokenStorageKey = "kronos.apiToken";

type Overview = {
  generated_at: string;
  agents: {
    healthy: number;
    degraded: number;
    capacity: number;
  };
  inventory: {
    targets: number;
    storages: number;
    schedules: number;
    schedules_paused: number;
    retention_policies: number;
    notification_rules: number;
    notification_rules_enabled: number;
    users: number;
  };
  jobs: {
    active: number;
    by_status: Record<string, number>;
  };
  backups: {
    total: number;
    protected: number;
    bytes_total: number;
    latest_completed_timestamp?: number;
    by_type: Record<string, number>;
  };
  health: {
    status: string;
    checks: Record<string, string>;
    error?: string;
  };
  attention: {
    degraded_agents: number;
    failed_jobs: number;
    readiness_errors: number;
    unprotected_backups: number;
    disabled_notification_rules: number;
  };
  latest_jobs?: Job[];
  latest_backups?: Backup[];
};

type Job = {
  id: string;
  operation?: string;
  target_id: string;
  storage_id: string;
  type?: string;
  status: string;
  queued_at: string;
  started_at?: string;
  ended_at?: string;
  error?: string;
};

type Backup = {
  id: string;
  target_id: string;
  storage_id: string;
  type: string;
  ended_at: string;
  size_bytes: number;
  protected: boolean;
};

type JobListResponse = {
  jobs?: Job[];
};

type BackupListResponse = {
  backups?: Backup[];
};

type Target = {
  id: string;
  name: string;
  driver: string;
  endpoint: string;
};

type Storage = {
  id: string;
  name: string;
  kind: string;
  uri: string;
};

type TargetListResponse = {
  targets?: Target[];
};

type StorageListResponse = {
  storages?: Storage[];
};

function AppLogo() {
  return (
    <div className="flex h-12 items-center gap-3 px-4">
      <img src="/kronos-mark.svg" className="h-8 w-8" alt="" />
      <div>
        <div className="font-wordmark text-lg font-black tracking-normal text-bronze">Kronos</div>
        <div className="text-xs text-muted">control plane</div>
      </div>
    </div>
  );
}

export function App() {
  const [overview, setOverview] = useState<Overview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [apiToken, setAPIToken] = useState(() => localStorage.getItem(tokenStorageKey) ?? "");
  const [draftToken, setDraftToken] = useState(apiToken);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [backups, setBackups] = useState<Backup[]>([]);
  const [targets, setTargets] = useState<Target[]>([]);
  const [storages, setStorages] = useState<Storage[]>([]);
  const [updatingBackupID, setUpdatingBackupID] = useState<string | null>(null);

  async function loadOverview({ refresh = false }: { refresh?: boolean } = {}) {
    if (refresh) {
      setRefreshing(true);
    } else {
      setLoading(true);
    }
    setError(null);
    setDetailError(null);
    try {
      const nextOverview = await requestJSON<Overview>("/api/v1/overview", apiToken);
      setOverview(nextOverview);
      const [jobResult, backupResult, targetResult, storageResult] = await Promise.allSettled([
        requestJSON<JobListResponse>("/api/v1/jobs", apiToken),
        requestJSON<BackupListResponse>("/api/v1/backups", apiToken),
        requestJSON<TargetListResponse>("/api/v1/targets", apiToken),
        requestJSON<StorageListResponse>("/api/v1/storages", apiToken),
      ]);
      if (jobResult.status === "fulfilled") {
        setJobs(sortJobs(jobResult.value.jobs ?? []).slice(0, 8));
      } else {
        setJobs(nextOverview.latest_jobs ?? []);
      }
      if (backupResult.status === "fulfilled") {
        setBackups(sortBackups(backupResult.value.backups ?? []).slice(0, 8));
      } else {
        setBackups(nextOverview.latest_backups ?? []);
      }
      if (targetResult.status === "fulfilled") {
        setTargets((targetResult.value.targets ?? []).slice(0, 8));
      } else {
        setTargets([]);
      }
      if (storageResult.status === "fulfilled") {
        setStorages((storageResult.value.storages ?? []).slice(0, 8));
      } else {
        setStorages([]);
      }
      const failedDetails = [jobResult, backupResult, targetResult, storageResult].filter((result) => result.status === "rejected").length;
      if (failedDetails > 0) {
        setDetailError("Some detail endpoints require additional read scopes");
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "overview request failed");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }

  async function toggleBackupProtection(backup: Backup) {
    setUpdatingBackupID(backup.id);
    setDetailError(null);
    try {
      const action = backup.protected ? "unprotect" : "protect";
      const updated = await requestJSON<Backup>(`/api/v1/backups/${encodeURIComponent(backup.id)}/${action}`, apiToken, {
        method: "POST",
      });
      setBackups((current) => current.map((item) => (item.id === updated.id ? updated : item)));
      setOverview((current) => {
        if (!current) {
          return current;
        }
        const protectedDelta = updated.protected === backup.protected ? 0 : updated.protected ? 1 : -1;
        const unprotectedDelta = updated.protected === backup.protected ? 0 : updated.protected ? -1 : 1;
        return {
          ...current,
          backups: {
            ...current.backups,
            protected: Math.max(0, current.backups.protected + protectedDelta),
          },
          attention: {
            ...current.attention,
            unprotected_backups: Math.max(0, current.attention.unprotected_backups + unprotectedDelta),
          },
        };
      });
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "backup protection update failed");
    } finally {
      setUpdatingBackupID(null);
    }
  }

  useEffect(() => {
    void loadOverview();
  }, [apiToken]);

  const generatedAt = useMemo(() => formatDateTime(overview?.generated_at), [overview?.generated_at]);
  const attentionTotal = overview ? sumValues(overview.attention) : 0;
  const queuedJobs = overview?.jobs.by_status.queued ?? 0;
  const latestJobs = jobs.length > 0 ? jobs : overview?.latest_jobs ?? [];
  const latestBackups = backups.length > 0 ? backups : overview?.latest_backups ?? [];

  return (
    <main className="min-h-screen bg-void text-marble">
      <div className="grid min-h-screen grid-cols-1 lg:grid-cols-[248px_1fr]">
        <aside className="border-line bg-panel/92 lg:border-r">
          <AppLogo />
          <nav className="grid gap-1 px-3 py-2">
            {navItems.map((item) => (
              <button
                key={item.label}
                className={`flex h-10 items-center gap-3 rounded-md px-3 text-sm font-medium transition ${
                  item.active ? "bg-bronze text-void" : "text-muted hover:bg-surface hover:text-marble"
                }`}
              >
                <item.icon className="h-4 w-4" />
                {item.label}
              </button>
            ))}
          </nav>
          <form
            className="mx-3 mt-3 grid gap-2 border-t border-line pt-4"
            onSubmit={(event) => {
              event.preventDefault();
              const nextToken = draftToken.trim();
              if (nextToken === "") {
                localStorage.removeItem(tokenStorageKey);
              } else {
                localStorage.setItem(tokenStorageKey, nextToken);
              }
              setAPIToken(nextToken);
            }}
          >
            <label className="text-xs font-semibold uppercase text-muted" htmlFor="api-token">
              API token
            </label>
            <input
              id="api-token"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              placeholder="Bearer token"
              type="password"
              value={draftToken}
              onChange={(event) => setDraftToken(event.target.value)}
            />
            <div className="grid grid-cols-2 gap-2">
              <Button variant="secondary" type="submit" icon={<KeyRound className="h-4 w-4" />}>
                Save
              </Button>
              <Button
                variant="ghost"
                type="button"
                onClick={() => {
                  localStorage.removeItem(tokenStorageKey);
                  setDraftToken("");
                  setAPIToken("");
                }}
              >
                Clear
              </Button>
            </div>
          </form>
        </aside>

        <section className="min-w-0">
          <header className="flex min-h-16 items-center justify-between gap-3 border-b border-line px-4 sm:px-6">
            <div className="min-w-0">
              <h1 className="text-xl font-semibold text-marble">Operations</h1>
              <p className="text-sm text-muted">{generatedAt ?? "Loading overview"}</p>
            </div>
            <div className="flex items-center gap-2">
              <Button variant="ghost" title="Search" aria-label="Search" icon={<Search className="h-4 w-4" />} />
              <Button variant="ghost" title="Notifications" aria-label="Notifications" icon={<Bell className="h-4 w-4" />} />
              <Button variant="primary" icon={<Play className="h-4 w-4" />}>
                Run backup
              </Button>
            </div>
          </header>

          <div className="grid gap-6 px-4 py-6 sm:px-6 xl:grid-cols-[minmax(0,1fr)_340px]">
            <section className="grid gap-6">
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                <Metric icon={<CheckCircle2 />} label="Healthy agents" value={metricValue(overview?.agents.healthy, loading)} tone="success" />
                <Metric icon={<Clock3 />} label="Queued jobs" value={metricValue(queuedJobs, loading)} tone="warning" />
                <Metric icon={<ShieldCheck />} label="Protected backups" value={metricValue(overview?.backups.protected, loading)} tone="bronze" />
                <Metric icon={<TriangleAlert />} label="Attention" value={metricValue(attentionTotal, loading)} tone="danger" />
              </div>

              {error ? (
                <section className="rounded-md border border-danger/50 bg-danger/10 p-4 text-sm text-danger-light">
                  {error}
                </section>
              ) : null}
              {detailError && !error ? (
                <section className="rounded-md border border-warning/45 bg-warning/10 p-4 text-sm text-warning">
                  {detailError}
                </section>
              ) : null}

              <section className="overflow-hidden rounded-md border border-line bg-panel">
                <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-3">
                  <h2 className="text-base font-semibold">Recent jobs</h2>
                  <Button variant="secondary" icon={<RotateCcw className={`h-4 w-4 ${refreshing ? "animate-spin" : ""}`} />} onClick={() => void loadOverview({ refresh: true })}>
                    {refreshing ? "Refreshing" : "Refresh"}
                  </Button>
                </div>
                <div className="overflow-x-auto">
                  <table className="w-full min-w-[680px] border-collapse text-left text-sm">
                    <thead className="bg-surface text-xs uppercase text-muted">
                      <tr>
                        <th className="px-4 py-3 font-semibold">Target</th>
                        <th className="px-4 py-3 font-semibold">Type</th>
                        <th className="px-4 py-3 font-semibold">Started</th>
                        <th className="px-4 py-3 font-semibold">Bytes</th>
                        <th className="px-4 py-3 font-semibold">Status</th>
                      </tr>
                    </thead>
                    <tbody>
                      {latestJobs.length > 0 ? (
                        latestJobs.map((job) => (
                          <tr key={job.id} className="border-t border-line">
                            <td className="px-4 py-3 font-medium text-marble">{job.target_id || job.id}</td>
                            <td className="px-4 py-3 text-muted">{job.operation || job.type || "job"}</td>
                            <td className="px-4 py-3 font-mono text-muted">{formatTime(job.started_at || job.queued_at)}</td>
                            <td className="px-4 py-3 text-muted">{job.error || job.storage_id || "pending"}</td>
                            <td className="px-4 py-3">
                              <span className={`inline-flex h-7 items-center rounded-md px-2 text-xs font-semibold ${statusClass[job.status] ?? "bg-surface text-muted"}`}>
                                {job.status}
                              </span>
                            </td>
                          </tr>
                        ))
                      ) : (
                        <tr className="border-t border-line">
                          <td className="px-4 py-6 text-sm text-muted" colSpan={5}>
                            {loading ? "Loading jobs" : "No recent jobs"}
                          </td>
                        </tr>
                      )}
                    </tbody>
                  </table>
                </div>
              </section>
            </section>

            <aside className="grid content-start gap-6">
              <section className="rounded-md border border-line bg-panel p-4">
                <h2 className="text-base font-semibold">Repository health</h2>
                <div className="mt-4 grid gap-3">
                  <HealthRow label="Readiness" value={overview?.health.status ?? "..."} tone={overview?.health.status === "ok" ? "success" : "warning"} />
                  <HealthRow label="Targets" value={metricValue(overview?.inventory.targets, loading)} tone="bronze" />
                  <HealthRow label="Storage used" value={overview ? formatBytes(overview.backups.bytes_total) : "..."} tone="indigo" />
                  <HealthRow label="Schedules paused" value={metricValue(overview?.inventory.schedules_paused, loading)} tone="warning" />
                </div>
              </section>

              <section className="rounded-md border border-line bg-panel p-4">
                <h2 className="text-base font-semibold">Inventory</h2>
                <div className="mt-4 grid gap-3">
                  <InventoryGroup
                    empty={loading ? "Loading targets" : "No targets"}
                    items={targets.map((target) => ({
                      key: target.id,
                      label: target.name || target.id,
                      value: target.driver || "target",
                    }))}
                  />
                  <InventoryGroup
                    empty={loading ? "Loading storage" : "No storage"}
                    items={storages.map((storage) => ({
                      key: storage.id,
                      label: storage.name || storage.id,
                      value: storage.kind || "storage",
                    }))}
                  />
                </div>
              </section>

              <section className="rounded-md border border-line bg-panel p-4">
                <h2 className="text-base font-semibold">Latest backups</h2>
                <div className="mt-4 grid gap-3">
                  {latestBackups.length > 0 ? (
                    latestBackups.map((backup) => (
                      <div key={backup.id} className="flex h-10 items-center justify-between gap-3 rounded-md bg-surface px-3 text-sm">
                        <span className="min-w-0 truncate">{backup.target_id || backup.id}</span>
                        <div className="flex shrink-0 items-center gap-2">
                          <span className="font-mono text-xs text-muted">{formatBytes(backup.size_bytes)}</span>
                          <Button
                            className="h-7 px-2 text-xs"
                            disabled={updatingBackupID === backup.id}
                            onClick={() => void toggleBackupProtection(backup)}
                            type="button"
                            variant={backup.protected ? "secondary" : "ghost"}
                          >
                            {updatingBackupID === backup.id ? "Saving" : backup.protected ? "Protected" : "Protect"}
                          </Button>
                        </div>
                      </div>
                    ))
                  ) : (
                    <div className="flex h-10 items-center justify-between rounded-md bg-surface px-3 text-sm text-muted">
                      <span>{loading ? "Loading backups" : "No backups yet"}</span>
                      <Clock3 className="h-4 w-4 text-bronze" />
                    </div>
                  )}
                </div>
              </section>
            </aside>
          </div>
        </section>
      </div>
    </main>
  );
}

function Metric({
  icon,
  label,
  value,
  tone,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  tone: keyof typeof metricTone;
}) {
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <div className={`mb-5 inline-flex h-9 w-9 items-center justify-center rounded-md ${metricTone[tone]}`}>{icon}</div>
      <div className="text-2xl font-semibold">{value}</div>
      <div className="mt-1 text-sm text-muted">{label}</div>
    </section>
  );
}

function HealthRow({ label, value, tone }: { label: string; value: string; tone: keyof typeof healthTone }) {
  return (
    <div className="flex h-10 items-center justify-between gap-4 rounded-md bg-surface px-3 text-sm">
      <span className="text-muted">{label}</span>
      <span className={`font-semibold ${healthTone[tone]}`}>{value}</span>
    </div>
  );
}

function InventoryGroup({ empty, items }: { empty: string; items: Array<{ key: string; label: string; value: string }> }) {
  if (items.length === 0) {
    return <div className="rounded-md bg-surface px-3 py-2 text-sm text-muted">{empty}</div>;
  }
  return (
    <div className="grid gap-2">
      {items.map((item) => (
        <div key={item.key} className="flex h-10 items-center justify-between gap-3 rounded-md bg-surface px-3 text-sm">
          <span className="min-w-0 truncate">{item.label}</span>
          <span className="shrink-0 font-mono text-xs text-muted">{item.value}</span>
        </div>
      ))}
    </div>
  );
}

async function requestJSON<T>(path: string, token: string, init: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (token.trim() !== "") {
    headers.Authorization = `Bearer ${token.trim()}`;
  }
  const response = await fetch(path, { ...init, headers: { ...headers, ...init.headers } });
  if (!response.ok) {
    if (response.status === 401 || response.status === 403) {
      throw new Error("API token required");
    }
    throw new Error(`${path} request failed with ${response.status}`);
  }
  return (await response.json()) as T;
}

function sortJobs(values: Job[]) {
  return [...values].sort((a, b) => Date.parse(b.queued_at) - Date.parse(a.queued_at));
}

function sortBackups(values: Backup[]) {
  return [...values].sort((a, b) => Date.parse(b.ended_at) - Date.parse(a.ended_at));
}

function metricValue(value: number | undefined, loading: boolean) {
  if (typeof value === "number") {
    return Intl.NumberFormat().format(value);
  }
  return loading ? "..." : "0";
}

function sumValues(values: Record<string, number>) {
  return Object.values(values).reduce((sum, value) => sum + value, 0);
}

function formatDateTime(value: string | undefined) {
  if (!value) {
    return null;
  }
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}

function formatTime(value: string | undefined) {
  if (!value) {
    return "...";
  }
  return new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function formatBytes(value: number) {
  if (value <= 0) {
    return "0 B";
  }
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const unit = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** unit).toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

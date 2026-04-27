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
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);

  async function loadOverview({ refresh = false }: { refresh?: boolean } = {}) {
    if (refresh) {
      setRefreshing(true);
    } else {
      setLoading(true);
    }
    setError(null);
    try {
      const response = await fetch("/api/v1/overview", {
        headers: { Accept: "application/json" },
      });
      if (!response.ok) {
        throw new Error(`overview request failed with ${response.status}`);
      }
      setOverview((await response.json()) as Overview);
    } catch (err) {
      setError(err instanceof Error ? err.message : "overview request failed");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }

  useEffect(() => {
    void loadOverview();
  }, []);

  const generatedAt = useMemo(() => formatDateTime(overview?.generated_at), [overview?.generated_at]);
  const attentionTotal = overview ? sumValues(overview.attention) : 0;
  const queuedJobs = overview?.jobs.by_status.queued ?? 0;
  const latestJobs = overview?.latest_jobs ?? [];

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
                <h2 className="text-base font-semibold">Latest backups</h2>
                <div className="mt-4 grid gap-3">
                  {(overview?.latest_backups ?? []).length > 0 ? (
                    overview?.latest_backups?.map((backup) => (
                      <div key={backup.id} className="flex h-10 items-center justify-between gap-3 rounded-md bg-surface px-3 text-sm">
                        <span className="min-w-0 truncate">{backup.target_id || backup.id}</span>
                        <span className="font-mono text-xs text-muted">{formatBytes(backup.size_bytes)}</span>
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

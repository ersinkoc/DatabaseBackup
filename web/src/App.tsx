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
  canceled: "bg-surface text-muted",
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
  schedule_id?: string;
  target_id: string;
  storage_id: string;
  agent_id?: string;
  type?: string;
  status: string;
  queued_at: string;
  started_at?: string;
  ended_at?: string;
  parent_backup_id?: string;
  restore_backup_id?: string;
  restore_manifest_id?: string;
  restore_manifest_ids?: string[];
  restore_target_id?: string;
  restore_at?: string;
  restore_dry_run?: boolean;
  restore_replace_existing?: boolean;
  error?: string;
};

type Backup = {
  id: string;
  target_id: string;
  storage_id: string;
  type: string;
  manifest_id?: string;
  started_at?: string;
  ended_at: string;
  size_bytes: number;
  chunk_count?: number;
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
  database?: string;
  options?: Record<string, unknown>;
  labels?: Record<string, string>;
  created_at?: string;
  updated_at?: string;
};

type Storage = {
  id: string;
  name: string;
  kind: string;
  uri: string;
  options?: Record<string, unknown>;
  labels?: Record<string, string>;
  created_at?: string;
  updated_at?: string;
};

type TargetListResponse = {
  targets?: Target[];
};

type StorageListResponse = {
  storages?: Storage[];
};

type Schedule = {
  id: string;
  name: string;
  target_id: string;
  storage_id: string;
  backup_type: string;
  expression: string;
  retention_policy_id?: string;
  paused: boolean;
  labels?: Record<string, string>;
  created_at?: string;
  updated_at?: string;
};

type RetentionPolicy = {
  id: string;
  name: string;
  rules: Array<{ kind: string; params?: Record<string, unknown> }>;
  created_at?: string;
  updated_at?: string;
};

type ScheduleListResponse = {
  schedules?: Schedule[];
};

type RetentionPolicyListResponse = {
  policies?: RetentionPolicy[];
};

type RestorePlanStep = {
  backup_id: string;
  type?: string;
  parent_id?: string;
  manifest_id?: string;
  target_id?: string;
  storage_id?: string;
  started_at?: string;
  ended_at?: string;
};

type RestorePlan = {
  backup_id: string;
  target_id: string;
  storage_id: string;
  at?: string;
  steps: RestorePlanStep[];
  warnings?: string[];
};

type RestoreStartResponse = {
  job: Job;
  plan: RestorePlan;
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
  const [updatingJobID, setUpdatingJobID] = useState<string | null>(null);
  const [selectedJob, setSelectedJob] = useState<Job | null>(null);
  const [loadingJobID, setLoadingJobID] = useState<string | null>(null);
  const [selectedBackup, setSelectedBackup] = useState<Backup | null>(null);
  const [loadingBackupID, setLoadingBackupID] = useState<string | null>(null);
  const [selectedTarget, setSelectedTarget] = useState<Target | null>(null);
  const [selectedStorage, setSelectedStorage] = useState<Storage | null>(null);
  const [loadingInventoryID, setLoadingInventoryID] = useState<string | null>(null);
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [retentionPolicies, setRetentionPolicies] = useState<RetentionPolicy[]>([]);
  const [selectedSchedule, setSelectedSchedule] = useState<Schedule | null>(null);
  const [selectedRetentionPolicy, setSelectedRetentionPolicy] = useState<RetentionPolicy | null>(null);
  const [loadingAutomationID, setLoadingAutomationID] = useState<string | null>(null);
  const [updatingScheduleID, setUpdatingScheduleID] = useState<string | null>(null);
  const [restoreTargetID, setRestoreTargetID] = useState("");
  const [restoreAt, setRestoreAt] = useState("");
  const [restorePlan, setRestorePlan] = useState<RestorePlan | null>(null);
  const [restoreJob, setRestoreJob] = useState<Job | null>(null);
  const [restoring, setRestoring] = useState<"preview" | "start" | null>(null);

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
      const [jobResult, backupResult, targetResult, storageResult, scheduleResult, retentionPolicyResult] = await Promise.allSettled([
        requestJSON<JobListResponse>("/api/v1/jobs", apiToken),
        requestJSON<BackupListResponse>("/api/v1/backups", apiToken),
        requestJSON<TargetListResponse>("/api/v1/targets", apiToken),
        requestJSON<StorageListResponse>("/api/v1/storages", apiToken),
        requestJSON<ScheduleListResponse>("/api/v1/schedules", apiToken),
        requestJSON<RetentionPolicyListResponse>("/api/v1/retention/policies", apiToken),
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
      if (scheduleResult.status === "fulfilled") {
        setSchedules((scheduleResult.value.schedules ?? []).slice(0, 8));
      } else {
        setSchedules([]);
      }
      if (retentionPolicyResult.status === "fulfilled") {
        setRetentionPolicies((retentionPolicyResult.value.policies ?? []).slice(0, 8));
      } else {
        setRetentionPolicies([]);
      }
      const failedDetails = [jobResult, backupResult, targetResult, storageResult, scheduleResult, retentionPolicyResult].filter((result) => result.status === "rejected").length;
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

  async function updateJob(job: Job, action: "cancel" | "retry") {
    setUpdatingJobID(job.id);
    setDetailError(null);
    try {
      const updated = await requestJSON<Job>(`/api/v1/jobs/${encodeURIComponent(job.id)}/${action}`, apiToken, {
        method: "POST",
      });
      setJobs((current) => sortJobs(current.map((item) => (item.id === updated.id ? updated : item))).slice(0, 8));
      setOverview((current) => updateOverviewJobCounts(current, job.status, updated.status));
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : `job ${action} failed`);
    } finally {
      setUpdatingJobID(null);
    }
  }

  async function inspectBackup(backup: Backup) {
    setLoadingBackupID(backup.id);
    setDetailError(null);
    try {
      const detail = await requestJSON<Backup>(`/api/v1/backups/${encodeURIComponent(backup.id)}`, apiToken);
      setSelectedBackup(detail);
      setRestoreTargetID(detail.target_id || "");
      setRestoreAt("");
      setRestorePlan(null);
      setRestoreJob(null);
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "backup detail request failed");
    } finally {
      setLoadingBackupID(null);
    }
  }

  async function inspectJob(job: Job) {
    setLoadingJobID(job.id);
    setDetailError(null);
    try {
      setSelectedJob(await requestJSON<Job>(`/api/v1/jobs/${encodeURIComponent(job.id)}`, apiToken));
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "job detail request failed");
    } finally {
      setLoadingJobID(null);
    }
  }

  async function inspectTarget(target: Target) {
    setLoadingInventoryID(target.id);
    setDetailError(null);
    try {
      setSelectedTarget(await requestJSON<Target>(`/api/v1/targets/${encodeURIComponent(target.id)}`, apiToken));
      setSelectedStorage(null);
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "target detail request failed");
    } finally {
      setLoadingInventoryID(null);
    }
  }

  async function inspectStorage(storage: Storage) {
    setLoadingInventoryID(storage.id);
    setDetailError(null);
    try {
      setSelectedStorage(await requestJSON<Storage>(`/api/v1/storages/${encodeURIComponent(storage.id)}`, apiToken));
      setSelectedTarget(null);
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "storage detail request failed");
    } finally {
      setLoadingInventoryID(null);
    }
  }

  async function inspectSchedule(schedule: Schedule) {
    setLoadingAutomationID(schedule.id);
    setDetailError(null);
    try {
      setSelectedSchedule(await requestJSON<Schedule>(`/api/v1/schedules/${encodeURIComponent(schedule.id)}`, apiToken));
      setSelectedRetentionPolicy(null);
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "schedule detail request failed");
    } finally {
      setLoadingAutomationID(null);
    }
  }

  async function inspectRetentionPolicy(policy: RetentionPolicy) {
    setLoadingAutomationID(policy.id);
    setDetailError(null);
    try {
      setSelectedRetentionPolicy(await requestJSON<RetentionPolicy>(`/api/v1/retention/policies/${encodeURIComponent(policy.id)}`, apiToken));
      setSelectedSchedule(null);
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "retention policy detail request failed");
    } finally {
      setLoadingAutomationID(null);
    }
  }

  async function toggleSchedulePause(schedule: Schedule) {
    setUpdatingScheduleID(schedule.id);
    setDetailError(null);
    try {
      const action = schedule.paused ? "resume" : "pause";
      const updated = await requestJSON<Schedule>(`/api/v1/schedules/${encodeURIComponent(schedule.id)}/${action}`, apiToken, {
        method: "POST",
      });
      setSchedules((current) => current.map((item) => (item.id === updated.id ? updated : item)));
      setSelectedSchedule((current) => (current?.id === updated.id ? updated : current));
      setOverview((current) => {
        if (!current || schedule.paused === updated.paused) {
          return current;
        }
        const delta = updated.paused ? 1 : -1;
        return {
          ...current,
          inventory: {
            ...current.inventory,
            schedules_paused: Math.max(0, current.inventory.schedules_paused + delta),
          },
        };
      });
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "schedule update failed");
    } finally {
      setUpdatingScheduleID(null);
    }
  }

  async function previewRestore() {
    if (!selectedBackup) {
      return;
    }
    setRestoring("preview");
    setDetailError(null);
    setRestoreJob(null);
    try {
      const plan = await requestJSON<RestorePlan>("/api/v1/restore/preview", apiToken, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(restorePayload(selectedBackup.id, restoreTargetID, restoreAt, true)),
      });
      setRestorePlan(plan);
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "restore preview failed");
    } finally {
      setRestoring(null);
    }
  }

  async function startDryRunRestore() {
    if (!selectedBackup) {
      return;
    }
    setRestoring("start");
    setDetailError(null);
    try {
      const response = await requestJSON<RestoreStartResponse>("/api/v1/restore", apiToken, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(restorePayload(selectedBackup.id, restoreTargetID, restoreAt, true)),
      });
      setRestorePlan(response.plan);
      setRestoreJob(response.job);
      setJobs((current) => sortJobs([response.job, ...current.filter((job) => job.id !== response.job.id)]).slice(0, 8));
      setOverview((current) => updateOverviewJobCounts(current, "", response.job.status));
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "restore dry-run request failed");
    } finally {
      setRestoring(null);
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
                        <th className="px-4 py-3 font-semibold">Action</th>
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
                            <td className="px-4 py-3">
                              <div className="flex items-center gap-2">
                                <JobActionButton job={job} updating={updatingJobID === job.id} onAction={updateJob} />
                                <Button
                                  className="h-7 px-2 text-xs"
                                  disabled={loadingJobID === job.id}
                                  onClick={() => void inspectJob(job)}
                                  type="button"
                                  variant="ghost"
                                >
                                  {loadingJobID === job.id ? "Loading" : "Details"}
                                </Button>
                              </div>
                            </td>
                          </tr>
                        ))
                      ) : (
                        <tr className="border-t border-line">
                          <td className="px-4 py-6 text-sm text-muted" colSpan={6}>
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
                      loading: loadingInventoryID === target.id,
                      onInspect: () => void inspectTarget(target),
                    }))}
                  />
                  <InventoryGroup
                    empty={loading ? "Loading storage" : "No storage"}
                    items={storages.map((storage) => ({
                      key: storage.id,
                      label: storage.name || storage.id,
                      value: storage.kind || "storage",
                      loading: loadingInventoryID === storage.id,
                      onInspect: () => void inspectStorage(storage),
                    }))}
                  />
                </div>
              </section>

              <section className="rounded-md border border-line bg-panel p-4">
                <h2 className="text-base font-semibold">Automation</h2>
                <div className="mt-4 grid gap-3">
                  <InventoryGroup
                    empty={loading ? "Loading schedules" : "No schedules"}
                    items={schedules.map((schedule) => ({
                      key: schedule.id,
                      label: schedule.name || schedule.id,
                      value: schedule.paused ? "paused" : schedule.backup_type || "schedule",
                      loading: loadingAutomationID === schedule.id,
                      onInspect: () => void inspectSchedule(schedule),
                    }))}
                  />
                  <InventoryGroup
                    empty={loading ? "Loading retention" : "No retention policies"}
                    items={retentionPolicies.map((policy) => ({
                      key: policy.id,
                      label: policy.name || policy.id,
                      value: `${policy.rules?.length ?? 0} rules`,
                      loading: loadingAutomationID === policy.id,
                      onInspect: () => void inspectRetentionPolicy(policy),
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
                          <Button
                            className="h-7 px-2 text-xs"
                            disabled={loadingBackupID === backup.id}
                            onClick={() => void inspectBackup(backup)}
                            type="button"
                            variant="ghost"
                          >
                            {loadingBackupID === backup.id ? "Loading" : "Details"}
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

              <TargetDetail target={selectedTarget} />
              <StorageDetail storage={selectedStorage} />
              <ScheduleDetail schedule={selectedSchedule} updating={updatingScheduleID === selectedSchedule?.id} onToggle={toggleSchedulePause} />
              <RetentionPolicyDetail policy={selectedRetentionPolicy} />
              <JobDetail job={selectedJob} />
              <BackupDetail
                backup={selectedBackup}
                restoreAt={restoreAt}
                restoreJob={restoreJob}
                restorePlan={restorePlan}
                restoreTargetID={restoreTargetID}
                restoring={restoring}
                targets={targets}
                onPreview={previewRestore}
                onRestoreAtChange={setRestoreAt}
                onStartDryRun={startDryRunRestore}
                onTargetChange={setRestoreTargetID}
              />
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
      <span className="shrink-0 text-muted">{label}</span>
      <span className={`min-w-0 truncate text-right font-semibold ${healthTone[tone]}`} title={value}>
        {value}
      </span>
    </div>
  );
}

function InventoryGroup({
  empty,
  items,
}: {
  empty: string;
  items: Array<{ key: string; label: string; value: string; loading?: boolean; onInspect?: () => void }>;
}) {
  if (items.length === 0) {
    return <div className="rounded-md bg-surface px-3 py-2 text-sm text-muted">{empty}</div>;
  }
  return (
    <div className="grid gap-2">
      {items.map((item) => (
        <div key={item.key} className="flex h-10 items-center justify-between gap-3 rounded-md bg-surface px-3 text-sm">
          <span className="min-w-0 truncate">{item.label}</span>
          <div className="flex shrink-0 items-center gap-2">
            <span className="font-mono text-xs text-muted">{item.value}</span>
            {item.onInspect ? (
              <Button className="h-7 px-2 text-xs" disabled={item.loading} onClick={item.onInspect} type="button" variant="ghost">
                {item.loading ? "Loading" : "Details"}
              </Button>
            ) : null}
          </div>
        </div>
      ))}
    </div>
  );
}

function JobActionButton({ job, updating, onAction }: { job: Job; updating: boolean; onAction: (job: Job, action: "cancel" | "retry") => Promise<void> }) {
  if (job.status === "queued" || job.status === "running" || job.status === "finalizing") {
    return (
      <Button className="h-7 px-2 text-xs" disabled={updating} onClick={() => void onAction(job, "cancel")} type="button" variant="ghost">
        {updating ? "Saving" : "Cancel"}
      </Button>
    );
  }
  if (job.status === "failed" || job.status === "canceled") {
    return (
      <Button className="h-7 px-2 text-xs" disabled={updating} onClick={() => void onAction(job, "retry")} type="button" variant="secondary">
        {updating ? "Saving" : "Retry"}
      </Button>
    );
  }
  return <span className="text-xs text-muted">-</span>;
}

function TargetDetail({ target }: { target: Target | null }) {
  if (!target) {
    return null;
  }
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <h2 className="text-base font-semibold">Target detail</h2>
      <div className="mt-4 grid gap-3">
        <HealthRow label="ID" value={target.id} tone="bronze" />
        <HealthRow label="Name" value={target.name || "-"} tone="indigo" />
        <HealthRow label="Driver" value={target.driver || "-"} tone="bronze" />
        <HealthRow label="Endpoint" value={target.endpoint || "-"} tone="indigo" />
        <HealthRow label="Database" value={target.database || "-"} tone="bronze" />
        <HealthRow label="Options" value={formatRecord(target.options)} tone="warning" />
        <HealthRow label="Labels" value={formatRecord(target.labels)} tone="indigo" />
        <HealthRow label="Created" value={formatDateTime(target.created_at) ?? "-"} tone="bronze" />
        <HealthRow label="Updated" value={formatDateTime(target.updated_at) ?? "-"} tone="indigo" />
      </div>
    </section>
  );
}

function StorageDetail({ storage }: { storage: Storage | null }) {
  if (!storage) {
    return null;
  }
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <h2 className="text-base font-semibold">Storage detail</h2>
      <div className="mt-4 grid gap-3">
        <HealthRow label="ID" value={storage.id} tone="bronze" />
        <HealthRow label="Name" value={storage.name || "-"} tone="indigo" />
        <HealthRow label="Kind" value={storage.kind || "-"} tone="bronze" />
        <HealthRow label="URI" value={storage.uri || "-"} tone="indigo" />
        <HealthRow label="Options" value={formatRecord(storage.options)} tone="warning" />
        <HealthRow label="Labels" value={formatRecord(storage.labels)} tone="indigo" />
        <HealthRow label="Created" value={formatDateTime(storage.created_at) ?? "-"} tone="bronze" />
        <HealthRow label="Updated" value={formatDateTime(storage.updated_at) ?? "-"} tone="indigo" />
      </div>
    </section>
  );
}

function ScheduleDetail({
  schedule,
  updating,
  onToggle,
}: {
  schedule: Schedule | null;
  updating: boolean;
  onToggle: (schedule: Schedule) => Promise<void>;
}) {
  if (!schedule) {
    return null;
  }
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">Schedule detail</h2>
        <Button className="h-7 px-2 text-xs" disabled={updating} onClick={() => void onToggle(schedule)} type="button" variant={schedule.paused ? "secondary" : "ghost"}>
          {updating ? "Saving" : schedule.paused ? "Resume" : "Pause"}
        </Button>
      </div>
      <div className="mt-4 grid gap-3">
        <HealthRow label="ID" value={schedule.id} tone="bronze" />
        <HealthRow label="Name" value={schedule.name || "-"} tone="indigo" />
        <HealthRow label="Status" value={schedule.paused ? "paused" : "active"} tone={schedule.paused ? "warning" : "success"} />
        <HealthRow label="Type" value={schedule.backup_type || "-"} tone="bronze" />
        <HealthRow label="Expression" value={schedule.expression || "-"} tone="indigo" />
        <HealthRow label="Target" value={schedule.target_id || "-"} tone="bronze" />
        <HealthRow label="Storage" value={schedule.storage_id || "-"} tone="indigo" />
        <HealthRow label="Retention" value={schedule.retention_policy_id || "-"} tone="bronze" />
        <HealthRow label="Labels" value={formatRecord(schedule.labels)} tone="indigo" />
        <HealthRow label="Created" value={formatDateTime(schedule.created_at) ?? "-"} tone="bronze" />
        <HealthRow label="Updated" value={formatDateTime(schedule.updated_at) ?? "-"} tone="indigo" />
      </div>
    </section>
  );
}

function RetentionPolicyDetail({ policy }: { policy: RetentionPolicy | null }) {
  if (!policy) {
    return null;
  }
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <h2 className="text-base font-semibold">Retention detail</h2>
      <div className="mt-4 grid gap-3">
        <HealthRow label="ID" value={policy.id} tone="bronze" />
        <HealthRow label="Name" value={policy.name || "-"} tone="indigo" />
        <HealthRow label="Rules" value={formatRules(policy.rules)} tone="warning" />
        <HealthRow label="Created" value={formatDateTime(policy.created_at) ?? "-"} tone="bronze" />
        <HealthRow label="Updated" value={formatDateTime(policy.updated_at) ?? "-"} tone="indigo" />
      </div>
    </section>
  );
}

function JobDetail({ job }: { job: Job | null }) {
  if (!job) {
    return null;
  }
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <h2 className="text-base font-semibold">Job detail</h2>
      <div className="mt-4 grid gap-3">
        <HealthRow label="ID" value={job.id} tone="bronze" />
        <HealthRow label="Status" value={job.status || "-"} tone={job.status === "succeeded" ? "success" : job.status === "failed" ? "warning" : "indigo"} />
        <HealthRow label="Operation" value={job.operation || job.type || "-"} tone="indigo" />
        <HealthRow label="Target" value={job.target_id || "-"} tone="bronze" />
        <HealthRow label="Storage" value={job.storage_id || "-"} tone="indigo" />
        <HealthRow label="Agent" value={job.agent_id || "-"} tone="bronze" />
        <HealthRow label="Queued" value={formatDateTime(job.queued_at) ?? "-"} tone="warning" />
        <HealthRow label="Started" value={formatDateTime(job.started_at) ?? "-"} tone="indigo" />
        <HealthRow label="Ended" value={formatDateTime(job.ended_at) ?? "-"} tone="success" />
        {job.schedule_id ? <HealthRow label="Schedule" value={job.schedule_id} tone="bronze" /> : null}
        {job.parent_backup_id ? <HealthRow label="Parent backup" value={job.parent_backup_id} tone="bronze" /> : null}
        {job.restore_backup_id ? <HealthRow label="Restore backup" value={job.restore_backup_id} tone="bronze" /> : null}
        {job.restore_target_id ? <HealthRow label="Restore target" value={job.restore_target_id} tone="bronze" /> : null}
        {job.restore_manifest_id ? <HealthRow label="Restore manifest" value={job.restore_manifest_id} tone="bronze" /> : null}
        {job.restore_at ? <HealthRow label="Restore at" value={formatDateTime(job.restore_at) ?? job.restore_at} tone="indigo" /> : null}
        {job.restore_manifest_ids && job.restore_manifest_ids.length > 0 ? (
          <HealthRow label="Manifests" value={job.restore_manifest_ids.join(", ")} tone="bronze" />
        ) : null}
        <HealthRow label="Dry run" value={job.restore_dry_run ? "yes" : "no"} tone="indigo" />
        <HealthRow label="Replace" value={job.restore_replace_existing ? "yes" : "no"} tone="warning" />
        {job.error ? <HealthRow label="Error" value={job.error} tone="warning" /> : null}
      </div>
    </section>
  );
}

function BackupDetail({
  backup,
  restoreAt,
  restoreJob,
  restorePlan,
  restoreTargetID,
  restoring,
  targets,
  onPreview,
  onRestoreAtChange,
  onStartDryRun,
  onTargetChange,
}: {
  backup: Backup | null;
  restoreAt: string;
  restoreJob: Job | null;
  restorePlan: RestorePlan | null;
  restoreTargetID: string;
  restoring: "preview" | "start" | null;
  targets: Target[];
  onPreview: () => Promise<void>;
  onRestoreAtChange: (value: string) => void;
  onStartDryRun: () => Promise<void>;
  onTargetChange: (value: string) => void;
}) {
  if (!backup) {
    return null;
  }
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <h2 className="text-base font-semibold">Backup detail</h2>
      <div className="mt-4 grid gap-3">
        <HealthRow label="ID" value={backup.id} tone="bronze" />
        <HealthRow label="Type" value={backup.type || "-"} tone="indigo" />
        <HealthRow label="Target" value={backup.target_id || "-"} tone="bronze" />
        <HealthRow label="Storage" value={backup.storage_id || "-"} tone="indigo" />
        <HealthRow label="Chunks" value={metricValue(backup.chunk_count, false)} tone="warning" />
        <HealthRow label="Size" value={formatBytes(backup.size_bytes)} tone="success" />
        <HealthRow label="Manifest" value={backup.manifest_id || "-"} tone="bronze" />
        <HealthRow label="Ended" value={formatDateTime(backup.ended_at) ?? "-"} tone="indigo" />
      </div>
      <div className="mt-5 border-t border-line pt-4">
        <h3 className="text-sm font-semibold text-marble">Restore validation</h3>
        <div className="mt-3 grid gap-3">
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="restore-target">
            Target
            <select
              id="restore-target"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition focus:border-bronze"
              value={restoreTargetID}
              onChange={(event) => onTargetChange(event.target.value)}
            >
              <option value={backup.target_id}>{backup.target_id || "Original target"}</option>
              {targets
                .filter((target) => target.id !== backup.target_id)
                .map((target) => (
                  <option key={target.id} value={target.id}>
                    {target.name || target.id}
                  </option>
                ))}
            </select>
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="restore-at">
            Point in time
            <input
              id="restore-at"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              placeholder="RFC3339, optional"
              value={restoreAt}
              onChange={(event) => onRestoreAtChange(event.target.value)}
            />
          </label>
          <div className="grid grid-cols-2 gap-2">
            <Button className="h-8 text-xs" disabled={restoring !== null} onClick={() => void onPreview()} type="button" variant="ghost">
              {restoring === "preview" ? "Previewing" : "Preview"}
            </Button>
            <Button className="h-8 text-xs" disabled={restoring !== null} onClick={() => void onStartDryRun()} type="button" variant="secondary">
              {restoring === "start" ? "Queuing" : "Queue dry run"}
            </Button>
          </div>
        </div>
        {restorePlan ? <RestorePlanDetail plan={restorePlan} /> : null}
        {restoreJob ? (
          <div className="mt-3 rounded-md border border-success/35 bg-success/10 px-3 py-2 text-xs text-success">
            Dry-run restore queued as {restoreJob.id}
          </div>
        ) : null}
      </div>
    </section>
  );
}

function RestorePlanDetail({ plan }: { plan: RestorePlan }) {
  return (
    <div className="mt-4 grid gap-3">
      <HealthRow label="Plan target" value={plan.target_id || "-"} tone="bronze" />
      <HealthRow label="Storage" value={plan.storage_id || "-"} tone="indigo" />
      <HealthRow label="Steps" value={metricValue(plan.steps.length, false)} tone="warning" />
      {plan.at ? <HealthRow label="At" value={formatDateTime(plan.at) ?? plan.at} tone="indigo" /> : null}
      {plan.steps.map((step, index) => (
        <HealthRow key={`${step.backup_id}-${index}`} label={`Step ${index + 1}`} value={`${step.type || "backup"} ${step.backup_id}`} tone="bronze" />
      ))}
      {plan.warnings && plan.warnings.length > 0 ? <HealthRow label="Warnings" value={plan.warnings.join(", ")} tone="warning" /> : null}
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

function restorePayload(backupID: string, targetID: string, at: string, dryRun: boolean) {
  const payload: Record<string, unknown> = {
    backup_id: backupID,
    dry_run: dryRun,
  };
  if (targetID.trim() !== "") {
    payload.target_id = targetID.trim();
  }
  if (at.trim() !== "") {
    payload.at = at.trim();
  }
  return payload;
}

function updateOverviewJobCounts(current: Overview | null, previousStatus: string, nextStatus: string) {
  if (!current || previousStatus === nextStatus) {
    return current;
  }
  const byStatus = { ...current.jobs.by_status };
  if (previousStatus !== "") {
    byStatus[previousStatus] = Math.max(0, (byStatus[previousStatus] ?? 0) - 1);
  }
  if (nextStatus !== "") {
    byStatus[nextStatus] = (byStatus[nextStatus] ?? 0) + 1;
  }
  const activeDelta = activeJobDelta(previousStatus, nextStatus);
  const failedDelta = failedJobDelta(previousStatus, nextStatus);
  return {
    ...current,
    jobs: {
      ...current.jobs,
      active: Math.max(0, current.jobs.active + activeDelta),
      by_status: byStatus,
    },
    attention: {
      ...current.attention,
      failed_jobs: Math.max(0, current.attention.failed_jobs + failedDelta),
    },
  };
}

function activeJobDelta(previousStatus: string, nextStatus: string) {
  return (isActiveJobStatus(nextStatus) ? 1 : 0) - (isActiveJobStatus(previousStatus) ? 1 : 0);
}

function failedJobDelta(previousStatus: string, nextStatus: string) {
  return (nextStatus === "failed" ? 1 : 0) - (previousStatus === "failed" ? 1 : 0);
}

function isActiveJobStatus(status: string) {
  return status === "running" || status === "finalizing";
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

function formatRecord(value: Record<string, unknown> | undefined) {
  if (!value || Object.keys(value).length === 0) {
    return "-";
  }
  return Object.entries(value)
    .map(([key, entry]) => `${key}=${String(entry)}`)
    .join(", ");
}

function formatRules(rules: RetentionPolicy["rules"] | undefined) {
  if (!rules || rules.length === 0) {
    return "-";
  }
  return rules
    .map((rule) => {
      const params = formatRecord(rule.params);
      return params === "-" ? rule.kind : `${rule.kind}(${params})`;
    })
    .join(", ");
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

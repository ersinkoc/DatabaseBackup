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
  Pencil,
  Plus,
  Play,
  RotateCcw,
  Save,
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
  restore_report?: RestoreReport;
  verify_backup_id?: string;
  verify_manifest_id?: string;
  verify_manifest_ids?: string[];
  verify_level?: string;
  verify_report?: VerificationReport;
  failure_evidence?: FailureEvidence;
  evidence_artifact?: EvidenceArtifact;
  error?: string;
};

type EvidenceArtifact = {
  id?: string;
  job_id?: string;
  kind?: string;
  sha256?: string;
  created_at?: string;
};

type FailureEvidence = {
  operation?: string;
  stage?: string;
  message?: string;
  backup_id?: string;
  target_id?: string;
  storage_id?: string;
  manifest_ids?: string[];
  at?: string;
};

type RestoreReport = {
  backup_id?: string;
  target_id?: string;
  manifest_ids?: string[];
  objects?: number;
  chunks?: number;
  stored_bytes?: number;
  restored_bytes?: number;
  dry_run?: boolean;
};

type VerificationReport = {
  backup_id?: string;
  level?: string;
  manifest_ids?: string[];
  objects?: number;
  chunks?: number;
  verified_chunks?: number;
  stored_bytes?: number;
  restored_bytes?: number;
};

type Backup = {
  id: string;
  target_id: string;
  storage_id: string;
  type: string;
  parent_id?: string;
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

type TargetForm = {
  id: string;
  name: string;
  driver: string;
  endpoint: string;
  database: string;
  username: string;
  password: string;
  tls: string;
  agent: string;
  tier: string;
};

type StorageForm = {
  id: string;
  name: string;
  kind: string;
  uri: string;
  region: string;
  endpoint: string;
  credentials: string;
  accessKey: string;
  secretKey: string;
  sessionToken: string;
  forcePathStyle: boolean;
};

type ScheduleForm = {
  id: string;
  name: string;
  targetID: string;
  storageID: string;
  backupType: string;
  expression: string;
  retentionPolicyID: string;
  paused: boolean;
};

type RetentionPolicyForm = {
  id: string;
  name: string;
  rulesJSON: string;
};

type BackupRunForm = {
  targetID: string;
  storageID: string;
  backupType: string;
  parentID: string;
};

type BackupVerificationReport = {
  backupID: string;
  ok: boolean;
  checks: Array<{ label: string; ok: boolean; value: string }>;
};

const emptyTargetForm: TargetForm = {
  id: "",
  name: "",
  driver: "redis",
  endpoint: "",
  database: "",
  username: "",
  password: "",
  tls: "",
  agent: "",
  tier: "",
};

const emptyStorageForm: StorageForm = {
  id: "",
  name: "",
  kind: "local",
  uri: "",
  region: "",
  endpoint: "",
  credentials: "",
  accessKey: "",
  secretKey: "",
  sessionToken: "",
  forcePathStyle: false,
};

const emptyScheduleForm: ScheduleForm = {
  id: "",
  name: "",
  targetID: "",
  storageID: "",
  backupType: "full",
  expression: "",
  retentionPolicyID: "",
  paused: false,
};

const emptyRetentionPolicyForm: RetentionPolicyForm = {
  id: "",
  name: "",
  rulesJSON: JSON.stringify([{ kind: "count", params: { n: 7 } }], null, 2),
};

const emptyBackupRunForm: BackupRunForm = {
  targetID: "",
  storageID: "",
  backupType: "full",
  parentID: "",
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
  const [deletingInventoryID, setDeletingInventoryID] = useState<string | null>(null);
  const [savingTarget, setSavingTarget] = useState(false);
  const [editingTargetID, setEditingTargetID] = useState<string | null>(null);
  const [targetForm, setTargetForm] = useState<TargetForm>(emptyTargetForm);
  const [savingStorage, setSavingStorage] = useState(false);
  const [editingStorageID, setEditingStorageID] = useState<string | null>(null);
  const [storageForm, setStorageForm] = useState<StorageForm>(emptyStorageForm);
  const [targetDeleteConfirmation, setTargetDeleteConfirmation] = useState("");
  const [storageDeleteConfirmation, setStorageDeleteConfirmation] = useState("");
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [retentionPolicies, setRetentionPolicies] = useState<RetentionPolicy[]>([]);
  const [selectedSchedule, setSelectedSchedule] = useState<Schedule | null>(null);
  const [selectedRetentionPolicy, setSelectedRetentionPolicy] = useState<RetentionPolicy | null>(null);
  const [loadingAutomationID, setLoadingAutomationID] = useState<string | null>(null);
  const [updatingScheduleID, setUpdatingScheduleID] = useState<string | null>(null);
  const [savingSchedule, setSavingSchedule] = useState(false);
  const [editingScheduleID, setEditingScheduleID] = useState<string | null>(null);
  const [scheduleForm, setScheduleForm] = useState<ScheduleForm>(emptyScheduleForm);
  const [savingRetentionPolicy, setSavingRetentionPolicy] = useState(false);
  const [editingRetentionPolicyID, setEditingRetentionPolicyID] = useState<string | null>(null);
  const [retentionPolicyForm, setRetentionPolicyForm] = useState<RetentionPolicyForm>(emptyRetentionPolicyForm);
  const [restoreTargetID, setRestoreTargetID] = useState("");
  const [restoreAt, setRestoreAt] = useState("");
  const [restoreConfirmation, setRestoreConfirmation] = useState("");
  const [restoreReplaceExisting, setRestoreReplaceExisting] = useState(false);
  const [restorePlan, setRestorePlan] = useState<RestorePlan | null>(null);
  const [restoreJob, setRestoreJob] = useState<Job | null>(null);
  const [restoreJobHistory, setRestoreJobHistory] = useState<Job[]>([]);
  const [restoring, setRestoring] = useState<"preview" | "start" | "live" | null>(null);
  const [backupRunForm, setBackupRunForm] = useState<BackupRunForm>(emptyBackupRunForm);
  const [backupRunJob, setBackupRunJob] = useState<Job | null>(null);
  const [queuingBackup, setQueuingBackup] = useState(false);
  const [backupVerificationReport, setBackupVerificationReport] = useState<BackupVerificationReport | null>(null);
  const [backupVerificationJob, setBackupVerificationJob] = useState<Job | null>(null);
  const [backupVerificationHistory, setBackupVerificationHistory] = useState<Job[]>([]);
  const [verifyingBackup, setVerifyingBackup] = useState(false);

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
        setTargets(sortTargets(targetResult.value.targets ?? []).slice(0, 8));
      } else {
        setTargets([]);
      }
      if (storageResult.status === "fulfilled") {
        setStorages(sortStorages(storageResult.value.storages ?? []).slice(0, 8));
      } else {
        setStorages([]);
      }
      if (scheduleResult.status === "fulfilled") {
        setSchedules(sortSchedules(scheduleResult.value.schedules ?? []).slice(0, 8));
      } else {
        setSchedules([]);
      }
      if (retentionPolicyResult.status === "fulfilled") {
        setRetentionPolicies(sortRetentionPolicies(retentionPolicyResult.value.policies ?? []).slice(0, 8));
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

  async function queueBackup() {
    if (!backupRunForm.targetID.trim() || !backupRunForm.storageID.trim()) {
      setDetailError("backup target and storage are required");
      return;
    }
    if ((backupRunForm.backupType === "incremental" || backupRunForm.backupType === "differential") && !backupRunForm.parentID.trim()) {
      setDetailError("parent backup is required for incremental or differential backups");
      return;
    }
    setQueuingBackup(true);
    setDetailError(null);
    try {
      const job = await requestJSON<Job>("/api/v1/backups/now", apiToken, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(backupRunPayload(backupRunForm)),
      });
      setBackupRunJob(job);
      setJobs((current) => sortJobs([job, ...current.filter((item) => item.id !== job.id)]).slice(0, 8));
      setOverview((current) => updateOverviewJobCounts(current, "", job.status));
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "backup queue request failed");
    } finally {
      setQueuingBackup(false);
    }
  }

  function verifyBackupMetadata(backup: Backup) {
    setBackupVerificationReport(backupMetadataReport(backup, backups));
  }

  async function queueBackupVerification(backup: Backup) {
    setVerifyingBackup(true);
    setDetailError(null);
    try {
      const job = await requestJSON<Job>(`/api/v1/backups/${encodeURIComponent(backup.id)}/verify`, apiToken, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ level: "chunk" }),
      });
      setBackupVerificationJob(job);
      setBackupVerificationHistory((current) => sortJobs([job, ...current.filter((item) => item.id !== job.id)]).slice(0, 5));
      setJobs((current) => sortJobs([job, ...current.filter((item) => item.id !== job.id)]).slice(0, 8));
      setOverview((current) => updateOverviewJobCounts(current, "", job.status));
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "backup verification request failed");
    } finally {
      setVerifyingBackup(false);
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
      const [detail, restoreJobs, verificationJobs] = await Promise.all([
        requestJSON<Backup>(`/api/v1/backups/${encodeURIComponent(backup.id)}`, apiToken),
        requestJSON<JobListResponse>(`/api/v1/jobs?operation=restore&restore_backup_id=${encodeURIComponent(backup.id)}`, apiToken),
        requestJSON<JobListResponse>(`/api/v1/jobs?operation=verify&verify_backup_id=${encodeURIComponent(backup.id)}`, apiToken),
      ]);
      setSelectedBackup(detail);
      setRestoreJobHistory(sortJobs(restoreJobs.jobs ?? []).slice(0, 5));
      setBackupVerificationHistory(sortJobs(verificationJobs.jobs ?? []).slice(0, 5));
      setRestoreTargetID(detail.target_id || "");
      setRestoreAt("");
      setRestoreConfirmation("");
      setRestoreReplaceExisting(false);
      setRestorePlan(null);
      setRestoreJob(null);
      setBackupVerificationReport(null);
      setBackupVerificationJob(null);
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
      setTargetDeleteConfirmation("");
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
      setStorageDeleteConfirmation("");
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "storage detail request failed");
    } finally {
      setLoadingInventoryID(null);
    }
  }

  async function editTarget(target: Target) {
    setLoadingInventoryID(target.id);
    setDetailError(null);
    try {
      const detail = await requestJSON<Target>(`/api/v1/targets/${encodeURIComponent(target.id)}?include_secrets=true`, apiToken);
      setEditingTargetID(detail.id);
      setTargetForm(targetFormFromTarget(detail));
      setTargetDeleteConfirmation("");
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "target edit request failed");
    } finally {
      setLoadingInventoryID(null);
    }
  }

  function resetTargetForm() {
    setEditingTargetID(null);
    setTargetForm(emptyTargetForm);
    setTargetDeleteConfirmation("");
  }

  async function saveTarget() {
    if (!targetForm.name.trim() || !targetForm.driver.trim() || !targetForm.endpoint.trim()) {
      setDetailError("target name, driver, and endpoint are required");
      return;
    }
    setSavingTarget(true);
    setDetailError(null);
    try {
      const payload = targetPayload(targetForm, editingTargetID);
      const saved = editingTargetID
        ? await requestJSON<Target>(`/api/v1/targets/${encodeURIComponent(editingTargetID)}`, apiToken, {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          })
        : await requestJSON<Target>("/api/v1/targets", apiToken, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          });
      setTargets((current) => sortTargets([saved, ...current.filter((item) => item.id !== saved.id)]).slice(0, 8));
      setSelectedTarget(saved);
      setSelectedStorage(null);
      setEditingTargetID(null);
      setTargetForm(emptyTargetForm);
      setTargetDeleteConfirmation("");
      if (!editingTargetID) {
        setOverview((current) =>
          current
            ? {
                ...current,
                inventory: {
                  ...current.inventory,
                  targets: current.inventory.targets + 1,
                },
              }
            : current,
        );
      }
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "target save failed");
    } finally {
      setSavingTarget(false);
    }
  }

  async function deleteTarget(target: Target) {
    if (targetDeleteConfirmation !== target.id) {
      return;
    }
    setDeletingInventoryID(target.id);
    setDetailError(null);
    try {
      await requestJSON<void>(`/api/v1/targets/${encodeURIComponent(target.id)}`, apiToken, { method: "DELETE" });
      setTargets((current) => current.filter((item) => item.id !== target.id));
      setSelectedTarget(null);
      setTargetDeleteConfirmation("");
      setOverview((current) =>
        current
          ? {
              ...current,
              inventory: {
                ...current.inventory,
                targets: Math.max(0, current.inventory.targets - 1),
              },
            }
          : current,
      );
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "target delete failed");
    } finally {
      setDeletingInventoryID(null);
    }
  }

  async function editStorage(storage: Storage) {
    setLoadingInventoryID(storage.id);
    setDetailError(null);
    try {
      const detail = await requestJSON<Storage>(`/api/v1/storages/${encodeURIComponent(storage.id)}?include_secrets=true`, apiToken);
      setEditingStorageID(detail.id);
      setStorageForm(storageFormFromStorage(detail));
      setStorageDeleteConfirmation("");
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "storage edit request failed");
    } finally {
      setLoadingInventoryID(null);
    }
  }

  function resetStorageForm() {
    setEditingStorageID(null);
    setStorageForm(emptyStorageForm);
    setStorageDeleteConfirmation("");
  }

  async function saveStorage() {
    if (!storageForm.name.trim() || !storageForm.kind.trim() || !storageForm.uri.trim()) {
      setDetailError("storage name, kind, and uri are required");
      return;
    }
    setSavingStorage(true);
    setDetailError(null);
    try {
      const payload = storagePayload(storageForm, editingStorageID);
      const saved = editingStorageID
        ? await requestJSON<Storage>(`/api/v1/storages/${encodeURIComponent(editingStorageID)}`, apiToken, {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          })
        : await requestJSON<Storage>("/api/v1/storages", apiToken, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          });
      setStorages((current) => sortStorages([saved, ...current.filter((item) => item.id !== saved.id)]).slice(0, 8));
      setSelectedStorage(saved);
      setSelectedTarget(null);
      setEditingStorageID(null);
      setStorageForm(emptyStorageForm);
      setStorageDeleteConfirmation("");
      if (!editingStorageID) {
        setOverview((current) =>
          current
            ? {
                ...current,
                inventory: {
                  ...current.inventory,
                  storages: current.inventory.storages + 1,
                },
              }
            : current,
        );
      }
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "storage save failed");
    } finally {
      setSavingStorage(false);
    }
  }

  async function deleteStorage(storage: Storage) {
    if (storageDeleteConfirmation !== storage.id) {
      return;
    }
    setDeletingInventoryID(storage.id);
    setDetailError(null);
    try {
      await requestJSON<void>(`/api/v1/storages/${encodeURIComponent(storage.id)}`, apiToken, { method: "DELETE" });
      setStorages((current) => current.filter((item) => item.id !== storage.id));
      setSelectedStorage(null);
      setStorageDeleteConfirmation("");
      setOverview((current) =>
        current
          ? {
              ...current,
              inventory: {
                ...current.inventory,
                storages: Math.max(0, current.inventory.storages - 1),
              },
            }
          : current,
      );
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "storage delete failed");
    } finally {
      setDeletingInventoryID(null);
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

  function editSchedule(schedule: Schedule) {
    setEditingScheduleID(schedule.id);
    setScheduleForm(scheduleFormFromSchedule(schedule));
  }

  function resetScheduleForm() {
    setEditingScheduleID(null);
    setScheduleForm(emptyScheduleForm);
  }

  async function saveSchedule() {
    if (!scheduleForm.name.trim() || !scheduleForm.targetID.trim() || !scheduleForm.storageID.trim() || !scheduleForm.expression.trim()) {
      setDetailError("schedule name, target, storage, and expression are required");
      return;
    }
    setSavingSchedule(true);
    setDetailError(null);
    try {
      const payload = schedulePayload(scheduleForm, editingScheduleID);
      const saved = editingScheduleID
        ? await requestJSON<Schedule>(`/api/v1/schedules/${encodeURIComponent(editingScheduleID)}`, apiToken, {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          })
        : await requestJSON<Schedule>("/api/v1/schedules", apiToken, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          });
      setSchedules((current) => sortSchedules([saved, ...current.filter((item) => item.id !== saved.id)]).slice(0, 8));
      setSelectedSchedule(saved);
      setSelectedRetentionPolicy(null);
      setEditingScheduleID(null);
      setScheduleForm(emptyScheduleForm);
      const previousSchedule = editingScheduleID ? schedules.find((item) => item.id === editingScheduleID) ?? (selectedSchedule?.id === editingScheduleID ? selectedSchedule : null) : null;
      setOverview((current) => updateOverviewScheduleCounts(current, previousSchedule, saved));
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "schedule save failed");
    } finally {
      setSavingSchedule(false);
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

  function editRetentionPolicy(policy: RetentionPolicy) {
    setEditingRetentionPolicyID(policy.id);
    setRetentionPolicyForm(retentionPolicyFormFromPolicy(policy));
  }

  function resetRetentionPolicyForm() {
    setEditingRetentionPolicyID(null);
    setRetentionPolicyForm(emptyRetentionPolicyForm);
  }

  async function saveRetentionPolicy() {
    if (!retentionPolicyForm.name.trim()) {
      setDetailError("retention policy name is required");
      return;
    }
    let payload: Record<string, unknown>;
    try {
      payload = retentionPolicyPayload(retentionPolicyForm, editingRetentionPolicyID);
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "retention policy rules are invalid");
      return;
    }
    setSavingRetentionPolicy(true);
    setDetailError(null);
    try {
      const saved = editingRetentionPolicyID
        ? await requestJSON<RetentionPolicy>(`/api/v1/retention/policies/${encodeURIComponent(editingRetentionPolicyID)}`, apiToken, {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          })
        : await requestJSON<RetentionPolicy>("/api/v1/retention/policies", apiToken, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          });
      setRetentionPolicies((current) => sortRetentionPolicies([saved, ...current.filter((item) => item.id !== saved.id)]).slice(0, 8));
      setSelectedRetentionPolicy(saved);
      setSelectedSchedule(null);
      setEditingRetentionPolicyID(null);
      setRetentionPolicyForm(emptyRetentionPolicyForm);
      if (!editingRetentionPolicyID) {
        setOverview((current) =>
          current
            ? {
                ...current,
                inventory: {
                  ...current.inventory,
                  retention_policies: current.inventory.retention_policies + 1,
                },
              }
            : current,
        );
      }
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "retention policy save failed");
    } finally {
      setSavingRetentionPolicy(false);
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
        body: JSON.stringify(restorePayload(selectedBackup.id, restoreTargetID, restoreAt, true, false)),
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
        body: JSON.stringify(restorePayload(selectedBackup.id, restoreTargetID, restoreAt, true, false)),
      });
      setRestorePlan(response.plan);
      setRestoreJob(response.job);
      setRestoreJobHistory((current) => sortJobs([response.job, ...current.filter((job) => job.id !== response.job.id)]).slice(0, 5));
      setJobs((current) => sortJobs([response.job, ...current.filter((job) => job.id !== response.job.id)]).slice(0, 8));
      setOverview((current) => updateOverviewJobCounts(current, "", response.job.status));
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "restore dry-run request failed");
    } finally {
      setRestoring(null);
    }
  }

  async function startLiveRestore() {
    if (!selectedBackup || restoreConfirmation !== selectedBackup.id) {
      return;
    }
    setRestoring("live");
    setDetailError(null);
    try {
      const response = await requestJSON<RestoreStartResponse>("/api/v1/restore", apiToken, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(restorePayload(selectedBackup.id, restoreTargetID, restoreAt, false, restoreReplaceExisting)),
      });
      setRestorePlan(response.plan);
      setRestoreJob(response.job);
      setRestoreJobHistory((current) => sortJobs([response.job, ...current.filter((job) => job.id !== response.job.id)]).slice(0, 5));
      setJobs((current) => sortJobs([response.job, ...current.filter((job) => job.id !== response.job.id)]).slice(0, 8));
      setOverview((current) => updateOverviewJobCounts(current, "", response.job.status));
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : "restore request failed");
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
              <Button variant="primary" icon={<Play className="h-4 w-4" />} onClick={() => void queueBackup()} type="button">
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

              <BackupRunPanel
                backups={backups}
                form={backupRunForm}
                job={backupRunJob}
                queuing={queuingBackup}
                storages={storages}
                targets={targets}
                onChange={(patch) => setBackupRunForm((current) => ({ ...current, ...patch }))}
                onQueue={queueBackup}
              />

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

              <TargetEditor
                editing={editingTargetID !== null}
                form={targetForm}
                saving={savingTarget}
                onChange={(patch) => setTargetForm((current) => ({ ...current, ...patch }))}
                onReset={resetTargetForm}
                onSave={saveTarget}
              />
              <StorageEditor
                editing={editingStorageID !== null}
                form={storageForm}
                saving={savingStorage}
                onChange={(patch) => setStorageForm((current) => ({ ...current, ...patch }))}
                onReset={resetStorageForm}
                onSave={saveStorage}
              />
              <ScheduleEditor
                editing={editingScheduleID !== null}
                form={scheduleForm}
                retentionPolicies={retentionPolicies}
                saving={savingSchedule}
                storages={storages}
                targets={targets}
                onChange={(patch) => setScheduleForm((current) => ({ ...current, ...patch }))}
                onReset={resetScheduleForm}
                onSave={saveSchedule}
              />
              <RetentionPolicyEditor
                editing={editingRetentionPolicyID !== null}
                form={retentionPolicyForm}
                saving={savingRetentionPolicy}
                onChange={(patch) => setRetentionPolicyForm((current) => ({ ...current, ...patch }))}
                onReset={resetRetentionPolicyForm}
                onSave={saveRetentionPolicy}
              />

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

              <TargetDetail
                confirmation={targetDeleteConfirmation}
                deleting={deletingInventoryID === selectedTarget?.id}
                target={selectedTarget}
                onConfirmationChange={setTargetDeleteConfirmation}
                onDelete={deleteTarget}
                onEdit={editTarget}
              />
              <StorageDetail
                confirmation={storageDeleteConfirmation}
                deleting={deletingInventoryID === selectedStorage?.id}
                storage={selectedStorage}
                onConfirmationChange={setStorageDeleteConfirmation}
                onDelete={deleteStorage}
                onEdit={editStorage}
              />
              <ScheduleDetail schedule={selectedSchedule} updating={updatingScheduleID === selectedSchedule?.id} onEdit={editSchedule} onToggle={toggleSchedulePause} />
              <RetentionPolicyDetail policy={selectedRetentionPolicy} onEdit={editRetentionPolicy} />
              <JobDetail job={selectedJob} />
              <BackupDetail
                backup={selectedBackup}
                backups={backups}
                report={backupVerificationReport}
                verificationHistory={backupVerificationHistory}
                verificationJob={backupVerificationJob}
                verifyingBackup={verifyingBackup}
                restoreAt={restoreAt}
                restoreConfirmation={restoreConfirmation}
                restoreHistory={restoreJobHistory}
                restoreJob={restoreJob}
                restorePlan={restorePlan}
                restoreReplaceExisting={restoreReplaceExisting}
                restoreTargetID={restoreTargetID}
                restoring={restoring}
                targets={targets}
                onPreview={previewRestore}
                onRestoreConfirmationChange={setRestoreConfirmation}
                onRestoreAtChange={setRestoreAt}
                onRestoreReplaceExistingChange={setRestoreReplaceExisting}
                onStartLive={startLiveRestore}
                onStartDryRun={startDryRunRestore}
                onTargetChange={setRestoreTargetID}
                onQueueVerification={queueBackupVerification}
                onVerify={verifyBackupMetadata}
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

function BackupRunPanel({
  backups,
  form,
  job,
  queuing,
  storages,
  targets,
  onChange,
  onQueue,
}: {
  backups: Backup[];
  form: BackupRunForm;
  job: Job | null;
  queuing: boolean;
  storages: Storage[];
  targets: Target[];
  onChange: (patch: Partial<BackupRunForm>) => void;
  onQueue: () => Promise<void>;
}) {
  const needsParent = form.backupType === "incremental" || form.backupType === "differential";
  const parentOptions = backups.filter((backup) => {
    if (form.targetID && backup.target_id !== form.targetID) {
      return false;
    }
    if (form.storageID && backup.storage_id !== form.storageID) {
      return false;
    }
    return true;
  });
  const disabled = queuing || !form.targetID || !form.storageID || (needsParent && !form.parentID);
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">Backup drill</h2>
        <Button className="h-7 px-2 text-xs" disabled={disabled} icon={<Play className="h-3.5 w-3.5" />} onClick={() => void onQueue()} type="button" variant="primary">
          {queuing ? "Queuing" : "Queue"}
        </Button>
      </div>
      <div className="mt-4 grid gap-3">
        <div className="grid grid-cols-2 gap-2">
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="backup-target">
            Target
            <select
              id="backup-target"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition focus:border-bronze"
              value={form.targetID}
              onChange={(event) => onChange({ targetID: event.target.value, parentID: "" })}
            >
              <option value="">select</option>
              {targets.map((target) => (
                <option key={target.id} value={target.id}>
                  {target.name || target.id}
                </option>
              ))}
            </select>
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="backup-storage">
            Storage
            <select
              id="backup-storage"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition focus:border-bronze"
              value={form.storageID}
              onChange={(event) => onChange({ storageID: event.target.value, parentID: "" })}
            >
              <option value="">select</option>
              {storages.map((storage) => (
                <option key={storage.id} value={storage.id}>
                  {storage.name || storage.id}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div className="grid grid-cols-2 gap-2">
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="backup-type">
            Type
            <select
              id="backup-type"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition focus:border-bronze"
              value={form.backupType}
              onChange={(event) => onChange({ backupType: event.target.value, parentID: event.target.value === "full" ? "" : form.parentID })}
            >
              <option value="full">full</option>
              <option value="incremental">incremental</option>
              <option value="differential">differential</option>
            </select>
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="backup-parent">
            Parent
            <select
              id="backup-parent"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition focus:border-bronze disabled:opacity-70"
              disabled={!needsParent}
              value={form.parentID}
              onChange={(event) => onChange({ parentID: event.target.value })}
            >
              <option value="">{needsParent ? "select" : "none"}</option>
              {parentOptions.map((backup) => (
                <option key={backup.id} value={backup.id}>
                  {backup.id}
                </option>
              ))}
            </select>
          </label>
        </div>
        {job ? (
          <div className="rounded-md border border-success/35 bg-success/10 px-3 py-2 text-xs text-success">
            Backup queued as {job.id}
          </div>
        ) : null}
      </div>
    </section>
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

function TargetEditor({
  editing,
  form,
  saving,
  onChange,
  onReset,
  onSave,
}: {
  editing: boolean;
  form: TargetForm;
  saving: boolean;
  onChange: (patch: Partial<TargetForm>) => void;
  onReset: () => void;
  onSave: () => Promise<void>;
}) {
  const disabled = saving || !form.name.trim() || !form.driver.trim() || !form.endpoint.trim();
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">Target editor</h2>
        <Button className="h-7 px-2 text-xs" icon={<Plus className="h-3.5 w-3.5" />} onClick={onReset} type="button" variant="ghost">
          New
        </Button>
      </div>
      <div className="mt-4 grid gap-3">
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="target-id">
          ID
          <input
            id="target-id"
            className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze disabled:opacity-70"
            disabled={editing}
            placeholder="optional"
            value={form.id}
            onChange={(event) => onChange({ id: event.target.value })}
          />
        </label>
        <div className="grid grid-cols-2 gap-2">
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="target-name">
            Name
            <input
              id="target-name"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              value={form.name}
              onChange={(event) => onChange({ name: event.target.value })}
            />
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="target-driver">
            Driver
            <select
              id="target-driver"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition focus:border-bronze"
              value={form.driver}
              onChange={(event) => onChange({ driver: event.target.value })}
            >
              <option value="redis">redis</option>
              <option value="postgres">postgres</option>
              <option value="mysql">mysql</option>
              <option value="mariadb">mariadb</option>
              <option value="mongodb">mongodb</option>
            </select>
          </label>
        </div>
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="target-endpoint">
          Endpoint
          <input
            id="target-endpoint"
            className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
            placeholder="host:port"
            value={form.endpoint}
            onChange={(event) => onChange({ endpoint: event.target.value })}
          />
        </label>
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="target-database">
          Database
          <input
            id="target-database"
            className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
            placeholder="optional"
            value={form.database}
            onChange={(event) => onChange({ database: event.target.value })}
          />
        </label>
        <div className="grid grid-cols-2 gap-2">
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="target-user">
            User
            <input
              id="target-user"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              value={form.username}
              onChange={(event) => onChange({ username: event.target.value })}
            />
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="target-password">
            Password
            <input
              id="target-password"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              type="password"
              value={form.password}
              onChange={(event) => onChange({ password: event.target.value })}
            />
          </label>
        </div>
        <div className="grid grid-cols-3 gap-2">
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="target-tls">
            TLS
            <select
              id="target-tls"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition focus:border-bronze"
              value={form.tls}
              onChange={(event) => onChange({ tls: event.target.value })}
            >
              <option value="">auto</option>
              <option value="disable">disable</option>
              <option value="require">require</option>
            </select>
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="target-agent">
            Agent
            <input
              id="target-agent"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              value={form.agent}
              onChange={(event) => onChange({ agent: event.target.value })}
            />
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="target-tier">
            Tier
            <input
              id="target-tier"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              value={form.tier}
              onChange={(event) => onChange({ tier: event.target.value })}
            />
          </label>
        </div>
        <Button className="h-8 text-xs" disabled={disabled} icon={<Save className="h-4 w-4" />} onClick={() => void onSave()} type="button" variant="primary">
          {saving ? "Saving" : editing ? "Update target" : "Create target"}
        </Button>
      </div>
    </section>
  );
}

function StorageEditor({
  editing,
  form,
  saving,
  onChange,
  onReset,
  onSave,
}: {
  editing: boolean;
  form: StorageForm;
  saving: boolean;
  onChange: (patch: Partial<StorageForm>) => void;
  onReset: () => void;
  onSave: () => Promise<void>;
}) {
  const disabled = saving || !form.name.trim() || !form.kind.trim() || !form.uri.trim();
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">Storage editor</h2>
        <Button className="h-7 px-2 text-xs" icon={<Plus className="h-3.5 w-3.5" />} onClick={onReset} type="button" variant="ghost">
          New
        </Button>
      </div>
      <div className="mt-4 grid gap-3">
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="storage-id">
          ID
          <input
            id="storage-id"
            className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze disabled:opacity-70"
            disabled={editing}
            placeholder="optional"
            value={form.id}
            onChange={(event) => onChange({ id: event.target.value })}
          />
        </label>
        <div className="grid grid-cols-2 gap-2">
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="storage-name">
            Name
            <input
              id="storage-name"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              value={form.name}
              onChange={(event) => onChange({ name: event.target.value })}
            />
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="storage-kind">
            Kind
            <select
              id="storage-kind"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition focus:border-bronze"
              value={form.kind}
              onChange={(event) => onChange({ kind: event.target.value })}
            >
              <option value="local">local</option>
              <option value="s3">s3</option>
            </select>
          </label>
        </div>
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="storage-uri">
          URI
          <input
            id="storage-uri"
            className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
            placeholder="file:///var/lib/kronos/repo or s3://bucket/prefix"
            value={form.uri}
            onChange={(event) => onChange({ uri: event.target.value })}
          />
        </label>
        <div className="grid grid-cols-2 gap-2">
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="storage-region">
            Region
            <input
              id="storage-region"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              value={form.region}
              onChange={(event) => onChange({ region: event.target.value })}
            />
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="storage-endpoint">
            Endpoint
            <input
              id="storage-endpoint"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              value={form.endpoint}
              onChange={(event) => onChange({ endpoint: event.target.value })}
            />
          </label>
        </div>
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="storage-credentials">
          Credentials
          <input
            id="storage-credentials"
            className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
            placeholder="mode or secret reference"
            value={form.credentials}
            onChange={(event) => onChange({ credentials: event.target.value })}
          />
        </label>
        <div className="grid grid-cols-2 gap-2">
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="storage-access-key">
            Access key
            <input
              id="storage-access-key"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              type="password"
              value={form.accessKey}
              onChange={(event) => onChange({ accessKey: event.target.value })}
            />
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="storage-secret-key">
            Secret key
            <input
              id="storage-secret-key"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              type="password"
              value={form.secretKey}
              onChange={(event) => onChange({ secretKey: event.target.value })}
            />
          </label>
        </div>
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="storage-session-token">
          Session token
          <input
            id="storage-session-token"
            className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
            type="password"
            value={form.sessionToken}
            onChange={(event) => onChange({ sessionToken: event.target.value })}
          />
        </label>
        <label className="flex min-h-9 items-center gap-2 rounded-md bg-surface px-3 text-sm text-muted" htmlFor="storage-force-path-style">
          <input
            id="storage-force-path-style"
            checked={form.forcePathStyle}
            className="h-4 w-4 accent-bronze"
            type="checkbox"
            onChange={(event) => onChange({ forcePathStyle: event.target.checked })}
          />
          Force path-style requests
        </label>
        <Button className="h-8 text-xs" disabled={disabled} icon={<Save className="h-4 w-4" />} onClick={() => void onSave()} type="button" variant="primary">
          {saving ? "Saving" : editing ? "Update storage" : "Create storage"}
        </Button>
      </div>
    </section>
  );
}

function ScheduleEditor({
  editing,
  form,
  retentionPolicies,
  saving,
  storages,
  targets,
  onChange,
  onReset,
  onSave,
}: {
  editing: boolean;
  form: ScheduleForm;
  retentionPolicies: RetentionPolicy[];
  saving: boolean;
  storages: Storage[];
  targets: Target[];
  onChange: (patch: Partial<ScheduleForm>) => void;
  onReset: () => void;
  onSave: () => Promise<void>;
}) {
  const disabled = saving || !form.name.trim() || !form.targetID.trim() || !form.storageID.trim() || !form.expression.trim();
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">Schedule editor</h2>
        <Button className="h-7 px-2 text-xs" icon={<Plus className="h-3.5 w-3.5" />} onClick={onReset} type="button" variant="ghost">
          New
        </Button>
      </div>
      <div className="mt-4 grid gap-3">
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="schedule-id">
          ID
          <input
            id="schedule-id"
            className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze disabled:opacity-70"
            disabled={editing}
            placeholder="optional"
            value={form.id}
            onChange={(event) => onChange({ id: event.target.value })}
          />
        </label>
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="schedule-name">
          Name
          <input
            id="schedule-name"
            className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
            value={form.name}
            onChange={(event) => onChange({ name: event.target.value })}
          />
        </label>
        <div className="grid grid-cols-2 gap-2">
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="schedule-target">
            Target
            <select
              id="schedule-target"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition focus:border-bronze"
              value={form.targetID}
              onChange={(event) => onChange({ targetID: event.target.value })}
            >
              <option value="">select</option>
              {targets.map((target) => (
                <option key={target.id} value={target.id}>
                  {target.name || target.id}
                </option>
              ))}
            </select>
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="schedule-storage">
            Storage
            <select
              id="schedule-storage"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition focus:border-bronze"
              value={form.storageID}
              onChange={(event) => onChange({ storageID: event.target.value })}
            >
              <option value="">select</option>
              {storages.map((storage) => (
                <option key={storage.id} value={storage.id}>
                  {storage.name || storage.id}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div className="grid grid-cols-2 gap-2">
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="schedule-type">
            Type
            <select
              id="schedule-type"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition focus:border-bronze"
              value={form.backupType}
              onChange={(event) => onChange({ backupType: event.target.value })}
            >
              <option value="full">full</option>
              <option value="incremental">incremental</option>
            </select>
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="schedule-retention">
            Retention
            <select
              id="schedule-retention"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition focus:border-bronze"
              value={form.retentionPolicyID}
              onChange={(event) => onChange({ retentionPolicyID: event.target.value })}
            >
              <option value="">none</option>
              {retentionPolicies.map((policy) => (
                <option key={policy.id} value={policy.id}>
                  {policy.name || policy.id}
                </option>
              ))}
            </select>
          </label>
        </div>
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="schedule-expression">
          Expression
          <input
            id="schedule-expression"
            className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
            placeholder="0 2 * * *"
            value={form.expression}
            onChange={(event) => onChange({ expression: event.target.value })}
          />
        </label>
        <label className="flex min-h-9 items-center gap-2 rounded-md bg-surface px-3 text-sm text-muted" htmlFor="schedule-paused">
          <input
            id="schedule-paused"
            checked={form.paused}
            className="h-4 w-4 accent-bronze"
            type="checkbox"
            onChange={(event) => onChange({ paused: event.target.checked })}
          />
          Paused
        </label>
        <Button className="h-8 text-xs" disabled={disabled} icon={<Save className="h-4 w-4" />} onClick={() => void onSave()} type="button" variant="primary">
          {saving ? "Saving" : editing ? "Update schedule" : "Create schedule"}
        </Button>
      </div>
    </section>
  );
}

function RetentionPolicyEditor({
  editing,
  form,
  saving,
  onChange,
  onReset,
  onSave,
}: {
  editing: boolean;
  form: RetentionPolicyForm;
  saving: boolean;
  onChange: (patch: Partial<RetentionPolicyForm>) => void;
  onReset: () => void;
  onSave: () => Promise<void>;
}) {
  const disabled = saving || !form.name.trim() || !form.rulesJSON.trim();
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">Retention editor</h2>
        <Button className="h-7 px-2 text-xs" icon={<Plus className="h-3.5 w-3.5" />} onClick={onReset} type="button" variant="ghost">
          New
        </Button>
      </div>
      <div className="mt-4 grid gap-3">
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="retention-id">
          ID
          <input
            id="retention-id"
            className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze disabled:opacity-70"
            disabled={editing}
            placeholder="optional"
            value={form.id}
            onChange={(event) => onChange({ id: event.target.value })}
          />
        </label>
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="retention-name">
          Name
          <input
            id="retention-name"
            className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
            value={form.name}
            onChange={(event) => onChange({ name: event.target.value })}
          />
        </label>
        <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="retention-rules">
          Rules
          <textarea
            id="retention-rules"
            className="min-h-36 resize-y rounded-md border border-line bg-surface px-3 py-2 font-mono text-xs normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
            spellCheck={false}
            value={form.rulesJSON}
            onChange={(event) => onChange({ rulesJSON: event.target.value })}
          />
        </label>
        <Button className="h-8 text-xs" disabled={disabled} icon={<Save className="h-4 w-4" />} onClick={() => void onSave()} type="button" variant="primary">
          {saving ? "Saving" : editing ? "Update retention" : "Create retention"}
        </Button>
      </div>
    </section>
  );
}

function TargetDetail({
  confirmation,
  deleting,
  target,
  onConfirmationChange,
  onDelete,
  onEdit,
}: {
  confirmation: string;
  deleting: boolean;
  target: Target | null;
  onConfirmationChange: (value: string) => void;
  onDelete: (target: Target) => Promise<void>;
  onEdit: (target: Target) => Promise<void>;
}) {
  if (!target) {
    return null;
  }
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">Target detail</h2>
        <Button className="h-7 px-2 text-xs" icon={<Pencil className="h-3.5 w-3.5" />} onClick={() => void onEdit(target)} type="button" variant="ghost">
          Edit
        </Button>
      </div>
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
      <DeleteResourceControl
        confirmation={confirmation}
        deleting={deleting}
        id={target.id}
        label="target"
        onChange={onConfirmationChange}
        onDelete={() => void onDelete(target)}
      />
    </section>
  );
}

function StorageDetail({
  confirmation,
  deleting,
  storage,
  onConfirmationChange,
  onDelete,
  onEdit,
}: {
  confirmation: string;
  deleting: boolean;
  storage: Storage | null;
  onConfirmationChange: (value: string) => void;
  onDelete: (storage: Storage) => Promise<void>;
  onEdit: (storage: Storage) => Promise<void>;
}) {
  if (!storage) {
    return null;
  }
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">Storage detail</h2>
        <Button className="h-7 px-2 text-xs" icon={<Pencil className="h-3.5 w-3.5" />} onClick={() => void onEdit(storage)} type="button" variant="ghost">
          Edit
        </Button>
      </div>
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
      <DeleteResourceControl
        confirmation={confirmation}
        deleting={deleting}
        id={storage.id}
        label="storage"
        onChange={onConfirmationChange}
        onDelete={() => void onDelete(storage)}
      />
    </section>
  );
}

function DeleteResourceControl({
  confirmation,
  deleting,
  id,
  label,
  onChange,
  onDelete,
}: {
  confirmation: string;
  deleting: boolean;
  id: string;
  label: string;
  onChange: (value: string) => void;
  onDelete: () => void;
}) {
  return (
    <div className="mt-5 border-t border-line pt-4">
      <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor={`${label}-delete-confirm`}>
        Confirm {label} ID
        <input
          id={`${label}-delete-confirm`}
          className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
          placeholder={id}
          value={confirmation}
          onChange={(event) => onChange(event.target.value)}
        />
      </label>
      <Button className="mt-3 h-8 w-full text-xs" disabled={deleting || confirmation !== id} onClick={onDelete} type="button" variant="ghost">
        {deleting ? "Deleting" : `Delete ${label}`}
      </Button>
    </div>
  );
}

function ScheduleDetail({
  schedule,
  updating,
  onEdit,
  onToggle,
}: {
  schedule: Schedule | null;
  updating: boolean;
  onEdit: (schedule: Schedule) => void;
  onToggle: (schedule: Schedule) => Promise<void>;
}) {
  if (!schedule) {
    return null;
  }
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">Schedule detail</h2>
        <div className="flex items-center gap-2">
          <Button className="h-7 px-2 text-xs" icon={<Pencil className="h-3.5 w-3.5" />} onClick={() => onEdit(schedule)} type="button" variant="ghost">
            Edit
          </Button>
          <Button className="h-7 px-2 text-xs" disabled={updating} onClick={() => void onToggle(schedule)} type="button" variant={schedule.paused ? "secondary" : "ghost"}>
            {updating ? "Saving" : schedule.paused ? "Resume" : "Pause"}
          </Button>
        </div>
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

function RetentionPolicyDetail({ policy, onEdit }: { policy: RetentionPolicy | null; onEdit: (policy: RetentionPolicy) => void }) {
  if (!policy) {
    return null;
  }
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">Retention detail</h2>
        <Button className="h-7 px-2 text-xs" icon={<Pencil className="h-3.5 w-3.5" />} onClick={() => onEdit(policy)} type="button" variant="ghost">
          Edit
        </Button>
      </div>
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
        {job.verify_backup_id ? <HealthRow label="Verify backup" value={job.verify_backup_id} tone="bronze" /> : null}
        {job.verify_manifest_id ? <HealthRow label="Verify manifest" value={job.verify_manifest_id} tone="bronze" /> : null}
        {job.verify_manifest_ids && job.verify_manifest_ids.length > 0 ? (
          <HealthRow label="Verify chain" value={job.verify_manifest_ids.join(", ")} tone="bronze" />
        ) : null}
        {job.verify_level ? <HealthRow label="Verify level" value={job.verify_level} tone="indigo" /> : null}
        {job.restore_report ? <RestoreReportDetail report={job.restore_report} /> : null}
        {job.verify_report ? <VerificationReportDetail report={job.verify_report} /> : null}
        {job.failure_evidence ? <FailureEvidenceDetail evidence={job.failure_evidence} /> : null}
        {job.evidence_artifact ? <EvidenceArtifactDetail artifact={job.evidence_artifact} jobID={job.id} /> : null}
        {job.operation === "restore" ? <HealthRow label="Dry run" value={job.restore_dry_run ? "yes" : "no"} tone="indigo" /> : null}
        {job.operation === "restore" ? <HealthRow label="Replace" value={job.restore_replace_existing ? "yes" : "no"} tone="warning" /> : null}
        {job.error ? <HealthRow label="Error" value={job.error} tone="warning" /> : null}
      </div>
    </section>
  );
}

function BackupDetail({
  backup,
  backups,
  report,
  verificationHistory,
  verificationJob,
  verifyingBackup,
  restoreAt,
  restoreConfirmation,
  restoreHistory,
  restoreJob,
  restorePlan,
  restoreReplaceExisting,
  restoreTargetID,
  restoring,
  targets,
  onPreview,
  onRestoreConfirmationChange,
  onRestoreAtChange,
  onRestoreReplaceExistingChange,
  onStartLive,
  onStartDryRun,
  onTargetChange,
  onQueueVerification,
  onVerify,
}: {
  backup: Backup | null;
  backups: Backup[];
  report: BackupVerificationReport | null;
  verificationHistory: Job[];
  verificationJob: Job | null;
  verifyingBackup: boolean;
  restoreAt: string;
  restoreConfirmation: string;
  restoreHistory: Job[];
  restoreJob: Job | null;
  restorePlan: RestorePlan | null;
  restoreReplaceExisting: boolean;
  restoreTargetID: string;
  restoring: "preview" | "start" | "live" | null;
  targets: Target[];
  onPreview: () => Promise<void>;
  onRestoreConfirmationChange: (value: string) => void;
  onRestoreAtChange: (value: string) => void;
  onRestoreReplaceExistingChange: (value: boolean) => void;
  onStartLive: () => Promise<void>;
  onStartDryRun: () => Promise<void>;
  onTargetChange: (value: string) => void;
  onQueueVerification: (backup: Backup) => Promise<void>;
  onVerify: (backup: Backup) => void;
}) {
  if (!backup) {
    return null;
  }
  const chainLabel = backup.parent_id ? (backups.some((item) => item.id === backup.parent_id) ? backup.parent_id : `${backup.parent_id} not loaded`) : "root";
  return (
    <section className="rounded-md border border-line bg-panel p-4">
      <h2 className="text-base font-semibold">Backup detail</h2>
      <div className="mt-4 grid gap-3">
        <HealthRow label="ID" value={backup.id} tone="bronze" />
        <HealthRow label="Type" value={backup.type || "-"} tone="indigo" />
        <HealthRow label="Target" value={backup.target_id || "-"} tone="bronze" />
        <HealthRow label="Storage" value={backup.storage_id || "-"} tone="indigo" />
        <HealthRow label="Parent" value={chainLabel} tone={backup.parent_id && !backups.some((item) => item.id === backup.parent_id) ? "warning" : "bronze"} />
        <HealthRow label="Chunks" value={metricValue(backup.chunk_count, false)} tone="warning" />
        <HealthRow label="Size" value={formatBytes(backup.size_bytes)} tone="success" />
        <HealthRow label="Manifest" value={backup.manifest_id || "-"} tone="bronze" />
        <HealthRow label="Ended" value={formatDateTime(backup.ended_at) ?? "-"} tone="indigo" />
      </div>
      <div className="mt-5 border-t border-line pt-4">
        <div className="flex items-center justify-between gap-3">
          <h3 className="text-sm font-semibold text-marble">Metadata verification</h3>
          <Button className="h-8 text-xs" onClick={() => onVerify(backup)} type="button" variant="secondary">
            Check metadata
          </Button>
        </div>
        {report && report.backupID === backup.id ? <BackupVerificationDetail report={report} /> : null}
        <div className="mt-4 grid gap-2">
          <Button className="h-8 text-xs" disabled={verifyingBackup || !backup.manifest_id} onClick={() => void onQueueVerification(backup)} type="button" variant="primary">
            {verifyingBackup ? "Queuing chunk check" : "Queue chunk check"}
          </Button>
          {verificationJob && verificationJob.verify_backup_id === backup.id ? (
            <div className="rounded-md border border-success/35 bg-success/10 px-3 py-2 text-xs text-success">
              Chunk verification queued as {verificationJob.id}
            </div>
          ) : null}
        </div>
        {verificationHistory.length > 0 ? <BackupVerificationHistory jobs={verificationHistory} /> : null}
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
          <label className="flex min-h-9 items-center gap-2 rounded-md bg-surface px-3 text-sm text-muted" htmlFor="replace-existing">
            <input
              id="replace-existing"
              checked={restoreReplaceExisting}
              className="h-4 w-4 accent-bronze"
              type="checkbox"
              onChange={(event) => onRestoreReplaceExistingChange(event.target.checked)}
            />
            Replace existing destination data
          </label>
          <label className="grid gap-1 text-xs font-semibold uppercase text-muted" htmlFor="restore-confirm">
            Confirm backup ID
            <input
              id="restore-confirm"
              className="h-9 rounded-md border border-line bg-surface px-3 text-sm normal-case text-marble outline-none transition placeholder:text-muted focus:border-bronze"
              placeholder={backup.id}
              value={restoreConfirmation}
              onChange={(event) => onRestoreConfirmationChange(event.target.value)}
            />
          </label>
          <Button
            className="h-8 text-xs"
            disabled={restoring !== null || restoreConfirmation !== backup.id}
            onClick={() => void onStartLive()}
            type="button"
            variant="primary"
          >
            {restoring === "live" ? "Queuing" : "Queue restore"}
          </Button>
        </div>
        {restorePlan ? <RestorePlanDetail plan={restorePlan} /> : null}
        {restoreJob ? (
          <div className={`mt-3 rounded-md border px-3 py-2 text-xs ${restoreJob.restore_dry_run ? "border-success/35 bg-success/10 text-success" : "border-warning/45 bg-warning/10 text-warning"}`}>
            {restoreJob.restore_dry_run ? "Dry-run restore" : "Restore"} queued as {restoreJob.id}
          </div>
        ) : null}
        {restoreHistory.length > 0 ? <BackupRestoreHistory jobs={restoreHistory} /> : null}
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

function BackupRestoreHistory({ jobs }: { jobs: Job[] }) {
  return (
    <div className="mt-4 grid gap-3">
      <h4 className="text-xs font-semibold uppercase text-muted">Restore history</h4>
      {jobs.map((job) => (
        <div key={job.id} className="rounded-md border border-line bg-surface px-3 py-2">
          <div className="flex items-center justify-between gap-3">
            <span className="font-mono text-xs text-marble">{job.id}</span>
            <span className={`rounded px-2 py-1 text-[11px] font-semibold ${statusClass[job.status] ?? "bg-surface text-muted"}`}>{job.status}</span>
          </div>
          <div className="mt-2 grid gap-2">
            <HealthRow label="Mode" value={job.restore_dry_run ? "dry run" : "live"} tone={job.restore_dry_run ? "success" : "warning"} />
            <HealthRow label="Target" value={job.restore_target_id || job.target_id || "-"} tone="bronze" />
            <HealthRow label="Queued" value={formatDateTime(job.queued_at) ?? "-"} tone="indigo" />
            {job.restore_at ? <HealthRow label="At" value={formatDateTime(job.restore_at) ?? job.restore_at} tone="indigo" /> : null}
            <HealthRow label="Replace" value={job.restore_replace_existing ? "yes" : "no"} tone={job.restore_replace_existing ? "warning" : "success"} />
            {job.restore_report ? <RestoreReportDetail report={job.restore_report} /> : null}
            {job.failure_evidence ? <FailureEvidenceDetail evidence={job.failure_evidence} /> : null}
            {job.evidence_artifact ? <EvidenceArtifactDetail artifact={job.evidence_artifact} jobID={job.id} /> : null}
            {job.error ? <HealthRow label="Error" value={job.error} tone="warning" /> : null}
          </div>
        </div>
      ))}
    </div>
  );
}

function RestoreReportDetail({ report }: { report: RestoreReport }) {
  return (
    <>
      <HealthRow label="Objects restored" value={metricValue(report.objects, false)} tone="success" />
      <HealthRow label="Chunks restored" value={metricValue(report.chunks, false)} tone="success" />
      {typeof report.stored_bytes === "number" ? <HealthRow label="Stored bytes" value={formatBytes(report.stored_bytes)} tone="bronze" /> : null}
      {typeof report.restored_bytes === "number" ? <HealthRow label="Restored bytes" value={formatBytes(report.restored_bytes)} tone="indigo" /> : null}
    </>
  );
}

function FailureEvidenceDetail({ evidence }: { evidence: FailureEvidence }) {
  return (
    <>
      <HealthRow label="Failure stage" value={evidence.stage || "-"} tone="warning" />
      <HealthRow label="Failure message" value={evidence.message || "-"} tone="warning" />
      {evidence.at ? <HealthRow label="Failure time" value={formatDateTime(evidence.at) ?? evidence.at} tone="warning" /> : null}
      {evidence.manifest_ids && evidence.manifest_ids.length > 0 ? <HealthRow label="Failure chain" value={evidence.manifest_ids.join(", ")} tone="bronze" /> : null}
    </>
  );
}

function EvidenceArtifactDetail({ artifact, jobID }: { artifact: EvidenceArtifact; jobID: string }) {
  return (
    <>
      <HealthRow label="Evidence SHA256" value={artifact.sha256 || "-"} tone="success" />
      <HealthRow label="Evidence export" value={`/api/v1/jobs/${encodeURIComponent(jobID)}/evidence`} tone="bronze" />
    </>
  );
}

function BackupVerificationDetail({ report }: { report: BackupVerificationReport }) {
  return (
    <div className="mt-4 grid gap-3">
      <HealthRow label="Status" value={report.ok ? "ready" : "attention"} tone={report.ok ? "success" : "warning"} />
      {report.checks.map((check) => (
        <HealthRow key={check.label} label={check.label} value={check.value} tone={check.ok ? "success" : "warning"} />
      ))}
    </div>
  );
}

function VerificationReportDetail({ report }: { report: VerificationReport }) {
  return (
    <>
      <HealthRow label="Objects checked" value={metricValue(report.objects, false)} tone="success" />
      <HealthRow label="Chunks checked" value={metricValue(report.chunks, false)} tone="success" />
      {typeof report.verified_chunks === "number" ? <HealthRow label="Chunks restored" value={metricValue(report.verified_chunks, false)} tone="success" /> : null}
      {typeof report.stored_bytes === "number" ? <HealthRow label="Stored bytes" value={formatBytes(report.stored_bytes)} tone="bronze" /> : null}
      {typeof report.restored_bytes === "number" ? <HealthRow label="Restored bytes" value={formatBytes(report.restored_bytes)} tone="indigo" /> : null}
    </>
  );
}

function BackupVerificationHistory({ jobs }: { jobs: Job[] }) {
  return (
    <div className="mt-4 grid gap-3">
      <h4 className="text-xs font-semibold uppercase text-muted">Verification history</h4>
      {jobs.map((job) => (
        <div key={job.id} className="rounded-md border border-line bg-surface px-3 py-2">
          <div className="flex items-center justify-between gap-3">
            <span className="font-mono text-xs text-marble">{job.id}</span>
            <span className={`rounded px-2 py-1 text-[11px] font-semibold ${statusClass[job.status] ?? "bg-surface text-muted"}`}>{job.status}</span>
          </div>
          <div className="mt-2 grid gap-2">
            <HealthRow label="Level" value={job.verify_level || "-"} tone="indigo" />
            <HealthRow label="Queued" value={formatDateTime(job.queued_at) ?? "-"} tone="warning" />
            {job.verify_report ? <VerificationReportDetail report={job.verify_report} /> : null}
            {job.error ? <HealthRow label="Error" value={job.error} tone="warning" /> : null}
          </div>
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
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

function sortJobs(values: Job[]) {
  return [...values].sort((a, b) => Date.parse(b.queued_at) - Date.parse(a.queued_at));
}

function sortBackups(values: Backup[]) {
  return [...values].sort((a, b) => Date.parse(b.ended_at) - Date.parse(a.ended_at));
}

function sortTargets(values: Target[]) {
  return [...values].sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id));
}

function sortStorages(values: Storage[]) {
  return [...values].sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id));
}

function sortSchedules(values: Schedule[]) {
  return [...values].sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id));
}

function sortRetentionPolicies(values: RetentionPolicy[]) {
  return [...values].sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id));
}

function targetFormFromTarget(target: Target): TargetForm {
  return {
    id: target.id,
    name: target.name || "",
    driver: target.driver || "redis",
    endpoint: target.endpoint || "",
    database: target.database || "",
    username: stringOption(target.options, "username"),
    password: secretOption(target.options, "password"),
    tls: stringOption(target.options, "tls"),
    agent: target.labels?.agent || "",
    tier: target.labels?.tier || "",
  };
}

function targetPayload(form: TargetForm, editingID: string | null) {
  const payload: Record<string, unknown> = {
    name: form.name.trim(),
    driver: form.driver.trim(),
    endpoint: form.endpoint.trim(),
  };
  const id = editingID || form.id.trim();
  if (id) {
    payload.id = id;
  }
  if (form.database.trim()) {
    payload.database = form.database.trim();
  }
  const options: Record<string, string> = {};
  if (form.username.trim()) {
    options.username = form.username.trim();
  }
  if (form.password.trim()) {
    options.password = form.password.trim();
  }
  if (form.tls.trim()) {
    options.tls = form.tls.trim();
  }
  if (Object.keys(options).length > 0) {
    payload.options = options;
  }
  const labels: Record<string, string> = {};
  if (form.agent.trim()) {
    labels.agent = form.agent.trim();
  }
  if (form.tier.trim()) {
    labels.tier = form.tier.trim();
  }
  if (Object.keys(labels).length > 0) {
    payload.labels = labels;
  }
  return payload;
}

function storageFormFromStorage(storage: Storage): StorageForm {
  return {
    id: storage.id,
    name: storage.name || "",
    kind: storage.kind || "local",
    uri: storage.uri || "",
    region: stringOption(storage.options, "region"),
    endpoint: stringOption(storage.options, "endpoint"),
    credentials: secretOption(storage.options, "credentials"),
    accessKey: secretOption(storage.options, "access_key"),
    secretKey: secretOption(storage.options, "secret_key"),
    sessionToken: secretOption(storage.options, "session_token"),
    forcePathStyle: boolOption(storage.options, "force_path_style"),
  };
}

function storagePayload(form: StorageForm, editingID: string | null) {
  const payload: Record<string, unknown> = {
    name: form.name.trim(),
    kind: form.kind.trim(),
    uri: form.uri.trim(),
  };
  const id = editingID || form.id.trim();
  if (id) {
    payload.id = id;
  }
  const options: Record<string, unknown> = {};
  if (form.region.trim()) {
    options.region = form.region.trim();
  }
  if (form.endpoint.trim()) {
    options.endpoint = form.endpoint.trim();
  }
  if (form.credentials.trim()) {
    options.credentials = form.credentials.trim();
  }
  if (form.accessKey.trim()) {
    options.access_key = form.accessKey.trim();
  }
  if (form.secretKey.trim()) {
    options.secret_key = form.secretKey.trim();
  }
  if (form.sessionToken.trim()) {
    options.session_token = form.sessionToken.trim();
  }
  if (form.forcePathStyle) {
    options.force_path_style = true;
  }
  if (Object.keys(options).length > 0) {
    payload.options = options;
  }
  return payload;
}

function scheduleFormFromSchedule(schedule: Schedule): ScheduleForm {
  return {
    id: schedule.id,
    name: schedule.name || "",
    targetID: schedule.target_id || "",
    storageID: schedule.storage_id || "",
    backupType: schedule.backup_type || "full",
    expression: schedule.expression || "",
    retentionPolicyID: schedule.retention_policy_id || "",
    paused: schedule.paused,
  };
}

function schedulePayload(form: ScheduleForm, editingID: string | null) {
  const payload: Record<string, unknown> = {
    name: form.name.trim(),
    target_id: form.targetID.trim(),
    storage_id: form.storageID.trim(),
    backup_type: form.backupType.trim() || "full",
    expression: form.expression.trim(),
    paused: form.paused,
  };
  const id = editingID || form.id.trim();
  if (id) {
    payload.id = id;
  }
  if (form.retentionPolicyID.trim()) {
    payload.retention_policy_id = form.retentionPolicyID.trim();
  }
  return payload;
}

function retentionPolicyFormFromPolicy(policy: RetentionPolicy): RetentionPolicyForm {
  return {
    id: policy.id,
    name: policy.name || "",
    rulesJSON: JSON.stringify(policy.rules || [], null, 2),
  };
}

function retentionPolicyPayload(form: RetentionPolicyForm, editingID: string | null) {
  const rules = JSON.parse(form.rulesJSON) as unknown;
  if (!Array.isArray(rules) || rules.length === 0) {
    throw new Error("retention policy rules must be a non-empty JSON array");
  }
  for (const [index, rule] of rules.entries()) {
    if (!isRetentionRule(rule)) {
      throw new Error(`retention policy rule ${index + 1} must include a kind`);
    }
  }
  const payload: Record<string, unknown> = {
    name: form.name.trim(),
    rules,
  };
  const id = editingID || form.id.trim();
  if (id) {
    payload.id = id;
  }
  return payload;
}

function isRetentionRule(value: unknown): value is RetentionPolicy["rules"][number] {
  return typeof value === "object" && value !== null && typeof (value as { kind?: unknown }).kind === "string" && (value as { kind: string }).kind.trim() !== "";
}

function backupRunPayload(form: BackupRunForm) {
  const payload: Record<string, unknown> = {
    target_id: form.targetID,
    storage_id: form.storageID,
    type: form.backupType,
  };
  if (form.parentID.trim()) {
    payload.parent_id = form.parentID.trim();
  }
  return payload;
}

function backupMetadataReport(backup: Backup, backups: Backup[]): BackupVerificationReport {
  const checks = [
    {
      label: "Manifest",
      ok: Boolean(backup.manifest_id),
      value: backup.manifest_id || "missing",
    },
    {
      label: "Chunks",
      ok: (backup.chunk_count ?? 0) > 0,
      value: metricValue(backup.chunk_count, false),
    },
    {
      label: "Size",
      ok: backup.size_bytes >= 0,
      value: formatBytes(backup.size_bytes),
    },
    {
      label: "Chain",
      ok: backupChainReady(backup, backups),
      value: backup.parent_id || "root",
    },
    {
      label: "Ended",
      ok: Boolean(formatDateTime(backup.ended_at)),
      value: formatDateTime(backup.ended_at) ?? "missing",
    },
    {
      label: "Protection",
      ok: true,
      value: backup.protected ? "protected" : "unprotected",
    },
  ];
  return {
    backupID: backup.id,
    ok: checks.every((check) => check.ok),
    checks,
  };
}

function backupChainReady(backup: Backup, backups: Backup[]) {
  if (backup.type !== "incremental" && backup.type !== "differential") {
    return true;
  }
  return Boolean(backup.parent_id && backups.some((item) => item.id === backup.parent_id));
}

function stringOption(values: Record<string, unknown> | undefined, key: string) {
  const value = values?.[key];
  return typeof value === "string" ? value : "";
}

function secretOption(values: Record<string, unknown> | undefined, key: string) {
  const value = stringOption(values, key);
  return value === "***REDACTED***" ? "" : value;
}

function boolOption(values: Record<string, unknown> | undefined, key: string) {
  return values?.[key] === true;
}

function restorePayload(backupID: string, targetID: string, at: string, dryRun: boolean, replaceExisting: boolean) {
  const payload: Record<string, unknown> = {
    backup_id: backupID,
    dry_run: dryRun,
  };
  if (replaceExisting) {
    payload.replace_existing = true;
  }
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

function updateOverviewScheduleCounts(current: Overview | null, previous: Schedule | null | undefined, next: Schedule) {
  if (!current) {
    return current;
  }
  const createdDelta = previous ? 0 : 1;
  const pausedDelta = (next.paused ? 1 : 0) - (previous?.paused ? 1 : 0);
  return {
    ...current,
    inventory: {
      ...current.inventory,
      schedules: current.inventory.schedules + createdDelta,
      schedules_paused: Math.max(0, current.inventory.schedules_paused + pausedDelta),
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

package obs

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/kronos/kronos/internal/buildinfo"
	"github.com/kronos/kronos/internal/core"
)

// MetricsSnapshot is a dependency-free Prometheus exposition snapshot.
type MetricsSnapshot struct {
	ProcessStartedAt         int64
	ProcessUptimeSeconds     int64
	AgentsHealthy            int
	AgentsDegraded           int
	AgentsCapacity           int
	TargetsTotal             int
	TargetsByDriver          map[core.TargetDriver]int
	StoragesTotal            int
	StoragesByKind           map[core.StorageKind]int
	SchedulesTotal           int
	SchedulesPaused          int
	SchedulesByType          map[core.BackupType]int
	JobsByStatus             map[core.JobStatus]int
	JobsByOperation          map[core.JobOperation]int
	JobsActive               int
	JobsActiveByOperation    map[core.JobOperation]int
	JobsActiveByAgent        map[string]int
	BackupsTotal             int
	BackupsByType            map[core.BackupType]int
	BackupsByTarget          map[core.ID]int
	BackupsByStorage         map[core.ID]int
	BackupsProtected         int
	BackupsBytesTotal        int64
	BackupsBytesByTarget     map[core.ID]int64
	BackupsBytesByStorage    map[core.ID]int64
	BackupsChunksTotal       int
	BackupsLatestCompleted   int64
	BackupsLatestByTarget    map[core.ID]int64
	BackupsLatestByStorage   map[core.ID]int64
	RetentionPoliciesTotal   int
	NotificationRulesTotal   int
	NotificationRulesEnabled int
	UsersTotal               int
	TokensTotal              int
	TokensRevoked            int
	TokensExpired            int
	AuditEventsTotal         int
	AuthRateLimitedTotal     uint64
}

// WritePrometheus writes metrics in the Prometheus text exposition format.
func WritePrometheus(w io.Writer, snapshot MetricsSnapshot) error {
	if _, err := fmt.Fprintln(w, "# HELP kronos_build_info Build metadata for the running Kronos binary."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_build_info gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_build_info{version=\"%s\",commit=\"%s\",build_date=\"%s\"} 1\n", sanitizeLabel(buildinfo.Version), sanitizeLabel(buildinfo.Commit), sanitizeLabel(buildinfo.BuildDate)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_process_start_timestamp Unix timestamp when this Kronos process started."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_process_start_timestamp gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_process_start_timestamp %d\n", snapshot.ProcessStartedAt); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_process_uptime_seconds Seconds since this Kronos process started."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_process_uptime_seconds gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_process_uptime_seconds %d\n", snapshot.ProcessUptimeSeconds); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_agents Number of known agents by health status."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_agents gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_agents{status=%q} %d\n", "healthy", snapshot.AgentsHealthy); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_agents{status=%q} %d\n", "degraded", snapshot.AgentsDegraded); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_agents_capacity Total schedulable capacity across healthy agents."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_agents_capacity gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_agents_capacity %d\n", snapshot.AgentsCapacity); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_targets_total Number of configured backup targets."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_targets_total gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_targets_total %d\n", snapshot.TargetsTotal); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_targets_by_driver", "Number of configured backup targets by driver.", "driver", sortedStringKeys(snapshot.TargetsByDriver), func(driver string) int {
		return snapshot.TargetsByDriver[core.TargetDriver(driver)]
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_storages_total Number of configured storage backends."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_storages_total gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_storages_total %d\n", snapshot.StoragesTotal); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_storages_by_kind", "Number of configured storage backends by kind.", "kind", sortedStringKeys(snapshot.StoragesByKind), func(kind string) int {
		return snapshot.StoragesByKind[core.StorageKind(kind)]
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_schedules_total Number of configured schedules."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_schedules_total gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_schedules_total %d\n", snapshot.SchedulesTotal); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_schedules_paused Number of configured schedules currently paused."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_schedules_paused gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_schedules_paused %d\n", snapshot.SchedulesPaused); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_schedules_by_type", "Number of configured schedules by backup type.", "type", sortedStringKeys(snapshot.SchedulesByType), func(backupType string) int {
		return snapshot.SchedulesByType[core.BackupType(backupType)]
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_jobs Number of jobs by lifecycle status."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_jobs gauge"); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_jobs", "Number of jobs by lifecycle status.", "status", sortedStringKeys(snapshot.JobsByStatus), func(status string) int {
		return snapshot.JobsByStatus[core.JobStatus(status)]
	}); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_jobs_by_operation", "Number of jobs by operation.", "operation", sortedStringKeys(snapshot.JobsByOperation), func(operation string) int {
		return snapshot.JobsByOperation[core.JobOperation(operation)]
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_jobs_active Number of currently active jobs."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_jobs_active gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_jobs_active %d\n", snapshot.JobsActive); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_jobs_active_by_operation", "Number of currently active jobs by operation.", "operation", sortedStringKeys(snapshot.JobsActiveByOperation), func(operation string) int {
		return snapshot.JobsActiveByOperation[core.JobOperation(operation)]
	}); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_jobs_active_by_agent", "Number of currently active jobs by agent ID.", "agent_id", sortedStringKeys(snapshot.JobsActiveByAgent), func(agentID string) int {
		return snapshot.JobsActiveByAgent[agentID]
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_backups_total Number of backup metadata records."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_backups_total gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_backups_total %d\n", snapshot.BackupsTotal); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_backups", "Number of backup metadata records by backup type.", "type", sortedStringKeys(snapshot.BackupsByType), func(backupType string) int {
		return snapshot.BackupsByType[core.BackupType(backupType)]
	}); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_backups_by_target", "Number of backup metadata records by target ID.", "target_id", sortedStringKeys(snapshot.BackupsByTarget), func(targetID string) int {
		return snapshot.BackupsByTarget[core.ID(targetID)]
	}); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_backups_by_storage", "Number of backup metadata records by storage ID.", "storage_id", sortedStringKeys(snapshot.BackupsByStorage), func(storageID string) int {
		return snapshot.BackupsByStorage[core.ID(storageID)]
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_backups_protected Number of backup metadata records protected from retention."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_backups_protected gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_backups_protected %d\n", snapshot.BackupsProtected); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_backups_bytes_total Total logical bytes recorded across backup metadata."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_backups_bytes_total gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_backups_bytes_total %d\n", snapshot.BackupsBytesTotal); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_backups_bytes_by_target", "Total logical bytes recorded across backup metadata by target ID.", "target_id", sortedStringKeys(snapshot.BackupsBytesByTarget), func(targetID string) int64 {
		return snapshot.BackupsBytesByTarget[core.ID(targetID)]
	}); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_backups_bytes_by_storage", "Total logical bytes recorded across backup metadata by storage ID.", "storage_id", sortedStringKeys(snapshot.BackupsBytesByStorage), func(storageID string) int64 {
		return snapshot.BackupsBytesByStorage[core.ID(storageID)]
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_backups_chunks_total Total chunks recorded across backup metadata."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_backups_chunks_total gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_backups_chunks_total %d\n", snapshot.BackupsChunksTotal); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_backups_latest_completed_timestamp Unix timestamp of the latest completed backup metadata record."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_backups_latest_completed_timestamp gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_backups_latest_completed_timestamp %d\n", snapshot.BackupsLatestCompleted); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_backups_latest_completed_by_target_timestamp", "Unix timestamp of the latest completed backup metadata record by target ID.", "target_id", sortedStringKeys(snapshot.BackupsLatestByTarget), func(targetID string) int64 {
		return snapshot.BackupsLatestByTarget[core.ID(targetID)]
	}); err != nil {
		return err
	}
	if err := writeLabeledGauge(w, "kronos_backups_latest_completed_by_storage_timestamp", "Unix timestamp of the latest completed backup metadata record by storage ID.", "storage_id", sortedStringKeys(snapshot.BackupsLatestByStorage), func(storageID string) int64 {
		return snapshot.BackupsLatestByStorage[core.ID(storageID)]
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_retention_policies_total Number of configured retention policies."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_retention_policies_total gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_retention_policies_total %d\n", snapshot.RetentionPoliciesTotal); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_notification_rules_total Number of configured notification rules."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_notification_rules_total gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_notification_rules_total %d\n", snapshot.NotificationRulesTotal); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_notification_rules_enabled Number of enabled notification rules."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_notification_rules_enabled gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_notification_rules_enabled %d\n", snapshot.NotificationRulesEnabled); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_users_total Number of configured users."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_users_total gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_users_total %d\n", snapshot.UsersTotal); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_tokens_total Number of API tokens."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_tokens_total gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_tokens_total %d\n", snapshot.TokensTotal); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_tokens_revoked Number of API tokens that have been revoked."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_tokens_revoked gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_tokens_revoked %d\n", snapshot.TokensRevoked); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_tokens_expired Number of API tokens past their expiration time."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_tokens_expired gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_tokens_expired %d\n", snapshot.TokensExpired); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_audit_events_total Number of audit events stored in the hash chain."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_audit_events_total gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_audit_events_total %d\n", snapshot.AuditEventsTotal); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP kronos_auth_rate_limited_total Number of auth verification requests rejected by rate limiting."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_auth_rate_limited_total counter"); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "kronos_auth_rate_limited_total %d\n", snapshot.AuthRateLimitedTotal)
	return err
}

func sanitizeLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func sortedStringKeys[K ~string, V any](values map[K]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, string(key))
	}
	sort.Strings(keys)
	return keys
}

type metricNumber interface {
	~int | ~int64
}

func writeLabeledGauge[T metricNumber](w io.Writer, name, help, label string, values []string, value func(string) T) error {
	if _, err := fmt.Fprintf(w, "# HELP %s %s\n", name, help); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "# TYPE %s gauge\n", name); err != nil {
		return err
	}
	for _, key := range values {
		if _, err := fmt.Fprintf(w, "%s{%s=%q} %d\n", name, label, sanitizeLabel(key), value(key)); err != nil {
			return err
		}
	}
	return nil
}

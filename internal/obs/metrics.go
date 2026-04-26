package obs

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/kronos/kronos/internal/core"
)

// MetricsSnapshot is a dependency-free Prometheus exposition snapshot.
type MetricsSnapshot struct {
	AgentsHealthy         int
	AgentsDegraded        int
	AgentsCapacity        int
	JobsByStatus          map[core.JobStatus]int
	JobsActive            int
	BackupsTotal          int
	BackupsByType         map[core.BackupType]int
	BackupsByTarget       map[core.ID]int
	BackupsByStorage      map[core.ID]int
	BackupsProtected      int
	BackupsBytesTotal     int64
	BackupsBytesByTarget  map[core.ID]int64
	BackupsBytesByStorage map[core.ID]int64
	BackupsChunksTotal    int
	AuditEventsTotal      int
	AuthRateLimitedTotal  uint64
}

// WritePrometheus writes metrics in the Prometheus text exposition format.
func WritePrometheus(w io.Writer, snapshot MetricsSnapshot) error {
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
	if _, err := fmt.Fprintln(w, "# HELP kronos_jobs_active Number of currently active jobs."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_jobs_active gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "kronos_jobs_active %d\n", snapshot.JobsActive); err != nil {
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

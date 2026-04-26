package obs

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kronos/kronos/internal/core"
)

func TestWritePrometheus(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := WritePrometheus(&out, MetricsSnapshot{
		ProcessStartedAt:     1777118400,
		ProcessUptimeSeconds: 120,
		AgentsHealthy:        2,
		AgentsDegraded:       1,
		AgentsCapacity:       5,
		TargetsTotal:         6,
		TargetsByDriver: map[core.TargetDriver]int{
			core.TargetDriverRedis: 6,
		},
		StoragesTotal: 7,
		StoragesByKind: map[core.StorageKind]int{
			core.StorageKindLocal: 5,
			core.StorageKindS3:    2,
		},
		SchedulesTotal:  8,
		SchedulesPaused: 2,
		SchedulesByType: map[core.BackupType]int{
			core.BackupTypeFull:         3,
			core.BackupTypeIncremental:  4,
			core.BackupTypeDifferential: 1,
		},
		JobsByStatus: map[core.JobStatus]int{
			core.JobStatusQueued:  3,
			core.JobStatusRunning: 1,
		},
		JobsByOperation: map[core.JobOperation]int{
			core.JobOperationBackup:  3,
			core.JobOperationRestore: 1,
		},
		JobsActive: 2,
		JobsActiveByOperation: map[core.JobOperation]int{
			core.JobOperationBackup:  1,
			core.JobOperationRestore: 1,
		},
		JobsActiveByAgent: map[string]int{
			"agent-a": 2,
			"agent-b": 1,
		},
		BackupsTotal:      4,
		BackupsByType:     map[core.BackupType]int{core.BackupTypeFull: 3, core.BackupTypeIncremental: 1},
		BackupsByTarget:   map[core.ID]int{"target-a": 2, "target-b": 2},
		BackupsByStorage:  map[core.ID]int{"storage-a": 3, "storage-b": 1},
		BackupsProtected:  2,
		BackupsBytesTotal: 4096,
		BackupsBytesByTarget: map[core.ID]int64{
			"target-a": 1536,
			"target-b": 2560,
		},
		BackupsBytesByStorage: map[core.ID]int64{
			"storage-a": 3072,
			"storage-b": 1024,
		},
		BackupsChunksTotal:     12,
		BackupsLatestCompleted: 1777118400,
		BackupsLatestByTarget: map[core.ID]int64{
			"target-a": 1777114800,
			"target-b": 1777118400,
		},
		BackupsLatestByStorage: map[core.ID]int64{
			"storage-a": 1777114800,
			"storage-b": 1777118400,
		},
		RetentionPoliciesTotal:   9,
		NotificationRulesTotal:   12,
		NotificationRulesEnabled: 8,
		UsersTotal:               10,
		TokensTotal:              11,
		TokensRevoked:            4,
		TokensExpired:            3,
		AuditEventsTotal:         6,
		AuthRateLimitedTotal:     5,
	})
	if err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	text := out.String()
	for _, want := range []string{
		`kronos_build_info{version="dev",commit="unknown",build_date="unknown"} 1`,
		`kronos_process_start_timestamp 1777118400`,
		`kronos_process_uptime_seconds 120`,
		`kronos_agents{status="healthy"} 2`,
		`kronos_agents{status="degraded"} 1`,
		`kronos_agents_capacity 5`,
		`kronos_targets_total 6`,
		`kronos_targets_by_driver{driver="redis"} 6`,
		`kronos_storages_total 7`,
		`kronos_storages_by_kind{kind="local"} 5`,
		`kronos_storages_by_kind{kind="s3"} 2`,
		`kronos_schedules_total 8`,
		`kronos_schedules_paused 2`,
		`kronos_schedules_by_type{type="diff"} 1`,
		`kronos_schedules_by_type{type="full"} 3`,
		`kronos_schedules_by_type{type="incr"} 4`,
		`kronos_jobs{status="queued"} 3`,
		`kronos_jobs{status="running"} 1`,
		`kronos_jobs_by_operation{operation="backup"} 3`,
		`kronos_jobs_by_operation{operation="restore"} 1`,
		`kronos_jobs_active 2`,
		`kronos_jobs_active_by_operation{operation="backup"} 1`,
		`kronos_jobs_active_by_operation{operation="restore"} 1`,
		`kronos_jobs_active_by_agent{agent_id="agent-a"} 2`,
		`kronos_jobs_active_by_agent{agent_id="agent-b"} 1`,
		`kronos_backups_total 4`,
		`kronos_backups{type="full"} 3`,
		`kronos_backups{type="incr"} 1`,
		`kronos_backups_by_target{target_id="target-a"} 2`,
		`kronos_backups_by_target{target_id="target-b"} 2`,
		`kronos_backups_by_storage{storage_id="storage-a"} 3`,
		`kronos_backups_by_storage{storage_id="storage-b"} 1`,
		`kronos_backups_protected 2`,
		`kronos_backups_bytes_total 4096`,
		`kronos_backups_bytes_by_target{target_id="target-a"} 1536`,
		`kronos_backups_bytes_by_target{target_id="target-b"} 2560`,
		`kronos_backups_bytes_by_storage{storage_id="storage-a"} 3072`,
		`kronos_backups_bytes_by_storage{storage_id="storage-b"} 1024`,
		`kronos_backups_chunks_total 12`,
		`kronos_backups_latest_completed_timestamp 1777118400`,
		`kronos_backups_latest_completed_by_target_timestamp{target_id="target-a"} 1777114800`,
		`kronos_backups_latest_completed_by_target_timestamp{target_id="target-b"} 1777118400`,
		`kronos_backups_latest_completed_by_storage_timestamp{storage_id="storage-a"} 1777114800`,
		`kronos_backups_latest_completed_by_storage_timestamp{storage_id="storage-b"} 1777118400`,
		`kronos_retention_policies_total 9`,
		`kronos_notification_rules_total 12`,
		`kronos_notification_rules_enabled 8`,
		`kronos_users_total 10`,
		`kronos_tokens_total 11`,
		`kronos_tokens_revoked 4`,
		`kronos_tokens_expired 3`,
		`kronos_audit_events_total 6`,
		`kronos_auth_rate_limited_total 5`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics missing %q in %s", want, text)
		}
	}
}

func TestWritePrometheusEscapesLabelsAndPropagatesWriterErrors(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := WritePrometheus(&out, MetricsSnapshot{
		JobsByStatus: map[core.JobStatus]int{
			core.JobStatus(`bad"value\status`): 1,
		},
		BackupsByType: map[core.BackupType]int{
			core.BackupType(`full"bad\type`): 2,
		},
		BackupsByTarget: map[core.ID]int{
			core.ID(`target"bad\id`): 3,
		},
		BackupsBytesByStorage: map[core.ID]int64{
			core.ID(`storage"bad\id`): 4,
		},
	})
	if err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	if !strings.Contains(out.String(), `kronos_jobs{status="bad\\\"value\\\\status"} 1`) {
		t.Fatalf("escaped metrics = %s", out.String())
	}
	for _, want := range []string{
		`kronos_backups{type="full\\\"bad\\\\type"} 2`,
		`kronos_backups_by_target{target_id="target\\\"bad\\\\id"} 3`,
		`kronos_backups_bytes_by_storage{storage_id="storage\\\"bad\\\\id"} 4`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("escaped metrics missing %q in %s", want, out.String())
		}
	}

	if err := WritePrometheus(failingWriter{}, MetricsSnapshot{}); err == nil {
		t.Fatal("WritePrometheus(failingWriter) error = nil, want error")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

var _ io.Writer = failingWriter{}

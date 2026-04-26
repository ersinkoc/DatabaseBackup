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
		AgentsHealthy:  2,
		AgentsDegraded: 1,
		AgentsCapacity: 5,
		JobsByStatus: map[core.JobStatus]int{
			core.JobStatusQueued:  3,
			core.JobStatusRunning: 1,
		},
		JobsActive:           2,
		BackupsTotal:         4,
		BackupsProtected:     2,
		BackupsBytesTotal:    4096,
		AuditEventsTotal:     6,
		AuthRateLimitedTotal: 5,
	})
	if err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	text := out.String()
	for _, want := range []string{
		`kronos_agents{status="healthy"} 2`,
		`kronos_agents{status="degraded"} 1`,
		`kronos_agents_capacity 5`,
		`kronos_jobs{status="queued"} 3`,
		`kronos_jobs{status="running"} 1`,
		`kronos_jobs_active 2`,
		`kronos_backups_total 4`,
		`kronos_backups_protected 2`,
		`kronos_backups_bytes_total 4096`,
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
	})
	if err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	if !strings.Contains(out.String(), `kronos_jobs{status="bad\\\"value\\\\status"} 1`) {
		t.Fatalf("escaped metrics = %s", out.String())
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

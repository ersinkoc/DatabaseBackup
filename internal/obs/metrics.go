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
	AgentsHealthy        int
	AgentsDegraded       int
	JobsByStatus         map[core.JobStatus]int
	BackupsTotal         int
	AuthRateLimitedTotal uint64
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
	if _, err := fmt.Fprintln(w, "# HELP kronos_jobs Number of jobs by lifecycle status."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE kronos_jobs gauge"); err != nil {
		return err
	}
	statuses := make([]string, 0, len(snapshot.JobsByStatus))
	for status := range snapshot.JobsByStatus {
		statuses = append(statuses, string(status))
	}
	sort.Strings(statuses)
	for _, status := range statuses {
		if _, err := fmt.Fprintf(w, "kronos_jobs{status=%q} %d\n", sanitizeLabel(status), snapshot.JobsByStatus[core.JobStatus(status)]); err != nil {
			return err
		}
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

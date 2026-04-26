package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kaudit "github.com/kronos/kronos/internal/audit"
	"github.com/kronos/kronos/internal/config"
	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/kvstore"
	"github.com/kronos/kronos/internal/obs"
	control "github.com/kronos/kronos/internal/server"
)

func TestServerHealthHandler(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newServerHandler(&config.Config{
		Projects: []config.ProjectConfig{{Name: "default"}},
	}))
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", resp.StatusCode)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(body) error = %v", err)
	}
	if !strings.Contains(body.String(), `"status":"ok"`) || !strings.Contains(body.String(), `"projects":1`) {
		t.Fatalf("body = %q", body.String())
	}
}

func TestServerReadinessHandler(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /readyz status = %d, want 200", resp.StatusCode)
	}
	var ready readinessResponse
	if err := json.NewDecoder(resp.Body).Decode(&ready); err != nil {
		t.Fatalf("Decode(/readyz) error = %v", err)
	}
	if ready.Status != "ok" || ready.Checks["jobs"] != "ok" || ready.Checks["audit"] != "ok" || ready.Checks["retention_policies"] != "ok" {
		t.Fatalf("readiness = %#v", ready)
	}
}

func TestServerReadinessReturnsUnavailableWhenStoreFails(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz status = %d, want 503", resp.StatusCode)
	}
	var ready readinessResponse
	if err := json.NewDecoder(resp.Body).Decode(&ready); err != nil {
		t.Fatalf("Decode(/readyz) error = %v", err)
	}
	if ready.Status != "error" || ready.Error == "" {
		t.Fatalf("readiness = %#v", ready)
	}
}

func TestServerRequestIDHeader(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newServerHandler(nil))
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	resp.Body.Close()
	generated := resp.Header.Get(obs.RequestIDHeader)
	if generated == "" {
		t.Fatalf("%s header is empty", obs.RequestIDHeader)
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set(obs.RequestIDHeader, "req-test-123")
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /healthz with request id error = %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get(obs.RequestIDHeader); got != "req-test-123" {
		t.Fatalf("%s = %q, want req-test-123", obs.RequestIDHeader, got)
	}
}

func TestAuditMetadataUsesRequestContext(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequestWithContext(obs.WithRequestID(context.Background(), "req-context-1"), http.MethodPost, "/api/v1/targets", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	req.Header.Set("X-Kronos-Agent-ID", "agent-context")
	got := auditMetadataWithRequest(req, map[string]any{"resource": "target"})
	if got["request_id"] != "req-context-1" || got["agent_id"] != "agent-context" || got["resource"] != "target" {
		t.Fatalf("auditMetadataWithRequest() = %#v", got)
	}
}

func TestRunServerLoadsConfigAndShutsDown(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "kronos.yaml")
	dataDir := filepath.Join(t.TempDir(), "data")
	data := []byte(`
server:
  listen: "127.0.0.1:0"
  data_dir: "` + dataDir + `"
projects:
  - name: default
    storages:
      - name: local
        backend: local
        path: "/tmp/repo"
    targets:
      - name: redis
        driver: redis
        connection:
          host: "127.0.0.1"
          port: 6379
    schedules:
      - name: redis-nightly
        target: redis
        type: full
        cron: "0 2 * * *"
        storage: local
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := runServer(ctx, &out, []string{"--config", path})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runServer() error = %v, want context.Canceled", err)
	}
	if !strings.Contains(out.String(), "kronos-server listening=") || !strings.Contains(out.String(), "projects=1") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestOpenServerStateFailsActiveJobs(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	db, err := kvstore.Open(filepath.Join(dataDir, "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	store, err := control.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore() error = %v", err)
	}
	if err := store.Save(core.Job{ID: "job-1", Status: core.JobStatusRunning, QueuedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, recovered, err := openServerState(dataDir)
	if err != nil {
		t.Fatalf("openServerState() error = %v", err)
	}
	defer reopened.Close()
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}
	store, err = control.NewJobStore(reopened)
	if err != nil {
		t.Fatalf("NewJobStore(reopen) error = %v", err)
	}
	job, ok, err := store.Get("job-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || job.Status != core.JobStatusFailed || job.Error != "server_lost" {
		t.Fatalf("job = %#v", job)
	}
}

func TestSeedAPIStoresFromConfig(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{Projects: []config.ProjectConfig{{
		Name: "default",
		Storages: []config.StorageConfig{{
			Name: "local-primary", Backend: "local", Path: "/var/lib/kronos/repo",
		}, {
			Name: "primary-s3", Backend: "s3", Bucket: "kronos-backups", Region: "eu-north-1", Endpoint: "https://s3.eu-north-1.amazonaws.com",
		}},
		Targets: []config.TargetConfig{{
			Name: "redis-prod", Driver: "redis", Agent: "agent-1", Tier: "tier0",
			Connection: config.ConnectionConfig{Host: "127.0.0.1", Port: 6379, User: "backup", Password: "secret", Database: "0", TLS: "disable"},
		}},
		Schedules: []config.ScheduleConfig{{
			Name: "redis-nightly", Target: "redis-prod", Storage: "local-primary", Type: "full", Cron: "0 2 * * *", Retention: "gfs-standard",
		}},
	}}}
	if err := seedAPIStoresFromConfig(stores, cfg, now); err != nil {
		t.Fatalf("seedAPIStoresFromConfig() error = %v", err)
	}
	target, ok, err := stores.targets.Get("default/redis-prod")
	if err != nil || !ok {
		t.Fatalf("Get(target) ok=%v err=%v", ok, err)
	}
	if target.Endpoint != "127.0.0.1:6379" || target.Labels["agent"] != "agent-1" || target.Database != "0" {
		t.Fatalf("target = %#v", target)
	}
	if target.Options["username"] != "backup" || target.Options["password"] != "secret" || target.Options["tls"] != "disable" {
		t.Fatalf("target options = %#v", target.Options)
	}
	storage, ok, err := stores.storages.Get("default/local-primary")
	if err != nil || !ok {
		t.Fatalf("Get(storage) ok=%v err=%v", ok, err)
	}
	if storage.URI != "file:///var/lib/kronos/repo" || storage.Kind != core.StorageKindLocal {
		t.Fatalf("storage = %#v", storage)
	}
	s3Storage, ok, err := stores.storages.Get("default/primary-s3")
	if err != nil || !ok {
		t.Fatalf("Get(s3 storage) ok=%v err=%v", ok, err)
	}
	if s3Storage.URI != "s3://kronos-backups" || s3Storage.Options["region"] != "eu-north-1" || s3Storage.Options["endpoint"] != "https://s3.eu-north-1.amazonaws.com" {
		t.Fatalf("s3 storage = %#v", s3Storage)
	}
	schedule, ok, err := stores.schedules.Get("default/redis-nightly")
	if err != nil || !ok {
		t.Fatalf("Get(schedule) ok=%v err=%v", ok, err)
	}
	if schedule.TargetID != "default/redis-prod" || schedule.StorageID != "default/local-primary" || schedule.RetentionPolicy != "gfs-standard" {
		t.Fatalf("schedule = %#v", schedule)
	}
}

func TestServerAgentHeartbeatEndpoints(t *testing.T) {
	t.Parallel()

	registry := control.NewAgentRegistry(func() time.Time {
		return time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	}, 30*time.Second)
	server := httptest.NewServer(newServerHandlerWithRegistry(nil, registry))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/agents/heartbeat", "application/json", strings.NewReader(`{"id":"agent-1","version":"dev","capacity":2}`))
	if err != nil {
		t.Fatalf("POST heartbeat error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST heartbeat status = %d, want 200", resp.StatusCode)
	}

	resp, err = server.Client().Get(server.URL + "/api/v1/agents")
	if err != nil {
		t.Fatalf("GET agents error = %v", err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(agents) error = %v", err)
	}
	if !strings.Contains(body.String(), `"id":"agent-1"`) || !strings.Contains(body.String(), `"status":"healthy"`) {
		t.Fatalf("agents body = %q", body.String())
	}

	resp, err = server.Client().Get(server.URL + "/api/v1/agents/agent-1")
	if err != nil {
		t.Fatalf("GET agent error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(agent) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"id":"agent-1"`) || !strings.Contains(body.String(), `"capacity":2`) {
		t.Fatalf("agent body = %q", body.String())
	}
}

func TestServerMetricsEndpoint(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	if err := stores.jobs.Save(core.Job{ID: "job-1", Operation: core.JobOperationBackup, Status: core.JobStatusQueued, QueuedAt: now}); err != nil {
		t.Fatalf("Save(job) error = %v", err)
	}
	if err := stores.jobs.Save(core.Job{ID: "job-2", Operation: core.JobOperationBackup, AgentID: "agent-1", Status: core.JobStatusRunning, QueuedAt: now, StartedAt: now}); err != nil {
		t.Fatalf("Save(running job) error = %v", err)
	}
	if err := stores.jobs.Save(core.Job{ID: "job-3", Operation: core.JobOperationRestore, AgentID: "agent-degraded", Status: core.JobStatusFinalizing, QueuedAt: now, StartedAt: now}); err != nil {
		t.Fatalf("Save(finalizing job) error = %v", err)
	}
	for _, target := range []core.Target{
		{ID: "target", Name: "redis", Driver: core.TargetDriverRedis, Endpoint: "127.0.0.1:6379"},
		{ID: "target-archive", Name: "archive", Driver: core.TargetDriverRedis, Endpoint: "127.0.0.1:6380"},
	} {
		if err := stores.targets.Save(target); err != nil {
			t.Fatalf("Save(target %s) error = %v", target.ID, err)
		}
	}
	for _, storage := range []core.Storage{
		{ID: "storage", Name: "primary", Kind: core.StorageKindLocal, URI: "file:///tmp/kronos-primary"},
		{ID: "storage-archive", Name: "archive", Kind: core.StorageKindS3, URI: "s3://kronos-archive"},
	} {
		if err := stores.storages.Save(storage); err != nil {
			t.Fatalf("Save(storage %s) error = %v", storage.ID, err)
		}
	}
	for _, schedule := range []core.Schedule{
		{ID: "schedule-1", Name: "hourly", TargetID: "target", StorageID: "storage", BackupType: core.BackupTypeFull, Expression: "0 * * * *"},
		{ID: "schedule-2", Name: "paused", TargetID: "target-archive", StorageID: "storage-archive", BackupType: core.BackupTypeIncremental, Expression: "0 2 * * *", Paused: true},
	} {
		if err := stores.schedules.Save(schedule); err != nil {
			t.Fatalf("Save(schedule %s) error = %v", schedule.ID, err)
		}
	}
	if err := stores.backups.Save(core.Backup{ID: "backup-1", TargetID: "target", StorageID: "storage", JobID: "job-1", Type: core.BackupTypeFull, ManifestID: "manifest-1", StartedAt: now.Add(-time.Hour), EndedAt: now, SizeBytes: 2048, ChunkCount: 7, Protected: true}); err != nil {
		t.Fatalf("Save(backup) error = %v", err)
	}
	if err := stores.backups.Save(core.Backup{ID: "backup-2", TargetID: "target-archive", StorageID: "storage-archive", JobID: "job-2", Type: core.BackupTypeIncremental, ManifestID: "manifest-2", StartedAt: now.Add(-30 * time.Minute), EndedAt: now, SizeBytes: 512, ChunkCount: 2}); err != nil {
		t.Fatalf("Save(incremental backup) error = %v", err)
	}
	if _, err := stores.audit.Append(context.Background(), core.AuditEvent{Action: "target.created", ResourceType: "target", ResourceID: "target"}); err != nil {
		t.Fatalf("Append(audit) error = %v", err)
	}
	if err := stores.policies.Save(core.RetentionPolicy{ID: "policy-1", Name: "daily", Rules: []core.RetentionRule{{Kind: "count", Params: map[string]any{"n": 7}}}}); err != nil {
		t.Fatalf("Save(policy) error = %v", err)
	}
	if err := stores.notifications.Save(core.NotificationRule{ID: "notification-1", Name: "ops", Events: []core.NotificationEvent{core.NotificationJobFailed}, WebhookURL: "https://hooks.example.com/kronos", Enabled: true}); err != nil {
		t.Fatalf("Save(notification enabled) error = %v", err)
	}
	if err := stores.notifications.Save(core.NotificationRule{ID: "notification-2", Name: "disabled", Events: []core.NotificationEvent{core.NotificationJobSucceeded}, WebhookURL: "https://hooks.example.com/disabled", Enabled: false}); err != nil {
		t.Fatalf("Save(notification disabled) error = %v", err)
	}
	if err := stores.users.Save(core.User{ID: "user-1", Email: "admin@example.com", DisplayName: "Admin", Role: core.RoleAdmin}); err != nil {
		t.Fatalf("Save(user) error = %v", err)
	}
	createdToken, err := stores.tokens.Create("metrics", "user-1", []string{"metrics:read"}, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("Create(token) error = %v", err)
	}
	if _, err := stores.tokens.Create("active", "user-1", []string{"backup:read"}, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("Create(active token) error = %v", err)
	}
	if _, err := stores.tokens.Create("expired", "user-1", []string{"backup:read"}, time.Now().UTC().Add(5*time.Millisecond)); err != nil {
		t.Fatalf("Create(expired token) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := stores.tokens.Revoke(createdToken.Token.ID); err != nil {
		t.Fatalf("Revoke(token) error = %v", err)
	}
	registry := control.NewAgentRegistry(func() time.Time { return now }, time.Minute)
	registry.Heartbeat(control.AgentHeartbeat{ID: "agent-1", Capacity: 3, Now: now})
	registry.Heartbeat(control.AgentHeartbeat{ID: "agent-default-capacity", Now: now})
	registry.Heartbeat(control.AgentHeartbeat{ID: "agent-degraded", Capacity: 9, Now: now.Add(-2 * time.Minute)})
	server := httptest.NewServer(newServerHandlerWithStores(nil, registry, stores))
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET metrics error = %v", err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(metrics) error = %v", err)
	}
	text := body.String()
	for _, want := range []string{
		`kronos_build_info{version="dev",commit="unknown",build_date="unknown"} 1`,
		`kronos_process_start_timestamp`,
		`kronos_process_uptime_seconds`,
		`kronos_agents{status="healthy"} 2`,
		`kronos_agents{status="degraded"} 1`,
		`kronos_agents_capacity 4`,
		`kronos_targets_total 2`,
		`kronos_targets_by_driver{driver="redis"} 2`,
		`kronos_storages_total 2`,
		`kronos_storages_by_kind{kind="local"} 1`,
		`kronos_storages_by_kind{kind="s3"} 1`,
		`kronos_schedules_total 2`,
		`kronos_schedules_paused 1`,
		`kronos_schedules_by_type{type="full"} 1`,
		`kronos_schedules_by_type{type="incr"} 1`,
		`kronos_jobs{status="queued"} 1`,
		`kronos_jobs{status="running"} 1`,
		`kronos_jobs{status="finalizing"} 1`,
		`kronos_jobs_by_operation{operation="backup"} 2`,
		`kronos_jobs_by_operation{operation="restore"} 1`,
		`kronos_jobs_active 2`,
		`kronos_jobs_active_by_operation{operation="backup"} 1`,
		`kronos_jobs_active_by_operation{operation="restore"} 1`,
		`kronos_jobs_active_by_agent{agent_id="agent-1"} 1`,
		`kronos_jobs_active_by_agent{agent_id="agent-degraded"} 1`,
		`kronos_backups_total 2`,
		`kronos_backups{type="full"} 1`,
		`kronos_backups{type="incr"} 1`,
		`kronos_backups_by_target{target_id="target"} 1`,
		`kronos_backups_by_target{target_id="target-archive"} 1`,
		`kronos_backups_by_storage{storage_id="storage"} 1`,
		`kronos_backups_by_storage{storage_id="storage-archive"} 1`,
		`kronos_backups_protected 1`,
		`kronos_backups_bytes_total 2560`,
		`kronos_backups_bytes_by_target{target_id="target"} 2048`,
		`kronos_backups_bytes_by_target{target_id="target-archive"} 512`,
		`kronos_backups_bytes_by_storage{storage_id="storage"} 2048`,
		`kronos_backups_bytes_by_storage{storage_id="storage-archive"} 512`,
		`kronos_backups_chunks_total 9`,
		`kronos_backups_latest_completed_timestamp 1777118400`,
		`kronos_backups_latest_completed_by_target_timestamp{target_id="target"} 1777118400`,
		`kronos_backups_latest_completed_by_target_timestamp{target_id="target-archive"} 1777118400`,
		`kronos_backups_latest_completed_by_storage_timestamp{storage_id="storage"} 1777118400`,
		`kronos_backups_latest_completed_by_storage_timestamp{storage_id="storage-archive"} 1777118400`,
		`kronos_retention_policies_total 1`,
		`kronos_notification_rules_total 2`,
		`kronos_notification_rules_enabled 1`,
		`kronos_users_total 1`,
		`kronos_tokens_total 3`,
		`kronos_tokens_revoked 1`,
		`kronos_tokens_expired 1`,
		`kronos_audit_events_total 1`,
		`kronos_auth_rate_limited_total 0`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics missing %q in %s", want, text)
		}
	}
}

func TestServerOverviewEndpoint(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 26, 9, 30, 0, 0, time.UTC)
	if err := stores.targets.Save(core.Target{ID: "target-1", Name: "redis", Driver: core.TargetDriverRedis, Endpoint: "127.0.0.1:6379"}); err != nil {
		t.Fatalf("Save(target) error = %v", err)
	}
	if err := stores.storages.Save(core.Storage{ID: "storage-1", Name: "local", Kind: core.StorageKindLocal, URI: "file:///tmp/kronos"}); err != nil {
		t.Fatalf("Save(storage) error = %v", err)
	}
	if err := stores.schedules.Save(core.Schedule{ID: "schedule-1", Name: "nightly", TargetID: "target-1", StorageID: "storage-1", BackupType: core.BackupTypeFull, Expression: "0 2 * * *", Paused: true}); err != nil {
		t.Fatalf("Save(schedule) error = %v", err)
	}
	if err := stores.jobs.Save(core.Job{ID: "job-old", Operation: core.JobOperationBackup, Status: core.JobStatusSucceeded, QueuedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("Save(old job) error = %v", err)
	}
	if err := stores.jobs.Save(core.Job{ID: "job-new", Operation: core.JobOperationBackup, AgentID: "agent-1", Status: core.JobStatusRunning, QueuedAt: now}); err != nil {
		t.Fatalf("Save(new job) error = %v", err)
	}
	if err := stores.backups.Save(core.Backup{ID: "backup-1", TargetID: "target-1", StorageID: "storage-1", JobID: "job-old", Type: core.BackupTypeFull, ManifestID: "manifest-1", StartedAt: now.Add(-2 * time.Hour), EndedAt: now.Add(-time.Hour), SizeBytes: 1024, ChunkCount: 4, Protected: true}); err != nil {
		t.Fatalf("Save(backup) error = %v", err)
	}
	if err := stores.policies.Save(core.RetentionPolicy{ID: "policy-1", Name: "daily", Rules: []core.RetentionRule{{Kind: "count", Params: map[string]any{"n": 7}}}}); err != nil {
		t.Fatalf("Save(policy) error = %v", err)
	}
	if err := stores.notifications.Save(core.NotificationRule{ID: "notification-1", Name: "ops", Events: []core.NotificationEvent{core.NotificationJobFailed}, WebhookURL: "https://hooks.example.com/kronos", Enabled: true}); err != nil {
		t.Fatalf("Save(notification) error = %v", err)
	}
	if err := stores.users.Save(core.User{ID: "user-1", Email: "admin@example.com", DisplayName: "Admin", Role: core.RoleAdmin}); err != nil {
		t.Fatalf("Save(user) error = %v", err)
	}
	registry := control.NewAgentRegistry(func() time.Time { return now }, time.Minute)
	registry.Heartbeat(control.AgentHeartbeat{ID: "agent-1", Capacity: 2, Now: now})
	server := httptest.NewServer(newServerHandlerWithStores(nil, registry, stores))
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/api/v1/overview")
	if err != nil {
		t.Fatalf("GET overview error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET overview status = %d, want 200", resp.StatusCode)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(overview) error = %v", err)
	}
	text := body.String()
	for _, want := range []string{
		`"healthy":1`,
		`"capacity":2`,
		`"targets":1`,
		`"schedules_paused":1`,
		`"active":1`,
		`"running":1`,
		`"health":{"status":"ok"`,
		`"jobs":"ok"`,
		`"bytes_total":1024`,
		`"protected":1`,
		`"notification_rules_enabled":1`,
		`"id":"job-new"`,
		`"id":"backup-1"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("overview missing %q in %s", want, text)
		}
	}
}

func TestServerMetricsReturnsErrorWhenAuditStoreFails(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	auditLog, err := kaudit.New(db, core.NewFakeClock(time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("audit.New() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, apiStores{audit: auditLog}))
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET metrics error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("GET metrics status = %d, want 500", resp.StatusCode)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(metrics) error = %v", err)
	}
	if !strings.Contains(body.String(), "list audit events") {
		t.Fatalf("metrics error body = %q", body.String())
	}
}

func TestServerAuditEndpoints(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	log, err := kaudit.New(db, core.NewFakeClock(time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("audit.New() error = %v", err)
	}
	stores.audit = log
	if _, err := log.Append(context.Background(), core.AuditEvent{Action: "target.created", ResourceType: "target", ResourceID: "target-1"}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if _, err := log.Append(context.Background(), core.AuditEvent{ActorID: "admin", Action: "backup.requested", ResourceType: "job", ResourceID: "job-1"}); err != nil {
		t.Fatalf("Append(backup) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/api/v1/audit")
	if err != nil {
		t.Fatalf("GET audit error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(audit) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"action":"target.created"`) || !strings.Contains(body.String(), `"resource_id":"target-1"`) {
		t.Fatalf("audit body = %q", body.String())
	}
	query := url.Values{}
	query.Set("action", "target.created")
	query.Set("resource_type", "target")
	query.Set("resource_id", "target-1")
	query.Set("since", "2026-04-25T11:59:00Z")
	query.Set("limit", "1")
	resp, err = server.Client().Get(server.URL + "/api/v1/audit?" + query.Encode())
	if err != nil {
		t.Fatalf("GET filtered audit error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(filtered audit) error = %v", err)
	}
	resp.Body.Close()
	if text := body.String(); !strings.Contains(text, `"action":"target.created"`) || strings.Contains(text, `"backup.requested"`) {
		t.Fatalf("filtered audit body = %q", text)
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/audit?limit=-1")
	if err != nil {
		t.Fatalf("GET invalid audit limit error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid audit limit status = %d, want 400", resp.StatusCode)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/audit/verify", nil)
	if err != nil {
		t.Fatalf("NewRequest(audit verify) error = %v", err)
	}
	req.Header.Set("X-Kronos-Actor", "auditor-1")
	req.Header.Set(obs.RequestIDHeader, "req-audit-verify-1")
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST audit verify error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(audit verify) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"ok":true`) {
		t.Fatalf("audit verify body = %q", body.String())
	}
	events, err := log.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List(after verify) error = %v", err)
	}
	if len(events) != 3 || events[2].Action != "audit.verified" || events[2].ActorID != "auditor-1" || events[2].Metadata["request_id"] != "req-audit-verify-1" {
		t.Fatalf("audit verify event = %#v", events)
	}
}

func TestServerAuditRecordsMutations(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	if err := stores.users.Save(core.User{ID: "user-1", Email: "ci@example.com", DisplayName: "CI", Role: core.RoleAdmin}); err != nil {
		t.Fatalf("Save(user) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/targets", strings.NewReader(`{"id":"target-1","name":"redis","driver":"redis","endpoint":"127.0.0.1:6379"}`))
	if err != nil {
		t.Fatalf("NewRequest(target) error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Kronos-Actor", "admin-1")
	req.Header.Set("X-Kronos-Agent-ID", "agent-audit")
	req.Header.Set(obs.RequestIDHeader, "req-audit-1")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST target error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST target status = %d, want 200", resp.StatusCode)
	}

	resp, err = server.Client().Post(server.URL+"/api/v1/backups/now", "application/json", strings.NewReader(`{"target_id":"target-1","storage_id":"storage-1","type":"full"}`))
	if err != nil {
		t.Fatalf("POST backup now error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST backup now status = %d, want 200", resp.StatusCode)
	}

	resp, err = server.Client().Get(server.URL + "/api/v1/audit")
	if err != nil {
		t.Fatalf("GET audit error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(audit) error = %v", err)
	}
	resp.Body.Close()
	text := body.String()
	for _, want := range []string{
		`"actor_id":"admin-1"`,
		`"action":"target.created"`,
		`"resource_type":"target"`,
		`"resource_id":"target-1"`,
		`"request_id":"req-audit-1"`,
		`"agent_id":"agent-audit"`,
		`"action":"backup.requested"`,
		`"resource_type":"job"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("audit body missing %q in %s", want, text)
		}
	}
}

func TestServerTokenEndpoints(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	if err := stores.users.Save(core.User{ID: "user-1", Email: "ci@example.com", DisplayName: "CI", Role: core.RoleAdmin}); err != nil {
		t.Fatalf("Save(user) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/tokens", "application/json", strings.NewReader(`{"name":"ci","user_id":"user-1","scopes":["backup:read","backup:write"]}`))
	if err != nil {
		t.Fatalf("POST token error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(create token) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST token status = %d, body=%q", resp.StatusCode, body.String())
	}
	text := body.String()
	if !strings.Contains(text, `"secret":"kro_`) || !strings.Contains(text, `"name":"ci"`) || strings.Contains(text, `"secret_hash"`) {
		t.Fatalf("create token body = %q", text)
	}
	var created struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(body.Bytes(), &created); err != nil {
		t.Fatalf("Unmarshal(created token) error = %v", err)
	}
	tokens, err := stores.tokens.List()
	if err != nil {
		t.Fatalf("List(tokens) error = %v", err)
	}
	if len(tokens) != 1 || tokens[0].Name != "ci" {
		t.Fatalf("tokens = %#v", tokens)
	}

	resp, err = server.Client().Get(server.URL + "/api/v1/tokens")
	if err != nil {
		t.Fatalf("GET tokens error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(tokens) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"tokens":[`) || strings.Contains(body.String(), `"secret"`) {
		t.Fatalf("tokens body = %q", body.String())
	}

	resp, err = server.Client().Get(server.URL + "/api/v1/tokens/" + tokens[0].ID.String())
	if err != nil {
		t.Fatalf("GET token error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(token) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"name":"ci"`) || strings.Contains(body.String(), `"secret"`) {
		t.Fatalf("token body status=%d body=%q", resp.StatusCode, body.String())
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/verify", nil)
	if err != nil {
		t.Fatalf("NewRequest(auth verify) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+created.Secret)
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST auth verify error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(auth verify) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"name":"ci"`) {
		t.Fatalf("auth verify status=%d body=%q", resp.StatusCode, body.String())
	}

	resp, err = server.Client().Post(server.URL+"/api/v1/tokens/"+tokens[0].ID.String()+"/revoke", "application/json", nil)
	if err != nil {
		t.Fatalf("POST token revoke error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(revoke token) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"revoked_at"`) {
		t.Fatalf("revoke token body = %q", body.String())
	}

	resp, err = server.Client().Post(server.URL+"/api/v1/tokens/prune", "application/json", strings.NewReader(`{"dry_run":true}`))
	if err != nil {
		t.Fatalf("POST token prune dry-run error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(prune token dry-run) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"deleted":1`) || !strings.Contains(body.String(), `"dry_run":true`) {
		t.Fatalf("prune token dry-run status=%d body=%q", resp.StatusCode, body.String())
	}
	if tokens, err = stores.tokens.List(); err != nil || len(tokens) != 1 {
		t.Fatalf("List(tokens after dry-run) tokens=%#v err=%v, want token retained", tokens, err)
	}

	resp, err = server.Client().Post(server.URL+"/api/v1/tokens/prune", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST token prune error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(prune token) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"deleted":1`) || !strings.Contains(body.String(), tokens[0].ID.String()) {
		t.Fatalf("prune token status=%d body=%q", resp.StatusCode, body.String())
	}
	tokens, err = stores.tokens.List()
	if err != nil {
		t.Fatalf("List(tokens after prune) error = %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("tokens after prune = %#v, want none", tokens)
	}
}

func TestServerAuthVerifyRateLimit(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	for i := 0; i < authVerifyRateLimit; i++ {
		req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/verify", nil)
		if err != nil {
			t.Fatalf("NewRequest(%d) error = %v", i, err)
		}
		req.Header.Set("Authorization", "Bearer kro_missing_secret")
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatalf("POST auth verify %d error = %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("POST auth verify %d status = %d, want 401", i, resp.StatusCode)
		}
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/verify", nil)
	if err != nil {
		t.Fatalf("NewRequest(rate limited) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer kro_missing_secret")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST auth verify rate limited error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("POST auth verify rate limited status = %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Fatal("POST auth verify rate limited Retry-After header is empty")
	}
}

func TestServerAuthVerifyRateLimitUsesConfig(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	cfg := &config.Config{
		Server: config.ServerConfig{
			Auth: config.AuthConfig{
				TokenVerifyRateLimit:  2,
				TokenVerifyRateWindow: "30s",
			},
		},
	}
	server := httptest.NewServer(newServerHandlerWithStores(cfg, nil, stores))
	defer server.Close()

	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/verify", nil)
		if err != nil {
			t.Fatalf("NewRequest(%d) error = %v", i, err)
		}
		req.Header.Set("Authorization", "Bearer kro_missing_secret")
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatalf("POST auth verify %d error = %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("POST auth verify %d status = %d, want 401", i, resp.StatusCode)
		}
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/verify", nil)
	if err != nil {
		t.Fatalf("NewRequest(rate limited) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer kro_missing_secret")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST auth verify rate limited error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("POST auth verify rate limited status = %d, want 429", resp.StatusCode)
	}
}

func TestServerAuthVerifyRateLimitMetrics(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	cfg := &config.Config{
		Server: config.ServerConfig{
			Auth: config.AuthConfig{
				TokenVerifyRateLimit:  1,
				TokenVerifyRateWindow: "1m",
			},
		},
	}
	server := httptest.NewServer(newServerHandlerWithStores(cfg, nil, stores))
	defer server.Close()

	for i, wantStatus := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/verify", nil)
		if err != nil {
			t.Fatalf("NewRequest(%d) error = %v", i, err)
		}
		req.Header.Set("Authorization", "Bearer kro_missing_secret")
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatalf("POST auth verify %d error = %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != wantStatus {
			t.Fatalf("POST auth verify %d status = %d, want %d", i, resp.StatusCode, wantStatus)
		}
	}

	resp, err := server.Client().Get(server.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET metrics error = %v", err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(metrics) error = %v", err)
	}
	if !strings.Contains(body.String(), `kronos_auth_rate_limited_total 1`) {
		t.Fatalf("metrics missing auth rate limit total in %s", body.String())
	}
}

func TestAuthRateLimiterPrunesExpiredClients(t *testing.T) {
	t.Parallel()

	limiter := newAuthRateLimiter(2, time.Minute)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	limiter.clients["expired"] = authRateWindow{start: now.Add(-2 * time.Minute), count: 2}
	limiter.clients["active"] = authRateWindow{start: now.Add(-30 * time.Second), count: 1}

	limiter.mu.Lock()
	limiter.pruneLocked(now)
	_, expiredOK := limiter.clients["expired"]
	_, activeOK := limiter.clients["active"]
	limiter.mu.Unlock()

	if expiredOK {
		t.Fatal("expired auth rate limiter client was not pruned")
	}
	if !activeOK {
		t.Fatal("active auth rate limiter client was pruned")
	}
}

func TestServerTokenCreateRejectsScopesOutsideUserRole(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	if err := stores.users.Save(core.User{ID: "viewer-1", Email: "viewer@example.com", DisplayName: "Viewer", Role: core.RoleViewer}); err != nil {
		t.Fatalf("Save(viewer) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/tokens", "application/json", strings.NewReader(`{"name":"bad","user_id":"viewer-1","scopes":["backup:write"]}`))
	if err != nil {
		t.Fatalf("POST bad token error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST bad token status = %d, want 403", resp.StatusCode)
	}

	resp, err = server.Client().Post(server.URL+"/api/v1/tokens", "application/json", strings.NewReader(`{"name":"good","user_id":"viewer-1","scopes":["backup:read"]}`))
	if err != nil {
		t.Fatalf("POST good token error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST good token status = %d, want 200", resp.StatusCode)
	}
}

func TestServerBearerTokenScopeEnforcement(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	if err := stores.backups.Save(core.Backup{
		ID: "backup-1", TargetID: "target-1", StorageID: "storage-1", JobID: "job-1",
		Type: core.BackupTypeFull, ManifestID: "manifest-1", StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save(backup) error = %v", err)
	}
	readToken, err := stores.tokens.Create("reader", "user-1", []string{"backup:read"}, time.Time{})
	if err != nil {
		t.Fatalf("Create(read token) error = %v", err)
	}
	writeToken, err := stores.tokens.Create("operator", "user-1", []string{"backup:*"}, time.Time{})
	if err != nil {
		t.Fatalf("Create(write token) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/backups", nil)
	if err != nil {
		t.Fatalf("NewRequest(list backups) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+readToken.Secret)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET backups error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET backups status = %d, want 200", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodPost, server.URL+"/api/v1/backups/now", strings.NewReader(`{"target_id":"target-1","storage_id":"storage-1"}`))
	if err != nil {
		t.Fatalf("NewRequest(backup now read token) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+readToken.Secret)
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST backup now read token error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST backup now read token status = %d, want 403", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodGet, server.URL+"/api/v1/backups", nil)
	if err != nil {
		t.Fatalf("NewRequest(malformed token) error = %v", err)
	}
	req.Header.Set("Authorization", "Token "+readToken.Secret)
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET backups malformed token error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET backups malformed token status = %d, want 401", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodPost, server.URL+"/api/v1/backups/now", strings.NewReader(`{"target_id":"target-1","storage_id":"storage-1"}`))
	if err != nil {
		t.Fatalf("NewRequest(backup now wildcard token) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+writeToken.Secret)
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST backup now wildcard token error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST backup now wildcard token status = %d, want 200", resp.StatusCode)
	}
}

func TestServerSecretResourceReadsRequireAgentScope(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	if err := stores.targets.Save(core.Target{
		ID: "target-1", Name: "redis", Driver: core.TargetDriverRedis, Endpoint: "127.0.0.1:6379", Options: map[string]any{"password": "secret"},
	}); err != nil {
		t.Fatalf("Save(target) error = %v", err)
	}
	readToken, err := stores.tokens.Create("reader", "user-1", []string{"target:read"}, time.Time{})
	if err != nil {
		t.Fatalf("Create(read token) error = %v", err)
	}
	agentToken, err := stores.tokens.Create("agent", "agent-1", []string{"target:read", "agent:write"}, time.Time{})
	if err != nil {
		t.Fatalf("Create(agent token) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/targets?include_secrets=true", nil)
	if err != nil {
		t.Fatalf("NewRequest(read token) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+readToken.Secret)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET targets read token error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET targets read token status = %d, want 403", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodGet, server.URL+"/api/v1/targets?include_secrets=true", nil)
	if err != nil {
		t.Fatalf("NewRequest(agent token) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+agentToken.Secret)
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET targets agent token error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(targets) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"password":"secret"`) {
		t.Fatalf("GET targets agent token status=%d body=%q", resp.StatusCode, body.String())
	}
}

func TestServerBearerTokenActorFeedsAudit(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	token, err := stores.tokens.Create("target-writer", "user-actor", []string{"target:write"}, time.Time{})
	if err != nil {
		t.Fatalf("Create(token) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/targets", strings.NewReader(`{"id":"target-1","name":"redis","driver":"redis","endpoint":"127.0.0.1:6379"}`))
	if err != nil {
		t.Fatalf("NewRequest(create target) error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Secret)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST target error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST target status = %d, want 200", resp.StatusCode)
	}

	events, err := stores.audit.List(context.Background(), 0)
	if err != nil {
		t.Fatalf("List(audit) error = %v", err)
	}
	if len(events) != 1 || events[0].ActorID != "user-actor" || events[0].Action != "target.created" {
		t.Fatalf("audit events = %#v", events)
	}
}

func TestServerUserEndpoints(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/users", "application/json", strings.NewReader(`{"id":"user-1","email":"ops@example.com","display_name":"Ops","role":"viewer"}`))
	if err != nil {
		t.Fatalf("POST user error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST user status = %d, want 200", resp.StatusCode)
	}

	resp, err = server.Client().Get(server.URL + "/api/v1/users")
	if err != nil {
		t.Fatalf("GET users error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(users) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"id":"user-1"`) || !strings.Contains(body.String(), `"role":"viewer"`) {
		t.Fatalf("users body = %q", body.String())
	}

	resp, err = server.Client().Get(server.URL + "/api/v1/users/user-1")
	if err != nil {
		t.Fatalf("GET user error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(user) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"email":"ops@example.com"`) {
		t.Fatalf("user inspect status=%d body=%q", resp.StatusCode, body.String())
	}

	resp, err = server.Client().Post(server.URL+"/api/v1/users/user-1/grant", "application/json", strings.NewReader(`{"role":"operator"}`))
	if err != nil {
		t.Fatalf("POST grant user error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(grant user) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"role":"operator"`) {
		t.Fatalf("grant user body = %q", body.String())
	}

	req, err := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/users/user-1", nil)
	if err != nil {
		t.Fatalf("NewRequest(delete user) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("DELETE user error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE user status = %d, want 204", resp.StatusCode)
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/users/user-1")
	if err != nil {
		t.Fatalf("GET deleted user error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted user status = %d, want 404", resp.StatusCode)
	}

	events, err := stores.audit.List(context.Background(), 0)
	if err != nil {
		t.Fatalf("List(audit) error = %v", err)
	}
	if len(events) != 3 || events[0].Action != "user.created" || events[1].Action != "user.role_granted" || events[2].Action != "user.deleted" {
		t.Fatalf("audit events = %#v", events)
	}
}

func TestServerJobsEndpoint(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	store, err := control.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	if err := store.Save(core.Job{ID: "job-1", Operation: core.JobOperationBackup, AgentID: "agent-1", TargetID: "target-1", StorageID: "storage-1", Status: core.JobStatusQueued, QueuedAt: now}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Save(core.Job{ID: "job-2", Operation: core.JobOperationBackup, AgentID: "agent-2", TargetID: "target-2", StorageID: "storage-1", Status: core.JobStatusRunning, QueuedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("Save(job-2) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, apiStores{jobs: store}))
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/api/v1/jobs")
	if err != nil {
		t.Fatalf("GET jobs error = %v", err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(jobs) error = %v", err)
	}
	if !strings.Contains(body.String(), `"id":"job-1"`) || !strings.Contains(body.String(), `"status":"queued"`) {
		t.Fatalf("jobs body = %q", body.String())
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/jobs?status=queued&operation=backup&target_id=target-1&storage_id=storage-1&agent_id=agent-1&since=2026-04-25T11:00:00Z")
	if err != nil {
		t.Fatalf("GET filtered jobs error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(filtered jobs) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"id":"job-1"`) || strings.Contains(body.String(), `"id":"job-2"`) {
		t.Fatalf("filtered jobs body = %q", body.String())
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/jobs?since=not-a-duration")
	if err != nil {
		t.Fatalf("GET invalid filtered jobs error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid filtered jobs status = %d, want 400", resp.StatusCode)
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/jobs/job-1")
	if err != nil {
		t.Fatalf("GET job error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(job) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"id":"job-1"`) {
		t.Fatalf("job inspect status=%d body=%q", resp.StatusCode, body.String())
	}
}

func TestServerJobClaimAndFinishEndpoints(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	if err := stores.jobs.Save(core.Job{ID: "job-1", TargetID: "target", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusQueued, QueuedAt: now}); err != nil {
		t.Fatalf("Save(job-1) error = %v", err)
	}
	var notificationPayload map[string]any
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&notificationPayload); err != nil {
			t.Fatalf("Decode(notification) error = %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer webhook.Close()
	if err := stores.notifications.Save(core.NotificationRule{
		ID:         "rule-1",
		Name:       "success-webhook",
		Events:     []core.NotificationEvent{core.NotificationJobSucceeded},
		WebhookURL: webhook.URL,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("Save(notification) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/jobs/claim", nil)
	if err != nil {
		t.Fatalf("NewRequest(claim) error = %v", err)
	}
	req.Header.Set("X-Kronos-Agent-ID", "agent-job-1")
	req.Header.Set(obs.RequestIDHeader, "req-job-claim-1")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST jobs claim error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(claim) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST jobs claim status = %d, body=%q", resp.StatusCode, body.String())
	}
	if !strings.Contains(body.String(), `"id":"job-1"`) || !strings.Contains(body.String(), `"status":"running"`) {
		t.Fatalf("claim body = %q", body.String())
	}
	claimed, ok, err := stores.jobs.Get("job-1")
	if err != nil || !ok || claimed.Status != core.JobStatusRunning || claimed.StartedAt.IsZero() {
		t.Fatalf("Get(job-1 after claim) = %#v ok=%v err=%v", claimed, ok, err)
	}

	resp, err = server.Client().Post(server.URL+"/api/v1/jobs/job-1/finish", "application/json", strings.NewReader(`{"status":"succeeded","backup":{"id":"backup-1","manifest_id":"manifest-1","size_bytes":42,"chunk_count":3}}`))
	if err != nil {
		t.Fatalf("POST jobs finish error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(finish) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST jobs finish status = %d, body=%q", resp.StatusCode, body.String())
	}
	if !strings.Contains(body.String(), `"status":"succeeded"`) {
		t.Fatalf("finish body = %q", body.String())
	}
	finished, ok, err := stores.jobs.Get("job-1")
	if err != nil || !ok || finished.Status != core.JobStatusSucceeded || finished.EndedAt.IsZero() {
		t.Fatalf("Get(job-1 after finish) = %#v ok=%v err=%v", finished, ok, err)
	}
	backup, ok, err := stores.backups.Get("backup-1")
	if err != nil || !ok || backup.JobID != "job-1" || backup.TargetID != "target" || backup.ManifestID != "manifest-1" {
		t.Fatalf("Get(backup-1 after finish) = %#v ok=%v err=%v", backup, ok, err)
	}
	events, err := stores.audit.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List(audit) error = %v", err)
	}
	if len(events) != 2 || events[0].Action != "job.claimed" || events[1].Action != "job.finished" {
		t.Fatalf("job audit events = %#v", events)
	}
	if events[0].Metadata["agent_id"] != "agent-job-1" || events[0].Metadata["request_id"] != "req-job-claim-1" {
		t.Fatalf("job claimed metadata = %#v", events[0].Metadata)
	}
	if notificationPayload["event"] != string(core.NotificationJobSucceeded) || notificationPayload["job_id"] != "job-1" {
		t.Fatalf("notification payload = %#v", notificationPayload)
	}
	deliveries, ok := events[1].Metadata["notifications"].([]any)
	if !ok || len(deliveries) != 1 {
		t.Fatalf("job finished notification metadata = %#v", events[1].Metadata)
	}
}

func TestServerJobClaimSkipsBusyTarget(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	fixtures := []core.Job{
		{ID: "running-a", TargetID: "target-a", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusRunning, QueuedAt: now.Add(-3 * time.Minute), StartedAt: now.Add(-2 * time.Minute)},
		{ID: "queued-a", TargetID: "target-a", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusQueued, QueuedAt: now.Add(-time.Minute)},
		{ID: "queued-b", TargetID: "target-b", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusQueued, QueuedAt: now},
	}
	for _, job := range fixtures {
		if err := stores.jobs.Save(job); err != nil {
			t.Fatalf("Save(%s) error = %v", job.ID, err)
		}
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/jobs/claim", "application/json", nil)
	if err != nil {
		t.Fatalf("POST jobs claim error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(claim) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"id":"queued-b"`) {
		t.Fatalf("claim status=%d body=%q", resp.StatusCode, body.String())
	}
	queuedA, ok, err := stores.jobs.Get("queued-a")
	if err != nil || !ok || queuedA.Status != core.JobStatusQueued {
		t.Fatalf("Get(queued-a) = %#v ok=%v err=%v", queuedA, ok, err)
	}
	queuedB, ok, err := stores.jobs.Get("queued-b")
	if err != nil || !ok || queuedB.Status != core.JobStatusRunning {
		t.Fatalf("Get(queued-b) = %#v ok=%v err=%v", queuedB, ok, err)
	}
}

func TestServerJobClaimFailsLostAgentBeforeClaiming(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	for _, job := range []core.Job{
		{ID: "running-a", AgentID: "agent-lost", TargetID: "target-a", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusRunning, QueuedAt: now.Add(-2 * time.Minute), StartedAt: now.Add(-2 * time.Minute)},
		{ID: "queued-a", TargetID: "target-a", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusQueued, QueuedAt: now.Add(-time.Minute)},
	} {
		if err := stores.jobs.Save(job); err != nil {
			t.Fatalf("Save(%s) error = %v", job.ID, err)
		}
	}
	registry := control.NewAgentRegistry(func() time.Time { return now }, time.Minute)
	registry.Heartbeat(control.AgentHeartbeat{ID: "agent-lost", Now: now.Add(-2 * time.Minute)})
	registry.Heartbeat(control.AgentHeartbeat{ID: "agent-ok", Capacity: 1, Now: now})
	server := httptest.NewServer(newServerHandlerWithStores(nil, registry, stores))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/jobs/claim", nil)
	if err != nil {
		t.Fatalf("NewRequest(claim) error = %v", err)
	}
	req.Header.Set("X-Kronos-Agent-ID", "agent-ok")
	req.Header.Set(obs.RequestIDHeader, "req-agent-lost-1")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST jobs claim error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(claim) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"id":"queued-a"`) {
		t.Fatalf("claim status=%d body=%q", resp.StatusCode, body.String())
	}
	lost, ok, err := stores.jobs.Get("running-a")
	if err != nil || !ok || lost.Status != core.JobStatusFailed || lost.Error != "agent_lost" {
		t.Fatalf("Get(running-a) = %#v ok=%v err=%v", lost, ok, err)
	}
	claimed, ok, err := stores.jobs.Get("queued-a")
	if err != nil || !ok || claimed.Status != core.JobStatusRunning || claimed.AgentID != "agent-ok" {
		t.Fatalf("Get(queued-a) = %#v ok=%v err=%v", claimed, ok, err)
	}
	events, err := stores.audit.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List(audit) error = %v", err)
	}
	if len(events) != 2 || events[0].Action != "agent_lost.jobs_failed" || events[1].Action != "job.claimed" {
		t.Fatalf("agent lost audit events = %#v", events)
	}
	if events[0].Metadata["request_id"] != "req-agent-lost-1" {
		t.Fatalf("agent lost metadata = %#v", events[0].Metadata)
	}
}

func TestServerJobClaimRespectsAgentCapacity(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	for _, job := range []core.Job{
		{ID: "running-a", TargetID: "target-a", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusRunning, QueuedAt: now.Add(-time.Minute), StartedAt: now.Add(-time.Minute)},
		{ID: "queued-b", TargetID: "target-b", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusQueued, QueuedAt: now},
	} {
		if err := stores.jobs.Save(job); err != nil {
			t.Fatalf("Save(%s) error = %v", job.ID, err)
		}
	}
	registry := control.NewAgentRegistry(func() time.Time { return now }, time.Minute)
	registry.Heartbeat(control.AgentHeartbeat{ID: "agent-1", Capacity: 1, Now: now})
	server := httptest.NewServer(newServerHandlerWithStores(nil, registry, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/jobs/claim", "application/json", nil)
	if err != nil {
		t.Fatalf("POST jobs claim error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(claim) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.Contains(body.String(), `"job"`) {
		t.Fatalf("claim status=%d body=%q", resp.StatusCode, body.String())
	}
	queuedB, ok, err := stores.jobs.Get("queued-b")
	if err != nil || !ok || queuedB.Status != core.JobStatusQueued {
		t.Fatalf("Get(queued-b) = %#v ok=%v err=%v", queuedB, ok, err)
	}
}

func TestServerJobClaimMatchesAssignedAgent(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	for _, target := range []core.Target{
		{ID: "target-a", Name: "redis-a", Driver: core.TargetDriverRedis, Endpoint: "127.0.0.1:6379", CreatedAt: now, UpdatedAt: now, Labels: map[string]string{"agent": "agent-a"}},
		{ID: "target-b", Name: "redis-b", Driver: core.TargetDriverRedis, Endpoint: "127.0.0.1:6380", CreatedAt: now, UpdatedAt: now, Labels: map[string]string{"agent": "agent-b"}},
	} {
		if err := stores.targets.Save(target); err != nil {
			t.Fatalf("Save(target %s) error = %v", target.ID, err)
		}
	}
	for _, job := range []core.Job{
		{ID: "job-a", TargetID: "target-a", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusQueued, QueuedAt: now.Add(-time.Minute)},
		{ID: "job-b", TargetID: "target-b", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusQueued, QueuedAt: now},
	} {
		if err := stores.jobs.Save(job); err != nil {
			t.Fatalf("Save(job %s) error = %v", job.ID, err)
		}
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/jobs/claim", nil)
	if err != nil {
		t.Fatalf("NewRequest(claim) error = %v", err)
	}
	req.Header.Set("X-Kronos-Agent-ID", "agent-b")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST jobs claim error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(claim) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"id":"job-b"`) {
		t.Fatalf("claim status=%d body=%q", resp.StatusCode, body.String())
	}
	jobB, ok, err := stores.jobs.Get("job-b")
	if err != nil || !ok || jobB.Status != core.JobStatusRunning || jobB.AgentID != "agent-b" {
		t.Fatalf("Get(job-b) = %#v ok=%v err=%v", jobB, ok, err)
	}
	jobA, ok, err := stores.jobs.Get("job-a")
	if err != nil || !ok || jobA.Status != core.JobStatusQueued {
		t.Fatalf("Get(job-a) = %#v ok=%v err=%v", jobA, ok, err)
	}
}

func TestFailLostAgentJobs(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	for _, job := range []core.Job{
		{ID: "lost-running", AgentID: "agent-lost", TargetID: "target-a", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusRunning, QueuedAt: now.Add(-time.Hour), StartedAt: now.Add(-time.Hour)},
		{ID: "healthy-running", AgentID: "agent-ok", TargetID: "target-b", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusRunning, QueuedAt: now.Add(-time.Hour), StartedAt: now.Add(-time.Hour)},
		{ID: "queued-lost", AgentID: "agent-lost", TargetID: "target-c", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusQueued, QueuedAt: now},
	} {
		if err := stores.jobs.Save(job); err != nil {
			t.Fatalf("Save(job %s) error = %v", job.ID, err)
		}
	}
	registry := control.NewAgentRegistry(func() time.Time { return now }, time.Minute)
	registry.Heartbeat(control.AgentHeartbeat{ID: "agent-lost", Now: now.Add(-2 * time.Minute)})
	registry.Heartbeat(control.AgentHeartbeat{ID: "agent-ok", Now: now})

	failed, failedJobIDs, err := failLostAgentJobs(stores.jobs, registry, now)
	if err != nil {
		t.Fatalf("failLostAgentJobs() error = %v", err)
	}
	if failed != 1 {
		t.Fatalf("failed = %d, want 1", failed)
	}
	if len(failedJobIDs) != 1 || failedJobIDs[0] != "lost-running" {
		t.Fatalf("failedJobIDs = %#v, want lost-running", failedJobIDs)
	}
	lost, ok, err := stores.jobs.Get("lost-running")
	if err != nil || !ok || lost.Status != core.JobStatusFailed || lost.Error != "agent_lost" || !lost.EndedAt.Equal(now) {
		t.Fatalf("Get(lost-running) = %#v ok=%v err=%v", lost, ok, err)
	}
	healthy, ok, err := stores.jobs.Get("healthy-running")
	if err != nil || !ok || healthy.Status != core.JobStatusRunning {
		t.Fatalf("Get(healthy-running) = %#v ok=%v err=%v", healthy, ok, err)
	}
	queued, ok, err := stores.jobs.Get("queued-lost")
	if err != nil || !ok || queued.Status != core.JobStatusQueued {
		t.Fatalf("Get(queued-lost) = %#v ok=%v err=%v", queued, ok, err)
	}
}

func TestServerFinishRestoreJobRejectsBackupMetadata(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	if err := stores.jobs.Save(core.Job{
		ID: "restore-job", Operation: core.JobOperationRestore, TargetID: "target", StorageID: "storage",
		RestoreBackupID: "backup-1", RestoreManifestID: "manifest-1", Status: core.JobStatusRunning, QueuedAt: now, StartedAt: now,
	}); err != nil {
		t.Fatalf("Save(restore job) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/jobs/restore-job/finish", "application/json", strings.NewReader(`{"status":"succeeded","backup":{"id":"backup-2","manifest_id":"manifest-2"}}`))
	if err != nil {
		t.Fatalf("POST restore finish with backup error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST restore finish with backup status = %d, want 400", resp.StatusCode)
	}

	resp, err = server.Client().Post(server.URL+"/api/v1/jobs/restore-job/finish", "application/json", strings.NewReader(`{"status":"succeeded"}`))
	if err != nil {
		t.Fatalf("POST restore finish error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST restore finish status = %d, want 200", resp.StatusCode)
	}
}

func TestServerJobCancelEndpoint(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	if err := stores.jobs.Save(core.Job{ID: "job-1", Status: core.JobStatusQueued, QueuedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("Save(job) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/jobs/job-1/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("POST job cancel error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(cancel) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST job cancel status = %d, body=%q", resp.StatusCode, body.String())
	}
	if !strings.Contains(body.String(), `"status":"canceled"`) {
		t.Fatalf("cancel body = %q", body.String())
	}
	job, ok, err := stores.jobs.Get("job-1")
	if err != nil || !ok || job.Status != core.JobStatusCanceled || job.Error != "canceled" {
		t.Fatalf("Get(job after cancel) = %#v ok=%v err=%v", job, ok, err)
	}
	resp, err = server.Client().Post(server.URL+"/api/v1/jobs/missing/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("POST missing job cancel error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST missing job cancel status = %d, want 404", resp.StatusCode)
	}
}

func TestServerJobRetryEndpoint(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	if err := stores.jobs.Save(core.Job{
		ID: "job-1", AgentID: "agent-1", TargetID: "target", StorageID: "storage", Type: core.BackupTypeFull,
		Status: core.JobStatusFailed, QueuedAt: now.Add(-time.Hour), StartedAt: now.Add(-time.Hour), EndedAt: now.Add(-time.Minute), Error: "agent_lost",
	}); err != nil {
		t.Fatalf("Save(job) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/jobs/job-1/retry", "application/json", nil)
	if err != nil {
		t.Fatalf("POST job retry error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(retry) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"status":"queued"`) {
		t.Fatalf("retry status=%d body=%q", resp.StatusCode, body.String())
	}
	job, ok, err := stores.jobs.Get("job-1")
	if err != nil || !ok || job.Status != core.JobStatusQueued || job.Error != "" || job.AgentID != "" || !job.StartedAt.IsZero() || !job.EndedAt.IsZero() {
		t.Fatalf("Get(job after retry) = %#v ok=%v err=%v", job, ok, err)
	}

	resp, err = server.Client().Post(server.URL+"/api/v1/jobs/job-1/retry", "application/json", nil)
	if err != nil {
		t.Fatalf("POST job retry queued error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST job retry queued status = %d, want 400", resp.StatusCode)
	}

	if err := stores.jobs.Save(core.Job{
		ID: "job-succeeded", TargetID: "target", StorageID: "storage", Type: core.BackupTypeFull,
		Status: core.JobStatusSucceeded, QueuedAt: now.Add(-time.Hour), StartedAt: now.Add(-time.Hour), EndedAt: now,
	}); err != nil {
		t.Fatalf("Save(succeeded job) error = %v", err)
	}
	resp, err = server.Client().Post(server.URL+"/api/v1/jobs/job-succeeded/retry", "application/json", nil)
	if err != nil {
		t.Fatalf("POST job retry succeeded error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST job retry succeeded status = %d, want 400", resp.StatusCode)
	}
	resp, err = server.Client().Post(server.URL+"/api/v1/jobs/missing/retry", "application/json", nil)
	if err != nil {
		t.Fatalf("POST missing job retry error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST missing job retry status = %d, want 404", resp.StatusCode)
	}
}

func TestServerSchedulerTickEndpoint(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	created := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	if err := stores.schedules.Save(core.Schedule{
		ID:         "schedule-1",
		Name:       "hourly",
		TargetID:   "target-1",
		StorageID:  "storage-1",
		BackupType: core.BackupTypeFull,
		Expression: "0 * * * *",
		CreatedAt:  created,
		UpdatedAt:  created,
	}); err != nil {
		t.Fatalf("Save(schedule) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/scheduler/tick", nil)
	if err != nil {
		t.Fatalf("NewRequest(scheduler tick) error = %v", err)
	}
	req.Header.Set(obs.RequestIDHeader, "req-scheduler-tick-1")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST scheduler tick error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(scheduler tick) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST scheduler tick status = %d, body=%q", resp.StatusCode, body.String())
	}
	if !strings.Contains(body.String(), `"schedule_id":"schedule-1"`) || !strings.Contains(body.String(), `"status":"queued"`) {
		t.Fatalf("scheduler tick body = %q", body.String())
	}
	jobs, err := stores.jobs.List()
	if err != nil {
		t.Fatalf("List(jobs) error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].ScheduleID != "schedule-1" {
		t.Fatalf("jobs = %#v", jobs)
	}
	events, err := stores.audit.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List(audit) error = %v", err)
	}
	if len(events) != 1 || events[0].Action != "schedule.tick" || events[0].Metadata["request_id"] != "req-scheduler-tick-1" {
		t.Fatalf("scheduler audit events = %#v", events)
	}
	if events[0].Metadata["job_count"] != float64(1) && events[0].Metadata["job_count"] != 1 {
		t.Fatalf("scheduler audit metadata = %#v", events[0].Metadata)
	}
}

func TestStartSchedulerLoopEnqueuesDueJobs(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	created := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	if err := stores.schedules.Save(core.Schedule{
		ID:         "schedule-1",
		Name:       "hourly",
		TargetID:   "target-1",
		StorageID:  "storage-1",
		BackupType: core.BackupTypeFull,
		Expression: "0 * * * *",
		CreatedAt:  created,
		UpdatedAt:  created,
	}); err != nil {
		t.Fatalf("Save(schedule) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startSchedulerLoop(ctx, io.Discard, stores, nil, time.Millisecond)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		jobs, err := stores.jobs.List()
		if err != nil {
			t.Fatalf("List(jobs) error = %v", err)
		}
		if len(jobs) == 1 && jobs[0].ScheduleID == "schedule-1" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	jobs, err := stores.jobs.List()
	if err != nil {
		t.Fatalf("List(jobs final) error = %v", err)
	}
	t.Fatalf("scheduler loop did not enqueue job, jobs=%#v", jobs)
}

func TestServerBackupNowEndpoint(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/backups/now", "application/json", strings.NewReader(`{"target_id":"target-1","storage_id":"storage-1","parent_id":"backup-parent"}`))
	if err != nil {
		t.Fatalf("POST backup now error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST backup now status = %d, want 200", resp.StatusCode)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(backup now) error = %v", err)
	}
	if !strings.Contains(body.String(), `"target_id":"target-1"`) ||
		!strings.Contains(body.String(), `"status":"queued"`) ||
		!strings.Contains(body.String(), `"type":"incr"`) ||
		!strings.Contains(body.String(), `"parent_backup_id":"backup-parent"`) {
		t.Fatalf("backup now body = %q", body.String())
	}
	jobs, err := stores.jobs.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].TargetID != "target-1" || jobs[0].Status != core.JobStatusQueued ||
		jobs[0].Type != core.BackupTypeIncremental || jobs[0].ParentBackupID != "backup-parent" {
		t.Fatalf("jobs = %#v", jobs)
	}
}

func TestServerBackupsListInspectProtect(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	if err := stores.backups.Save(core.Backup{
		ID: "backup-1", TargetID: "target-1", StorageID: "storage-1", JobID: "job-1",
		Type: core.BackupTypeFull, ManifestID: "manifest-1", StartedAt: now.Add(-time.Hour), EndedAt: now,
		SizeBytes: 42, ChunkCount: 3,
	}); err != nil {
		t.Fatalf("Save(backup) error = %v", err)
	}
	if err := stores.backups.Save(core.Backup{
		ID: "backup-2", TargetID: "target-2", StorageID: "storage-1", JobID: "job-2",
		Type: core.BackupTypeIncremental, ManifestID: "manifest-2", StartedAt: now.Add(-48 * time.Hour), EndedAt: now.Add(-47 * time.Hour),
		SizeBytes: 12, ChunkCount: 1, Protected: true,
	}); err != nil {
		t.Fatalf("Save(backup-2) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/api/v1/backups")
	if err != nil {
		t.Fatalf("GET backups error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(backups) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"id":"backup-1"`) || !strings.Contains(body.String(), `"chunk_count":3`) {
		t.Fatalf("backups body = %q", body.String())
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/backups/backup-1")
	if err != nil {
		t.Fatalf("GET backup error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(backup) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"manifest_id":"manifest-1"`) {
		t.Fatalf("GET backup status=%d body=%q", resp.StatusCode, body.String())
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/backups/missing")
	if err != nil {
		t.Fatalf("GET missing backup error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing backup status = %d, want 404", resp.StatusCode)
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/backups?target_id=target-1&type=full&since=2026-04-25T00:00:00Z&protected=false")
	if err != nil {
		t.Fatalf("GET filtered backups error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(filtered backups) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"id":"backup-1"`) || strings.Contains(body.String(), `"id":"backup-2"`) {
		t.Fatalf("filtered backups body = %q", body.String())
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/backups?protected=maybe")
	if err != nil {
		t.Fatalf("GET invalid filtered backups error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid filtered backups status = %d, want 400", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/backups/backup-1/protect", nil)
	if err != nil {
		t.Fatalf("NewRequest(protect) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST protect error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(protect) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"protected":true`) {
		t.Fatalf("protect body = %q", body.String())
	}

	req, err = http.NewRequest(http.MethodPost, server.URL+"/api/v1/backups/backup-1/unprotect", nil)
	if err != nil {
		t.Fatalf("NewRequest(unprotect) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST unprotect error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(unprotect) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"protected":false`) {
		t.Fatalf("unprotect body = %q", body.String())
	}
	req, err = http.NewRequest(http.MethodPost, server.URL+"/api/v1/backups/missing/protect", nil)
	if err != nil {
		t.Fatalf("NewRequest(protect missing) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST protect missing error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST protect missing status = %d, want 404", resp.StatusCode)
	}
}

func TestParseBackupListTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	got, err := parseBackupListTime("2026-04-25T10:00:00Z", now)
	if err != nil {
		t.Fatalf("parseBackupListTime(rfc3339) error = %v", err)
	}
	if want := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("parseBackupListTime(rfc3339) = %s, want %s", got, want)
	}
	for input, want := range map[string]time.Duration{
		"24h": 24 * time.Hour,
		"2d":  48 * time.Hour,
		"1w":  7 * 24 * time.Hour,
	} {
		got, err := parseRelativeDuration(input)
		if err != nil {
			t.Fatalf("parseRelativeDuration(%s) error = %v", input, err)
		}
		if got != want {
			t.Fatalf("parseRelativeDuration(%s) = %s, want %s", input, got, want)
		}
	}
	for _, input := range []string{"-1h", "-1d", "bad"} {
		if _, err := parseRelativeDuration(input); err == nil {
			t.Fatalf("parseRelativeDuration(%s) error = nil, want error", input)
		}
	}
}

func TestServerRetentionPlanEndpoint(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	for _, backup := range []core.Backup{
		{ID: "backup-old", TargetID: "target", StorageID: "storage", JobID: "job-1", Type: core.BackupTypeFull, ManifestID: "manifest-1", StartedAt: now.Add(-3 * time.Hour), EndedAt: now.Add(-2 * time.Hour)},
		{ID: "backup-new", TargetID: "target", StorageID: "storage", JobID: "job-2", Type: core.BackupTypeFull, ManifestID: "manifest-2", StartedAt: now.Add(-time.Hour), EndedAt: now},
	} {
		if err := stores.backups.Save(backup); err != nil {
			t.Fatalf("Save(%s) error = %v", backup.ID, err)
		}
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	request := `{"now":"2026-04-25T12:00:00Z","policy":{"rules":[{"kind":"count","params":{"n":1}}]}}`
	resp, err := server.Client().Post(server.URL+"/api/v1/retention/plan", "application/json", strings.NewReader(request))
	if err != nil {
		t.Fatalf("POST retention plan error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST retention plan status = %d, want 200", resp.StatusCode)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(retention plan) error = %v", err)
	}
	text := body.String()
	if !strings.Contains(text, `"id":"backup-new"`) || !strings.Contains(text, `"keep":true`) || !strings.Contains(text, `"id":"backup-old"`) {
		t.Fatalf("retention plan body = %q", text)
	}
}

func TestServerRetentionApplyEndpoint(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	for _, backup := range []core.Backup{
		{ID: "backup-old", TargetID: "target", StorageID: "storage", JobID: "job-1", Type: core.BackupTypeFull, ManifestID: "manifest-1", StartedAt: now.Add(-3 * time.Hour), EndedAt: now.Add(-2 * time.Hour)},
		{ID: "backup-new", TargetID: "target", StorageID: "storage", JobID: "job-2", Type: core.BackupTypeFull, ManifestID: "manifest-2", StartedAt: now.Add(-time.Hour), EndedAt: now},
	} {
		if err := stores.backups.Save(backup); err != nil {
			t.Fatalf("Save(%s) error = %v", backup.ID, err)
		}
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	request := `{"dry_run":true,"now":"2026-04-25T12:00:00Z","policy":{"rules":[{"kind":"count","params":{"n":1}}]}}`
	resp, err := server.Client().Post(server.URL+"/api/v1/retention/apply", "application/json", strings.NewReader(request))
	if err != nil {
		t.Fatalf("POST retention apply dry-run error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(retention apply dry-run) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST retention apply dry-run status = %d, body=%q", resp.StatusCode, body.String())
	}
	if !strings.Contains(body.String(), `"deleted":["backup-old"]`) || !strings.Contains(body.String(), `"dry_run":true`) {
		t.Fatalf("retention apply dry-run body = %q", body.String())
	}
	if _, ok, err := stores.backups.Get("backup-old"); err != nil || !ok {
		t.Fatalf("Get(backup-old after dry-run) ok=%v err=%v, want present", ok, err)
	}

	request = `{"now":"2026-04-25T12:00:00Z","policy":{"rules":[{"kind":"count","params":{"n":1}}]}}`
	resp, err = server.Client().Post(server.URL+"/api/v1/retention/apply", "application/json", strings.NewReader(request))
	if err != nil {
		t.Fatalf("POST retention apply error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(retention apply) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST retention apply status = %d, body=%q", resp.StatusCode, body.String())
	}
	if !strings.Contains(body.String(), `"deleted":["backup-old"]`) || !strings.Contains(body.String(), `"dry_run":false`) {
		t.Fatalf("retention apply body = %q", body.String())
	}
	if _, ok, err := stores.backups.Get("backup-old"); err != nil || ok {
		t.Fatalf("Get(backup-old after apply) ok=%v err=%v, want missing", ok, err)
	}
	if _, ok, err := stores.backups.Get("backup-new"); err != nil || !ok {
		t.Fatalf("Get(backup-new after apply) ok=%v err=%v, want present", ok, err)
	}
}

func TestServerRetentionApplyKeepsParentForKeptChild(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	for _, backup := range []core.Backup{
		{ID: "full", TargetID: "target", StorageID: "storage", JobID: "job-1", Type: core.BackupTypeFull, ManifestID: "manifest-1", StartedAt: now.Add(-3 * time.Hour), EndedAt: now.Add(-3 * time.Hour)},
		{ID: "incr", ParentID: "full", TargetID: "target", StorageID: "storage", JobID: "job-2", Type: core.BackupTypeIncremental, ManifestID: "manifest-2", StartedAt: now.Add(-time.Hour), EndedAt: now.Add(-time.Hour)},
		{ID: "old", TargetID: "target", StorageID: "storage", JobID: "job-3", Type: core.BackupTypeFull, ManifestID: "manifest-3", StartedAt: now.Add(-4 * time.Hour), EndedAt: now.Add(-4 * time.Hour)},
	} {
		if err := stores.backups.Save(backup); err != nil {
			t.Fatalf("Save(%s) error = %v", backup.ID, err)
		}
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	request := `{"now":"2026-04-25T12:00:00Z","policy":{"rules":[{"kind":"count","params":{"n":1}}]}}`
	resp, err := server.Client().Post(server.URL+"/api/v1/retention/apply", "application/json", strings.NewReader(request))
	if err != nil {
		t.Fatalf("POST retention apply error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(retention apply) error = %v", err)
	}
	resp.Body.Close()
	text := body.String()
	if !strings.Contains(text, `"deleted":["old"]`) || strings.Contains(text, `"deleted":["full"`) {
		t.Fatalf("retention apply body = %q", text)
	}
	if _, ok, err := stores.backups.Get("full"); err != nil || !ok {
		t.Fatalf("Get(full after apply) ok=%v err=%v, want present", ok, err)
	}
	if _, ok, err := stores.backups.Get("incr"); err != nil || !ok {
		t.Fatalf("Get(incr after apply) ok=%v err=%v, want present", ok, err)
	}
	if _, ok, err := stores.backups.Get("old"); err != nil || ok {
		t.Fatalf("Get(old after apply) ok=%v err=%v, want missing", ok, err)
	}
}

func TestServerRestorePreviewEndpoint(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	for _, backup := range []core.Backup{
		{ID: "full", TargetID: "target", StorageID: "storage", JobID: "job-1", Type: core.BackupTypeFull, ManifestID: "manifest-1", StartedAt: now.Add(-3 * time.Hour), EndedAt: now.Add(-3 * time.Hour)},
		{ID: "incr", ParentID: "full", TargetID: "target", StorageID: "storage", JobID: "job-2", Type: core.BackupTypeIncremental, ManifestID: "manifest-2", StartedAt: now.Add(-time.Hour), EndedAt: now.Add(-time.Hour)},
	} {
		if err := stores.backups.Save(backup); err != nil {
			t.Fatalf("Save(%s) error = %v", backup.ID, err)
		}
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/restore/preview", "application/json", strings.NewReader(`{"backup_id":"incr","target_id":"restore-target"}`))
	if err != nil {
		t.Fatalf("POST restore preview error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(restore preview) error = %v", err)
	}
	resp.Body.Close()
	text := body.String()
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"backup_id":"incr"`) || !strings.Contains(text, `"target_id":"restore-target"`) || !strings.Contains(text, `"backup_id":"full"`) {
		t.Fatalf("restore preview status=%d body=%q", resp.StatusCode, text)
	}
}

func TestServerRestoreStartEndpoint(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	if err := stores.backups.Save(core.Backup{
		ID: "backup-1", TargetID: "source-target", StorageID: "storage-1", JobID: "job-1",
		Type: core.BackupTypeFull, ManifestID: "manifest-1", StartedAt: now.Add(-time.Hour), EndedAt: now,
	}); err != nil {
		t.Fatalf("Save(backup) error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/restore", "application/json", strings.NewReader(`{"backup_id":"backup-1","target_id":"restore-target","at":"2026-04-25T12:30:00Z","dry_run":true,"replace_existing":true}`))
	if err != nil {
		t.Fatalf("POST restore start error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(restore start) error = %v", err)
	}
	resp.Body.Close()
	text := body.String()
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"operation":"restore"`) || !strings.Contains(text, `"restore_backup_id":"backup-1"`) || !strings.Contains(text, `"restore_manifest_id":"manifest-1"`) || !strings.Contains(text, `"target_id":"restore-target"`) || !strings.Contains(text, `"restore_dry_run":true`) || !strings.Contains(text, `"restore_replace_existing":true`) {
		t.Fatalf("restore start status=%d body=%q", resp.StatusCode, text)
	}
	jobs, err := stores.jobs.List()
	if err != nil {
		t.Fatalf("List(jobs) error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].Operation != core.JobOperationRestore || jobs[0].RestoreBackupID != "backup-1" || jobs[0].RestoreManifestID != "manifest-1" || jobs[0].TargetID != "restore-target" || !jobs[0].RestoreDryRun || !jobs[0].RestoreReplaceExisting {
		t.Fatalf("jobs = %#v", jobs)
	}
	events, err := stores.audit.List(context.Background(), 0)
	if err != nil {
		t.Fatalf("List(audit) error = %v", err)
	}
	if len(events) != 1 || events[0].Action != "restore.requested" || events[0].ResourceID != jobs[0].ID {
		t.Fatalf("audit events = %#v", events)
	}
	resp, err = server.Client().Post(server.URL+"/api/v1/jobs/claim", "application/json", nil)
	if err != nil {
		t.Fatalf("POST jobs claim restore error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(claim restore) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"operation":"restore"`) || !strings.Contains(body.String(), `"status":"running"`) {
		t.Fatalf("claim restore status=%d body=%q", resp.StatusCode, body.String())
	}
}

func TestServerRestoreStartIncludesManifestChain(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	for _, backup := range []core.Backup{
		{ID: "full", TargetID: "source-target", StorageID: "storage-1", JobID: "job-1", Type: core.BackupTypeFull, ManifestID: "manifest-full", StartedAt: now.Add(-2 * time.Hour), EndedAt: now.Add(-2 * time.Hour)},
		{ID: "incr", ParentID: "full", TargetID: "source-target", StorageID: "storage-1", JobID: "job-2", Type: core.BackupTypeIncremental, ManifestID: "manifest-incr", StartedAt: now.Add(-time.Hour), EndedAt: now.Add(-time.Hour)},
	} {
		if err := stores.backups.Save(backup); err != nil {
			t.Fatalf("Save(%s) error = %v", backup.ID, err)
		}
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/restore", "application/json", strings.NewReader(`{"backup_id":"incr","target_id":"restore-target"}`))
	if err != nil {
		t.Fatalf("POST restore chain error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(restore chain) error = %v", err)
	}
	resp.Body.Close()
	text := body.String()
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"restore_manifest_ids":["manifest-full","manifest-incr"]`) {
		t.Fatalf("restore chain status=%d body=%q", resp.StatusCode, text)
	}
}

func TestServerRetentionPolicyCRUD(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/retention/policies", "application/json", strings.NewReader(`{"id":"policy-1","name":"daily","rules":[{"kind":"count","params":{"n":7}}]}`))
	if err != nil {
		t.Fatalf("POST retention policy error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST retention policy status = %d, want 200", resp.StatusCode)
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/retention/policies")
	if err != nil {
		t.Fatalf("GET retention policies error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(retention policies) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"id":"policy-1"`) || !strings.Contains(body.String(), `"kind":"count"`) {
		t.Fatalf("retention policies body = %q", body.String())
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/retention/policies/policy-1")
	if err != nil {
		t.Fatalf("GET retention policy error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(retention policy) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"name":"daily"`) {
		t.Fatalf("GET retention policy status=%d body=%q", resp.StatusCode, body.String())
	}
	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/v1/retention/policies/policy-1", strings.NewReader(`{"id":"ignored","name":"weekly","rules":[{"kind":"time","params":{"duration":"168h"}}]}`))
	if err != nil {
		t.Fatalf("NewRequest(update policy) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("PUT retention policy error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(update policy) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"id":"policy-1"`) || !strings.Contains(body.String(), `"name":"weekly"`) || !strings.Contains(body.String(), `"kind":"time"`) {
		t.Fatalf("PUT retention policy status=%d body=%q", resp.StatusCode, body.String())
	}
	req, err = http.NewRequest(http.MethodDelete, server.URL+"/api/v1/retention/policies/policy-1", nil)
	if err != nil {
		t.Fatalf("NewRequest(delete policy) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("DELETE retention policy error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE retention policy status = %d, want 204", resp.StatusCode)
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/retention/policies/policy-1")
	if err != nil {
		t.Fatalf("GET deleted retention policy error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted retention policy status = %d, want 404", resp.StatusCode)
	}
}

func TestServerTargetCRUD(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	payload := `{"id":"target-1","name":"redis","driver":"redis","endpoint":"127.0.0.1:6379","options":{"password":"secret","tls":"disable"}}`
	resp, err := server.Client().Post(server.URL+"/api/v1/targets", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST target error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST target status = %d, want 200", resp.StatusCode)
	}

	resp, err = server.Client().Get(server.URL + "/api/v1/targets")
	if err != nil {
		t.Fatalf("GET targets error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(targets) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"id":"target-1"`) || !strings.Contains(body.String(), `"driver":"redis"`) || strings.Contains(body.String(), "secret") || !strings.Contains(body.String(), `"password":"***REDACTED***"`) {
		t.Fatalf("targets body = %q", body.String())
	}

	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/v1/targets/target-1", strings.NewReader(`{"name":"redis-prod","driver":"redis","endpoint":"127.0.0.1:6380","options":{"password":"new-secret"}}`))
	if err != nil {
		t.Fatalf("NewRequest(PUT) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("PUT target error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT target status = %d, want 200", resp.StatusCode)
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/targets/target-1")
	if err != nil {
		t.Fatalf("GET target error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(target) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"name":"redis-prod"`) || strings.Contains(body.String(), "new-secret") {
		t.Fatalf("target body = %q", body.String())
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/targets/target-1?include_secrets=true")
	if err != nil {
		t.Fatalf("GET target with secrets error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(target secrets) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), "new-secret") {
		t.Fatalf("target secrets status=%d body=%q", resp.StatusCode, body.String())
	}

	req, err = http.NewRequest(http.MethodDelete, server.URL+"/api/v1/targets/target-1", nil)
	if err != nil {
		t.Fatalf("NewRequest(DELETE) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("DELETE target error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE target status = %d, want 204", resp.StatusCode)
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/targets/target-1")
	if err != nil {
		t.Fatalf("GET deleted target error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted target status = %d, want 404", resp.StatusCode)
	}
}

func TestServerNotificationCRUD(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/api/v1/notifications", "application/json", strings.NewReader(`{"id":"notification-1","name":"ops","events":["job.failed"],"webhook_url":"https://hooks.example.com/kronos","secret":"shared","enabled":true}`))
	if err != nil {
		t.Fatalf("POST notification error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(create notification) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"id":"notification-1"`) || !strings.Contains(body.String(), `"secret":"***REDACTED***"`) || strings.Contains(body.String(), `"secret":"shared"`) {
		t.Fatalf("POST notification status=%d body=%q", resp.StatusCode, body.String())
	}

	resp, err = server.Client().Get(server.URL + "/api/v1/notifications")
	if err != nil {
		t.Fatalf("GET notifications error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(list notifications) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"notifications"`) || !strings.Contains(body.String(), `"notification-1"`) || !strings.Contains(body.String(), `"secret":"***REDACTED***"`) || strings.Contains(body.String(), `"secret":"shared"`) {
		t.Fatalf("GET notifications status=%d body=%q", resp.StatusCode, body.String())
	}

	resp, err = server.Client().Get(server.URL + "/api/v1/notifications/notification-1?include_secrets=true")
	if err != nil {
		t.Fatalf("GET notification with secrets error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(notification secrets) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"secret":"shared"`) {
		t.Fatalf("GET notification secrets status=%d body=%q", resp.StatusCode, body.String())
	}

	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/v1/notifications/notification-1", strings.NewReader(`{"name":"ops-updated","events":["job.succeeded","job.failed"],"webhook_url":"https://hooks.example.com/updated","enabled":false}`))
	if err != nil {
		t.Fatalf("NewRequest(update notification) error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("PUT notification error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(update notification) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"name":"ops-updated"`) || !strings.Contains(body.String(), `"enabled":false`) {
		t.Fatalf("PUT notification status=%d body=%q", resp.StatusCode, body.String())
	}
	got, ok, err := stores.notifications.Get("notification-1")
	if err != nil || !ok || got.Name != "ops-updated" || got.Enabled {
		t.Fatalf("Get(notification after update) = %#v ok=%v err=%v", got, ok, err)
	}

	req, err = http.NewRequest(http.MethodDelete, server.URL+"/api/v1/notifications/notification-1", nil)
	if err != nil {
		t.Fatalf("NewRequest(delete notification) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("DELETE notification error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE notification status=%d, want 204", resp.StatusCode)
	}
	if _, ok, err := stores.notifications.Get("notification-1"); err != nil || ok {
		t.Fatalf("Get(deleted notification) ok=%v err=%v, want missing", ok, err)
	}
	events, err := stores.audit.List(context.Background(), 0)
	if err != nil {
		t.Fatalf("List(audit) error = %v", err)
	}
	if len(events) != 3 || events[0].Action != "notification.created" || events[1].Action != "notification.updated" || events[2].Action != "notification.deleted" {
		t.Fatalf("audit events = %#v", events)
	}
}

func TestServerScheduleCRUD(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	payload := `{"id":"schedule-1","name":"nightly","target_id":"target-1","storage_id":"storage-1","backup_type":"full","expression":"0 2 * * *"}`
	resp, err := server.Client().Post(server.URL+"/api/v1/schedules", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST schedule error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST schedule status = %d, want 200", resp.StatusCode)
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/schedules")
	if err != nil {
		t.Fatalf("GET schedules error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(schedules) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"id":"schedule-1"`) || !strings.Contains(body.String(), `"expression":"0 2 * * *"`) {
		t.Fatalf("schedules body = %q", body.String())
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/schedules/schedule-1")
	if err != nil {
		t.Fatalf("GET schedule error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(schedule) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"name":"nightly"`) {
		t.Fatalf("GET schedule status=%d body=%q", resp.StatusCode, body.String())
	}
	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/v1/schedules/schedule-1", strings.NewReader(`{"name":"early","target_id":"target-1","storage_id":"storage-1","backup_type":"full","expression":"0 1 * * *"}`))
	if err != nil {
		t.Fatalf("NewRequest(update schedule) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("PUT schedule error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(update schedule) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"name":"early"`) || !strings.Contains(body.String(), `"expression":"0 1 * * *"`) {
		t.Fatalf("PUT schedule status=%d body=%q", resp.StatusCode, body.String())
	}
	resp, err = server.Client().Post(server.URL+"/api/v1/schedules/schedule-1/pause", "application/json", nil)
	if err != nil {
		t.Fatalf("POST schedule pause error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(schedule pause) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"paused":true`) {
		t.Fatalf("schedule pause body = %q", body.String())
	}
	resp, err = server.Client().Post(server.URL+"/api/v1/schedules/schedule-1/resume", "application/json", nil)
	if err != nil {
		t.Fatalf("POST schedule resume error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(schedule resume) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"paused":false`) {
		t.Fatalf("schedule resume body = %q", body.String())
	}
	req, err = http.NewRequest(http.MethodDelete, server.URL+"/api/v1/schedules/schedule-1", nil)
	if err != nil {
		t.Fatalf("NewRequest(DELETE) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("DELETE schedule error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE schedule status = %d, want 204", resp.StatusCode)
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/schedules/schedule-1")
	if err != nil {
		t.Fatalf("GET deleted schedule error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted schedule status = %d, want 404", resp.StatusCode)
	}
}

func TestServerStorageCRUD(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	payload := `{"id":"storage-1","name":"local","kind":"local","uri":"file:///repo","options":{"access_key":"access","secret_key":"secret","region":"eu-north-1"}}`
	resp, err := server.Client().Post(server.URL+"/api/v1/storages", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST storage error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST storage status = %d, want 200", resp.StatusCode)
	}

	resp, err = server.Client().Get(server.URL + "/api/v1/storages")
	if err != nil {
		t.Fatalf("GET storages error = %v", err)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(storages) error = %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(body.String(), `"id":"storage-1"`) || !strings.Contains(body.String(), `"kind":"local"`) || strings.Contains(body.String(), `"secret"`) || !strings.Contains(body.String(), `"access_key":"***REDACTED***"`) || !strings.Contains(body.String(), `"region":"eu-north-1"`) {
		t.Fatalf("storages body = %q", body.String())
	}

	resp, err = server.Client().Get(server.URL + "/api/v1/storages/storage-1")
	if err != nil {
		t.Fatalf("GET storage error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(storage) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `"name":"local"`) || strings.Contains(body.String(), `"secret"`) {
		t.Fatalf("GET storage status=%d body=%q", resp.StatusCode, body.String())
	}
	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/v1/storages/storage-1", strings.NewReader(`{"name":"primary-local","kind":"local","uri":"file:///repo2","options":{"secret_key":"new-secret"}}`))
	if err != nil {
		t.Fatalf("NewRequest(PUT) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("PUT storage error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT storage status = %d, want 200", resp.StatusCode)
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/storages/storage-1?include_secrets=true")
	if err != nil {
		t.Fatalf("GET storage with secrets error = %v", err)
	}
	body.Reset()
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("ReadFrom(storage secrets) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), "new-secret") {
		t.Fatalf("GET storage secrets status=%d body=%q", resp.StatusCode, body.String())
	}
	req, err = http.NewRequest(http.MethodDelete, server.URL+"/api/v1/storages/storage-1", nil)
	if err != nil {
		t.Fatalf("NewRequest(DELETE) error = %v", err)
	}
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatalf("DELETE storage error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE storage status = %d, want 204", resp.StatusCode)
	}
	resp, err = server.Client().Get(server.URL + "/api/v1/storages/storage-1")
	if err != nil {
		t.Fatalf("GET deleted storage error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted storage status = %d, want 404", resp.StatusCode)
	}
}

func TestServeControlPlaneStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	out := &lockedBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- serveControlPlane(ctx, out, "127.0.0.1:0", nil)
	}()
	for !strings.Contains(out.String(), "kronos-server listening=") {
		time.Sleep(time.Millisecond)
	}
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("serveControlPlane() error = %v, want context.Canceled", err)
	}
}

func TestServerRejectsBadRequestsAndMethods(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	cases := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{method: http.MethodPost, path: "/healthz", status: http.StatusMethodNotAllowed},
		{method: http.MethodPost, path: "/readyz", status: http.StatusMethodNotAllowed},
		{method: http.MethodPost, path: "/api/v1/agents/heartbeat", body: `{"status":"healthy"}`, status: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/v1/users", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/v1/users/missing/grant", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/v1/tokens", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/v1/auth/verify", body: `{`, status: http.StatusUnauthorized},
		{method: http.MethodPost, path: "/api/v1/backups/now", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodGet, path: "/api/v1/jobs?since=not-time", status: http.StatusBadRequest},
		{method: http.MethodGet, path: "/api/v1/backups?since=not-time", status: http.StatusBadRequest},
		{method: http.MethodGet, path: "/api/v1/audit?since=not-time", status: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/v1/retention/plan", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/v1/retention/apply", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/v1/retention/policies", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPut, path: "/api/v1/retention/policies/missing", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/v1/restore/preview", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/v1/restore", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/v1/targets", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPut, path: "/api/v1/targets/missing", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/v1/storages", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPut, path: "/api/v1/storages/missing", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPost, path: "/api/v1/schedules", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPut, path: "/api/v1/schedules/missing", body: `{`, status: http.StatusBadRequest},
		{method: http.MethodPatch, path: "/api/v1/schedules/missing", status: http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, server.URL+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("NewRequest() error = %v", err)
			}
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatalf("Do() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				var body bytes.Buffer
				_, _ = body.ReadFrom(resp.Body)
				t.Fatalf("status = %d, want %d, body=%q", resp.StatusCode, tc.status, body.String())
			}
		})
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

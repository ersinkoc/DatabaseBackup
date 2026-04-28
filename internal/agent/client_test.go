package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/kvstore"
	"github.com/kronos/kronos/internal/obs"
	control "github.com/kronos/kronos/internal/server"
)

func TestClientHeartbeatClaimFinish(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	jobs, err := control.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore() error = %v", err)
	}
	backups, err := control.NewBackupStore(db)
	if err != nil {
		t.Fatalf("NewBackupStore() error = %v", err)
	}
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	if err := jobs.Save(core.Job{ID: "job-1", TargetID: "target", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusQueued, QueuedAt: now}); err != nil {
		t.Fatalf("Save(job) error = %v", err)
	}
	registry := control.NewAgentRegistry(func() time.Time { return now }, time.Minute)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agents/heartbeat":
			if r.Method != http.MethodPost {
				t.Fatalf("heartbeat method = %s", r.Method)
			}
			var heartbeat control.AgentHeartbeat
			decodeTestJSON(t, w, r, &heartbeat)
			writeTestJSON(t, w, registry.Heartbeat(heartbeat))
		case "/api/v1/jobs/claim":
			if r.Method != http.MethodPost {
				t.Fatalf("claim method = %s", r.Method)
			}
			list, err := jobs.List()
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			for _, job := range list {
				if job.Status == core.JobStatusQueued {
					job.Status = core.JobStatusRunning
					job.StartedAt = now
					if err := jobs.Save(job); err != nil {
						t.Fatalf("Save(running) error = %v", err)
					}
					writeTestJSON(t, w, claimResponse{Job: &job})
					return
				}
			}
			writeTestJSON(t, w, claimResponse{})
		case "/api/v1/jobs/job-1/finish":
			var request finishRequest
			decodeTestJSON(t, w, r, &request)
			job, ok, err := jobs.Get("job-1")
			if err != nil || !ok {
				t.Fatalf("Get(job) ok=%v err=%v", ok, err)
			}
			job.Status = request.Status
			job.EndedAt = now.Add(time.Minute)
			job.Error = request.Error
			if err := jobs.Save(job); err != nil {
				t.Fatalf("Save(finished) error = %v", err)
			}
			if request.Backup != nil {
				if err := backups.Save(*request.Backup); err != nil {
					t.Fatalf("Save(backup) error = %v", err)
				}
			}
			writeTestJSON(t, w, job)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	snapshot, err := client.Heartbeat(context.Background(), control.AgentHeartbeat{ID: "agent-1"})
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if snapshot.ID != "agent-1" || snapshot.Status != control.AgentHealthy {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	job, err := client.Claim(context.Background())
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if job == nil || job.ID != "job-1" || job.Status != core.JobStatusRunning {
		t.Fatalf("claimed job = %#v", job)
	}
	finished, err := client.Finish(context.Background(), "job-1", core.JobStatusSucceeded, "", core.JobResult{
		Backup: &core.Backup{ID: "backup-1", TargetID: "target", StorageID: "storage", JobID: "job-1", Type: core.BackupTypeFull, ManifestID: "manifest-1", StartedAt: now, EndedAt: now.Add(time.Minute)},
	})
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if finished.Status != core.JobStatusSucceeded {
		t.Fatalf("finished = %#v", finished)
	}
	if _, ok, err := backups.Get("backup-1"); err != nil || !ok {
		t.Fatalf("Get(backup-1) ok=%v err=%v", ok, err)
	}
}

func TestClientSendsBearerToken(t *testing.T) {
	t.Parallel()

	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		writeTestJSON(t, w, control.AgentSnapshot{ID: "agent-1", Status: control.AgentHealthy})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	client.Token = "agent-secret"
	if _, err := client.Heartbeat(context.Background(), control.AgentHeartbeat{ID: "agent-1"}); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if gotAuth != "Bearer agent-secret" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
}

func TestClientSendsAgentIDHeader(t *testing.T) {
	t.Parallel()

	var gotAgentID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAgentID = r.Header.Get("X-Kronos-Agent-ID")
		writeTestJSON(t, w, claimResponse{})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	client.AgentID = "agent-1"
	if _, err := client.Claim(context.Background()); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if gotAgentID != "agent-1" {
		t.Fatalf("X-Kronos-Agent-ID = %q", gotAgentID)
	}
}

func TestClientSendsRequestIDHeader(t *testing.T) {
	t.Parallel()

	var gotRequestIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestIDs = append(gotRequestIDs, r.Header.Get(obs.RequestIDHeader))
		writeTestJSON(t, w, control.AgentSnapshot{ID: "agent-1", Status: control.AgentHealthy})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	ctx := obs.WithRequestID(context.Background(), "req-agent-1")
	if _, err := client.Heartbeat(ctx, control.AgentHeartbeat{ID: "agent-1"}); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if _, err := client.Heartbeat(context.Background(), control.AgentHeartbeat{ID: "agent-1"}); err != nil {
		t.Fatalf("Heartbeat(background) error = %v", err)
	}
	if len(gotRequestIDs) != 2 {
		t.Fatalf("request ids = %#v, want 2 entries", gotRequestIDs)
	}
	if gotRequestIDs[0] != "req-agent-1" {
		t.Fatalf("%s = %q, want req-agent-1", obs.RequestIDHeader, gotRequestIDs[0])
	}
	if gotRequestIDs[1] == "" {
		t.Fatalf("%s fallback is empty", obs.RequestIDHeader)
	}
}

func TestClientErrorIncludesRequestIDAndBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(obs.RequestIDHeader, "req-agent-error-1")
		http.Error(w, "agent denied", http.StatusForbidden)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.Claim(context.Background())
	if err == nil {
		t.Fatal("Claim() error = nil, want error")
	}
	if text := err.Error(); !strings.Contains(text, "request_id=req-agent-error-1") || !strings.Contains(text, "agent denied") {
		t.Fatalf("Claim() error = %q", text)
	}
}

func TestClientFetchesResources(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/targets":
			if r.Method != http.MethodGet {
				t.Fatalf("targets method = %s", r.Method)
			}
			if r.URL.Query().Get("include_secrets") != "true" {
				t.Fatalf("targets include_secrets = %q", r.URL.Query().Get("include_secrets"))
			}
			writeTestJSON(t, w, targetsResponse{Targets: []core.Target{{ID: "target-1", Name: "redis", Driver: core.TargetDriverRedis}}})
		case "/api/v1/targets/target-1":
			if r.URL.Query().Get("include_secrets") != "true" {
				t.Fatalf("target include_secrets = %q", r.URL.Query().Get("include_secrets"))
			}
			writeTestJSON(t, w, core.Target{ID: "target-1", Name: "redis", Driver: core.TargetDriverRedis})
		case "/api/v1/storages":
			if r.Method != http.MethodGet {
				t.Fatalf("storages method = %s", r.Method)
			}
			if r.URL.Query().Get("include_secrets") != "true" {
				t.Fatalf("storages include_secrets = %q", r.URL.Query().Get("include_secrets"))
			}
			writeTestJSON(t, w, storagesResponse{Storages: []core.Storage{{ID: "storage-1", Name: "repo", Kind: core.StorageKindLocal}}})
		case "/api/v1/storages/storage-1":
			if r.URL.Query().Get("include_secrets") != "true" {
				t.Fatalf("storage include_secrets = %q", r.URL.Query().Get("include_secrets"))
			}
			writeTestJSON(t, w, core.Storage{ID: "storage-1", Name: "repo", Kind: core.StorageKindLocal})
		case "/api/v1/backups":
			if r.Method != http.MethodGet {
				t.Fatalf("backups method = %s", r.Method)
			}
			writeTestJSON(t, w, backupsResponse{Backups: []core.Backup{{ID: "backup-1", TargetID: "target-1", StorageID: "storage-1", Type: core.BackupTypeFull, ManifestID: "manifest-1", EndedAt: now}}})
		case "/api/v1/backups/backup-1":
			writeTestJSON(t, w, core.Backup{ID: "backup-1", TargetID: "target-1", StorageID: "storage-1", Type: core.BackupTypeFull, ManifestID: "manifest-1", EndedAt: now})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	targets, err := client.ListTargets(context.Background())
	if err != nil || len(targets) != 1 || targets[0].ID != "target-1" {
		t.Fatalf("ListTargets() targets=%#v err=%v", targets, err)
	}
	target, err := client.GetTarget(context.Background(), "target-1")
	if err != nil || target.ID != "target-1" {
		t.Fatalf("GetTarget() target=%#v err=%v", target, err)
	}
	storages, err := client.ListStorages(context.Background())
	if err != nil || len(storages) != 1 || storages[0].ID != "storage-1" {
		t.Fatalf("ListStorages() storages=%#v err=%v", storages, err)
	}
	storage, err := client.GetStorage(context.Background(), "storage-1")
	if err != nil || storage.ID != "storage-1" {
		t.Fatalf("GetStorage() storage=%#v err=%v", storage, err)
	}
	backups, err := client.ListBackups(context.Background())
	if err != nil || len(backups) != 1 || backups[0].ID != "backup-1" {
		t.Fatalf("ListBackups() backups=%#v err=%v", backups, err)
	}
	backup, err := client.GetBackup(context.Background(), "backup-1")
	if err != nil || backup.ID != "backup-1" {
		t.Fatalf("GetBackup() backup=%#v err=%v", backup, err)
	}
}

func TestNewClientReadsTokenFromEnv(t *testing.T) {
	t.Setenv("KRONOS_TOKEN", "env-secret")

	client, err := NewClient("http://127.0.0.1:8500", nil)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client.Token != "env-secret" {
		t.Fatalf("client.Token = %q, want env-secret", client.Token)
	}
}

func TestNewClientRejectsBadAddress(t *testing.T) {
	t.Parallel()

	if _, err := NewClient("://bad", nil); err == nil {
		t.Fatal("NewClient(bad) error = nil, want error")
	}
}

func decodeTestJSON(t *testing.T, w http.ResponseWriter, r *http.Request, dst any) {
	t.Helper()
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		t.Fatalf("Decode() error = %v", err)
	}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}

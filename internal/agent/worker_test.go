package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kronos/kronos/internal/core"
	control "github.com/kronos/kronos/internal/server"
)

func TestWorkerRunsClaimedJobAndFinishesSuccess(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	job := core.Job{ID: "job-1", TargetID: "target", StorageID: "storage", Type: core.BackupTypeFull, Status: core.JobStatusQueued, QueuedAt: now}
	var finished finishRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agents/heartbeat":
			writeTestJSON(t, w, control.AgentSnapshot{ID: "agent-1", Status: control.AgentHealthy, LastHeartbeat: now})
		case "/api/v1/jobs/claim":
			if job.Status != core.JobStatusQueued {
				writeTestJSON(t, w, claimResponse{})
				return
			}
			job.Status = core.JobStatusRunning
			writeTestJSON(t, w, claimResponse{Job: &job})
		case "/api/v1/jobs/job-1/finish":
			decodeTestJSON(t, w, r, &finished)
			job.Status = finished.Status
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
	worker := Worker{
		Client: client,
		Heartbeat: control.AgentHeartbeat{
			ID: "agent-1",
		},
		Executor:       staticExecutor{backup: &core.Backup{ID: "backup-1", TargetID: "target", StorageID: "storage", JobID: "job-1", Type: core.BackupTypeFull, ManifestID: "manifest-1", StartedAt: now, EndedAt: now.Add(time.Minute)}},
		MaxJobsPerTick: 1,
	}
	if err := worker.tick(context.Background()); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if finished.Status != core.JobStatusSucceeded || finished.Backup == nil || finished.Backup.ID != "backup-1" {
		t.Fatalf("finished request = %#v", finished)
	}
}

func TestWorkerFinishesFailedWhenExecutorErrors(t *testing.T) {
	t.Parallel()

	job := core.Job{ID: "job-1", Status: core.JobStatusQueued}
	var finished finishRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agents/heartbeat":
			writeTestJSON(t, w, control.AgentSnapshot{ID: "agent-1", Status: control.AgentHealthy})
		case "/api/v1/jobs/claim":
			job.Status = core.JobStatusRunning
			writeTestJSON(t, w, claimResponse{Job: &job})
		case "/api/v1/jobs/job-1/finish":
			decodeTestJSON(t, w, r, &finished)
			writeTestJSON(t, w, core.Job{ID: "job-1", Status: finished.Status, Error: finished.Error})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	worker := Worker{
		Client:    client,
		Heartbeat: control.AgentHeartbeat{ID: "agent-1"},
		Executor:  staticExecutor{err: fmt.Errorf("boom")},
	}
	if err := worker.tick(context.Background()); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if finished.Status != core.JobStatusFailed || finished.Error != "boom" || finished.Backup != nil {
		t.Fatalf("finished request = %#v", finished)
	}
}

func TestWorkerSendsRestoreFailureEvidence(t *testing.T) {
	t.Parallel()

	job := core.Job{
		ID:                 "restore-job",
		Operation:          core.JobOperationRestore,
		Status:             core.JobStatusQueued,
		TargetID:           "source-target",
		StorageID:          "storage-1",
		RestoreBackupID:    "backup-1",
		RestoreManifestIDs: []core.ID{"manifest-full", "manifest-incr"},
		RestoreTargetID:    "restore-target",
	}
	var finished finishRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agents/heartbeat":
			writeTestJSON(t, w, control.AgentSnapshot{ID: "agent-1", Status: control.AgentHealthy})
		case "/api/v1/jobs/claim":
			job.Status = core.JobStatusRunning
			writeTestJSON(t, w, claimResponse{Job: &job})
		case "/api/v1/jobs/restore-job/finish":
			decodeTestJSON(t, w, r, &finished)
			writeTestJSON(t, w, core.Job{ID: "restore-job", Status: finished.Status, Error: finished.Error, FailureEvidence: finished.Failure})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	worker := Worker{
		Client:    client,
		Heartbeat: control.AgentHeartbeat{ID: "agent-1"},
		Executor:  staticExecutor{err: fmt.Errorf("restore boom")},
	}
	if err := worker.tick(context.Background()); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if finished.Failure == nil || finished.Failure.Operation != core.JobOperationRestore || finished.Failure.Stage != "restore" || finished.Failure.Message != "restore boom" || finished.Failure.BackupID != "backup-1" || finished.Failure.TargetID != "restore-target" || len(finished.Failure.ManifestIDs) != 2 {
		t.Fatalf("failure evidence = %#v", finished.Failure)
	}
}

func TestWorkerSyncsResourcesBeforeClaim(t *testing.T) {
	t.Parallel()

	var claimed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agents/heartbeat":
			writeTestJSON(t, w, control.AgentSnapshot{ID: "agent-1", Status: control.AgentHealthy})
		case "/api/v1/targets":
			writeTestJSON(t, w, targetsResponse{Targets: []core.Target{{ID: "target-1", Name: "redis", Driver: core.TargetDriverRedis}}})
		case "/api/v1/jobs/claim":
			claimed = true
			writeTestJSON(t, w, claimResponse{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	executor := &syncingExecutor{}
	worker := Worker{
		Client:    client,
		Heartbeat: control.AgentHeartbeat{ID: "agent-1"},
		Executor:  executor,
	}
	if err := worker.tick(context.Background()); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if !executor.synced || executor.targets != 1 {
		t.Fatalf("executor sync synced=%v targets=%d", executor.synced, executor.targets)
	}
	if !claimed {
		t.Fatal("claim was not attempted after sync")
	}
}

func TestWorkerRunValidatesRequiredFields(t *testing.T) {
	t.Parallel()

	if err := (Worker{}).Run(context.Background()); err == nil {
		t.Fatal("Run(missing client) error = nil, want error")
	}
	if err := (Worker{Client: &Client{}}).Run(context.Background()); err == nil {
		t.Fatal("Run(missing executor) error = nil, want error")
	}
	if err := (Worker{Client: &Client{}, Executor: staticExecutor{}}).Run(context.Background()); err == nil {
		t.Fatal("Run(missing agent id) error = nil, want error")
	}
}

func TestWorkerRunStopsWhenContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agents/heartbeat":
			writeTestJSON(t, w, control.AgentSnapshot{ID: "agent-1", Status: control.AgentHealthy})
		case "/api/v1/jobs/claim":
			cancel()
			writeTestJSON(t, w, claimResponse{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	worker := Worker{
		Client:       client,
		Heartbeat:    control.AgentHeartbeat{ID: "agent-1"},
		Executor:     staticExecutor{},
		PollInterval: time.Millisecond,
	}
	if err := worker.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

type staticExecutor struct {
	backup *core.Backup
	err    error
}

func (e staticExecutor) Execute(context.Context, core.Job) (core.JobResult, error) {
	return core.JobResult{Backup: e.backup}, e.err
}

type syncingExecutor struct {
	synced  bool
	targets int
}

func (e *syncingExecutor) SyncResources(ctx context.Context, client *Client) error {
	targets, err := client.ListTargets(ctx)
	if err != nil {
		return err
	}
	e.synced = true
	e.targets = len(targets)
	return nil
}

func (e *syncingExecutor) Execute(context.Context, core.Job) (core.JobResult, error) {
	return core.JobResult{}, nil
}

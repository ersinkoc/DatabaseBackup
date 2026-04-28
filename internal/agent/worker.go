package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/kronos/kronos/internal/core"
	control "github.com/kronos/kronos/internal/server"
)

// Executor runs one claimed job on the agent host.
type Executor interface {
	Execute(ctx context.Context, job core.Job) (core.JobResult, error)
}

// ResourceSyncer refreshes executor-side metadata before claims are evaluated.
type ResourceSyncer interface {
	SyncResources(ctx context.Context, client *Client) error
}

// Worker polls the control plane for work and reports terminal status.
type Worker struct {
	Client         *Client
	Executor       Executor
	Heartbeat      control.AgentHeartbeat
	PollInterval   time.Duration
	IdleBackoff    time.Duration
	MaxJobsPerTick int
}

// Run starts the worker loop until ctx is canceled.
func (w Worker) Run(ctx context.Context) error {
	if w.Client == nil {
		return fmt.Errorf("agent client is required")
	}
	if w.Executor == nil {
		return fmt.Errorf("agent executor is required")
	}
	if w.Heartbeat.ID == "" {
		return fmt.Errorf("agent id is required")
	}
	interval := w.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if err := w.tick(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.tick(ctx); err != nil {
				return err
			}
		}
	}
}

func (w Worker) tick(ctx context.Context) error {
	if _, err := w.Client.Heartbeat(ctx, w.Heartbeat); err != nil {
		return err
	}
	w.Client.AgentID = w.Heartbeat.ID
	if syncer, ok := w.Executor.(ResourceSyncer); ok {
		if err := syncer.SyncResources(ctx, w.Client); err != nil {
			return err
		}
	}
	limit := w.MaxJobsPerTick
	if limit <= 0 {
		limit = 1
	}
	for i := 0; i < limit; i++ {
		job, err := w.Client.Claim(ctx)
		if err != nil {
			return err
		}
		if job == nil {
			return nil
		}
		if err := w.execute(ctx, *job); err != nil {
			return err
		}
	}
	return nil
}

func (w Worker) execute(ctx context.Context, job core.Job) error {
	result, err := w.Executor.Execute(ctx, job)
	if err != nil {
		_, finishErr := w.Client.Finish(ctx, job.ID, core.JobStatusFailed, err.Error(), failedJobResult(job, err))
		if finishErr != nil {
			return fmt.Errorf("job %s failed: %v; finish failed: %w", job.ID, err, finishErr)
		}
		return nil
	}
	_, err = w.Client.Finish(ctx, job.ID, core.JobStatusSucceeded, "", result)
	return err
}

func failedJobResult(job core.Job, err error) core.JobResult {
	if job.Operation != core.JobOperationRestore || err == nil {
		return core.JobResult{}
	}
	manifestIDs := append([]core.ID(nil), job.RestoreManifestIDs...)
	if len(manifestIDs) == 0 && !job.RestoreManifestID.IsZero() {
		manifestIDs = []core.ID{job.RestoreManifestID}
	}
	targetID := job.RestoreTargetID
	if targetID.IsZero() {
		targetID = job.TargetID
	}
	return core.JobResult{
		Failure: &core.FailureEvidence{
			Operation:   job.Operation,
			Stage:       "restore",
			Message:     err.Error(),
			BackupID:    job.RestoreBackupID,
			TargetID:    targetID,
			StorageID:   job.StorageID,
			ManifestIDs: manifestIDs,
		},
	}
}

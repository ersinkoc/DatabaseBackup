package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kronos/kronos/internal/chunk"
	kcompress "github.com/kronos/kronos/internal/compress"
	"github.com/kronos/kronos/internal/core"
	kcrypto "github.com/kronos/kronos/internal/crypto"
	"github.com/kronos/kronos/internal/drivers"
	"github.com/kronos/kronos/internal/manifest"
	"github.com/kronos/kronos/internal/secret"
	"github.com/kronos/kronos/internal/storage"
	"github.com/kronos/kronos/internal/storage/storagetest"
)

func TestBackupExecutorRunsFullBackupAndCommitsManifest(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{1}, 64)))
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	registry := drivers.NewRegistry()
	driver := &executorFakeDriver{}
	if err := registry.Register(driver); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	backend := storagetest.NewMemoryBackend("memory")
	clock := core.NewFakeClock(time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))
	executor := BackupExecutor{
		Drivers: registry,
		Targets: map[core.ID]drivers.Target{
			"target-1": {Name: "redis-prod", Driver: "fake"},
		},
		Backends: map[core.ID]storage.Backend{
			"storage-1": backend,
		},
		PipelineFactory: testPipelineFactory(t),
		PrivateKey:      privateKey,
		Clock:           clock,
	}
	result, err := executor.Execute(context.Background(), core.Job{
		ID: "job-1", TargetID: "target-1", StorageID: "storage-1", Type: core.BackupTypeFull,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	backup := result.Backup
	if backup == nil || backup.JobID != "job-1" || backup.TargetID != "target-1" || backup.ManifestID.IsZero() || backup.ChunkCount == 0 {
		t.Fatalf("backup = %#v", backup)
	}
	rc, _, err := backend.Get(context.Background(), string(backup.ManifestID))
	if err != nil {
		t.Fatalf("Get(manifest) error = %v", err)
	}
	defer rc.Close()
	var manifestBytes bytes.Buffer
	if _, err := manifestBytes.ReadFrom(rc); err != nil {
		t.Fatalf("ReadFrom(manifest) error = %v", err)
	}
	committed, err := manifest.Parse(manifestBytes.Bytes())
	if err != nil {
		t.Fatalf("Parse(manifest) error = %v", err)
	}
	if err := committed.Verify(publicKey); err != nil {
		t.Fatalf("Verify(manifest) error = %v", err)
	}
	if committed.Target != "redis-prod" || committed.Driver.Name != "fake" || committed.Stats.ChunkCount != backup.ChunkCount {
		t.Fatalf("manifest = %#v backup=%#v", committed, backup)
	}
}

func TestBackupExecutorReportsUnimplementedDriver(t *testing.T) {
	t.Parallel()

	_, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{9}, 64)))
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	registry := drivers.NewRegistry()
	driver := &executorFakeDriver{}
	if err := registry.Register(driver); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	executor := BackupExecutor{
		Drivers: registry,
		Targets: map[core.ID]drivers.Target{
			"target-1": {Name: "pg", Driver: "postgres"},
		},
		Backends: map[core.ID]storage.Backend{
			"storage-1": storagetest.NewMemoryBackend("memory"),
		},
		PipelineFactory: testPipelineFactory(t),
		PrivateKey:      privateKey,
	}
	_, err = executor.Execute(context.Background(), core.Job{
		ID:        "job-1",
		TargetID:  "target-1",
		StorageID: "storage-1",
		Type:      core.BackupTypeFull,
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want unsupported driver error")
	}
	if !strings.Contains(err.Error(), "not implemented") || !strings.Contains(err.Error(), "registered target drivers: fake") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestBackupExecutorRunsIncrementalBackupWithParent(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{4}, 64)))
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	registry := drivers.NewRegistry()
	driver := &executorFakeDriver{}
	if err := registry.Register(driver); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	backend := storagetest.NewMemoryBackend("memory")
	clock := core.NewFakeClock(time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))
	executor := BackupExecutor{
		Drivers: registry,
		Targets: map[core.ID]drivers.Target{
			"target-1": {Name: "redis-prod", Driver: "fake"},
		},
		Backends: map[core.ID]storage.Backend{
			"storage-1": backend,
		},
		PipelineFactory: testPipelineFactory(t),
		PublicKey:       publicKey,
		PrivateKey:      privateKey,
		Clock:           clock,
	}
	parentResult, err := executor.Execute(context.Background(), core.Job{
		ID: "parent-job", Operation: core.JobOperationBackup, TargetID: "target-1", StorageID: "storage-1", Type: core.BackupTypeFull,
	})
	if err != nil {
		t.Fatalf("Execute(parent) error = %v", err)
	}
	parent := parentResult.Backup
	executor.Backups = map[core.ID]core.Backup{parent.ID: *parent}
	incrResult, err := executor.Execute(context.Background(), core.Job{
		ID: "incr-job", Operation: core.JobOperationBackup, TargetID: "target-1", StorageID: "storage-1", Type: core.BackupTypeIncremental, ParentBackupID: parent.ID,
	})
	if err != nil {
		t.Fatalf("Execute(incremental) error = %v", err)
	}
	incr := incrResult.Backup
	if incr == nil || incr.Type != core.BackupTypeIncremental || incr.ParentID != parent.ID || driver.incrementalParent != parent.ID.String() {
		t.Fatalf("incremental backup=%#v parent=%#v incrementalParent=%q", incr, parent, driver.incrementalParent)
	}
	rc, _, err := backend.Get(context.Background(), string(incr.ManifestID))
	if err != nil {
		t.Fatalf("Get(incremental manifest) error = %v", err)
	}
	defer rc.Close()
	var data bytes.Buffer
	if _, err := data.ReadFrom(rc); err != nil {
		t.Fatalf("ReadFrom(incremental manifest) error = %v", err)
	}
	committed, err := manifest.Parse(data.Bytes())
	if err != nil {
		t.Fatalf("Parse(incremental manifest) error = %v", err)
	}
	if committed.ParentID == nil || *committed.ParentID != parent.ID.String() || committed.Type != core.BackupTypeIncremental {
		t.Fatalf("incremental manifest = %#v", committed)
	}
}

func TestBackupExecutorRunsRestoreJob(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{2}, 64)))
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	registry := drivers.NewRegistry()
	driver := &executorFakeDriver{}
	if err := registry.Register(driver); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	backend := storagetest.NewMemoryBackend("memory")
	clock := core.NewFakeClock(time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))
	executor := BackupExecutor{
		Drivers: registry,
		Targets: map[core.ID]drivers.Target{
			"source-target":  {Name: "redis-prod", Driver: "fake"},
			"restore-target": {Name: "redis-restore", Driver: "fake"},
		},
		Backends: map[core.ID]storage.Backend{
			"storage-1": backend,
		},
		PipelineFactory: testPipelineFactory(t),
		PublicKey:       publicKey,
		PrivateKey:      privateKey,
		Clock:           clock,
	}
	backupResult, err := executor.Execute(context.Background(), core.Job{
		ID: "backup-job", Operation: core.JobOperationBackup, TargetID: "source-target", StorageID: "storage-1", Type: core.BackupTypeFull,
	})
	if err != nil {
		t.Fatalf("Execute(backup) error = %v", err)
	}
	backup := backupResult.Backup
	restored, err := executor.Execute(context.Background(), core.Job{
		ID:                     "restore-job",
		Operation:              core.JobOperationRestore,
		TargetID:               "restore-target",
		StorageID:              "storage-1",
		RestoreBackupID:        backup.ID,
		RestoreManifestID:      backup.ManifestID,
		RestoreTargetID:        "restore-target",
		RestoreDryRun:          true,
		RestoreReplaceExisting: true,
	})
	if err != nil {
		t.Fatalf("Execute(restore) error = %v", err)
	}
	if restored.Backup != nil || restored.Verification != nil || restored.Restore == nil {
		t.Fatalf("restore returned result = %#v, want restore report", restored)
	}
	if restored.Restore.BackupID != backup.ID || restored.Restore.TargetID != "restore-target" || !restored.Restore.DryRun || restored.Restore.Objects != 1 || restored.Restore.Chunks != 1 || restored.Restore.StoredBytes <= 0 || restored.Restore.RestoredBytes <= 0 {
		t.Fatalf("restore report = %#v", restored.Restore)
	}
	if len(driver.restored) != 2 || string(driver.restored[0].Payload) != "alpha" || !driver.restored[1].Done {
		t.Fatalf("restored records = %#v", driver.restored)
	}
	if !driver.restoreOptions.DryRun || !driver.restoreOptions.ReplaceExisting || driver.restoreOptions.Metadata["backup_id"] != backup.ID.String() {
		t.Fatalf("restore options = %#v", driver.restoreOptions)
	}
}

func TestBackupExecutorRunsChunkVerificationJob(t *testing.T) {
	t.Parallel()

	executor, _, backup, _ := executorWithCommittedBackup(t, 6)
	verified, err := executor.Execute(context.Background(), core.Job{
		ID:                "verify-job",
		Operation:         core.JobOperationVerify,
		TargetID:          "target-1",
		StorageID:         "storage-1",
		VerifyBackupID:    backup.ID,
		VerifyManifestID:  backup.ManifestID,
		VerifyManifestIDs: []core.ID{backup.ManifestID},
		VerifyLevel:       core.JobVerificationChunk,
	})
	if err != nil {
		t.Fatalf("Execute(verify) error = %v", err)
	}
	if verified.Backup != nil || verified.Verification == nil || verified.Verification.VerifiedChunks == 0 || verified.Verification.RestoredBytes == 0 {
		t.Fatalf("verify result = %#v, want chunk verification report", verified)
	}
}

func TestBackupExecutorVerificationFailsForMissingChunk(t *testing.T) {
	t.Parallel()

	executor, backend, backup, publicKey := executorWithCommittedBackup(t, 7)
	committed := loadExecutorManifest(t, backend, backup.ManifestID, publicKey)
	chunkKey := committed.Objects[0].Chunks[0].Key
	if err := backend.Delete(context.Background(), chunkKey); err != nil {
		t.Fatalf("Delete(chunk) error = %v", err)
	}

	_, err := executor.Execute(context.Background(), core.Job{
		ID:               "verify-job",
		Operation:        core.JobOperationVerify,
		StorageID:        "storage-1",
		VerifyBackupID:   backup.ID,
		VerifyManifestID: backup.ManifestID,
		VerifyLevel:      core.JobVerificationChunk,
	})
	if err == nil {
		t.Fatal("Execute(verify) error = nil, want missing chunk failure")
	}
	if !strings.Contains(err.Error(), "missing chunks") || !strings.Contains(err.Error(), chunkKey) {
		t.Fatalf("Execute(verify) error = %q, want missing chunk key %q", err, chunkKey)
	}
}

func TestBackupExecutorVerificationFailsForCorruptedChunk(t *testing.T) {
	t.Parallel()

	executor, backend, backup, publicKey := executorWithCommittedBackup(t, 8)
	committed := loadExecutorManifest(t, backend, backup.ManifestID, publicKey)
	chunkKey := committed.Objects[0].Chunks[0].Key
	if err := backend.Delete(context.Background(), chunkKey); err != nil {
		t.Fatalf("Delete(chunk) error = %v", err)
	}
	if _, err := backend.Put(context.Background(), chunkKey, strings.NewReader("not an encrypted chunk envelope"), -1); err != nil {
		t.Fatalf("Put(corrupt chunk) error = %v", err)
	}

	_, err := executor.Execute(context.Background(), core.Job{
		ID:               "verify-job",
		Operation:        core.JobOperationVerify,
		StorageID:        "storage-1",
		VerifyBackupID:   backup.ID,
		VerifyManifestID: backup.ManifestID,
		VerifyLevel:      core.JobVerificationChunk,
	})
	if err == nil {
		t.Fatal("Execute(verify) error = nil, want corrupted chunk failure")
	}
	if !strings.Contains(err.Error(), `verify object "stream" chunks`) || !strings.Contains(err.Error(), "decrypt chunk") {
		t.Fatalf("Execute(verify) error = %q, want chunk decrypt failure", err)
	}
}

func TestBackupExecutorRunsPreBackupShellHook(t *testing.T) {
	t.Parallel()

	executor, _, _, _ := executorWithCommittedBackup(t, 10)
	executor.Backends["storage-1"] = storagetest.NewMemoryBackend("memory")
	hookFile := filepath.Join(t.TempDir(), "pre-hook")
	_, err := executor.Execute(context.Background(), core.Job{
		ID:        "hook-job",
		Operation: core.JobOperationBackup,
		TargetID:  "target-1",
		StorageID: "storage-1",
		Type:      core.BackupTypeFull,
		Hooks: core.JobHooks{PreBackup: []core.JobHookAction{{
			Shell: "printf '%s:%s' \"$KRONOS_HOOK\" \"$KRONOS_JOB_ID\" > " + hookFile,
		}}},
	})
	if err != nil {
		t.Fatalf("Execute(backup with hook) error = %v", err)
	}
	data, err := os.ReadFile(hookFile)
	if err != nil {
		t.Fatalf("ReadFile(hookFile) error = %v", err)
	}
	if string(data) != "pre_backup:hook-job" {
		t.Fatalf("hook output = %q", data)
	}
}

func TestBackupExecutorRunsFailureShellHook(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{11}, 64)))
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	registry := drivers.NewRegistry()
	driver := &executorFakeDriver{backupErr: fmt.Errorf("driver boom")}
	if err := registry.Register(driver); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	backend := storagetest.NewMemoryBackend("memory")
	executor := BackupExecutor{
		Drivers: registry,
		Targets: map[core.ID]drivers.Target{
			"target-1": {Name: "redis-prod", Driver: "fake"},
		},
		Backends: map[core.ID]storage.Backend{
			"storage-1": backend,
		},
		PipelineFactory: testPipelineFactory(t),
		PublicKey:       publicKey,
		PrivateKey:      privateKey,
	}
	hookFile := filepath.Join(t.TempDir(), "failure-hook")
	_, err = executor.Execute(context.Background(), core.Job{
		ID:        "hook-job",
		Operation: core.JobOperationBackup,
		TargetID:  "target-1",
		StorageID: "storage-1",
		Type:      core.BackupTypeFull,
		Hooks: core.JobHooks{OnFailure: []core.JobHookAction{{
			Shell: "printf '%s:%s' \"$KRONOS_HOOK\" \"$KRONOS_JOB_ERROR\" > " + hookFile,
		}}},
	})
	if err == nil {
		t.Fatal("Execute(backup) error = nil, want driver failure")
	}
	if !strings.Contains(err.Error(), "driver boom") {
		t.Fatalf("Execute(backup) error = %v, want driver failure", err)
	}
	data, readErr := os.ReadFile(hookFile)
	if readErr != nil {
		t.Fatalf("ReadFile(hookFile) error = %v", readErr)
	}
	if !strings.Contains(string(data), "on_failure:") || !strings.Contains(string(data), "driver boom") {
		t.Fatalf("failure hook output = %q", data)
	}
}

func TestBackupExecutorRunsWebhookHook(t *testing.T) {
	t.Parallel()

	var payload map[string]any
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode(webhook) error = %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer webhook.Close()

	executor, _, _, _ := executorWithCommittedBackup(t, 12)
	executor.Backends["storage-1"] = storagetest.NewMemoryBackend("memory")
	_, err := executor.Execute(context.Background(), core.Job{
		ID:        "webhook-job",
		Operation: core.JobOperationBackup,
		TargetID:  "target-1",
		StorageID: "storage-1",
		Type:      core.BackupTypeFull,
		Hooks: core.JobHooks{PreBackup: []core.JobHookAction{{
			WebhookURL: webhook.URL,
		}}},
	})
	if err != nil {
		t.Fatalf("Execute(backup with webhook hook) error = %v", err)
	}
	if payload["hook"] != "pre_backup" || payload["job_id"] != "webhook-job" {
		t.Fatalf("webhook payload = %#v", payload)
	}
}

func TestBackupExecutorRestoresManifestChain(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{3}, 64)))
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	registry := drivers.NewRegistry()
	driver := &executorFakeDriver{}
	if err := registry.Register(driver); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	backend := storagetest.NewMemoryBackend("memory")
	clock := core.NewFakeClock(time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))
	executor := BackupExecutor{
		Drivers: registry,
		Targets: map[core.ID]drivers.Target{
			"source-target":  {Name: "redis-prod", Driver: "fake"},
			"restore-target": {Name: "redis-restore", Driver: "fake"},
		},
		Backends: map[core.ID]storage.Backend{
			"storage-1": backend,
		},
		PipelineFactory: testPipelineFactory(t),
		PublicKey:       publicKey,
		PrivateKey:      privateKey,
		Clock:           clock,
	}
	fullResult, err := executor.Execute(context.Background(), core.Job{
		ID: "full-job", Operation: core.JobOperationBackup, TargetID: "source-target", StorageID: "storage-1", Type: core.BackupTypeFull,
	})
	if err != nil {
		t.Fatalf("Execute(full) error = %v", err)
	}
	full := fullResult.Backup
	driver.payload = "bravo"
	incrResult, err := executor.Execute(context.Background(), core.Job{
		ID: "incr-job", Operation: core.JobOperationBackup, TargetID: "source-target", StorageID: "storage-1", Type: core.BackupTypeFull,
	})
	if err != nil {
		t.Fatalf("Execute(incr fixture) error = %v", err)
	}
	incr := incrResult.Backup
	driver.restored = nil
	restored, err := executor.Execute(context.Background(), core.Job{
		ID:                     "restore-job",
		Operation:              core.JobOperationRestore,
		TargetID:               "restore-target",
		StorageID:              "storage-1",
		RestoreBackupID:        incr.ID,
		RestoreManifestID:      incr.ManifestID,
		RestoreManifestIDs:     []core.ID{full.ManifestID, incr.ManifestID},
		RestoreTargetID:        "restore-target",
		RestoreDryRun:          true,
		RestoreReplaceExisting: true,
	})
	if err != nil {
		t.Fatalf("Execute(chain restore) error = %v", err)
	}
	if restored.Restore == nil || restored.Restore.Objects != 2 || restored.Restore.Chunks != 2 || len(restored.Restore.ManifestIDs) != 2 {
		t.Fatalf("chain restore report = %#v", restored.Restore)
	}
	if len(driver.restored) != 4 || string(driver.restored[0].Payload) != "alpha" || string(driver.restored[2].Payload) != "bravo" {
		t.Fatalf("restored chain records = %#v", driver.restored)
	}
}

func TestBackupExecutorValidatesConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := (BackupExecutor{}).Execute(context.Background(), core.Job{Type: core.BackupTypeFull}); err == nil {
		t.Fatal("Execute(empty) error = nil, want error")
	}
}

func TestBackupExecutorSyncResources(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	storageRoot := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/targets":
			writeTestJSON(t, w, targetsResponse{Targets: []core.Target{{
				ID:       "target-1",
				Name:     "redis",
				Driver:   core.TargetDriverRedis,
				Endpoint: "127.0.0.1:6379",
				Database: "0",
				Options:  map[string]any{"tls": "disable", "username": "backup", "password": "${env:KRONOS_TEST_TARGET_PASSWORD}"},
			}}})
		case "/api/v1/storages":
			writeTestJSON(t, w, storagesResponse{Storages: []core.Storage{{
				ID:   "storage-1",
				Name: "repo",
				Kind: core.StorageKindLocal,
				URI:  "file://" + storageRoot,
				Options: map[string]any{
					"secret_key": "${env:KRONOS_TEST_S3_SECRET}",
				},
			}}})
		case "/api/v1/backups":
			writeTestJSON(t, w, backupsResponse{Backups: []core.Backup{{
				ID: "backup-1", TargetID: "target-1", StorageID: "storage-1", Type: core.BackupTypeFull, ManifestID: "manifest-1", EndedAt: now,
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	executor := &BackupExecutor{SecretResolver: secret.ResolverFunc(func(ctx context.Context, ref secret.Reference) (string, error) {
		switch ref.Path {
		case "KRONOS_TEST_TARGET_PASSWORD":
			return "secret", nil
		case "KRONOS_TEST_S3_SECRET":
			return "s3-secret", nil
		default:
			return "", core.WrapKind(core.ErrorKindNotFound, "lookup env", fmt.Errorf("environment variable %q is not set", ref.Path))
		}
	})}
	if err := executor.SyncResources(context.Background(), client); err != nil {
		t.Fatalf("SyncResources() error = %v", err)
	}
	target := executor.Targets["target-1"]
	if target.Name != "redis" || target.Driver != "redis" || target.Connection["addr"] != "127.0.0.1:6379" || target.Connection["database"] != "0" || target.Connection["username"] != "backup" || target.Connection["password"] != "secret" || target.Options["tls"] != "disable" {
		t.Fatalf("synced target = %#v", target)
	}
	if target.Options["password"] != "secret" {
		t.Fatalf("target options password = %q, want resolved secret", target.Options["password"])
	}
	if backend := executor.Backends["storage-1"]; backend == nil || backend.Name() != "repo" {
		t.Fatalf("synced backend = %#v", backend)
	}
	if backup := executor.Backups["backup-1"]; backup.ID != "backup-1" || backup.ManifestID != "manifest-1" {
		t.Fatalf("synced backup = %#v", backup)
	}
}

func TestBackupExecutorSyncResourcesRejectsMissingSecretReference(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/targets":
			writeTestJSON(t, w, targetsResponse{Targets: []core.Target{{
				ID: "target-1", Name: "redis", Driver: core.TargetDriverRedis, Endpoint: "127.0.0.1:6379",
				Options: map[string]any{"password": "${env:KRONOS_MISSING_SECRET}"},
			}}})
		case "/api/v1/storages":
			writeTestJSON(t, w, storagesResponse{Storages: nil})
		case "/api/v1/backups":
			writeTestJSON(t, w, backupsResponse{Backups: nil})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	err = (&BackupExecutor{}).SyncResources(context.Background(), client)
	if err == nil || !strings.Contains(err.Error(), "target target-1 secrets") || !strings.Contains(err.Error(), "KRONOS_MISSING_SECRET") {
		t.Fatalf("SyncResources() error = %v, want missing secret context", err)
	}
}

func TestOpenStorageBackendRejectsUnsupportedKind(t *testing.T) {
	t.Parallel()

	_, err := OpenStorageBackend(core.Storage{
		ID:   "storage-1",
		Name: "repo",
		Kind: core.StorageKind("webdav"),
		URI:  "webdav://example.com/repo",
	})
	if err == nil {
		t.Fatal("OpenStorageBackend() error = nil, want unsupported kind error")
	}
	if !strings.Contains(err.Error(), "not implemented") || !strings.Contains(err.Error(), "local, s3, sftp, azure, gcs") {
		t.Fatalf("OpenStorageBackend() error = %v, want implemented supported list", err)
	}
}

func TestExecutorStorageAndOptionHelpers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	backend, err := OpenStorageBackend(core.Storage{Name: "local", Kind: core.StorageKindLocal, URI: "file://" + root})
	if err != nil {
		t.Fatalf("OpenStorageBackend(local) error = %v", err)
	}
	if backend.Name() != "local" {
		t.Fatalf("local backend name = %q", backend.Name())
	}
	if _, err := localStorageRoot(""); err == nil {
		t.Fatal("localStorageRoot(empty) error = nil, want error")
	}
	if _, err := localStorageRoot("s3://bucket"); err == nil {
		t.Fatal("localStorageRoot(s3) error = nil, want error")
	}
	if got, err := localStorageRoot("relative/path"); err != nil || got != "relative/path" {
		t.Fatalf("localStorageRoot(relative) = %q, %v", got, err)
	}
	if got, err := localStorageRoot("file:opaque-root"); err != nil || got != "opaque-root" {
		t.Fatalf("localStorageRoot(opaque) = %q, %v", got, err)
	}

	if got, err := s3Bucket("s3:/path-bucket/rest"); err != nil || got != "path-bucket" {
		t.Fatalf("s3Bucket(path) = %q, %v", got, err)
	}
	if _, err := s3Bucket("http://bucket"); err == nil {
		t.Fatal("s3Bucket(http) error = nil, want error")
	}
	if _, err := s3Bucket("s3:///"); err == nil {
		t.Fatal("s3Bucket(empty) error = nil, want error")
	}

	target := targetConnection(core.Target{
		Endpoint: "127.0.0.1:6379",
		Database: "0",
		Options:  map[string]any{"user": "fallback", "tls": "true", "port": 6379},
	})
	if target["addr"] != "127.0.0.1:6379" || target["database"] != "0" || target["username"] != "fallback" || target["port"] != "6379" {
		t.Fatalf("targetConnection() = %#v", target)
	}
	if got := optionString(map[string]any{"flag": true, "n": 42.0}, "missing", "flag"); got != "true" {
		t.Fatalf("optionString(bool) = %q", got)
	}
	if !optionBool(map[string]any{"enabled": "true"}, "enabled") {
		t.Fatal("optionBool(string true) = false, want true")
	}
	if optionBool(map[string]any{"enabled": "nope"}, "enabled") {
		t.Fatal("optionBool(invalid string) = true, want false")
	}
}

func TestExecutorPublicKeyAndRefsHelpers(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{8}, 64)))
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	if got, err := (BackupExecutor{PublicKey: publicKey}).publicKey(); err != nil || !bytes.Equal(got, publicKey) {
		t.Fatalf("publicKey(explicit) = %x, %v", got, err)
	}
	if got, err := (BackupExecutor{PrivateKey: privateKey}).publicKey(); err != nil || !bytes.Equal(got, publicKey) {
		t.Fatalf("publicKey(derived) = %x, %v", got, err)
	}
	if _, err := (BackupExecutor{}).publicKey(); err == nil {
		t.Fatal("publicKey(missing) error = nil, want error")
	}
	if refs, err := manifestRefs(manifest.Manifest{}); err != nil || refs != nil {
		t.Fatalf("manifestRefs(empty) = %#v, %v; want nil, nil", refs, err)
	}
	if got := stringPtrFromID(""); got != nil {
		t.Fatalf("stringPtrFromID(empty) = %#v, want nil", got)
	}
	if got := stringPtrFromID("backup-1"); got == nil || *got != "backup-1" {
		t.Fatalf("stringPtrFromID(value) = %#v", got)
	}
}

func TestS3StorageConfigParsesCredentials(t *testing.T) {
	t.Parallel()

	cfg, err := s3StorageConfig(core.Storage{
		ID:   "storage-1",
		Name: "repo",
		Kind: core.StorageKindS3,
		URI:  "s3://bucket",
		Options: map[string]any{
			"region":           "eu-north-1",
			"endpoint":         "https://s3.example.com",
			"force_path_style": true,
			"credentials":      `{"access_key":"access","secret_key":"secret","session_token":"token"}`,
		},
	})
	if err != nil {
		t.Fatalf("s3StorageConfig() error = %v", err)
	}
	if cfg.Bucket != "bucket" || cfg.Region != "eu-north-1" || cfg.Endpoint != "https://s3.example.com" || !cfg.ForcePathStyle {
		t.Fatalf("config = %#v", cfg)
	}
	if cfg.Credentials.AccessKey != "access" || cfg.Credentials.SecretKey != "secret" || cfg.Credentials.SessionToken != "token" {
		t.Fatalf("credentials = %#v", cfg.Credentials)
	}
}

func TestS3StorageConfigRejectsUnknownCredentialMode(t *testing.T) {
	t.Parallel()

	_, err := s3StorageConfig(core.Storage{
		ID:      "storage-1",
		Name:    "repo",
		Kind:    core.StorageKindS3,
		URI:     "s3://bucket",
		Options: map[string]any{"region": "eu-north-1", "credentials": "vault-ref"},
	})
	if err == nil {
		t.Fatal("s3StorageConfig() error = nil, want unknown credential mode error")
	}
}

func TestSFTPStorageConfigParsesURIAndOptions(t *testing.T) {
	t.Parallel()

	cfg, err := sftpStorageConfig(core.Storage{
		ID:   "storage-1",
		Name: "repo",
		Kind: core.StorageKindSFTP,
		URI:  "sftp://uri-user:uri-pass@example.com:2222/backups",
		Options: map[string]any{
			"username":                 "option-user",
			"password":                 "option-pass",
			"private_key_path":         "/run/keys/sftp",
			"known_hosts":              "/run/known_hosts",
			"insecure_ignore_host_key": true,
		},
	})
	if err != nil {
		t.Fatalf("sftpStorageConfig() error = %v", err)
	}
	if cfg.Name != "repo" || cfg.Address != "example.com:2222" || cfg.Root != "/backups" {
		t.Fatalf("sftp config location = %#v", cfg)
	}
	if cfg.Username != "option-user" || cfg.Password != "option-pass" || cfg.PrivateKeyPath != "/run/keys/sftp" {
		t.Fatalf("sftp config auth = %#v", cfg)
	}
	if cfg.KnownHostsPath != "/run/known_hosts" || !cfg.InsecureIgnoreHostKey {
		t.Fatalf("sftp config host key = %#v", cfg)
	}
}

func TestAzureStorageConfigParsesURIAndOptions(t *testing.T) {
	t.Parallel()

	cfg, err := azureStorageConfig(core.Storage{
		ID:   "storage-1",
		Name: "repo",
		Kind: core.StorageKindAzure,
		URI:  "azure://acct/container/path/prefix",
		Options: map[string]any{
			"endpoint":    "http://127.0.0.1:10000/acct",
			"account_key": "key",
			"sas_token":   "sv=test&sig=fake",
		},
	})
	if err != nil {
		t.Fatalf("azureStorageConfig() error = %v", err)
	}
	if cfg.Name != "repo" || cfg.AccountName != "acct" || cfg.Container != "container" || cfg.Prefix != "path/prefix" {
		t.Fatalf("azure config location = %#v", cfg)
	}
	if cfg.Endpoint != "http://127.0.0.1:10000/acct" || cfg.AccountKey != "key" || cfg.SASToken != "sv=test&sig=fake" {
		t.Fatalf("azure config auth = %#v", cfg)
	}
}

func TestGCSStorageConfigParsesURIAndOptions(t *testing.T) {
	t.Parallel()

	cfg, err := gcsStorageConfig(core.Storage{
		ID:   "storage-1",
		Name: "repo",
		Kind: core.StorageKindGCS,
		URI:  "gcs://bucket/path/prefix",
		Options: map[string]any{
			"endpoint":     "http://127.0.0.1:4443",
			"bearer_token": "token",
			"api_key":      "api-key",
		},
	})
	if err != nil {
		t.Fatalf("gcsStorageConfig() error = %v", err)
	}
	if cfg.Name != "repo" || cfg.Bucket != "bucket" || cfg.Prefix != "path/prefix" {
		t.Fatalf("gcs config location = %#v", cfg)
	}
	if cfg.Endpoint != "http://127.0.0.1:4443" || cfg.BearerToken != "token" || cfg.APIKey != "api-key" {
		t.Fatalf("gcs config auth = %#v", cfg)
	}
}

func testPipelineFactory(t *testing.T) PipelineFactory {
	t.Helper()
	return func(backend storage.Backend) (*chunk.Pipeline, error) {
		chunker, err := chunk.NewFastCDC(64, 128, 512)
		if err != nil {
			return nil, err
		}
		compressor, err := kcompress.New(kcompress.AlgorithmNone)
		if err != nil {
			return nil, err
		}
		cipher, err := kcrypto.NewAES256GCM(bytes.Repeat([]byte{9}, 32))
		if err != nil {
			return nil, err
		}
		return &chunk.Pipeline{
			Chunker:     chunker,
			Compressor:  compressor,
			Cipher:      cipher,
			KeyID:       "agent-test-key",
			Backend:     backend,
			Concurrency: 2,
		}, nil
	}
}

func executorWithCommittedBackup(t *testing.T, seed byte) (BackupExecutor, *storagetest.MemoryBackend, *core.Backup, ed25519.PublicKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{seed}, 64)))
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	registry := drivers.NewRegistry()
	driver := &executorFakeDriver{}
	if err := registry.Register(driver); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	backend := storagetest.NewMemoryBackend("memory")
	executor := BackupExecutor{
		Drivers: registry,
		Targets: map[core.ID]drivers.Target{
			"target-1": {Name: "redis-prod", Driver: "fake"},
		},
		Backends: map[core.ID]storage.Backend{
			"storage-1": backend,
		},
		PipelineFactory: testPipelineFactory(t),
		PublicKey:       publicKey,
		PrivateKey:      privateKey,
		Clock:           core.NewFakeClock(time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)),
	}
	backupResult, err := executor.Execute(context.Background(), core.Job{
		ID: "backup-job", Operation: core.JobOperationBackup, TargetID: "target-1", StorageID: "storage-1", Type: core.BackupTypeFull,
	})
	if err != nil {
		t.Fatalf("Execute(backup) error = %v", err)
	}
	backup := backupResult.Backup
	if backup == nil {
		t.Fatal("Execute(backup) returned nil backup")
	}
	executor.Backups = map[core.ID]core.Backup{backup.ID: *backup}
	return executor, backend, backup, publicKey
}

func loadExecutorManifest(t *testing.T, backend storage.Backend, manifestID core.ID, publicKey ed25519.PublicKey) manifest.Manifest {
	t.Helper()
	rc, _, err := backend.Get(context.Background(), string(manifestID))
	if err != nil {
		t.Fatalf("Get(manifest) error = %v", err)
	}
	defer rc.Close()
	var manifestBytes bytes.Buffer
	if _, err := manifestBytes.ReadFrom(rc); err != nil {
		t.Fatalf("ReadFrom(manifest) error = %v", err)
	}
	committed, err := manifest.Parse(manifestBytes.Bytes())
	if err != nil {
		t.Fatalf("Parse(manifest) error = %v", err)
	}
	if err := committed.Verify(publicKey); err != nil {
		t.Fatalf("Verify(manifest) error = %v", err)
	}
	if len(committed.Objects) == 0 || len(committed.Objects[0].Chunks) == 0 {
		t.Fatalf("manifest has no chunk references: %#v", committed)
	}
	return committed
}

type executorFakeDriver struct {
	restored          []drivers.Record
	backupErr         error
	payload           string
	incrementalParent string
	restoreOptions    drivers.RestoreOptions
}

func (*executorFakeDriver) Name() string { return "fake" }

func (*executorFakeDriver) Version(context.Context, drivers.Target) (string, error) { return "1", nil }

func (*executorFakeDriver) Test(context.Context, drivers.Target) error { return nil }

func (d *executorFakeDriver) BackupFull(ctx context.Context, target drivers.Target, w drivers.RecordWriter) (drivers.ResumePoint, error) {
	if d.backupErr != nil {
		return drivers.ResumePoint{}, d.backupErr
	}
	obj := drivers.ObjectRef{Name: "keys", Kind: "stream"}
	payload := d.payload
	if payload == "" {
		payload = "alpha"
	}
	if err := w.WriteRecord(obj, []byte(payload)); err != nil {
		return drivers.ResumePoint{}, err
	}
	if err := w.FinishObject(obj, 1); err != nil {
		return drivers.ResumePoint{}, err
	}
	return drivers.ResumePoint{Driver: "fake", Position: "done"}, nil
}

func (d *executorFakeDriver) BackupIncremental(ctx context.Context, target drivers.Target, parent manifest.Manifest, w drivers.RecordWriter) (drivers.ResumePoint, error) {
	d.incrementalParent = parent.BackupID
	obj := drivers.ObjectRef{Name: "keys", Kind: "stream"}
	if err := w.WriteRecord(obj, []byte("delta")); err != nil {
		return drivers.ResumePoint{}, err
	}
	if err := w.FinishObject(obj, 1); err != nil {
		return drivers.ResumePoint{}, err
	}
	return drivers.ResumePoint{Driver: "fake", Position: "incremental"}, nil
}

func (*executorFakeDriver) Stream(context.Context, drivers.Target, drivers.ResumePoint, drivers.StreamWriter) error {
	return nil
}

func (d *executorFakeDriver) Restore(ctx context.Context, target drivers.Target, r drivers.RecordReader, opts drivers.RestoreOptions) error {
	d.restoreOptions = opts
	for {
		record, err := r.NextRecord()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		d.restored = append(d.restored, record)
	}
}

func (*executorFakeDriver) ReplayStream(context.Context, drivers.Target, drivers.StreamReader, drivers.ReplayTarget) error {
	return nil
}

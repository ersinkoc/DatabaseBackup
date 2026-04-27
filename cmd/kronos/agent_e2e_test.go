//go:build e2e

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/kvstore"
	control "github.com/kronos/kronos/internal/server"
)

func TestE2EWorkerBacksUpRedisThroughControlPlane(t *testing.T) {
	redisServer := startE2ERedisServer(t)
	repoDir := t.TempDir()
	stores := newE2EControlPlaneStores(t, redisServer.endpoint, repoDir)

	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	_, privateKey := newE2EKeys(t)
	job := enqueueE2EBackup(t, server, "target-redis", "storage-local")
	done, cancel := startE2EWorker(t, server, privateKey, "agent-e2e")

	backup := waitForE2EBackup(t, stores.backups, stores.jobs, done, job.ID)
	cancel()
	expectE2EWorkerCanceled(t, done)
	finished, ok, err := stores.jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("Get(job) error = %v", err)
	}
	if !ok || finished.Status != core.JobStatusSucceeded {
		t.Fatalf("finished job ok=%v job=%#v", ok, finished)
	}
	if backup.TargetID != "target-redis" || backup.StorageID != "storage-local" || backup.ManifestID.IsZero() || backup.ChunkCount == 0 {
		t.Fatalf("backup = %#v", backup)
	}
	if len(redisServer.restores()) != 0 {
		t.Fatalf("restore commands during backup = %#v", redisServer.restores())
	}
}

func TestE2EWorkerRestoresRedisThroughControlPlane(t *testing.T) {
	redisServer := startE2ERedisServer(t)
	repoDir := t.TempDir()
	stores := newE2EControlPlaneStores(t, redisServer.endpoint, repoDir)
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	_, privateKey := newE2EKeys(t)
	backupJob := enqueueE2EBackup(t, server, "target-redis", "storage-local")
	done, cancel := startE2EWorker(t, server, privateKey, "agent-e2e")
	backup := waitForE2EBackup(t, stores.backups, stores.jobs, done, backupJob.ID)
	cancel()
	expectE2EWorkerCanceled(t, done)

	restoreJob := enqueueE2ERestore(t, server, backup.ID, "target-redis")
	done, cancel = startE2EWorker(t, server, privateKey, "agent-e2e")
	waitForE2EJobStatus(t, stores.jobs, done, restoreJob.ID, core.JobStatusSucceeded)
	cancel()
	expectE2EWorkerCanceled(t, done)

	restores := redisServer.restores()
	if len(restores) != 1 {
		t.Fatalf("RESTORE commands = %#v, want one command", restores)
	}
	want := []string{"RESTORE", "user:1", "0", "dump-user-1", "REPLACE"}
	if fmt.Sprint(restores[0]) != fmt.Sprint(want) {
		t.Fatalf("RESTORE command = %#v, want %#v", restores[0], want)
	}
	finished, ok, err := stores.jobs.Get(restoreJob.ID)
	if err != nil {
		t.Fatalf("Get(restore job) error = %v", err)
	}
	if !ok || finished.Operation != core.JobOperationRestore || finished.Status != core.JobStatusSucceeded {
		t.Fatalf("finished restore ok=%v job=%#v", ok, finished)
	}
}

func TestE2ERetentionApplyPrunesBackupMetadata(t *testing.T) {
	stores := newE2EAPIStores(t)
	server := httptest.NewServer(newServerHandlerWithStores(nil, nil, stores))
	defer server.Close()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	backups := []core.Backup{
		{
			ID:         "backup-old",
			TargetID:   "target-redis",
			StorageID:  "storage-local",
			JobID:      "job-old",
			Type:       core.BackupTypeFull,
			ManifestID: "manifest-old",
			StartedAt:  now.Add(-3 * time.Hour),
			EndedAt:    now.Add(-2 * time.Hour),
		},
		{
			ID:         "backup-new",
			TargetID:   "target-redis",
			StorageID:  "storage-local",
			JobID:      "job-new",
			Type:       core.BackupTypeFull,
			ManifestID: "manifest-new",
			StartedAt:  now.Add(-1 * time.Hour),
			EndedAt:    now,
		},
	}
	for _, backup := range backups {
		if err := stores.backups.Save(backup); err != nil {
			t.Fatalf("Save(backup %s) error = %v", backup.ID, err)
		}
	}

	dryRun := applyE2ERetention(t, server, true)
	if !dryRun.DryRun {
		t.Fatalf("dry-run response = %#v", dryRun)
	}
	assertE2EDeletedBackups(t, dryRun.Deleted, "backup-old")
	if _, ok, err := stores.backups.Get("backup-old"); err != nil || !ok {
		t.Fatalf("dry-run backup-old exists=%v error=%v", ok, err)
	}

	applied := applyE2ERetention(t, server, false)
	if applied.DryRun {
		t.Fatalf("apply response = %#v", applied)
	}
	assertE2EDeletedBackups(t, applied.Deleted, "backup-old")
	if _, ok, err := stores.backups.Get("backup-old"); err != nil || ok {
		t.Fatalf("backup-old exists=%v error=%v, want deleted", ok, err)
	}
	if _, ok, err := stores.backups.Get("backup-new"); err != nil || !ok {
		t.Fatalf("backup-new exists=%v error=%v, want kept", ok, err)
	}

	events, err := stores.audit.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List(audit) error = %v", err)
	}
	for _, event := range events {
		if event.Action == "retention.applied" {
			return
		}
	}
	t.Fatalf("retention.applied audit event not found in %#v", events)
}

func TestE2EClaimFailsLostAgentJobAndUnblocksTarget(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	registry := control.NewAgentRegistry(func() time.Time { return now }, time.Minute)
	stores := newE2EAPIStores(t)
	for _, job := range []core.Job{
		{
			ID:        "running-lost",
			AgentID:   "agent-lost",
			TargetID:  "target-redis",
			StorageID: "storage-local",
			Type:      core.BackupTypeFull,
			Status:    core.JobStatusRunning,
			QueuedAt:  now.Add(-3 * time.Minute),
			StartedAt: now.Add(-2 * time.Minute),
		},
		{
			ID:        "queued-next",
			TargetID:  "target-redis",
			StorageID: "storage-local",
			Type:      core.BackupTypeFull,
			Status:    core.JobStatusQueued,
			QueuedAt:  now.Add(-1 * time.Minute),
		},
	} {
		if err := stores.jobs.Save(job); err != nil {
			t.Fatalf("Save(job %s) error = %v", job.ID, err)
		}
	}
	server := httptest.NewServer(newServerHandlerWithStores(nil, registry, stores))
	defer server.Close()

	postE2EHeartbeat(t, server, control.AgentHeartbeat{ID: "agent-lost", Capacity: 1, Now: now.Add(-2 * time.Minute)})
	postE2EHeartbeat(t, server, control.AgentHeartbeat{ID: "agent-ok", Capacity: 1, Now: now})

	claimed := claimE2EJob(t, server, "agent-ok")
	if claimed.Job == nil || claimed.Job.ID != "queued-next" || claimed.Job.Status != core.JobStatusRunning || claimed.Job.AgentID != "agent-ok" {
		t.Fatalf("claimed job = %#v", claimed.Job)
	}
	lost, ok, err := stores.jobs.Get("running-lost")
	if err != nil || !ok || lost.Status != core.JobStatusFailed || lost.Error != "agent_lost" || lost.EndedAt.IsZero() {
		t.Fatalf("Get(running-lost) = %#v ok=%v err=%v", lost, ok, err)
	}
	next, ok, err := stores.jobs.Get("queued-next")
	if err != nil || !ok || next.Status != core.JobStatusRunning || next.AgentID != "agent-ok" {
		t.Fatalf("Get(queued-next) = %#v ok=%v err=%v", next, ok, err)
	}
	events, err := stores.audit.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List(audit) error = %v", err)
	}
	if len(events) != 2 || events[0].Action != "agent_lost.jobs_failed" || events[1].Action != "job.claimed" {
		t.Fatalf("agent recovery audit events = %#v", events)
	}
	if fmt.Sprint(events[0].Metadata["job_count"]) != "1" {
		t.Fatalf("agent_lost.jobs_failed metadata = %#v", events[0].Metadata)
	}
}

func enqueueE2EBackup(t *testing.T, server *httptest.Server, targetID, storageID core.ID) core.Job {
	t.Helper()

	payload := fmt.Sprintf(`{"target_id":%q,"storage_id":%q,"type":"full"}`, targetID, storageID)
	resp, err := server.Client().Post(server.URL+"/api/v1/backups/now", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /api/v1/backups/now error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/v1/backups/now status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var job core.Job
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		t.Fatalf("Decode(job) error = %v", err)
	}
	if job.ID.IsZero() {
		t.Fatal("queued job id is empty")
	}
	return job
}

func enqueueE2ERestore(t *testing.T, server *httptest.Server, backupID, targetID core.ID) core.Job {
	t.Helper()

	payload := fmt.Sprintf(`{"backup_id":%q,"target_id":%q,"replace_existing":true}`, backupID, targetID)
	resp, err := server.Client().Post(server.URL+"/api/v1/restore", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /api/v1/restore error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/v1/restore status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var response restoreStartResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("Decode(restore response) error = %v", err)
	}
	if response.Job.ID.IsZero() || response.Job.Operation != core.JobOperationRestore {
		t.Fatalf("restore response = %#v", response)
	}
	return response.Job
}

func postE2EHeartbeat(t *testing.T, server *httptest.Server, heartbeat control.AgentHeartbeat) {
	t.Helper()

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(heartbeat); err != nil {
		t.Fatalf("Encode(heartbeat) error = %v", err)
	}
	resp, err := server.Client().Post(server.URL+"/api/v1/agents/heartbeat", "application/json", &body)
	if err != nil {
		t.Fatalf("POST /api/v1/agents/heartbeat error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/v1/agents/heartbeat status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
}

func claimE2EJob(t *testing.T, server *httptest.Server, agentID string) claimJobResponse {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/jobs/claim", nil)
	if err != nil {
		t.Fatalf("NewRequest(claim) error = %v", err)
	}
	req.Header.Set("X-Kronos-Agent-ID", agentID)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/jobs/claim error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/v1/jobs/claim status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var response claimJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("Decode(claim response) error = %v", err)
	}
	return response
}

func applyE2ERetention(t *testing.T, server *httptest.Server, dryRun bool) retentionApplyResponse {
	t.Helper()

	payload := fmt.Sprintf(`{"dry_run":%t,"now":"2026-04-25T12:00:00Z","policy":{"rules":[{"kind":"count","params":{"n":1}}]}}`, dryRun)
	resp, err := server.Client().Post(server.URL+"/api/v1/retention/apply", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /api/v1/retention/apply error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/v1/retention/apply status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var response retentionApplyResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("Decode(retention apply response) error = %v", err)
	}
	return response
}

func assertE2EDeletedBackups(t *testing.T, got []core.ID, want core.ID) {
	t.Helper()

	if len(got) != 1 || got[0] != want {
		t.Fatalf("deleted backups = %#v, want [%s]", got, want)
	}
}

func waitForE2EBackup(t *testing.T, backups *control.BackupStore, jobs *control.JobStore, done <-chan error, jobID core.ID) core.Backup {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		items, err := backups.List()
		if err != nil {
			t.Fatalf("List(backups) error = %v", err)
		}
		for _, backup := range items {
			if backup.JobID == jobID {
				return backup
			}
		}
		select {
		case err := <-done:
			job, ok, getErr := jobs.Get(jobID)
			if getErr != nil {
				t.Fatalf("worker exited with %v; Get(job) error = %v", err, getErr)
			}
			t.Fatalf("worker exited before backup was saved: err=%v job_ok=%v job=%#v", err, ok, job)
		case <-time.After(10 * time.Millisecond):
		}
	}
	job, ok, err := jobs.Get(jobID)
	if err != nil {
		t.Fatalf("timed out waiting for backup for job %s; Get(job) error = %v", jobID, err)
	}
	t.Fatalf("timed out waiting for backup for job %s; job_ok=%v job=%#v", jobID, ok, job)
	return core.Backup{}
}

func waitForE2EJobStatus(t *testing.T, jobs *control.JobStore, done <-chan error, jobID core.ID, status core.JobStatus) core.Job {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, ok, err := jobs.Get(jobID)
		if err != nil {
			t.Fatalf("Get(job) error = %v", err)
		}
		if ok && job.Status == status {
			return job
		}
		select {
		case err := <-done:
			t.Fatalf("worker exited before job %s reached %s: err=%v job_ok=%v job=%#v", jobID, status, err, ok, job)
		case <-time.After(10 * time.Millisecond):
		}
	}
	job, ok, err := jobs.Get(jobID)
	if err != nil {
		t.Fatalf("timed out waiting for job %s status %s; Get(job) error = %v", jobID, status, err)
	}
	t.Fatalf("timed out waiting for job %s status %s; job_ok=%v job=%#v", jobID, status, ok, job)
	return core.Job{}
}

func newE2EControlPlaneStores(t *testing.T, redisEndpoint string, repoDir string) apiStores {
	t.Helper()

	stores := newE2EAPIStores(t)
	if err := stores.targets.Save(core.Target{
		ID:        "target-redis",
		Name:      "redis-e2e",
		Driver:    core.TargetDriverRedis,
		Endpoint:  redisEndpoint,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save(target) error = %v", err)
	}
	if err := stores.storages.Save(core.Storage{
		ID:        "storage-local",
		Name:      "local-e2e",
		Kind:      core.StorageKindLocal,
		URI:       "file://" + repoDir,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save(storage) error = %v", err)
	}
	return stores
}

func newE2EAPIStores(t *testing.T) apiStores {
	t.Helper()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stores, err := newAPIStores(db)
	if err != nil {
		t.Fatalf("newAPIStores() error = %v", err)
	}
	return stores
}

func newE2EKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{3}, 64)))
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return publicKey, privateKey
}

func startE2EWorker(t *testing.T, server *httptest.Server, privateKey ed25519.PrivateKey, agentID string) (<-chan error, context.CancelFunc) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runAgentWorkerWithToken(ctx, server.Client(), server.URL, control.AgentHeartbeat{ID: agentID, Capacity: 1}, 5*time.Millisecond, "", agentWorkerOptions{
			ManifestPrivateKeyHex: hex.EncodeToString(privateKey),
			ChunkKeyHex:           hex.EncodeToString(bytes.Repeat([]byte{8}, 32)),
			ChunkAlgorithm:        "aes-256-gcm",
			Compression:           "none",
			KeyID:                 "e2e-key",
		})
	}()
	return done, cancel
}

func expectE2EWorkerCanceled(t *testing.T, done <-chan error) {
	t.Helper()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("worker error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for worker cancellation")
	}
}

type e2eRedisServer struct {
	endpoint string
	restored chan []string
}

func (s *e2eRedisServer) restores() [][]string {
	var out [][]string
	for {
		select {
		case command := <-s.restored:
			out = append(out, command)
		default:
			return out
		}
	}
}

func startE2ERedisServer(t *testing.T) *e2eRedisServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	server := &e2eRedisServer{endpoint: listener.Addr().String(), restored: make(chan []string, 16)}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleE2ERedisConn(t, conn, server)
		}
	}()
	return server
}

func handleE2ERedisConn(t *testing.T, conn net.Conn, server *e2eRedisServer) {
	t.Helper()
	defer conn.Close()

	reader := bufio.NewReader(conn)
	for {
		command, err := readE2ERedisCommand(reader)
		if err != nil {
			if err != io.EOF {
				t.Errorf("readE2ERedisCommand() error = %v", err)
			}
			return
		}
		if len(command) == 0 {
			continue
		}
		switch strings.ToUpper(command[0]) {
		case "HELLO":
			writeE2ERedisRaw(t, conn, "*2\r\n$6\r\nserver\r\n$5\r\nredis\r\n")
		case "ACL":
			writeE2ERedisArray(t, conn, "user default on nopass ~* &* +@all")
		case "SCAN":
			writeE2ERedisRaw(t, conn, "*2\r\n$1\r\n0\r\n*1\r\n$6\r\nuser:1\r\n")
		case "TYPE":
			writeE2ERedisRaw(t, conn, "+string\r\n")
		case "PTTL":
			writeE2ERedisRaw(t, conn, ":0\r\n")
		case "DUMP":
			writeE2ERedisBulk(t, conn, "dump-user-1")
		case "INFO":
			writeE2ERedisBulk(t, conn, "# Server\r\nredis_version:7.2.0\r\n")
		case "RESTORE":
			server.restored <- append([]string(nil), command...)
			writeE2ERedisRaw(t, conn, "+OK\r\n")
		default:
			writeE2ERedisRaw(t, conn, fmt.Sprintf("-ERR unsupported command %s\r\n", command[0]))
			return
		}
	}
}

func readE2ERedisCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("RESP array expected, got %q", line)
	}
	count, err := strconv.Atoi(strings.TrimPrefix(line, "*"))
	if err != nil {
		return nil, err
	}
	command := make([]string, 0, count)
	for i := 0; i < count; i++ {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		header = strings.TrimSuffix(header, "\r\n")
		if !strings.HasPrefix(header, "$") {
			return nil, fmt.Errorf("RESP bulk expected, got %q", header)
		}
		size, err := strconv.Atoi(strings.TrimPrefix(header, "$"))
		if err != nil {
			return nil, err
		}
		data := make([]byte, size+2)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		if string(data[size:]) != "\r\n" {
			return nil, fmt.Errorf("RESP bulk missing CRLF terminator")
		}
		command = append(command, string(data[:size]))
	}
	return command, nil
}

func writeE2ERedisBulk(t *testing.T, conn net.Conn, value string) {
	t.Helper()
	writeE2ERedisRaw(t, conn, fmt.Sprintf("$%d\r\n%s\r\n", len(value), value))
}

func writeE2ERedisArray(t *testing.T, conn net.Conn, values ...string) {
	t.Helper()
	writeE2ERedisRaw(t, conn, fmt.Sprintf("*%d\r\n", len(values)))
	for _, value := range values {
		writeE2ERedisBulk(t, conn, value)
	}
}

func writeE2ERedisRaw(t *testing.T, conn net.Conn, value string) {
	t.Helper()
	if _, err := fmt.Fprint(conn, value); err != nil {
		t.Errorf("Write(%q) error = %v", value, err)
	}
}

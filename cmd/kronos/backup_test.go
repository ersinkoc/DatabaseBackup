package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
	"github.com/kronos/kronos/internal/manifest"
	"github.com/kronos/kronos/internal/obs"
	"github.com/kronos/kronos/internal/storage/local"
)

func TestRunBackupVerify(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	backend, err := local.New("local", repo)
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}
	if _, err := backend.Put(context.Background(), "data/aa/bb/hash", bytes.NewReader([]byte("payload")), 7); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	m := manifest.New()
	m.BackupID = "backup-1"
	m.Target = "target"
	m.Driver = manifest.Driver{Name: "postgres", Version: "17"}
	m.Type = core.BackupTypeFull
	m.StartedAt = time.Now().UTC()
	m.FinishedAt = m.StartedAt
	m.Encryption = manifest.Encryption{Algorithm: "aes-256-gcm", KeyID: "k1"}
	m.Objects = []manifest.Object{{
		Schema: "public",
		Name:   "users",
		Chunks: []manifest.ChunkRef{{Hash: "abc", Key: "data/aa/bb/hash", Size: 7, StoredSize: 7}},
	}}
	m.Stats = manifest.Stats{LogicalSizeBytes: 7, StoredSizeBytes: 7, ChunkCount: 1}
	if err := m.Sign(privateKey); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var out bytes.Buffer
	err = run(context.Background(), &out, []string{
		"backup", "verify",
		"--manifest", manifestPath,
		"--public-key", hex.EncodeToString(publicKey),
		"--storage-local", repo,
	})
	if err != nil {
		t.Fatalf("backup verify error = %v", err)
	}
	if !strings.Contains(out.String(), `"ok":true`) || !strings.Contains(out.String(), `"level":"manifest"`) || !strings.Contains(out.String(), `"objects":1`) || !strings.Contains(out.String(), `"chunks":1`) {
		t.Fatalf("backup verify output = %q", out.String())
	}
}

func TestRunBackupVerifyManifestKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	backend, err := local.New("local", repo)
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}
	if _, err := backend.Put(context.Background(), "data/aa/bb/hash", bytes.NewReader([]byte("payload")), 7); err != nil {
		t.Fatalf("Put(chunk) error = %v", err)
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	m := manifest.New()
	m.BackupID = "backup-1"
	m.Target = "target"
	m.Driver = manifest.Driver{Name: "redis", Version: "7.2"}
	m.Type = core.BackupTypeFull
	m.StartedAt = time.Now().UTC()
	m.FinishedAt = m.StartedAt
	m.Encryption = manifest.Encryption{Algorithm: "aes-256-gcm", KeyID: "k1"}
	m.Objects = []manifest.Object{{
		Name:   "stream",
		Chunks: []manifest.ChunkRef{{Hash: "abc", Key: "data/aa/bb/hash", Size: 7, StoredSize: 7}},
	}}
	m.Stats = manifest.Stats{LogicalSizeBytes: 7, StoredSizeBytes: 7, ChunkCount: 1}
	if err := m.Sign(privateKey); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	key := "manifests/2026/04/23/backup-1.manifest"
	if _, err := backend.Put(context.Background(), key, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("Put(manifest) error = %v", err)
	}

	var out bytes.Buffer
	err = run(context.Background(), &out, []string{
		"backup", "verify",
		"--manifest-key", key,
		"--public-key", hex.EncodeToString(publicKey),
		"--storage-local", repo,
	})
	if err != nil {
		t.Fatalf("backup verify --manifest-key error = %v", err)
	}
	if !strings.Contains(out.String(), `"ok":true`) || !strings.Contains(out.String(), `"level":"manifest"`) {
		t.Fatalf("backup verify --manifest-key output = %q", out.String())
	}
}

func TestRunBackupVerifyChunkLevel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	backend, err := local.New("local", repo)
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}
	chunkKey := bytes.Repeat([]byte{8}, 32)
	cipher, err := kcrypto.NewAES256GCM(chunkKey)
	if err != nil {
		t.Fatalf("NewAES256GCM() error = %v", err)
	}
	compressor, err := kcompress.New(kcompress.AlgorithmNone)
	if err != nil {
		t.Fatalf("compress.New() error = %v", err)
	}
	chunker, err := chunk.NewFastCDC(4, 8, 16)
	if err != nil {
		t.Fatalf("NewFastCDC() error = %v", err)
	}
	pipeline := &chunk.Pipeline{
		Chunker:     chunker,
		Compressor:  compressor,
		Cipher:      cipher,
		KeyID:       "k1",
		Backend:     backend,
		Concurrency: 2,
	}
	input := []byte("chunk verification through the CLI")
	refs, _, err := pipeline.Feed(context.Background(), bytes.NewReader(input))
	if err != nil {
		t.Fatalf("Feed() error = %v", err)
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	chunks := make([]manifest.ChunkRef, 0, len(refs))
	for _, ref := range refs {
		chunks = append(chunks, manifest.ChunkFromPipeline(ref))
	}
	m := manifest.New()
	m.BackupID = "backup-1"
	m.Target = "target"
	m.Driver = manifest.Driver{Name: "redis", Version: "7.2"}
	m.Type = core.BackupTypeFull
	m.StartedAt = time.Now().UTC()
	m.FinishedAt = m.StartedAt
	m.Encryption = manifest.Encryption{Algorithm: "aes-256-gcm", KeyID: "k1"}
	m.Objects = []manifest.Object{{Name: "stream", Chunks: chunks}}
	if err := m.Sign(privateKey); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var out bytes.Buffer
	err = run(context.Background(), &out, []string{
		"backup", "verify",
		"--manifest", manifestPath,
		"--level", "chunk",
		"--chunk-key", hex.EncodeToString(chunkKey),
		"--public-key", hex.EncodeToString(publicKey),
		"--storage-local", repo,
	})
	if err != nil {
		t.Fatalf("backup verify --level chunk error = %v", err)
	}
	if !strings.Contains(out.String(), `"ok":true`) || !strings.Contains(out.String(), `"level":"chunk"`) || !strings.Contains(out.String(), `"verified_chunks":`) {
		t.Fatalf("backup verify --level chunk output = %q", out.String())
	}
}

func TestRunBackupNowAndList(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/backups/now":
			if r.Method != http.MethodPost {
				t.Fatalf("backup now method = %s", r.Method)
			}
			defer r.Body.Close()
			var body bytes.Buffer
			if _, err := body.ReadFrom(r.Body); err != nil {
				t.Fatalf("ReadFrom(request) error = %v", err)
			}
			text := body.String()
			if !strings.Contains(text, `"target_id":"target-1"`) ||
				!strings.Contains(text, `"storage_id":"storage-1"`) ||
				!strings.Contains(text, `"type":"incr"`) ||
				!strings.Contains(text, `"parent_id":"backup-parent"`) {
				t.Fatalf("backup now request = %q", text)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"job-1","status":"queued"}`)
		case "/api/v1/backups":
			if r.Method != http.MethodGet {
				t.Fatalf("backup list method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"backups":[{"id":"backup-1"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{
		"backup", "now", "--server", server.URL, "--target", "target-1", "--storage", "storage-1", "--parent", "backup-parent",
	}); err != nil {
		t.Fatalf("backup now error = %v", err)
	}
	if !strings.Contains(out.String(), `"id":"job-1"`) || !strings.Contains(out.String(), `"status":"queued"`) {
		t.Fatalf("backup now output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"backup", "list", "--server", server.URL}); err != nil {
		t.Fatalf("backup list error = %v", err)
	}
	if !strings.Contains(out.String(), `"backups":[{"id":"backup-1"}]`) {
		t.Fatalf("backup list output = %q", out.String())
	}
}

func TestRunBackupNowUsesGlobalToken(t *testing.T) {
	t.Parallel()

	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"job-1","status":"queued"}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{
		"--token", "secret-token", "backup", "now", "--server", server.URL, "--target", "target-1", "--storage", "storage-1",
	}); err != nil {
		t.Fatalf("backup now error = %v", err)
	}
	if auth != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", auth)
	}
}

func TestRunBackupListUsesGlobalServer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/backups" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"backups":[{"id":"backup-1"}]}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"--server", server.URL, "backup", "list"}); err != nil {
		t.Fatalf("backup list error = %v", err)
	}
	if !strings.Contains(out.String(), `"backup-1"`) {
		t.Fatalf("backup list output = %q", out.String())
	}
}

func TestRunBackupListPassesFilters(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/backups" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("target_id") != "target-1" || query.Get("storage_id") != "storage-1" || query.Get("type") != "full" || query.Get("since") != "7d" || query.Get("until") != "2026-04-25T12:00:00Z" || query.Get("protected") != "true" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"backups":[{"id":"backup-1"}]}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{
		"backup", "list",
		"--server", server.URL,
		"--target", "target-1",
		"--storage", "storage-1",
		"--type", "full",
		"--since", "7d",
		"--until", "2026-04-25T12:00:00Z",
		"--protected", "true",
	}); err != nil {
		t.Fatalf("backup list filters error = %v", err)
	}
	if !strings.Contains(out.String(), `"backup-1"`) {
		t.Fatalf("backup list filters output = %q", out.String())
	}
}

func TestRunControlRequestPrettyOutput(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"backups":[{"id":"backup-1"}]}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"--server", server.URL, "--output", "pretty", "backup", "list"}); err != nil {
		t.Fatalf("backup list pretty error = %v", err)
	}
	if !strings.Contains(out.String(), "{\n  \"backups\"") || !strings.Contains(out.String(), "    {\n      \"id\": \"backup-1\"") {
		t.Fatalf("pretty output = %q", out.String())
	}
}

func TestRunControlRequestYAMLOutput(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"backups":[{"id":"backup-1","protected":true}]}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"--server", server.URL, "--output", "yaml", "backup", "list"}); err != nil {
		t.Fatalf("backup list yaml error = %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "backups:") || !strings.Contains(text, "id: backup-1") || !strings.Contains(text, "protected: true") {
		t.Fatalf("yaml output = %q", text)
	}
}

func TestRunControlRequestTableOutput(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"backups":[{"id":"backup-1","protected":true},{"id":"backup-2","protected":false}]}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"--server", server.URL, "--output", "table", "backup", "list"}); err != nil {
		t.Fatalf("backup list table error = %v", err)
	}
	text := out.String()
	for _, want := range []string{"ID", "PROTECTED", "backup-1", "true", "backup-2", "false"} {
		if !strings.Contains(text, want) {
			t.Fatalf("table output missing %q: %q", want, text)
		}
	}
}

func TestRunBackupInspectProtectUnprotect(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/backups/backup-1":
			if r.Method != http.MethodGet {
				t.Fatalf("backup inspect method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"backup-1","protected":false}`)
		case "/api/v1/backups/backup-1/protect":
			if r.Method != http.MethodPost {
				t.Fatalf("backup protect method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"backup-1","protected":true}`)
		case "/api/v1/backups/backup-1/unprotect":
			if r.Method != http.MethodPost {
				t.Fatalf("backup unprotect method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"backup-1","protected":false}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"backup", "inspect", "--server", server.URL, "--id", "backup-1"}); err != nil {
		t.Fatalf("backup inspect error = %v", err)
	}
	if !strings.Contains(out.String(), `"id":"backup-1"`) {
		t.Fatalf("backup inspect output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"backup", "protect", "--server", server.URL, "--id", "backup-1"}); err != nil {
		t.Fatalf("backup protect error = %v", err)
	}
	if !strings.Contains(out.String(), `"protected":true`) {
		t.Fatalf("backup protect output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"backup", "unprotect", "--server", server.URL, "--id", "backup-1"}); err != nil {
		t.Fatalf("backup unprotect error = %v", err)
	}
	if !strings.Contains(out.String(), `"protected":false`) {
		t.Fatalf("backup unprotect output = %q", out.String())
	}
}

func TestControlRequestsUseKronosTokenEnv(t *testing.T) {
	t.Setenv("KRONOS_TOKEN", "env-secret")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer env-secret" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := getControlJSON(context.Background(), server.Client(), server.URL, "/api/v1/anything", &out); err != nil {
		t.Fatalf("getControlJSON() error = %v", err)
	}
	if !strings.Contains(out.String(), `"ok":true`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestControlRequestsSendRequestID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(obs.RequestIDHeader); got != "req-cli-1" {
			t.Fatalf("%s = %q, want req-cli-1", obs.RequestIDHeader, got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	ctx := obs.WithRequestID(context.Background(), "req-cli-1")
	if err := getControlJSON(ctx, server.Client(), server.URL, "/api/v1/anything", &out); err != nil {
		t.Fatalf("getControlJSON() error = %v", err)
	}
	if !strings.Contains(out.String(), `"ok":true`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunGlobalRequestIDFlag(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(obs.RequestIDHeader); got != "req-global-1" {
			t.Fatalf("%s = %q, want req-global-1", obs.RequestIDHeader, got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"backups":[]}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), &out, []string{"backup", "list", "--server", server.URL, "--request-id", "req-global-1"})
	if err != nil {
		t.Fatalf("backup list with --request-id error = %v", err)
	}
}

func TestControlRequestErrorIncludesRequestID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(obs.RequestIDHeader, "req-error-1")
		http.Error(w, "bad thing", http.StatusBadGateway)
	}))
	defer server.Close()

	var out bytes.Buffer
	err := getControlJSON(context.Background(), server.Client(), server.URL, "/api/v1/anything", &out)
	if err == nil {
		t.Fatal("getControlJSON() error = nil, want error")
	}
	if text := err.Error(); !strings.Contains(text, "request_id=req-error-1") || !strings.Contains(text, "bad thing") {
		t.Fatalf("getControlJSON() error = %q", text)
	}
}

func TestRunBackupNowRequiresTargetAndStorage(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"backup", "now", "--target", "target-1"}); err == nil {
		t.Fatal("backup now without storage error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"backup", "now", "--storage", "storage-1"}); err == nil {
		t.Fatal("backup now without target error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"backup", "inspect"}); err == nil {
		t.Fatal("backup inspect without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"backup", "protect"}); err == nil {
		t.Fatal("backup protect without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"backup", "list", "--since", "1h", "--until", "2h"}); err == nil {
		t.Fatal("backup list inverted time range error = nil, want error")
	}
}

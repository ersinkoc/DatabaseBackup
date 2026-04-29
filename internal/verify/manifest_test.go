package verify

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/kronos/kronos/internal/chunk"
	kcompress "github.com/kronos/kronos/internal/compress"
	"github.com/kronos/kronos/internal/core"
	kcrypto "github.com/kronos/kronos/internal/crypto"
	"github.com/kronos/kronos/internal/manifest"
	"github.com/kronos/kronos/internal/storage/storagetest"
)

func TestManifestVerification(t *testing.T) {
	t.Parallel()

	backend := storagetest.NewMemoryBackend("memory")
	if _, err := backend.Put(context.Background(), "data/aa/bb/hash", bytes.NewReader([]byte("payload")), 7); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	m := signedManifest(t, privateKey, "data/aa/bb/hash")

	report, err := Manifest(context.Background(), backend, m, publicKey)
	if err != nil {
		t.Fatalf("Manifest() error = %v", err)
	}
	if report.Objects != 1 || report.Chunks != 1 || report.StoredBytes != 7 {
		t.Fatalf("report = %#v", report)
	}
}

func TestManifestVerificationRejectsMissingChunk(t *testing.T) {
	t.Parallel()

	backend := storagetest.NewMemoryBackend("memory")
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	m := signedManifest(t, privateKey, "data/missing")

	report, err := Manifest(context.Background(), backend, m, publicKey)
	if err == nil {
		t.Fatal("Manifest() error = nil, want missing chunk")
	}
	if !strings.Contains(err.Error(), "data/missing") {
		t.Fatalf("Manifest() error = %q, want missing chunk key", err)
	}
	if len(report.MissingChunks) != 1 || report.MissingChunks[0] != "data/missing" {
		t.Fatalf("MissingChunks = %v", report.MissingChunks)
	}
}

func TestManifestVerificationRejectsBadSignature(t *testing.T) {
	t.Parallel()

	backend := storagetest.NewMemoryBackend("memory")
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey(second) error = %v", err)
	}
	m := signedManifest(t, privateKey, "data/aa/bb/hash")

	if _, err := Manifest(context.Background(), backend, m, publicKey); err == nil {
		t.Fatal("Manifest() error = nil, want signature error")
	}
}

func TestChunksVerificationRestoresAndHashes(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	pipeline := testPipeline(t)
	input := []byte("verify chunk integrity end to end")
	refs, _, err := pipeline.Feed(context.Background(), bytes.NewReader(input))
	if err != nil {
		t.Fatalf("Feed() error = %v", err)
	}
	m := signedManifestFromRefs(t, privateKey, refs)

	report, err := Chunks(context.Background(), pipeline, m, publicKey)
	if err != nil {
		t.Fatalf("Chunks() error = %v", err)
	}
	if report.VerifiedChunks != len(refs) || report.RestoredBytes != int64(len(input)) {
		t.Fatalf("chunk report = %#v, want %d chunks and %d bytes", report, len(refs), len(input))
	}
}

func TestChunksVerificationRejectsCorruptHash(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	pipeline := testPipeline(t)
	refs, _, err := pipeline.Feed(context.Background(), bytes.NewReader([]byte("verify me")))
	if err != nil {
		t.Fatalf("Feed() error = %v", err)
	}
	m := signedManifestFromRefs(t, privateKey, refs)
	m.Objects[0].Chunks[0].Hash = chunk.HashBytes([]byte("wrong")).String()
	m.Signature = nil
	if err := m.Sign(privateKey); err != nil {
		t.Fatalf("Sign(tampered) error = %v", err)
	}

	if _, err := Chunks(context.Background(), pipeline, m, publicKey); err == nil {
		t.Fatal("Chunks() error = nil, want corrupt hash error")
	}
}

func signedManifest(t *testing.T, privateKey ed25519.PrivateKey, key string) manifest.Manifest {
	t.Helper()
	now := time.Date(2026, 4, 23, 2, 0, 0, 0, time.UTC)
	m := manifest.New()
	m.BackupID = "backup-1"
	m.Target = "target"
	m.Driver = manifest.Driver{Name: "postgres", Version: "17"}
	m.Type = core.BackupTypeFull
	m.StartedAt = now
	m.FinishedAt = now
	m.Encryption = manifest.Encryption{Algorithm: "aes-256-gcm", KeyID: "k1"}
	m.Objects = []manifest.Object{{
		Schema: "public",
		Name:   "users",
		Chunks: []manifest.ChunkRef{{Hash: "abc", Key: key, Size: 7, StoredSize: 7}},
	}}
	m.Stats = manifest.Stats{LogicalSizeBytes: 7, StoredSizeBytes: 7, ChunkCount: 1}
	if err := m.Sign(privateKey); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return m
}

func signedManifestFromRefs(t *testing.T, privateKey ed25519.PrivateKey, refs []chunk.ChunkRef) manifest.Manifest {
	t.Helper()
	now := time.Date(2026, 4, 23, 2, 0, 0, 0, time.UTC)
	chunks := make([]manifest.ChunkRef, 0, len(refs))
	for _, ref := range refs {
		chunks = append(chunks, manifest.ChunkFromPipeline(ref))
	}
	m := manifest.New()
	m.BackupID = "backup-1"
	m.Target = "target"
	m.Driver = manifest.Driver{Name: "redis", Version: "7.2"}
	m.Type = core.BackupTypeFull
	m.StartedAt = now
	m.FinishedAt = now
	m.Encryption = manifest.Encryption{Algorithm: "aes-256-gcm", KeyID: "k1"}
	m.Objects = []manifest.Object{{Name: "stream", Chunks: chunks}}
	if err := m.Sign(privateKey); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return m
}

func testPipeline(t *testing.T) *chunk.Pipeline {
	t.Helper()
	cipher, err := kcrypto.NewAES256GCM(bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatalf("NewAES256GCM() error = %v", err)
	}
	chunker, err := chunk.NewFastCDC(4, 8, 16)
	if err != nil {
		t.Fatalf("NewFastCDC() error = %v", err)
	}
	compressor, err := kcompress.New(kcompress.AlgorithmNone)
	if err != nil {
		t.Fatalf("compress.New() error = %v", err)
	}
	return &chunk.Pipeline{
		Chunker:     chunker,
		Compressor:  compressor,
		Cipher:      cipher,
		KeyID:       "k1",
		Backend:     storagetest.NewMemoryBackend("memory"),
		Concurrency: 2,
	}
}

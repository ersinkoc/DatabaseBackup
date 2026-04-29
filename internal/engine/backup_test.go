package engine

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/kronos/kronos/internal/chunk"
	kcompress "github.com/kronos/kronos/internal/compress"
	kcrypto "github.com/kronos/kronos/internal/crypto"
	"github.com/kronos/kronos/internal/drivers"
	"github.com/kronos/kronos/internal/manifest"
	"github.com/kronos/kronos/internal/storage/storagetest"
)

func TestBackupFullAndRestoreRoundTrip(t *testing.T) {
	t.Parallel()

	compressor, err := kcompress.New(kcompress.AlgorithmNone)
	if err != nil {
		t.Fatalf("compress.New() error = %v", err)
	}
	cipher, err := kcrypto.NewAES256GCM(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatalf("NewAES256GCM() error = %v", err)
	}
	pipeline := &chunk.Pipeline{
		Chunker:     mustEngineChunker(t),
		Compressor:  compressor,
		Cipher:      cipher,
		KeyID:       "engine-key",
		Backend:     storagetest.NewMemoryBackend("memory"),
		Concurrency: 2,
	}
	driver := &fakeEngineDriver{}
	result, err := BackupFull(context.Background(), driver, drivers.Target{Name: "target"}, pipeline)
	if err != nil {
		t.Fatalf("BackupFull() error = %v", err)
	}
	if result.ResumePoint.Driver != "fake" || len(result.Chunks) == 0 || result.Stats.Chunks == 0 {
		t.Fatalf("result = %#v", result)
	}
	stats, err := Restore(context.Background(), driver, drivers.Target{Name: "target"}, pipeline, result.Chunks, drivers.RestoreOptions{})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if stats.Chunks != len(result.Chunks) || len(driver.restored) != 2 {
		t.Fatalf("restore stats=%#v restored=%#v", stats, driver.restored)
	}
	if string(driver.restored[0].Payload) != "row-1" || !driver.restored[1].Done {
		t.Fatalf("restored records = %#v", driver.restored)
	}
}

func TestBackupFullAndRestoreRoundTripLargeRecord(t *testing.T) {
	t.Parallel()

	compressor, err := kcompress.New(kcompress.AlgorithmNone)
	if err != nil {
		t.Fatalf("compress.New() error = %v", err)
	}
	cipher, err := kcrypto.NewAES256GCM(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatalf("NewAES256GCM() error = %v", err)
	}
	pipeline := &chunk.Pipeline{
		Chunker:     mustEngineChunker(t),
		Compressor:  compressor,
		Cipher:      cipher,
		KeyID:       "engine-key",
		Backend:     storagetest.NewMemoryBackend("memory"),
		Concurrency: 2,
	}
	payload := bytes.Repeat([]byte("x"), 128*1024)
	driver := &fakeEngineDriver{fullPayload: payload}
	result, err := BackupFull(context.Background(), driver, drivers.Target{Name: "target"}, pipeline)
	if err != nil {
		t.Fatalf("BackupFull() error = %v", err)
	}
	if _, err := Restore(context.Background(), driver, drivers.Target{Name: "target"}, pipeline, result.Chunks, drivers.RestoreOptions{}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if len(driver.restored) == 0 {
		t.Fatal("restored records are empty")
	}
	if !bytes.Equal(driver.restored[0].Payload, payload) {
		t.Fatalf("restored first payload length = %d, want %d", len(driver.restored[0].Payload), len(payload))
	}
}

func TestBackupIncremental(t *testing.T) {
	t.Parallel()

	compressor, err := kcompress.New(kcompress.AlgorithmNone)
	if err != nil {
		t.Fatalf("compress.New() error = %v", err)
	}
	cipher, err := kcrypto.NewAES256GCM(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("NewAES256GCM() error = %v", err)
	}
	pipeline := &chunk.Pipeline{
		Chunker:     mustEngineChunker(t),
		Compressor:  compressor,
		Cipher:      cipher,
		KeyID:       "engine-key",
		Backend:     storagetest.NewMemoryBackend("memory"),
		Concurrency: 2,
	}
	driver := &fakeEngineDriver{}
	parent := manifest.Manifest{BackupID: "parent-backup"}
	result, err := BackupIncremental(context.Background(), driver, drivers.Target{Name: "target"}, parent, pipeline)
	if err != nil {
		t.Fatalf("BackupIncremental() error = %v", err)
	}
	if result.ResumePoint.Position != "incremental" || len(result.Chunks) == 0 || driver.incrementalParent != "parent-backup" {
		t.Fatalf("result=%#v incrementalParent=%q", result, driver.incrementalParent)
	}
}

func TestBackupIncrementalFallsBackToFull(t *testing.T) {
	t.Parallel()

	compressor, err := kcompress.New(kcompress.AlgorithmNone)
	if err != nil {
		t.Fatalf("compress.New() error = %v", err)
	}
	cipher, err := kcrypto.NewAES256GCM(bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatalf("NewAES256GCM() error = %v", err)
	}
	pipeline := &chunk.Pipeline{
		Chunker:     mustEngineChunker(t),
		Compressor:  compressor,
		Cipher:      cipher,
		KeyID:       "engine-key",
		Backend:     storagetest.NewMemoryBackend("memory"),
		Concurrency: 2,
	}
	driver := &fakeEngineDriver{incrementalUnsupported: true}
	parent := manifest.Manifest{BackupID: "parent-backup"}
	result, err := BackupIncremental(context.Background(), driver, drivers.Target{Name: "target"}, parent, pipeline)
	if err != nil {
		t.Fatalf("BackupIncremental() error = %v", err)
	}
	if result.ResumePoint.Position != "done" || len(result.Chunks) == 0 {
		t.Fatalf("result=%#v", result)
	}
}

func mustEngineChunker(t *testing.T) *chunk.FastCDC {
	t.Helper()
	chunker, err := chunk.NewFastCDC(64, 128, 512)
	if err != nil {
		t.Fatalf("NewFastCDC() error = %v", err)
	}
	return chunker
}

type fakeEngineDriver struct {
	restored               []drivers.Record
	incrementalParent      string
	incrementalUnsupported bool
	fullPayload            []byte
}

func (*fakeEngineDriver) Name() string { return "fake" }

func (*fakeEngineDriver) Version(context.Context, drivers.Target) (string, error) { return "1", nil }

func (*fakeEngineDriver) Test(context.Context, drivers.Target) error { return nil }

func (d *fakeEngineDriver) BackupFull(ctx context.Context, target drivers.Target, w drivers.RecordWriter) (drivers.ResumePoint, error) {
	obj := drivers.ObjectRef{Name: "object", Kind: "table"}
	payload := []byte("row-1")
	if len(d.fullPayload) > 0 {
		payload = d.fullPayload
	}
	if err := w.WriteRecord(obj, payload); err != nil {
		return drivers.ResumePoint{}, err
	}
	if err := w.FinishObject(obj, 1); err != nil {
		return drivers.ResumePoint{}, err
	}
	return drivers.ResumePoint{Driver: "fake", Position: "done"}, nil
}

func (d *fakeEngineDriver) BackupIncremental(ctx context.Context, target drivers.Target, parent manifest.Manifest, w drivers.RecordWriter) (drivers.ResumePoint, error) {
	if d.incrementalUnsupported {
		return drivers.ResumePoint{}, drivers.ErrIncrementalUnsupported
	}
	d.incrementalParent = parent.BackupID
	obj := drivers.ObjectRef{Name: "object", Kind: "table"}
	if err := w.WriteRecord(obj, []byte("delta-1")); err != nil {
		return drivers.ResumePoint{}, err
	}
	if err := w.FinishObject(obj, 1); err != nil {
		return drivers.ResumePoint{}, err
	}
	return drivers.ResumePoint{Driver: "fake", Position: "incremental"}, nil
}

func (*fakeEngineDriver) Stream(context.Context, drivers.Target, drivers.ResumePoint, drivers.StreamWriter) error {
	return nil
}

func (d *fakeEngineDriver) Restore(ctx context.Context, target drivers.Target, r drivers.RecordReader, opts drivers.RestoreOptions) error {
	for {
		record, err := r.NextRecord()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		d.restored = append(d.restored, record)
	}
}

func (*fakeEngineDriver) ReplayStream(context.Context, drivers.Target, drivers.StreamReader, drivers.ReplayTarget) error {
	return nil
}

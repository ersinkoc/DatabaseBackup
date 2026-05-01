package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/storage"
	"github.com/kronos/kronos/internal/storage/storagetest"
)

func TestBackendImplementsStorageBackend(t *testing.T) {
	t.Parallel()

	var _ storage.Backend = (*Backend)(nil)
}

func TestBackendConformance(t *testing.T) {
	t.Parallel()

	storagetest.RunBackendConformance(t, func(t *testing.T) storage.Backend {
		t.Helper()
		return newTestBackend(t)
	}, storagetest.ConformanceOptions{InvalidKeyErrors: true})
}

func TestPutGetHeadExistsDeleteList(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := newTestBackend(t)
	if got := backend.Name(); got != "test-local" {
		t.Fatalf("Name() = %q", got)
	}
	payload := []byte("time devours; kronos preserves")

	info, err := backend.Put(ctx, "data/aa/object", bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if info.Size != int64(len(payload)) || info.ETag == "" {
		t.Fatalf("Put() info = %#v", info)
	}

	exists, err := backend.Exists(ctx, "data/aa/object")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if !exists {
		t.Fatal("Exists() = false, want true")
	}

	head, err := backend.Head(ctx, "data/aa/object")
	if err != nil {
		t.Fatalf("Head() error = %v", err)
	}
	if head.Size != info.Size || head.ETag != info.ETag {
		t.Fatalf("Head() = %#v, want size/etag from %#v", head, info)
	}

	rc, gotInfo, err := backend.Get(ctx, "data/aa/object")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer rc.Close()
	var got bytes.Buffer
	if _, err := got.ReadFrom(rc); err != nil {
		t.Fatalf("ReadFrom(Get()) error = %v", err)
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatalf("Get() payload = %q, want %q", got.Bytes(), payload)
	}
	if gotInfo.ETag != info.ETag {
		t.Fatalf("Get() etag = %q, want %q", gotInfo.ETag, info.ETag)
	}

	page, err := backend.List(ctx, "data/", "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "data/aa/object" {
		t.Fatalf("List() = %#v", page)
	}

	if err := backend.Delete(ctx, "data/aa/object"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	exists, err = backend.Exists(ctx, "data/aa/object")
	if err != nil {
		t.Fatalf("Exists(after delete) error = %v", err)
	}
	if exists {
		t.Fatal("Exists(after delete) = true, want false")
	}
}

func TestGetRange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := newTestBackend(t)
	if _, err := backend.Put(ctx, "ranges/object", bytes.NewReader([]byte("abcdef")), 6); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	rc, err := backend.GetRange(ctx, "ranges/object", 2, 3)
	if err != nil {
		t.Fatalf("GetRange() error = %v", err)
	}
	defer rc.Close()

	var got bytes.Buffer
	if _, err := got.ReadFrom(rc); err != nil {
		t.Fatalf("ReadFrom(GetRange()) error = %v", err)
	}
	if got.String() != "cde" {
		t.Fatalf("GetRange() = %q, want cde", got.String())
	}
}

func TestOperationsRespectCanceledContextAndMissingObjects(t *testing.T) {
	t.Parallel()

	backend := newTestBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Put(ctx, "key", bytes.NewReader([]byte("x")), 1); err == nil {
		t.Fatal("Put(canceled) error = nil, want error")
	}
	if _, _, err := backend.Get(ctx, "key"); err == nil {
		t.Fatal("Get(canceled) error = nil, want error")
	}
	if _, err := backend.GetRange(ctx, "key", 0, 1); err == nil {
		t.Fatal("GetRange(canceled) error = nil, want error")
	}
	if _, err := backend.Head(ctx, "key"); err == nil {
		t.Fatal("Head(canceled) error = nil, want error")
	}
	if err := backend.Delete(ctx, "key"); err == nil {
		t.Fatal("Delete(canceled) error = nil, want error")
	}
	if _, err := backend.List(ctx, "", ""); err == nil {
		t.Fatal("List(canceled) error = nil, want error")
	}
	if _, _, err := backend.Get(context.Background(), "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Get(missing) error = %v, want not found", err)
	}
	if _, err := backend.Head(context.Background(), "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Head(missing) error = %v, want not found", err)
	}
	if _, err := backend.GetRange(context.Background(), "missing", 0, 1); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetRange(missing) error = %v, want not found", err)
	}
}

func TestPutRejectsInvalidKeys(t *testing.T) {
	t.Parallel()

	backend := newTestBackend(t)
	for _, key := range []string{"", ".", "../escape", "a/../escape", "/absolute", "back\\slash"} {
		key := key
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			_, err := backend.Put(context.Background(), key, bytes.NewReader([]byte("x")), 1)
			var invalid storage.InvalidKeyError
			if !errors.As(err, &invalid) {
				t.Fatalf("Put(%q) error = %v, want InvalidKeyError", key, err)
			}
		})
	}
}

func TestPutDuplicateFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := newTestBackend(t)
	if _, err := backend.Put(ctx, "objects/one", bytes.NewReader([]byte("first")), 5); err != nil {
		t.Fatalf("Put(first) error = %v", err)
	}

	_, err := backend.Put(ctx, "objects/one", bytes.NewReader([]byte("second")), 6)
	if !errors.Is(err, core.ErrConflict) {
		t.Fatalf("Put(duplicate) error = %v, want ErrConflict", err)
	}
}

func TestConstructorAndOperationErrors(t *testing.T) {
	t.Parallel()

	if _, err := New("", ""); err == nil {
		t.Fatal("New(empty root) error = nil, want error")
	}
	backend, err := New("", t.TempDir())
	if err != nil {
		t.Fatalf("New(default name) error = %v", err)
	}
	if backend.Name() != "local" {
		t.Fatalf("Name() = %q, want local", backend.Name())
	}
	if _, err := backend.Put(context.Background(), "objects/short", bytes.NewReader([]byte("abc")), 4); err == nil {
		t.Fatal("Put(size mismatch) error = nil, want error")
	}
	if _, err := backend.Put(context.Background(), "objects/read-error", errReader{}, -1); err == nil {
		t.Fatal("Put(read error) error = nil, want error")
	}
	if _, err := backend.GetRange(context.Background(), "objects/missing", -1, 1); err == nil {
		t.Fatal("GetRange(negative offset) error = nil, want error")
	}
	if err := backend.Delete(context.Background(), "objects/missing"); err != nil {
		t.Fatalf("Delete(missing) error = %v", err)
	}
	if got := hashFile(backend.root + "/missing"); got != "" {
		t.Fatalf("hashFile(missing) = %q, want empty", got)
	}
}

func TestListSkipsTemporaryFilesAndPaginates(t *testing.T) {
	t.Parallel()

	backend := newTestBackend(t)
	for _, key := range []string{"data/a", "data/b.lock", "data/c.tmp"} {
		path, err := backend.pathForKey(key)
		if err != nil {
			t.Fatalf("pathForKey(%q) error = %v", key, err)
		}
		if err := os.MkdirAll(filepathDir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", key, err)
		}
	}
	page, err := backend.List(context.Background(), "data/", "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "data/a" {
		t.Fatalf("List() = %#v, want only data/a", page)
	}
}

func TestConcurrentWritersToSameKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := newTestBackend(t)
	var successes atomic.Int32
	var conflicts atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := []byte(fmt.Sprintf("payload-%03d", i))
			_, err := backend.Put(ctx, "concurrent/object", bytes.NewReader(payload), int64(len(payload)))
			if err == nil {
				successes.Add(1)
				return
			}
			if errors.Is(err, core.ErrConflict) {
				conflicts.Add(1)
				return
			}
			t.Errorf("Put() unexpected error = %v", err)
		}()
	}
	wg.Wait()

	if successes.Load() != 1 {
		t.Fatalf("successes = %d, want 1", successes.Load())
	}
	if conflicts.Load() != 99 {
		t.Fatalf("conflicts = %d, want 99", conflicts.Load())
	}

	rc, _, err := backend.Get(ctx, "concurrent/object")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer rc.Close()
	var got bytes.Buffer
	if _, err := got.ReadFrom(rc); err != nil {
		t.Fatalf("ReadFrom(Get()) error = %v", err)
	}
	if got.Len() != len("payload-000") {
		t.Fatalf("stored payload length = %d, want %d", got.Len(), len("payload-000"))
	}
}

func FuzzPutGetRoundTrip(f *testing.F) {
	f.Add([]byte("kronos"))
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xff}, 1024))

	f.Fuzz(func(t *testing.T, payload []byte) {
		backend := newTestBackend(t)
		ctx := context.Background()
		_, err := backend.Put(ctx, "fuzz/object", bytes.NewReader(payload), int64(len(payload)))
		if err != nil {
			t.Fatalf("Put() error = %v", err)
		}

		rc, _, err := backend.Get(ctx, "fuzz/object")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		defer rc.Close()
		var got bytes.Buffer
		if _, err := got.ReadFrom(rc); err != nil {
			t.Fatalf("ReadFrom(Get()) error = %v", err)
		}
		if !bytes.Equal(got.Bytes(), payload) {
			t.Fatal("round trip payload mismatch")
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func filepathDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}

func newTestBackend(t *testing.T) *Backend {
	t.Helper()

	backend, err := New("test-local", t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return backend
}

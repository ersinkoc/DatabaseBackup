package storagetest

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/storage"
)

func TestMemoryBackendCRUDAndList(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := NewMemoryBackend("mem")
	if got := backend.Name(); got != "mem" {
		t.Fatalf("Name() = %q", got)
	}

	info, err := backend.Put(ctx, "chunks/b", strings.NewReader("bravo"), 5)
	if err != nil {
		t.Fatalf("Put(b) error = %v", err)
	}
	if info.Key != "chunks/b" || info.Size != 5 || info.ETag == "" {
		t.Fatalf("Put() info = %#v", info)
	}
	if _, err := backend.Put(ctx, "chunks/a", strings.NewReader("alpha"), -1); err != nil {
		t.Fatalf("Put(a) error = %v", err)
	}
	if _, err := backend.Put(ctx, "other/c", strings.NewReader("charlie"), -1); err != nil {
		t.Fatalf("Put(c) error = %v", err)
	}
	if _, err := backend.Put(ctx, "chunks/a", strings.NewReader("again"), -1); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("Put(duplicate) error = %v, want conflict", err)
	}

	reader, gotInfo, err := backend.Get(ctx, "chunks/a")
	if err != nil {
		t.Fatalf("Get(a) error = %v", err)
	}
	var data strings.Builder
	if _, err := io.Copy(&data, reader); err != nil {
		t.Fatalf("Copy(a) error = %v", err)
	}
	_ = reader.Close()
	if data.String() != "alpha" || gotInfo.Size != 5 {
		t.Fatalf("Get(a) = %q %#v", data.String(), gotInfo)
	}

	rangeReader, err := backend.GetRange(ctx, "chunks/b", 1, 3)
	if err != nil {
		t.Fatalf("GetRange(b) error = %v", err)
	}
	data.Reset()
	if _, err := io.Copy(&data, rangeReader); err != nil {
		t.Fatalf("Copy(range) error = %v", err)
	}
	_ = rangeReader.Close()
	if data.String() != "rav" {
		t.Fatalf("GetRange() = %q", data.String())
	}

	exists, err := backend.Exists(ctx, "missing")
	if err != nil {
		t.Fatalf("Exists(missing) error = %v", err)
	}
	if exists {
		t.Fatal("Exists(missing) = true, want false")
	}
	exists, err = backend.Exists(ctx, "chunks/a")
	if err != nil || !exists {
		t.Fatalf("Exists(a) = %v, %v; want true, nil", exists, err)
	}

	page, err := backend.List(ctx, "chunks/", "chunks/a")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "chunks/b" {
		t.Fatalf("List() = %#v", page.Objects)
	}

	if err := backend.Delete(ctx, "chunks/a"); err != nil {
		t.Fatalf("Delete(a) error = %v", err)
	}
	if _, _, err := backend.Get(ctx, "chunks/a"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Get(deleted) error = %v, want not found", err)
	}
}

func TestMemoryBackendConformance(t *testing.T) {
	t.Parallel()

	RunBackendConformance(t, func(t *testing.T) storage.Backend {
		t.Helper()
		return NewMemoryBackend("mem")
	}, ConformanceOptions{InvalidKeyErrors: true})
}

func TestMemoryBackendRejectsBadInputs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := NewMemoryBackend("mem")
	if _, err := backend.Put(ctx, "short", strings.NewReader("abc"), 4); err == nil {
		t.Fatal("Put(size mismatch) error = nil, want error")
	}
	if _, err := backend.Put(ctx, "bad-reader", errReader{}, -1); err == nil {
		t.Fatal("Put(reader error) error = nil, want error")
	}
	if _, err := backend.Put(ctx, "key", strings.NewReader("abc"), -1); err != nil {
		t.Fatalf("Put(key) error = %v", err)
	}
	if _, err := backend.GetRange(ctx, "key", -1, 1); err == nil {
		t.Fatal("GetRange(negative offset) error = nil, want error")
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := backend.Put(canceled, "x", strings.NewReader("x"), -1); err == nil {
		t.Fatal("Put(canceled) error = nil, want error")
	}
	if _, _, err := backend.Get(canceled, "key"); err == nil {
		t.Fatal("Get(canceled) error = nil, want error")
	}
	if _, err := backend.Head(canceled, "key"); err == nil {
		t.Fatal("Head(canceled) error = nil, want error")
	}
	if _, err := backend.GetRange(canceled, "key", 0, 1); err == nil {
		t.Fatal("GetRange(canceled) error = nil, want error")
	}
	if err := backend.Delete(canceled, "key"); err == nil {
		t.Fatal("Delete(canceled) error = nil, want error")
	}
	if _, err := backend.List(canceled, "", ""); err == nil {
		t.Fatal("List(canceled) error = nil, want error")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

package storagetest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/storage"
)

// BackendFactory returns a clean backend for one conformance subtest.
type BackendFactory func(t *testing.T) storage.Backend

// ConformanceOptions controls optional backend contract checks.
type ConformanceOptions struct {
	InvalidKeyErrors bool
}

// RunBackendConformance verifies common storage.Backend behaviour.
func RunBackendConformance(t *testing.T, factory BackendFactory, opts ConformanceOptions) {
	t.Helper()

	t.Run("put_get_head_exists_delete_list", func(t *testing.T) {
		ctx := context.Background()
		backend := factory(t)
		payload := []byte("time devours; kronos preserves")

		info, err := backend.Put(ctx, "data/aa/object", bytes.NewReader(payload), int64(len(payload)))
		if err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		if info.Key != "data/aa/object" || info.Size != int64(len(payload)) || info.ETag == "" {
			t.Fatalf("Put() info = %#v", info)
		}

		exists, err := backend.Exists(ctx, "data/aa/object")
		if err != nil || !exists {
			t.Fatalf("Exists(existing) = %v, %v; want true, nil", exists, err)
		}

		head, err := backend.Head(ctx, "data/aa/object")
		if err != nil {
			t.Fatalf("Head() error = %v", err)
		}
		if head.Key != "data/aa/object" || head.Size != int64(len(payload)) {
			t.Fatalf("Head() = %#v", head)
		}

		reader, gotInfo, err := backend.Get(ctx, "data/aa/object")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		got, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			t.Fatalf("ReadAll(Get()) error = %v", readErr)
		}
		if closeErr != nil {
			t.Fatalf("Close(Get()) error = %v", closeErr)
		}
		if !bytes.Equal(got, payload) || gotInfo.Size != int64(len(payload)) {
			t.Fatalf("Get() = %q %#v", got, gotInfo)
		}

		for _, key := range []string{"data/aa/a", "data/aa/c", "other/z"} {
			if _, err := backend.Put(ctx, key, strings.NewReader(key), int64(len(key))); err != nil {
				t.Fatalf("Put(%s) error = %v", key, err)
			}
		}
		page, err := backend.List(ctx, "data/aa/", "")
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		keys := objectKeys(page.Objects)
		if strings.Join(keys, ",") != "data/aa/a,data/aa/c,data/aa/object" {
			t.Fatalf("List() keys = %v", keys)
		}

		if err := backend.Delete(ctx, "data/aa/object"); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		exists, err = backend.Exists(ctx, "data/aa/object")
		if err != nil || exists {
			t.Fatalf("Exists(deleted) = %v, %v; want false, nil", exists, err)
		}
		if err := backend.Delete(ctx, "data/aa/object"); err != nil {
			t.Fatalf("Delete(missing) error = %v", err)
		}
	})

	t.Run("range_missing_conflict_and_size", func(t *testing.T) {
		ctx := context.Background()
		backend := factory(t)
		if _, err := backend.Put(ctx, "ranges/object", strings.NewReader("abcdef"), 6); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		rangeReader, err := backend.GetRange(ctx, "ranges/object", 2, 3)
		if err != nil {
			t.Fatalf("GetRange() error = %v", err)
		}
		got, readErr := io.ReadAll(rangeReader)
		closeErr := rangeReader.Close()
		if readErr != nil {
			t.Fatalf("ReadAll(GetRange()) error = %v", readErr)
		}
		if closeErr != nil {
			t.Fatalf("Close(GetRange()) error = %v", closeErr)
		}
		if string(got) != "cde" {
			t.Fatalf("GetRange() = %q, want cde", got)
		}

		if _, err := backend.Put(ctx, "ranges/object", strings.NewReader("again"), 5); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("Put(conflict) error = %v, want ErrConflict", err)
		}
		if _, err := backend.Put(ctx, "short/object", strings.NewReader("abc"), 4); err == nil {
			t.Fatal("Put(size mismatch) error = nil, want error")
		}
		if _, _, err := backend.Get(ctx, "missing/object"); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
		}
		if _, err := backend.Head(ctx, "missing/object"); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("Head(missing) error = %v, want ErrNotFound", err)
		}
		if _, err := backend.GetRange(ctx, "missing/object", 0, 1); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("GetRange(missing) error = %v, want ErrNotFound", err)
		}
	})

	if opts.InvalidKeyErrors {
		t.Run("invalid_keys", func(t *testing.T) {
			backend := factory(t)
			for _, key := range []string{"", ".", "../escape", "a/../escape", "/absolute", `back\slash`} {
				_, err := backend.Put(context.Background(), key, strings.NewReader("x"), 1)
				var invalid storage.InvalidKeyError
				if !errors.As(err, &invalid) {
					t.Fatalf("Put(%q) error = %v, want InvalidKeyError", key, err)
				}
			}
		})
	}

	t.Run("canceled_context", func(t *testing.T) {
		backend := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := backend.Put(ctx, "key", strings.NewReader("x"), 1); err == nil {
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
	})
}

func objectKeys(objects []storage.ObjectInfo) []string {
	keys := make([]string, 0, len(objects))
	for _, object := range objects {
		keys = append(keys, object.Key)
	}
	sort.Strings(keys)
	return keys
}

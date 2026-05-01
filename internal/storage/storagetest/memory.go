// Package storagetest contains storage backend implementations for tests.
package storagetest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/storage"
)

// MemoryBackend is an in-memory storage backend for tests.
type MemoryBackend struct {
	mu      sync.RWMutex
	name    string
	objects map[string]memoryObject
}

var _ storage.Backend = (*MemoryBackend)(nil)

type memoryObject struct {
	data      []byte
	info      storage.ObjectInfo
	updatedAt time.Time
}

// NewMemoryBackend returns an empty test backend.
func NewMemoryBackend(name string) *MemoryBackend {
	return &MemoryBackend{name: name, objects: make(map[string]memoryObject)}
}

// Name returns the backend name.
func (b *MemoryBackend) Name() string {
	return b.name
}

// Put stores the stream at key.
func (b *MemoryBackend) Put(ctx context.Context, key string, r io.Reader, size int64) (storage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := validateKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	var buf bytes.Buffer
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(&buf, hash), r)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	if size >= 0 && written != size {
		return storage.ObjectInfo{}, fmt.Errorf("size mismatch for %q: wrote %d bytes, expected %d", key, written, size)
	}

	now := time.Now().UTC()
	info := storage.ObjectInfo{
		Key:       key,
		Size:      written,
		ETag:      hex.EncodeToString(hash.Sum(nil)),
		UpdatedAt: now,
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.objects[key]; exists {
		return storage.ObjectInfo{}, core.WrapKind(core.ErrorKindConflict, "put object", fmt.Errorf("object %q already exists", key))
	}
	data := append([]byte(nil), buf.Bytes()...)
	b.objects[key] = memoryObject{data: data, info: info, updatedAt: now}
	return info, nil
}

// Get returns a full object stream.
func (b *MemoryBackend) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	if err := validateKey(key); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	obj, ok := b.objects[key]
	if !ok {
		return nil, storage.ObjectInfo{}, core.WrapKind(core.ErrorKindNotFound, "get object", fmt.Errorf("object %q not found", key))
	}
	return io.NopCloser(bytes.NewReader(obj.data)), obj.info, nil
}

// GetRange returns a byte range from an object.
func (b *MemoryBackend) GetRange(ctx context.Context, key string, off, length int64) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	obj, ok := b.objects[key]
	if !ok {
		return nil, core.WrapKind(core.ErrorKindNotFound, "get object range", fmt.Errorf("object %q not found", key))
	}
	if off < 0 || length < 0 || off > int64(len(obj.data)) {
		return nil, fmt.Errorf("invalid range off=%d length=%d", off, length)
	}
	end := off + length
	if end > int64(len(obj.data)) {
		end = int64(len(obj.data))
	}
	return io.NopCloser(bytes.NewReader(obj.data[off:end])), nil
}

// Head returns object metadata.
func (b *MemoryBackend) Head(ctx context.Context, key string) (storage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := validateKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	obj, ok := b.objects[key]
	if !ok {
		return storage.ObjectInfo{}, core.WrapKind(core.ErrorKindNotFound, "head object", fmt.Errorf("object %q not found", key))
	}
	return obj.info, nil
}

// Exists reports whether key exists.
func (b *MemoryBackend) Exists(ctx context.Context, key string) (bool, error) {
	_, err := b.Head(ctx, key)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, core.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// Delete removes key. Missing keys are ignored.
func (b *MemoryBackend) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateKey(key); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, key)
	return nil
}

func validateKey(key string) error {
	cleaned := path.Clean(key)
	if key == "" || cleaned == "." || path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return storage.InvalidKeyError{Key: key}
	}
	if strings.Contains(key, "\\") {
		return storage.InvalidKeyError{Key: key}
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == ".." {
			return storage.InvalidKeyError{Key: key}
		}
	}
	return nil
}

// List returns objects with prefix after token, ordered by key.
func (b *MemoryBackend) List(ctx context.Context, prefix string, token string) (storage.ListPage, error) {
	if err := ctx.Err(); err != nil {
		return storage.ListPage{}, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()

	keys := make([]string, 0, len(b.objects))
	for key := range b.objects {
		if strings.HasPrefix(key, prefix) && (token == "" || key > token) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	objects := make([]storage.ObjectInfo, 0, len(keys))
	for _, key := range keys {
		objects = append(objects, b.objects[key].info)
	}
	return storage.ListPage{Objects: objects}, nil
}

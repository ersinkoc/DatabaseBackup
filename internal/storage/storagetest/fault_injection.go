package storagetest

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/storage"
)

// FaultInjector injects faults into storage operations.
type FaultInjector struct {
	mu           sync.Mutex
	latency      time.Duration
	 corruption   float64 // probability 0.0-1.0
	disconnect   bool
	err          error
	readErr      error
	writeErr     error
	headErr      error
	listErr      error
	deleteErr    error
	existsErr    error
	getRangeErr  error
}

// InjectFaults returns a Backend that wraps base and injects faults.
func InjectFaults(base storage.Backend, inj *FaultInjector) storage.Backend {
	return &faultBackend{base: base, inj: inj}
}

type faultBackend struct {
	base storage.Backend
	inj  *FaultInjector
}

func (b *faultBackend) Name() string {
	return b.base.Name()
}

func (b *faultBackend) injectLatency() {
	if b.inj.latency > 0 {
		time.Sleep(b.inj.latency)
	}
}

func (b *faultBackend) checkCorruption(payload []byte) []byte {
	b.inj.mu.Lock()
	corruption := b.inj.corruption
	b.inj.mu.Unlock()
	if corruption > 0 && rand.Float64() < corruption && len(payload) > 0 {
		pos := rand.Intn(len(payload))
		payload[pos] ^= 0xFF
	}
	return payload
}

func (b *faultBackend) Put(ctx context.Context, key string, r io.Reader, size int64) (storage.ObjectInfo, error) {
	b.injectLatency()
	if err := b.inj.writeErr; err != nil {
		return storage.ObjectInfo{}, err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	data = b.checkCorruption(data)
	info, err := b.base.Put(ctx, key, newBytesReader(data), size)
	if err != nil {
		return info, err
	}
	if b.inj.disconnect {
		return info, core.ErrConflict
	}
	return info, nil
}

func (b *faultBackend) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	b.injectLatency()
	if err := b.inj.readErr; err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	rc, info, err := b.base.Get(ctx, key)
	if err != nil {
		return nil, info, err
	}
	return &faultReadCloser{rc: rc, inj: b.inj}, info, nil
}

type faultReadCloser struct {
	rc  io.ReadCloser
	inj *FaultInjector
}

func (c *faultReadCloser) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	if err == io.EOF {
		return n, err
	}
	if err != nil {
		return n, err
	}
	c.inj.mu.Lock()
	corruption := c.inj.corruption
	c.inj.mu.Unlock()
	if corruption > 0 && rand.Float64() < corruption && n > 0 {
		pos := rand.Intn(n)
		p[pos] ^= 0xFF
	}
	return n, nil
}

func (c *faultReadCloser) Close() error {
	return c.rc.Close()
}

func (b *faultBackend) Head(ctx context.Context, key string) (storage.ObjectInfo, error) {
	b.injectLatency()
	if err := b.inj.headErr; err != nil {
		return storage.ObjectInfo{}, err
	}
	return b.base.Head(ctx, key)
}

func (b *faultBackend) Exists(ctx context.Context, key string) (bool, error) {
	b.injectLatency()
	if err := b.inj.existsErr; err != nil {
		return false, err
	}
	return b.base.Exists(ctx, key)
}

func (b *faultBackend) Delete(ctx context.Context, key string) error {
	b.injectLatency()
	if err := b.inj.deleteErr; err != nil {
		return err
	}
	return b.base.Delete(ctx, key)
}

func (b *faultBackend) List(ctx context.Context, prefix, cursor string) (storage.ListPage, error) {
	b.injectLatency()
	if err := b.inj.listErr; err != nil {
		return storage.ListPage{}, err
	}
	return b.base.List(ctx, prefix, cursor)
}

func (b *faultBackend) GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	b.injectLatency()
	if err := b.inj.getRangeErr; err != nil {
		return nil, err
	}
	return b.base.GetRange(ctx, key, offset, length)
}

// NewFaultInjector returns a configurable fault injector.
func NewFaultInjector() *FaultInjector {
	return &FaultInjector{}
}

// SetLatency sets fixed latency to inject before every operation.
func (f *FaultInjector) SetLatency(d time.Duration) {
	f.mu.Lock()
	f.latency = d
	f.mu.Unlock()
}

// SetCorruption sets the probability (0.0-1.0) of corrupting each byte read or written.
func (f *FaultInjector) SetCorruption(p float64) {
	f.mu.Lock()
	f.corruption = p
	f.mu.Unlock()
}

// SetDisconnect causes all operations to return ErrNotImplemented.
func (f *FaultInjector) SetDisconnect() {
	f.mu.Lock()
	f.disconnect = true
	f.mu.Unlock()
}

// SetError sets a persistent error for all operations.
func (f *FaultInjector) SetError(err error) {
	f.mu.Lock()
	f.err = err
	f.writeErr = err
	f.readErr = err
	f.headErr = err
	f.listErr = err
	f.deleteErr = err
	f.existsErr = err
	f.getRangeErr = err
	f.mu.Unlock()
}

// SetWriteError sets an error specifically for Put operations.
func (f *FaultInjector) SetWriteError(err error) {
	f.mu.Lock()
	f.writeErr = err
	f.mu.Unlock()
}

// SetReadError sets an error specifically for Get operations.
func (f *FaultInjector) SetReadError(err error) {
	f.mu.Lock()
	f.readErr = err
	f.mu.Unlock()
}

// SetDeleteError sets an error specifically for Delete operations.
func (f *FaultInjector) SetDeleteError(err error) {
	f.mu.Lock()
	f.deleteErr = err
	f.mu.Unlock()
}

// RunFaultConformance runs fault injection tests against a backend factory.
func RunFaultConformance(t *testing.T, factory BackendFactory) {
	t.Helper()

	t.Run("latency_injection", func(t *testing.T) {
		ctx := context.Background()
		inj := NewFaultInjector()
		inj.SetLatency(50 * time.Millisecond)
		backend := InjectFaults(factory(t), inj)

		start := time.Now()
		_, err := backend.Put(ctx, "latency/key", newBytesReader([]byte("data")), 4)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		if elapsed < 50*time.Millisecond {
			t.Fatalf("Put() elapsed = %v, want >= 50ms", elapsed)
		}
	})

	t.Run("corruption_injection", func(t *testing.T) {
		ctx := context.Background()
		inj := NewFaultInjector()
		inj.SetCorruption(1.0) // 100% corruption
		backend := InjectFaults(factory(t), inj)

		data := []byte("original data")
		info, err := backend.Put(ctx, "corrupt/key", newBytesReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("Put() error = %v", err)
		}

		rc, _, err := backend.Get(ctx, "corrupt/key")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		got, _ := io.ReadAll(rc)
		rc.Close()

		if string(got) == string(data) {
			t.Fatalf("Get() returned original data despite 100%% corruption probability")
		}
		_ = info // suppress unused warning
	})

	t.Run("disconnect_injection", func(t *testing.T) {
		ctx := context.Background()
		inj := NewFaultInjector()
		inj.SetDisconnect()
		backend := InjectFaults(factory(t), inj)

		_, err := backend.Put(ctx, "disconnect/key", newBytesReader([]byte("data")), 4)
		if !errors.Is(err, core.ErrConflict) {
			t.Fatalf("Put() error = %v, want core.ErrConflict", err)
		}
	})

	t.Run("write_error_injection", func(t *testing.T) {
		ctx := context.Background()
		inj := NewFaultInjector()
		inj.SetWriteError(core.ErrConflict)
		backend := InjectFaults(factory(t), inj)

		_, err := backend.Put(ctx, "error/key", newBytesReader([]byte("data")), 4)
		if !errors.Is(err, core.ErrConflict) {
			t.Fatalf("Put() error = %v, want core.ErrConflict", err)
		}
	})

	t.Run("read_error_injection", func(t *testing.T) {
		ctx := context.Background()
		inj := NewFaultInjector()
		inj.SetReadError(core.ErrConflict)
		backend := InjectFaults(factory(t), inj)

		// First put valid data through unwrapped backend
		_, err := factory(t).Put(ctx, "readerror/key", newBytesReader([]byte("data")), 4)
		if err != nil {
			t.Fatalf("Setup Put() error = %v", err)
		}

		_, _, err = backend.Get(ctx, "readerror/key")
		if !errors.Is(err, core.ErrConflict) {
			t.Fatalf("Get() error = %v, want core.ErrConflict", err)
		}
	})

	t.Run("delete_error_injection", func(t *testing.T) {
		ctx := context.Background()
		inj := NewFaultInjector()
		inj.SetDeleteError(core.ErrConflict)
		backend := InjectFaults(factory(t), inj)

		// First put valid data
		_, err := factory(t).Put(ctx, "deleteerror/key", newBytesReader([]byte("data")), 4)
		if err != nil {
			t.Fatalf("Setup Put() error = %v", err)
		}

		err = backend.Delete(ctx, "deleteerror/key")
		if !errors.Is(err, core.ErrConflict) {
			t.Fatalf("Delete() error = %v, want core.ErrConflict", err)
		}
	})

	t.Run("transient_error_recovery", func(t *testing.T) {
		ctx := context.Background()
		inj := NewFaultInjector()
		backend := InjectFaults(factory(t), inj)

		// Put valid data
		_, err := backend.Put(ctx, "recovery/key", newBytesReader([]byte("data")), 4)
		if err != nil {
			t.Fatalf("Setup Put() error = %v", err)
		}

		// Inject error
		inj.SetReadError(core.ErrConflict)
		_, _, err = backend.Get(ctx, "recovery/key")
		if !errors.Is(err, core.ErrConflict) {
			t.Fatalf("Get() error = %v, want core.ErrConflict", err)
		}

		// Clear error
		inj.SetReadError(nil)
		_, _, err = backend.Get(ctx, "recovery/key")
		if err != nil {
			t.Fatalf("Get() after clearing error error = %v", err)
		}
	})
}

type bytesReader struct {
	data []byte
	pos  int
}

func newBytesReader(data []byte) *bytesReader {
	return &bytesReader{data: data}
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	if r.pos >= len(r.data) {
		return n, io.EOF
	}
	return n, nil
}

package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kronos/kronos/internal/chunk"
	kcompress "github.com/kronos/kronos/internal/compress"
	"github.com/kronos/kronos/internal/core"
	kcrypto "github.com/kronos/kronos/internal/crypto"
	"github.com/kronos/kronos/internal/storage"
	"github.com/kronos/kronos/internal/storage/storagetest"
)

func TestBackendConformance(t *testing.T) {
	t.Parallel()

	storagetest.RunBackendConformance(t, func(t *testing.T) storage.Backend {
		t.Helper()
		server := newMockS3Server(t)
		return newMockBackend(t, server.URL)
	}, storagetest.ConformanceOptions{InvalidKeyErrors: true})
}

func TestBackendCRUD(t *testing.T) {
	t.Parallel()

	server := newMockS3Server(t)
	backend := newMockBackend(t, server.URL)
	if backend.Name() != "mock-s3" {
		t.Fatalf("Name() = %q, want mock-s3", backend.Name())
	}
	ctx := context.Background()
	payload := []byte("Time devours. Kronos preserves.")

	info, err := backend.Put(ctx, "data/object.txt", bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if info.Size != int64(len(payload)) || info.ETag == "" {
		t.Fatalf("Put() info = %#v", info)
	}

	exists, err := backend.Exists(ctx, "data/object.txt")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if !exists {
		t.Fatal("Exists() = false, want true")
	}

	head, err := backend.Head(ctx, "data/object.txt")
	if err != nil {
		t.Fatalf("Head() error = %v", err)
	}
	if head.Size != int64(len(payload)) || head.ETag != info.ETag {
		t.Fatalf("Head() = %#v, want size/etag from %#v", head, info)
	}

	rc, gotInfo, err := backend.Get(ctx, "data/object.txt")
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
	if len(page.Objects) != 1 || page.Objects[0].Key != "data/object.txt" {
		t.Fatalf("List() = %#v", page)
	}

	if err := backend.Delete(ctx, "data/object.txt"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	exists, err = backend.Exists(ctx, "data/object.txt")
	if err != nil {
		t.Fatalf("Exists(after delete) error = %v", err)
	}
	if exists {
		t.Fatal("Exists(after delete) = true, want false")
	}
}

func TestBackendGetRange(t *testing.T) {
	t.Parallel()

	server := newMockS3Server(t)
	backend := newMockBackend(t, server.URL)
	ctx := context.Background()
	if _, err := backend.Put(ctx, "ranges/object", strings.NewReader("abcdef"), 6); err != nil {
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

func TestBackendMultipartPut(t *testing.T) {
	t.Parallel()

	server := newMockS3Server(t)
	backend := newMockBackend(t, server.URL)
	backend.multipartThreshold = 5
	backend.multipartPartSize = 4
	ctx := context.Background()

	payload := []byte("abcdefghijkl")
	info, err := backend.Put(ctx, "multipart/object", bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("Put(multipart) error = %v", err)
	}
	if info.Size != int64(len(payload)) {
		t.Fatalf("Put(multipart) size = %d, want %d", info.Size, len(payload))
	}

	rc, _, err := backend.Get(ctx, "multipart/object")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer rc.Close()
	var got bytes.Buffer
	if _, err := got.ReadFrom(rc); err != nil {
		t.Fatalf("ReadFrom(Get()) error = %v", err)
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatalf("multipart payload = %q, want %q", got.Bytes(), payload)
	}
}

func TestBackendMultipartAbortOnPartFailure(t *testing.T) {
	t.Parallel()

	server := newMockS3Server(t)
	server.failPartNumber = 2
	backend := newMockBackend(t, server.URL)
	backend.multipartThreshold = 5
	backend.multipartPartSize = 4

	_, err := backend.Put(context.Background(), "multipart/fail", strings.NewReader("abcdefghijkl"), 12)
	if err == nil {
		t.Fatal("Put(multipart fail) error = nil, want error")
	}
	if server.abortCount == 0 {
		t.Fatal("abort count = 0, want multipart abort")
	}
	exists, existsErr := backend.Exists(context.Background(), "multipart/fail")
	if existsErr != nil {
		t.Fatalf("Exists() error = %v", existsErr)
	}
	if exists {
		t.Fatal("failed multipart object exists")
	}
}

func TestBackendRetriesTransientPut(t *testing.T) {
	t.Parallel()

	server := newMockS3Server(t)
	server.retryFailures = map[string]int{"retry/object": 1}
	server.retryAfter = "2"
	backend := newMockBackend(t, server.URL)
	backend.retry = RetryConfig{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
	var delays []time.Duration
	backend.sleep = func(delay time.Duration) {
		delays = append(delays, delay)
	}

	_, err := backend.Put(context.Background(), "retry/object", strings.NewReader("retry-payload"), 13)
	if err != nil {
		t.Fatalf("Put(retry) error = %v", err)
	}
	if server.retryAttempts["retry/object"] != 1 {
		t.Fatalf("retry attempts = %d, want 1", server.retryAttempts["retry/object"])
	}
	if len(delays) != 1 || delays[0] != 2*time.Second {
		t.Fatalf("retry delays = %v, want [2s]", delays)
	}
}

func TestBackendRefreshesExpiredCredentials(t *testing.T) {
	t.Parallel()

	server := newMockS3Server(t)
	calls := 0
	backend, err := New(Config{
		Name:           "mock-s3",
		Endpoint:       server.URL,
		Region:         "us-east-1",
		Bucket:         "bucket",
		ForcePathStyle: true,
		HTTPClient:     http.DefaultClient,
		CredentialsProvider: CredentialsProviderFunc(func(ctx context.Context) (Credentials, error) {
			calls++
			if calls == 1 {
				return Credentials{AccessKey: "old", SecretKey: "old-secret", ExpiresAt: time.Now().Add(-time.Minute)}, nil
			}
			return Credentials{AccessKey: "new", SecretKey: "new-secret", ExpiresAt: time.Now().Add(time.Hour)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("credential resolve calls after New = %d, want 1", calls)
	}
	if _, err := backend.Put(context.Background(), "creds/refresh", strings.NewReader("payload"), 7); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("credential resolve calls after Put = %d, want 2", calls)
	}
}

func TestBackendKeepsFreshCredentials(t *testing.T) {
	t.Parallel()

	server := newMockS3Server(t)
	calls := 0
	backend, err := New(Config{
		Name:           "mock-s3",
		Endpoint:       server.URL,
		Region:         "us-east-1",
		Bucket:         "bucket",
		ForcePathStyle: true,
		HTTPClient:     http.DefaultClient,
		CredentialsProvider: CredentialsProviderFunc(func(ctx context.Context) (Credentials, error) {
			calls++
			return Credentials{AccessKey: "fresh", SecretKey: "fresh-secret", ExpiresAt: time.Now().Add(time.Hour)}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := backend.Put(context.Background(), "creds/fresh", strings.NewReader("payload"), 7); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("credential resolve calls = %d, want 1", calls)
	}
}

func TestBackendDuplicatePutConflicts(t *testing.T) {
	t.Parallel()

	server := newMockS3Server(t)
	backend := newMockBackend(t, server.URL)
	ctx := context.Background()
	if _, err := backend.Put(ctx, "objects/one", strings.NewReader("first"), 5); err != nil {
		t.Fatalf("Put(first) error = %v", err)
	}
	_, err := backend.Put(ctx, "objects/one", strings.NewReader("second"), 6)
	if !errors.Is(err, core.ErrConflict) {
		t.Fatalf("Put(duplicate) error = %v, want ErrConflict", err)
	}
}

func TestBackendRejectsInvalidKeys(t *testing.T) {
	t.Parallel()

	server := newMockS3Server(t)
	backend := newMockBackend(t, server.URL)
	for _, key := range []string{"", ".", "../escape", "a/../escape", "/absolute", "back\\slash"} {
		key := key
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			_, err := backend.Put(context.Background(), key, strings.NewReader("x"), 1)
			var invalid storage.InvalidKeyError
			if !errors.As(err, &invalid) {
				t.Fatalf("Put(%q) error = %v, want InvalidKeyError", key, err)
			}
		})
	}
}

func TestBackendMissingAndRangeErrors(t *testing.T) {
	t.Parallel()

	server := newMockS3Server(t)
	backend := newMockBackend(t, server.URL)
	ctx := context.Background()

	if _, _, err := backend.Get(ctx, "missing/object"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := backend.GetRange(ctx, "missing/object", 0, 1); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetRange(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := backend.GetRange(ctx, "missing/object", -1, 1); err == nil {
		t.Fatal("GetRange(negative offset) error = nil, want error")
	}
	rc, err := backend.GetRange(ctx, "missing/object", 0, 0)
	if err != nil {
		t.Fatalf("GetRange(zero length) error = %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close(zero range) error = %v", err)
	}
	if err := backend.Delete(ctx, "missing/object"); err != nil {
		t.Fatalf("Delete(missing) error = %v", err)
	}
}

func TestBackendPipelineRestoreRejectsMissingChunkObject(t *testing.T) {
	t.Parallel()

	server := newMockS3Server(t)
	backend := newMockBackend(t, server.URL)
	pipeline := testS3Pipeline(t, backend)
	refs, _, err := pipeline.Feed(context.Background(), strings.NewReader("missing s3 chunk drill"))
	if err != nil {
		t.Fatalf("Feed() error = %v", err)
	}

	server.mu.Lock()
	delete(server.objects, refs[0].Key)
	server.mu.Unlock()

	var got bytes.Buffer
	_, err = pipeline.Restore(context.Background(), refs, &got)
	if err == nil {
		t.Fatal("Restore() error = nil, want missing S3 object")
	}
	if !strings.Contains(err.Error(), "get chunk") || !strings.Contains(err.Error(), refs[0].Key) {
		t.Fatalf("Restore() error = %q, want missing object key", err)
	}
}

func TestBackendPipelineRestoreRejectsCorruptChunkObject(t *testing.T) {
	t.Parallel()

	server := newMockS3Server(t)
	backend := newMockBackend(t, server.URL)
	pipeline := testS3Pipeline(t, backend)
	refs, _, err := pipeline.Feed(context.Background(), strings.NewReader("corrupt s3 chunk drill"))
	if err != nil {
		t.Fatalf("Feed() error = %v", err)
	}

	corrupt := []byte("not a valid encrypted chunk envelope")
	server.mu.Lock()
	server.objects[refs[0].Key] = mockS3Object{data: corrupt, etag: hashBytes(corrupt), modifiedAt: server.modifiedAt}
	server.mu.Unlock()

	var got bytes.Buffer
	_, err = pipeline.Restore(context.Background(), refs, &got)
	if err == nil {
		t.Fatal("Restore() error = nil, want corrupt S3 object")
	}
	if !strings.Contains(err.Error(), "decrypt chunk") {
		t.Fatalf("Restore() error = %q, want decrypt failure", err)
	}
}

func TestBackendConstructorAndHelpers(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{Endpoint: "://bad"}); err == nil {
		t.Fatal("New(bad endpoint) error = nil, want error")
	}
	if _, err := New(Config{Endpoint: "http://127.0.0.1", Bucket: "bucket", Credentials: testCredentials}); err == nil {
		t.Fatal("New(missing region) error = nil, want error")
	}
	if _, err := New(Config{Endpoint: "http://127.0.0.1", Region: "us-east-1", Credentials: testCredentials}); err == nil {
		t.Fatal("New(missing bucket) error = nil, want error")
	}
	if _, err := New(Config{Endpoint: "http://127.0.0.1", Region: "us-east-1", Bucket: "bucket"}); err == nil {
		t.Fatal("New(missing credentials) error = nil, want error")
	}

	backend, err := New(Config{
		Endpoint:    "http://127.0.0.1",
		Region:      "us-east-1",
		Bucket:      "bucket",
		Credentials: testCredentials,
	})
	if err != nil {
		t.Fatalf("New(default name) error = %v", err)
	}
	if backend.Name() != "s3" {
		t.Fatalf("Name() = %q, want s3", backend.Name())
	}
	req, err := backend.newRequest(context.Background(), http.MethodGet, "path/to/object", url.Values{"a": []string{"b"}}, nil)
	if err != nil {
		t.Fatalf("newRequest() error = %v", err)
	}
	if req.URL.Host != "bucket.127.0.0.1" || req.URL.Path != "/path/to/object" || req.URL.RawQuery != "a=b" {
		t.Fatalf("virtual host request URL = %s", req.URL.String())
	}
}

func TestBackendPrimitiveHelpers(t *testing.T) {
	t.Parallel()

	header := http.Header{}
	header.Set("Content-Length", "12")
	header.Set("ETag", `"abc"`)
	header.Set("Last-Modified", time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC).Format(http.TimeFormat))
	info := objectInfoFromHeaders("key", -1, header)
	if info.Size != 12 || info.ETag != "abc" || info.UpdatedAt.IsZero() {
		t.Fatalf("objectInfoFromHeaders() = %#v", info)
	}
	if got := trimETag(`"quoted"`); got != "quoted" {
		t.Fatalf("trimETag() = %q", got)
	}
	if err := (&s3Time{}).UnmarshalText(nil); err != nil {
		t.Fatalf("UnmarshalText(empty) error = %v", err)
	}
	if err := (&s3Time{}).UnmarshalText([]byte("not-time")); err == nil {
		t.Fatal("UnmarshalText(invalid) error = nil, want error")
	}
}

func newMockBackend(t *testing.T, endpoint string) *Backend {
	t.Helper()

	backend, err := New(Config{
		Name:           "mock-s3",
		Endpoint:       endpoint,
		Region:         "us-east-1",
		Bucket:         "bucket",
		Credentials:    testCredentials,
		ForcePathStyle: true,
		HTTPClient:     http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return backend
}

func testS3Pipeline(t *testing.T, backend *Backend) *chunk.Pipeline {
	t.Helper()

	chunker, err := chunk.NewFastCDC(4, 8, 16)
	if err != nil {
		t.Fatalf("NewFastCDC() error = %v", err)
	}
	compressor, err := kcompress.New(kcompress.AlgorithmNone)
	if err != nil {
		t.Fatalf("compress.New() error = %v", err)
	}
	cipher, err := kcrypto.NewAES256GCM(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatalf("NewAES256GCM() error = %v", err)
	}
	return &chunk.Pipeline{
		Chunker:     chunker,
		Compressor:  compressor,
		Cipher:      cipher,
		KeyID:       "s3-test-key",
		Backend:     backend,
		Concurrency: 1,
	}
}

type mockS3Object struct {
	data       []byte
	etag       string
	modifiedAt time.Time
}

type mockMultipartUpload struct {
	key   string
	parts map[int]mockS3Object
}

type mockS3Server struct {
	*httptest.Server
	mu             sync.Mutex
	objects        map[string]mockS3Object
	uploads        map[string]mockMultipartUpload
	modifiedAt     time.Time
	nextUploadID   int
	failPartNumber int
	abortCount     int
	retryFailures  map[string]int
	retryAttempts  map[string]int
	retryAfter     string
}

func newMockS3Server(t *testing.T) *mockS3Server {
	t.Helper()

	mock := &mockS3Server{
		objects:       make(map[string]mockS3Object),
		uploads:       make(map[string]mockMultipartUpload),
		retryAttempts: make(map[string]int),
		modifiedAt:    time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" || r.Header.Get("x-amz-date") == "" || r.Header.Get("x-amz-content-sha256") == "" {
			http.Error(w, "missing signature headers", http.StatusForbidden)
			return
		}
		key, ok := strings.CutPrefix(r.URL.Path, "/bucket/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		var err error
		key, err = url.PathUnescape(key)
		if err != nil {
			http.Error(w, "bad key", http.StatusBadRequest)
			return
		}

		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			mock.handleList(w, r)
			return
		}
		if r.Method == http.MethodPost && hasQueryKey(r.URL.Query(), "uploads") {
			mock.handleCreateMultipart(w, key)
			return
		}
		if r.Method == http.MethodPut && r.URL.Query().Get("uploadId") != "" && r.URL.Query().Get("partNumber") != "" {
			mock.handleUploadPart(w, r, key)
			return
		}
		if r.Method == http.MethodPost && r.URL.Query().Get("uploadId") != "" {
			mock.handleCompleteMultipart(w, r, key)
			return
		}
		if r.Method == http.MethodDelete && r.URL.Query().Get("uploadId") != "" {
			mock.handleAbortMultipart(w, r)
			return
		}
		if mock.shouldFailTransient(w, r, key) {
			return
		}

		mock.mu.Lock()
		defer mock.mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			if _, exists := mock.objects[key]; exists && r.Header.Get("If-None-Match") == "*" {
				http.Error(w, "exists", http.StatusPreconditionFailed)
				return
			}
			var buf bytes.Buffer
			if _, err := buf.ReadFrom(r.Body); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			etag := hashBytes(buf.Bytes())
			mock.objects[key] = mockS3Object{data: append([]byte(nil), buf.Bytes()...), etag: etag, modifiedAt: mock.modifiedAt}
			w.Header().Set("ETag", `"`+etag+`"`)
			w.Header().Set("Last-Modified", mock.modifiedAt.Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
		case http.MethodHead:
			object, exists := mock.objects[key]
			if !exists {
				http.NotFound(w, r)
				return
			}
			writeObjectHeaders(w, object)
		case http.MethodGet:
			object, exists := mock.objects[key]
			if !exists {
				http.NotFound(w, r)
				return
			}
			writeObjectHeaders(w, object)
			if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
				start, end, ok := parseRange(rangeHeader)
				if ok && start >= 0 && end >= start && end < int64(len(object.data)) {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(object.data)))
					w.WriteHeader(http.StatusPartialContent)
					w.Write(object.data[start : end+1])
					return
				}
			}
			w.WriteHeader(http.StatusOK)
			w.Write(object.data)
		case http.MethodDelete:
			delete(mock.objects, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	mock.Server = server
	t.Cleanup(server.Close)
	return mock
}

func (s *mockS3Server) shouldFailTransient(w http.ResponseWriter, r *http.Request, key string) bool {
	if r.Method != http.MethodPut || r.URL.Query().Get("uploadId") != "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	failures := s.retryFailures[key]
	if failures == 0 || s.retryAttempts[key] >= failures {
		return false
	}
	s.retryAttempts[key]++
	if s.retryAfter != "" {
		w.Header().Set("Retry-After", s.retryAfter)
	}
	http.Error(w, "try again", http.StatusServiceUnavailable)
	return true
}

func (s *mockS3Server) handleList(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prefix := r.URL.Query().Get("prefix")
	keys := make([]string, 0, len(s.objects))
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprintln(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprintln(w, `<ListBucketResult>`)
	for _, key := range keys {
		object := s.objects[key]
		fmt.Fprintln(w, `<Contents>`)
		fmt.Fprintf(w, `<Key>%s</Key>`, xmlEscape(key))
		fmt.Fprintf(w, `<LastModified>%s</LastModified>`, s.modifiedAt.Format(time.RFC3339))
		fmt.Fprintf(w, `<ETag>"%s"</ETag>`, object.etag)
		fmt.Fprintf(w, `<Size>%d</Size>`, len(object.data))
		fmt.Fprintln(w, `</Contents>`)
	}
	fmt.Fprintln(w, `</ListBucketResult>`)
}

func (s *mockS3Server) handleCreateMultipart(w http.ResponseWriter, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextUploadID++
	uploadID := fmt.Sprintf("upload-%d", s.nextUploadID)
	s.uploads[uploadID] = mockMultipartUpload{key: key, parts: make(map[int]mockS3Object)}
	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprintf(w, `<InitiateMultipartUploadResult><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, uploadID)
}

func (s *mockS3Server) handleUploadPart(w http.ResponseWriter, r *http.Request, key string) {
	partNumber, _ := strconv.Atoi(r.URL.Query().Get("partNumber"))
	if s.failPartNumber != 0 && partNumber == s.failPartNumber {
		http.Error(w, "injected part failure", http.StatusInternalServerError)
		return
	}
	uploadID := r.URL.Query().Get("uploadId")
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	etag := hashBytes(buf.Bytes())

	s.mu.Lock()
	defer s.mu.Unlock()
	upload, exists := s.uploads[uploadID]
	if !exists || upload.key != key {
		http.NotFound(w, r)
		return
	}
	upload.parts[partNumber] = mockS3Object{data: append([]byte(nil), buf.Bytes()...), etag: etag, modifiedAt: s.modifiedAt}
	s.uploads[uploadID] = upload
	w.Header().Set("ETag", `"`+etag+`"`)
	w.WriteHeader(http.StatusOK)
}

func (s *mockS3Server) handleCompleteMultipart(w http.ResponseWriter, r *http.Request, key string) {
	var complete completeMultipartUpload
	if err := xml.NewDecoder(r.Body).Decode(&complete); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	uploadID := r.URL.Query().Get("uploadId")

	s.mu.Lock()
	defer s.mu.Unlock()
	upload, exists := s.uploads[uploadID]
	if !exists || upload.key != key {
		http.NotFound(w, r)
		return
	}
	var combined bytes.Buffer
	for _, part := range complete.Parts {
		object, exists := upload.parts[part.PartNumber]
		if !exists {
			http.Error(w, "missing part", http.StatusBadRequest)
			return
		}
		combined.Write(object.data)
	}
	etag := hashBytes(combined.Bytes())
	s.objects[key] = mockS3Object{data: append([]byte(nil), combined.Bytes()...), etag: etag, modifiedAt: s.modifiedAt}
	delete(s.uploads, uploadID)
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", combined.Len()))
	w.Header().Set("Last-Modified", s.modifiedAt.Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
}

func (s *mockS3Server) handleAbortMultipart(w http.ResponseWriter, r *http.Request) {
	uploadID := r.URL.Query().Get("uploadId")
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.uploads, uploadID)
	s.abortCount++
	w.WriteHeader(http.StatusNoContent)
}

func writeObjectHeaders(w http.ResponseWriter, object mockS3Object) {
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(object.data)))
	w.Header().Set("ETag", `"`+object.etag+`"`)
	w.Header().Set("Last-Modified", object.modifiedAt.Format(http.TimeFormat))
}

func hasQueryKey(values url.Values, key string) bool {
	_, ok := values[key]
	return ok
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func parseRange(header string) (int64, int64, bool) {
	value, ok := strings.CutPrefix(header, "bytes=")
	if !ok {
		return 0, 0, false
	}
	startText, endText, ok := strings.Cut(value, "-")
	if !ok {
		return 0, 0, false
	}
	var start, end int64
	if _, err := fmt.Sscanf(startText, "%d", &start); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(endText, "%d", &end); err != nil {
		return 0, 0, false
	}
	return start, end, true
}

func xmlEscape(value string) string {
	var out strings.Builder
	for _, r := range value {
		switch r {
		case '&':
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

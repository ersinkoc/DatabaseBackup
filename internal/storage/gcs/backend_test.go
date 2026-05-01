package gcs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/storage"
	"github.com/kronos/kronos/internal/storage/storagetest"
)

func TestBackendConformance(t *testing.T) {
	t.Parallel()

	storagetest.RunBackendConformance(t, func(t *testing.T) storage.Backend {
		t.Helper()
		server := newGCSTestServer(t)
		backend, err := New(Config{
			Name:        "gcs",
			Bucket:      "repo",
			Endpoint:    server.URL,
			BearerToken: "test-token",
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		return backend
	}, storagetest.ConformanceOptions{InvalidKeyErrors: true})
}

func TestBackendCRUDAgainstHTTPServer(t *testing.T) {
	t.Parallel()

	server := newGCSTestServer(t)
	backend, err := New(Config{
		Name:        "gcs",
		Bucket:      "repo",
		Endpoint:    server.URL,
		BearerToken: "test-token",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	payload := []byte("kronos gcs backend payload")
	info, err := backend.Put(context.Background(), "data/object.txt", bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if info.Key != "data/object.txt" || info.Size != int64(len(payload)) || info.ETag == "" {
		t.Fatalf("Put() info = %#v", info)
	}
	if _, err := backend.Put(context.Background(), "data/object.txt", bytes.NewReader(payload), int64(len(payload))); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("Put(conflict) error = %v, want ErrConflict", err)
	}

	stream, gotInfo, err := backend.Get(context.Background(), "data/object.txt")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	got, err := io.ReadAll(stream)
	closeErr := stream.Close()
	if err != nil {
		t.Fatalf("ReadAll(Get()) error = %v", err)
	}
	if closeErr != nil {
		t.Fatalf("Close(Get()) error = %v", closeErr)
	}
	if !bytes.Equal(got, payload) || gotInfo.Size != int64(len(payload)) {
		t.Fatalf("Get() = %q %#v", got, gotInfo)
	}

	rangeStream, err := backend.GetRange(context.Background(), "data/object.txt", 7, 3)
	if err != nil {
		t.Fatalf("GetRange() error = %v", err)
	}
	gotRange, err := io.ReadAll(rangeStream)
	closeErr = rangeStream.Close()
	if err != nil {
		t.Fatalf("ReadAll(GetRange()) error = %v", err)
	}
	if closeErr != nil {
		t.Fatalf("Close(GetRange()) error = %v", closeErr)
	}
	if string(gotRange) != "gcs" {
		t.Fatalf("GetRange() = %q, want gcs", gotRange)
	}

	head, err := backend.Head(context.Background(), "data/object.txt")
	if err != nil {
		t.Fatalf("Head() error = %v", err)
	}
	if head.Size != int64(len(payload)) {
		t.Fatalf("Head() = %#v", head)
	}
	exists, err := backend.Exists(context.Background(), "data/object.txt")
	if err != nil || !exists {
		t.Fatalf("Exists(existing) = %v, %v; want true, nil", exists, err)
	}
	missing, err := backend.Exists(context.Background(), "data/missing.txt")
	if err != nil || missing {
		t.Fatalf("Exists(missing) = %v, %v; want false, nil", missing, err)
	}

	page, err := backend.List(context.Background(), "data/", "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "data/object.txt" {
		t.Fatalf("List() = %#v", page)
	}
	if err := backend.Delete(context.Background(), "data/object.txt"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	exists, err = backend.Exists(context.Background(), "data/object.txt")
	if err != nil || exists {
		t.Fatalf("Exists(deleted) = %v, %v; want false, nil", exists, err)
	}
}

func TestNewAndKeyValidation(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{}); err == nil {
		t.Fatal("New(missing bucket) error = nil, want error")
	}
	if _, err := New(Config{Bucket: "repo", Endpoint: "://bad"}); err == nil {
		t.Fatal("New(invalid endpoint) error = nil, want error")
	}
	for _, key := range []string{"", ".", "/abs", "../escape", "a/../b", `a\b`} {
		var invalid storage.InvalidKeyError
		if err := validateKey(key); !errors.As(err, &invalid) {
			t.Fatalf("validateKey(%q) error = %v, want InvalidKeyError", key, err)
		}
	}
}

func TestBearerTokenAuthorization(t *testing.T) {
	t.Parallel()

	backend, err := New(Config{Bucket: "repo", Endpoint: "https://storage.example.test", BearerToken: "token"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	req, err := backend.objectRequest(context.Background(), http.MethodGet, "data/object.txt", nil, nil)
	if err != nil {
		t.Fatalf("objectRequest() error = %v", err)
	}
	if err := backend.authorize(req); err != nil {
		t.Fatalf("authorize() error = %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer token" {
		t.Fatalf("Authorization = %q, want Bearer token", got)
	}
}

type gcsTestServer struct {
	*httptest.Server
	mu      sync.Mutex
	objects map[string][]byte
}

func newGCSTestServer(t *testing.T) *gcsTestServer {
	t.Helper()
	server := &gcsTestServer{objects: map[string][]byte{}}
	server.Server = httptest.NewServer(http.HandlerFunc(server.serveHTTP))
	t.Cleanup(server.Close)
	return server
}

func (s *gcsTestServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(w, "missing bearer", http.StatusForbidden)
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/upload/storage/v1/b/repo/o"):
		s.upload(w, r)
	case strings.HasPrefix(r.URL.Path, "/storage/v1/b/repo/o/"):
		s.object(w, r)
	case r.URL.Path == "/storage/v1/b/repo/o":
		s.list(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *gcsTestServer) upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Query().Get("uploadType") != "media" {
		http.Error(w, "bad upload", http.StatusBadRequest)
		return
	}
	name := r.URL.Query().Get("name")
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[name]; ok && r.URL.Query().Get("ifGenerationMatch") == "0" {
		http.Error(w, "exists", http.StatusPreconditionFailed)
		return
	}
	s.objects[name] = data
	writeGCSJSON(w, objectResourceFor(name, data))
}

func (s *gcsTestServer) object(w http.ResponseWriter, r *http.Request) {
	name, err := urlPathUnescape(strings.TrimPrefix(r.URL.Path, "/storage/v1/b/repo/o/"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.get(w, r, name)
	case http.MethodDelete:
		s.delete(w, name)
	default:
		http.Error(w, "bad method", http.StatusMethodNotAllowed)
	}
}

func (s *gcsTestServer) get(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	data, ok := s.objects[name]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("alt") != "media" {
		writeGCSJSON(w, objectResourceFor(name, data))
		return
	}
	if value := r.Header.Get("Range"); value != "" {
		start, end := parseTestRange(value, len(data))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", end-start))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start:end])
		return
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	_, _ = w.Write(data)
}

func (s *gcsTestServer) delete(w http.ResponseWriter, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[name]; !ok {
		http.Error(w, "missing", http.StatusNotFound)
		return
	}
	delete(s.objects, name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *gcsTestServer) list(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	s.mu.Lock()
	objects := make(map[string][]byte, len(s.objects))
	for name, data := range s.objects {
		if strings.HasPrefix(name, prefix) {
			objects[name] = append([]byte(nil), data...)
		}
	}
	s.mu.Unlock()
	var names []string
	for name := range objects {
		names = append(names, name)
	}
	sort.Strings(names)
	response := listResponse{}
	for _, name := range names {
		response.Items = append(response.Items, objectResourceFor(name, objects[name]))
	}
	writeGCSJSON(w, response)
}

func objectResourceFor(name string, data []byte) objectResource {
	sum := sha256.Sum256(data)
	return objectResource{
		Name:    name,
		Size:    fmt.Sprintf("%d", len(data)),
		MD5Hash: base64.StdEncoding.EncodeToString(sum[:]),
		Updated: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func writeGCSJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func parseTestRange(value string, size int) (int, int) {
	value = strings.TrimPrefix(value, "bytes=")
	parts := strings.SplitN(value, "-", 2)
	start, _ := strconv.Atoi(parts[0])
	end, _ := strconv.Atoi(parts[1])
	if end >= size {
		end = size - 1
	}
	return start, end + 1
}

func urlPathUnescape(value string) (string, error) {
	value = strings.ReplaceAll(value, "%2F", "%2f")
	return url.PathUnescape(value)
}

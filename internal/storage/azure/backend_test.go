package azure

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
		server := newAzureTestServer(t)
		backend, err := New(Config{
			Name:        "az",
			AccountName: "devstoreaccount1",
			Container:   "repo",
			Endpoint:    server.URL,
			SASToken:    "sv=test&sig=fake",
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		return backend
	}, storagetest.ConformanceOptions{InvalidKeyErrors: true})
}

func TestBackendCRUDAgainstHTTPServer(t *testing.T) {
	t.Parallel()

	server := newAzureTestServer(t)
	backend, err := New(Config{
		Name:        "az",
		AccountName: "devstoreaccount1",
		Container:   "repo",
		Endpoint:    server.URL,
		SASToken:    "sv=test&sig=fake",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	payload := []byte("kronos azure blob payload")
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

	rangeStream, err := backend.GetRange(context.Background(), "data/object.txt", 7, 5)
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
	if string(gotRange) != "azure" {
		t.Fatalf("GetRange() = %q, want azure", gotRange)
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

	if _, err := New(Config{Container: "repo"}); err == nil {
		t.Fatal("New(missing account/endpoint) error = nil, want error")
	}
	if _, err := New(Config{AccountName: "acct"}); err == nil {
		t.Fatal("New(missing container) error = nil, want error")
	}
	if _, err := New(Config{AccountName: "acct", Container: "repo", AccountKey: "not-base64"}); err == nil {
		t.Fatal("New(invalid key) error = nil, want error")
	}
	for _, key := range []string{"", ".", "/abs", "../escape", "a/../b", `a\b`} {
		var invalid storage.InvalidKeyError
		if err := validateKey(key); !errors.As(err, &invalid) {
			t.Fatalf("validateKey(%q) error = %v, want InvalidKeyError", key, err)
		}
	}
}

func TestSharedKeyAuthorizationHeader(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand.Read() error = %v", err)
	}
	backend, err := New(Config{
		AccountName: "acct",
		AccountKey:  base64.StdEncoding.EncodeToString(key),
		Container:   "repo",
		Endpoint:    "https://acct.blob.core.windows.net",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	req, err := backend.newRequest(context.Background(), http.MethodPut, "data/blob.txt", nil, nil)
	if err != nil {
		t.Fatalf("newRequest() error = %v", err)
	}
	req.Header.Set("Content-Length", "5")
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	if err := backend.sign(req); err != nil {
		t.Fatalf("sign() error = %v", err)
	}
	if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "SharedKey acct:") {
		t.Fatalf("Authorization = %q, want SharedKey acct prefix", got)
	}
}

type azureTestServer struct {
	*httptest.Server
	mu      sync.Mutex
	objects map[string][]byte
}

func newAzureTestServer(t *testing.T) *azureTestServer {
	t.Helper()
	server := &azureTestServer{objects: map[string][]byte{}}
	server.Server = httptest.NewServer(http.HandlerFunc(server.serveHTTP))
	t.Cleanup(server.Close)
	return server
}

func (s *azureTestServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("sig") != "fake" {
		http.Error(w, "missing sas", http.StatusForbidden)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 1 || parts[0] != "repo" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("comp") == "list" {
		s.list(w, r)
		return
	}
	key := strings.Join(parts[1:], "/")
	switch r.Method {
	case http.MethodPut:
		s.put(w, r, key)
	case http.MethodGet:
		s.get(w, r, key)
	case http.MethodHead:
		s.head(w, r, key)
	case http.MethodDelete:
		s.delete(w, key)
	default:
		http.Error(w, "bad method", http.StatusMethodNotAllowed)
	}
}

func (s *azureTestServer) put(w http.ResponseWriter, r *http.Request, key string) {
	if r.Header.Get("x-ms-blob-type") != "BlockBlob" {
		http.Error(w, "missing blob type", http.StatusBadRequest)
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[key]; ok && r.Header.Get("If-None-Match") == "*" {
		http.Error(w, "exists", http.StatusPreconditionFailed)
		return
	}
	s.objects[key] = data
	w.Header().Set("ETag", `"etag-`+key+`"`)
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusCreated)
}

func (s *azureTestServer) get(w http.ResponseWriter, r *http.Request, key string) {
	s.mu.Lock()
	data, ok := s.objects[key]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("ETag", `"etag-`+key+`"`)
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	if value := r.Header.Get("x-ms-range"); value != "" {
		start, end := parseTestRange(value, len(data))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", end-start))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start:end])
		return
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	_, _ = w.Write(data)
}

func (s *azureTestServer) head(w http.ResponseWriter, r *http.Request, key string) {
	s.mu.Lock()
	data, ok := s.objects[key]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("ETag", `"etag-`+key+`"`)
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
}

func (s *azureTestServer) delete(w http.ResponseWriter, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[key]; !ok {
		http.Error(w, "missing", http.StatusNotFound)
		return
	}
	delete(s.objects, key)
	w.WriteHeader(http.StatusAccepted)
}

func (s *azureTestServer) list(w http.ResponseWriter, r *http.Request) {
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
	type properties struct {
		LastModified  string `xml:"Last-Modified"`
		ETag          string `xml:"Etag"`
		ContentLength int    `xml:"Content-Length"`
	}
	type item struct {
		Name       string     `xml:"Name"`
		Properties properties `xml:"Properties"`
	}
	var response struct {
		XMLName    xml.Name `xml:"EnumerationResults"`
		Blobs      []item   `xml:"Blobs>Blob"`
		NextMarker string   `xml:"NextMarker"`
	}
	for _, name := range names {
		response.Blobs = append(response.Blobs, item{
			Name: name,
			Properties: properties{
				LastModified:  time.Now().UTC().Format(http.TimeFormat),
				ETag:          `"etag-` + name + `"`,
				ContentLength: len(objects[name]),
			},
		})
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(response)
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

package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/kvstore"
)

func newTestServerHandler(t *testing.T) (*testHandler, func()) {
	t.Helper()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "api_integration.db"))
	if err != nil {
		t.Fatalf("kvstore.Open() error = %v", err)
	}

	clock := core.NewFakeClock(time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC))
	tokens, err := NewTokenStore(db, clock)
	if err != nil {
		db.Close()
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	users, err := NewUserStore(db)
	if err != nil {
		db.Close()
		t.Fatalf("NewUserStore() error = %v", err)
	}
	jobs, err := NewJobStore(db)
	if err != nil {
		db.Close()
		t.Fatalf("NewJobStore() error = %v", err)
	}

	return &testHandler{tokens: tokens, users: users, jobs: jobs, clock: clock}, func() { db.Close() }
}

type testHandler struct {
	tokens *TokenStore
	users  *UserStore
	jobs   *JobStore
	clock  *core.FakeClock
}

func (h *testHandler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/api/v1/tokens":
		switch r.Method {
		case http.MethodGet:
			h.handleListTokens(w)
		case http.MethodPost:
			h.handleCreateToken(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case len(path) > 15 && path[:15] == "/api/v1/tokens/":
		id := path[15:]
		switch {
		case r.Method == http.MethodGet:
			h.handleGetToken(w, id)
		case r.Method == http.MethodPost && len(id) > 7 && id[len(id)-7:] == "/revoke":
			h.handleRevokeToken(w, id[:len(id)-7])
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case path == "/api/v1/jobs":
		switch r.Method {
		case http.MethodGet:
			h.handleListJobs(w)
		case http.MethodPost:
			h.handleCreateJob(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case path == "/api/v1/jobs/claim":
		if r.Method == http.MethodPost {
			h.handleClaimJob(w)
		}
	case len(path) > 11 && path[:11] == "/api/v1/jobs/":
		id := path[11:]
		switch {
		case r.Method == http.MethodGet:
			h.handleGetJob(w, id)
		case r.Method == http.MethodDelete:
			h.handleCancelJob(w, id)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case path == "/api/v1/users":
		if r.Method == http.MethodPost {
			h.handleCreateUser(w, r)
		}
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (h *testHandler) handleListTokens(w http.ResponseWriter) {
	tokens, err := h.tokens.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(tokens)
}

func (h *testHandler) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string    `json:"name"`
		UserID    string    `json:"user_id"`
		Scopes    []string  `json:"scopes"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	created, err := h.tokens.Create(req.Name, core.ID(req.UserID), req.Scopes, req.ExpiresAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(created)
}

func (h *testHandler) handleGetToken(w http.ResponseWriter, id string) {
	token, ok, err := h.tokens.Get(core.ID(id))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(token)
}

func (h *testHandler) handleRevokeToken(w http.ResponseWriter, id string) {
	_, err := h.tokens.Revoke(core.ID(id))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *testHandler) handleListJobs(w http.ResponseWriter) {
	jobs, err := h.jobs.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(jobs)
}

func (h *testHandler) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var job core.Job
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if job.ID.IsZero() {
		id, err := core.NewID(h.clock)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		job.ID = id
	}
	if err := h.jobs.Save(job); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(job)
}

func (h *testHandler) handleClaimJob(w http.ResponseWriter) {
	jobs, err := h.jobs.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, job := range jobs {
		if job.Status == core.JobStatusQueued {
			job.Status = core.JobStatusRunning
			job.AgentID = "test-agent"
			h.jobs.Save(job)
			json.NewEncoder(w).Encode(job)
			return
		}
	}
	http.Error(w, "no jobs available", http.StatusNotFound)
}

func (h *testHandler) handleGetJob(w http.ResponseWriter, id string) {
	job, ok, err := h.jobs.Get(core.ID(id))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(job)
}

func (h *testHandler) handleCancelJob(w http.ResponseWriter, id string) {
	job, ok, err := h.jobs.Get(core.ID(id))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	job.Status = core.JobStatusCanceled
	job.EndedAt = h.clock.Now()
	if err := h.jobs.Save(job); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *testHandler) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Pass string `json:"pass"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := core.NewID(h.clock)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	user := core.User{
		ID:          id,
		Email:       req.Name + "@local",
		DisplayName: req.Name,
		Role:        core.RoleAdmin,
		CreatedAt:   h.clock.Now(),
	}
	if err := h.users.Save(user); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"id": string(user.ID)})
}

// TestJobIDRoundTrip tests that Job IDs survive JSON encode/decode and store operations.
func TestJobIDRoundTrip(t *testing.T) {
	t.Parallel()

	h, cleanup := newTestServerHandler(t)
	defer cleanup()

	jobID, err := core.NewID(h.clock)
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}

	job := core.Job{
		ID:        jobID,
		Operation: core.JobOperationBackup,
		TargetID:  core.ID("target-1"),
		StorageID: core.ID("storage-1"),
		Status:    core.JobStatusQueued,
		QueuedAt:  h.clock.Now(),
	}

	// Save job
	if err := h.jobs.Save(job); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// List jobs
	jobs, err := h.jobs.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs count = %d, want 1", len(jobs))
	}
	if jobs[0].ID != job.ID {
		t.Fatalf("listed job.ID = %s, want %s", jobs[0].ID, job.ID)
	}

	// Get by ID
	got, ok, err := h.jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if got.ID != job.ID {
		t.Fatalf("Get() job.ID = %s, want %s", got.ID, job.ID)
	}

	// JSON round-trip
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded core.Job
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.ID != job.ID {
		t.Fatalf("JSON round-trip ID = %s, want %s", decoded.ID, job.ID)
	}
}

// TestTokenCreateScopes verifies token scopes are preserved through API operations.
func TestTokenCreateScopes(t *testing.T) {
	t.Parallel()

	h, cleanup := newTestServerHandler(t)
	defer cleanup()

	scopes := []string{"backup:read", "backup:write", "job:read"}
	tokenReq := struct {
		Name      string    `json:"name"`
		UserID    string    `json:"user_id"`
		Scopes    []string  `json:"scopes"`
		ExpiresAt time.Time `json:"expires_at"`
	}{
		Name:      "scoped-token",
		UserID:    "user-1",
		Scopes:    scopes,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	buf, _ := json.Marshal(tokenReq)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", bytes.NewReader(buf))
	w := httptest.NewRecorder()
	h.serveHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create token status = %d, want 200", w.Code)
	}

	var created struct {
		Token struct {
			Scopes []string `json:"scopes"`
		} `json:"token"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode error = %v", err)
	}
	if len(created.Token.Scopes) != len(scopes) {
		t.Fatalf("scopes = %v, want %v", created.Token.Scopes, scopes)
	}
}

// TestAPINotFound verifies 404 for unknown paths.
func TestAPINotFound(t *testing.T) {
	t.Parallel()

	h, cleanup := newTestServerHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nonexistent", nil)
	w := httptest.NewRecorder()
	h.serveHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("nonexistent path status = %d, want 404", w.Code)
	}
}

// TestAPIMethodNotAllowed verifies 405 for wrong methods.
func TestAPIMethodNotAllowed(t *testing.T) {
	t.Parallel()

	h, cleanup := newTestServerHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/tokens", nil)
	w := httptest.NewRecorder()
	h.serveHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete tokens status = %d, want 405", w.Code)
	}
}

// TestAPIBadJSON verifies 400 for malformed JSON.
func TestAPIBadJSON(t *testing.T) {
	t.Parallel()

	h, cleanup := newTestServerHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	h.serveHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad json status = %d, want 400", w.Code)
	}
}

// Suppress unused variable warning
var _ = io.Discard
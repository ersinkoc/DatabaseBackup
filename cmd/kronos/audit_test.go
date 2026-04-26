package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kronos/kronos/internal/obs"
)

func TestRunAuditListAndVerify(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/audit":
			if r.Method != http.MethodGet {
				t.Fatalf("audit list method = %s", r.Method)
			}
			if r.URL.RawQuery != "" {
				t.Fatalf("audit list query = %q, want empty", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"events":[{"seq":1,"actor_id":"admin","action":"target.created","resource_type":"target","resource_id":"target-1"},{"seq":2,"action":"backup.requested","resource_type":"job","resource_id":"job-1","metadata":{"target_id":"target-1"}}]}`)
		case "/api/v1/audit/verify":
			if r.Method != http.MethodPost {
				t.Fatalf("audit verify method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"audit", "list", "--server", server.URL}); err != nil {
		t.Fatalf("audit list error = %v", err)
	}
	if !strings.Contains(out.String(), `"action":"target.created"`) {
		t.Fatalf("audit list output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"audit", "verify", "--server", server.URL}); err != nil {
		t.Fatalf("audit verify error = %v", err)
	}
	if !strings.Contains(out.String(), `"ok":true`) {
		t.Fatalf("audit verify output = %q", out.String())
	}
}

func TestRunAuditListPassesFilters(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/audit" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		wants := map[string]string{
			"actor_id":      "admin",
			"action":        "target.created",
			"resource_type": "target",
			"resource_id":   "target-1",
			"since":         "7d",
			"until":         "2026-04-25T12:00:00Z",
			"limit":         "10",
		}
		for key, want := range wants {
			if got := query.Get(key); got != want {
				t.Fatalf("query %s = %q, want %q", key, got, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"events":[]}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), &out, []string{
		"audit", "list", "--server", server.URL,
		"--actor", "admin",
		"--action", "target.created",
		"--resource-type", "target",
		"--resource-id", "target-1",
		"--since", "7d",
		"--until", "2026-04-25T12:00:00Z",
		"--limit", "10",
	})
	if err != nil {
		t.Fatalf("audit list error = %v", err)
	}
}

func TestFetchAuditEventsPropagatesRequestIDAndErrors(t *testing.T) {
	t.Parallel()

	var gotRequestID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestID = r.Header.Get("X-Kronos-Request-ID")
		w.Header().Set("X-Kronos-Request-ID", "req-audit-error-1")
		http.Error(w, "audit unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := fetchAuditEvents(obs.WithRequestID(context.Background(), "req-audit-fetch-1"), server.Client(), server.URL, nil)
	if err == nil {
		t.Fatal("fetchAuditEvents() error = nil, want error")
	}
	if gotRequestID != "req-audit-fetch-1" {
		t.Fatalf("X-Kronos-Request-ID = %q, want req-audit-fetch-1", gotRequestID)
	}
	if text := err.Error(); !strings.Contains(text, "request_id=req-audit-error-1") || !strings.Contains(text, "audit unavailable") {
		t.Fatalf("fetchAuditEvents() error = %q", text)
	}
}

func TestRunAuditTailAndSearch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/audit" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"events":[{"seq":1,"action":"target.created","resource_type":"target","resource_id":"target-1"},{"seq":2,"action":"storage.created","resource_type":"storage","resource_id":"storage-1"},{"seq":3,"action":"backup.requested","resource_type":"job","resource_id":"job-1","metadata":{"target_id":"target-1"}}]}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"audit", "tail", "--server", server.URL, "--limit", "2"}); err != nil {
		t.Fatalf("audit tail error = %v", err)
	}
	if strings.Contains(out.String(), `"seq":1`) || !strings.Contains(out.String(), `"seq":2`) || !strings.Contains(out.String(), `"seq":3`) {
		t.Fatalf("audit tail output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"audit", "search", "--server", server.URL, "--query", "storage"}); err != nil {
		t.Fatalf("audit search error = %v", err)
	}
	if !strings.Contains(out.String(), `"storage.created"`) || strings.Contains(out.String(), `"backup.requested"`) {
		t.Fatalf("audit search output = %q", out.String())
	}
}

func TestRunAuditSearchPassesFilters(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/audit" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("action"); got != "backup.requested" {
			t.Fatalf("action query = %q, want backup.requested", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"events":[{"seq":1,"action":"backup.requested","resource_type":"job","resource_id":"job-1"},{"seq":2,"action":"backup.failed","resource_type":"job","resource_id":"job-2"}]}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"audit", "search", "--server", server.URL, "--query", "job-1", "--action", "backup.requested"}); err != nil {
		t.Fatalf("audit search error = %v", err)
	}
	if !strings.Contains(out.String(), `"resource_id":"job-1"`) || strings.Contains(out.String(), `"resource_id":"job-2"`) {
		t.Fatalf("audit search output = %q", out.String())
	}
}

func TestRunAuditRejectsUnknownSubcommand(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"audit", "missing"}); err == nil {
		t.Fatal("audit missing error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"audit", "search"}); err == nil {
		t.Fatal("audit search without query error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"audit", "tail", "--limit", "-1"}); err == nil {
		t.Fatal("audit tail negative limit error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"audit", "list", "--limit", "-1"}); err == nil {
		t.Fatal("audit list negative limit error = nil, want error")
	}
}

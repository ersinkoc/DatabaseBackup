package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunJobsListCancelAndRetry(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/jobs":
			if r.Method != http.MethodGet {
				t.Fatalf("jobs list method = %s", r.Method)
			}
			query := r.URL.Query()
			if query.Get("status") != "" || query.Get("operation") != "" || query.Get("target_id") != "" || query.Get("storage_id") != "" || query.Get("agent_id") != "" || query.Get("since") != "" || query.Get("until") != "" {
				t.Fatalf("unexpected jobs list query = %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jobs":[{"id":"job-1","status":"queued"}]}`)
		case "/api/v1/jobs/job-1":
			if r.Method != http.MethodGet {
				t.Fatalf("jobs inspect method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"job-1","operation":"backup","status":"queued"}`)
		case "/api/v1/jobs/job-1/cancel":
			if r.Method != http.MethodPost {
				t.Fatalf("jobs cancel method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"job-1","status":"canceled"}`)
		case "/api/v1/jobs/job-1/retry":
			if r.Method != http.MethodPost {
				t.Fatalf("jobs retry method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"job-1","status":"queued"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"jobs", "list", "--server", server.URL}); err != nil {
		t.Fatalf("jobs list error = %v", err)
	}
	if !strings.Contains(out.String(), `"status":"queued"`) {
		t.Fatalf("jobs list output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"jobs", "inspect", "--server", server.URL, "--id", "job-1"}); err != nil {
		t.Fatalf("jobs inspect error = %v", err)
	}
	if !strings.Contains(out.String(), `"operation":"backup"`) {
		t.Fatalf("jobs inspect output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"jobs", "cancel", "--server", server.URL, "--id", "job-1"}); err != nil {
		t.Fatalf("jobs cancel error = %v", err)
	}
	if !strings.Contains(out.String(), `"status":"canceled"`) {
		t.Fatalf("jobs cancel output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"jobs", "retry", "--server", server.URL, "--id", "job-1"}); err != nil {
		t.Fatalf("jobs retry error = %v", err)
	}
	if !strings.Contains(out.String(), `"status":"queued"`) {
		t.Fatalf("jobs retry output = %q", out.String())
	}
}

func TestRunJobsListPassesFilters(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/jobs" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("status") != "running" || query.Get("operation") != "backup" || query.Get("target_id") != "target-1" || query.Get("storage_id") != "storage-1" || query.Get("agent_id") != "agent-1" || query.Get("since") != "72h" || query.Get("until") != "2026-04-25T12:00:00Z" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jobs":[{"id":"job-1","status":"running"}]}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{
		"jobs", "list",
		"--server", server.URL,
		"--status", "running",
		"--operation", "backup",
		"--target", "target-1",
		"--storage", "storage-1",
		"--agent", "agent-1",
		"--since", "72h",
		"--until", "2026-04-25T12:00:00Z",
	}); err != nil {
		t.Fatalf("jobs list filters error = %v", err)
	}
	if !strings.Contains(out.String(), `"job-1"`) {
		t.Fatalf("jobs list filters output = %q", out.String())
	}
}

func TestRunJobsCancelRequiresID(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"jobs", "cancel"}); err == nil {
		t.Fatal("jobs cancel without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"jobs", "inspect"}); err == nil {
		t.Fatal("jobs inspect without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"jobs", "retry"}); err == nil {
		t.Fatal("jobs retry without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"jobs", "list", "--since", "1h", "--until", "2h"}); err == nil {
		t.Fatal("jobs list inverted time range error = nil, want error")
	}
}

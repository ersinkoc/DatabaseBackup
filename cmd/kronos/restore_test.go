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

func TestRunRestorePreview(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/restore/preview" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		defer r.Body.Close()
		var body bytes.Buffer
		if _, err := body.ReadFrom(r.Body); err != nil {
			t.Fatalf("ReadFrom(request) error = %v", err)
		}
		text := body.String()
		if !strings.Contains(text, `"backup_id":"backup-1"`) || !strings.Contains(text, `"target_id":"restore-target"`) || !strings.Contains(text, `"at":"2026-04-25T12:00:00Z"`) {
			t.Fatalf("restore preview request = %q", text)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"backup_id":"backup-1","target_id":"restore-target","steps":[{"backup_id":"backup-1"}]}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"restore", "preview", "--server", server.URL, "--backup", "backup-1", "--target", "restore-target", "--at", "2026-04-25T12:00:00Z"}); err != nil {
		t.Fatalf("restore preview error = %v", err)
	}
	if !strings.Contains(out.String(), `"steps":[{"backup_id":"backup-1"`) {
		t.Fatalf("restore preview output = %q", out.String())
	}
}

func TestRunRestoreStart(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/restore" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		defer r.Body.Close()
		var body bytes.Buffer
		if _, err := body.ReadFrom(r.Body); err != nil {
			t.Fatalf("ReadFrom(request) error = %v", err)
		}
		text := body.String()
		if !strings.Contains(text, `"backup_id":"backup-1"`) || !strings.Contains(text, `"target_id":"restore-target"`) || !strings.Contains(text, `"dry_run":true`) || !strings.Contains(text, `"replace_existing":true`) {
			t.Fatalf("restore start request = %q", text)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"job":{"id":"job-1","operation":"restore","status":"queued"},"plan":{"backup_id":"backup-1","target_id":"restore-target","steps":[{"backup_id":"backup-1"}]}}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"restore", "start", "--server", server.URL, "--backup", "backup-1", "--target", "restore-target", "--dry-run", "--replace-existing"}); err != nil {
		t.Fatalf("restore start error = %v", err)
	}
	if !strings.Contains(out.String(), `"operation":"restore"`) || !strings.Contains(out.String(), `"status":"queued"`) {
		t.Fatalf("restore start output = %q", out.String())
	}
}

func TestRunRestoreStartRequiresConfirmation(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"restore", "start", "--backup", "backup-1"}); err == nil {
		t.Fatal("restore start without --dry-run or --yes error = nil, want error")
	}
}

func TestRunRestorePreviewRequiresFields(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"restore", "preview"}); err == nil {
		t.Fatal("restore preview without backup error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"restore", "preview", "--backup", "backup-1", "--at", "bad"}); err == nil {
		t.Fatal("restore preview with bad at error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"restore", "preview", "--backup", "backup-1", "--at", "2999-01-01T00:00:00Z"}); err == nil {
		t.Fatal("restore preview with future at error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"restore", "start"}); err == nil {
		t.Fatal("restore start without backup error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"restore", "start", "--backup", "backup-1", "--dry-run", "--at", "bad"}); err == nil {
		t.Fatal("restore start with bad at error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"restore", "start", "--backup", "backup-1", "--dry-run", "--at", "2999-01-01T00:00:00Z"}); err == nil {
		t.Fatal("restore start with future at error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"restore", "missing"}); err == nil {
		t.Fatal("restore missing error = nil, want error")
	}
}

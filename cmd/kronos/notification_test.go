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

func TestRunNotificationAddListInspectUpdateRemove(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/notifications":
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"notifications":[{"id":"notification-1","name":"ops"}]}`)
			case http.MethodPost:
				defer r.Body.Close()
				var body bytes.Buffer
				if _, err := body.ReadFrom(r.Body); err != nil {
					t.Fatalf("ReadFrom(add request) error = %v", err)
				}
				text := body.String()
				for _, want := range []string{`"id":"notification-1"`, `"events":["job.failed","job.succeeded"]`, `"webhook_url":"https://hooks.example.com/kronos"`, `"secret":"shared"`, `"enabled":true`} {
					if !strings.Contains(text, want) {
						t.Fatalf("notification add request missing %q in %s", want, text)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"notification-1","name":"ops"}`)
			default:
				t.Fatalf("notifications method = %s", r.Method)
			}
		case "/api/v1/notifications/notification-1":
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"notification-1","name":"ops","events":["job.failed"]}`)
			case http.MethodPut:
				defer r.Body.Close()
				var body bytes.Buffer
				if _, err := body.ReadFrom(r.Body); err != nil {
					t.Fatalf("ReadFrom(update request) error = %v", err)
				}
				text := body.String()
				if !strings.Contains(text, `"name":"ops-updated"`) || !strings.Contains(text, `"enabled":false`) {
					t.Fatalf("notification update request = %s", text)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"notification-1","name":"ops-updated","enabled":false}`)
			case http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("notification method = %s", r.Method)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{
		"notification", "add", "--server", server.URL,
		"--id", "notification-1",
		"--name", "ops",
		"--event", "job.failed,job.succeeded",
		"--webhook-url", "https://hooks.example.com/kronos",
		"--secret", "shared",
	}); err != nil {
		t.Fatalf("notification add error = %v", err)
	}
	if !strings.Contains(out.String(), `"id":"notification-1"`) {
		t.Fatalf("notification add output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"notification", "list", "--server", server.URL}); err != nil {
		t.Fatalf("notification list error = %v", err)
	}
	if !strings.Contains(out.String(), `"notifications":[{"id":"notification-1"`) {
		t.Fatalf("notification list output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"notification", "inspect", "--server", server.URL, "--id", "notification-1"}); err != nil {
		t.Fatalf("notification inspect error = %v", err)
	}
	if !strings.Contains(out.String(), `"events":["job.failed"]`) {
		t.Fatalf("notification inspect output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{
		"notification", "update", "--server", server.URL,
		"--id", "notification-1",
		"--name", "ops-updated",
		"--event", "job.canceled",
		"--webhook-url", "https://hooks.example.com/updated",
		"--enabled=false",
	}); err != nil {
		t.Fatalf("notification update error = %v", err)
	}
	if !strings.Contains(out.String(), `"enabled":false`) {
		t.Fatalf("notification update output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"notification", "remove", "--server", server.URL, "--id", "notification-1"}); err != nil {
		t.Fatalf("notification remove error = %v", err)
	}
	if out.String() != "" {
		t.Fatalf("notification remove output = %q, want empty", out.String())
	}
}

func TestRunNotificationRequiresFields(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"notification"}); err == nil {
		t.Fatal("notification without subcommand error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"notification", "add"}); err == nil {
		t.Fatal("notification add without fields error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"notification", "inspect"}); err == nil {
		t.Fatal("notification inspect without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"notification", "remove"}); err == nil {
		t.Fatal("notification remove without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"notification", "update", "--name", "ops", "--event", "job.failed", "--webhook-url", "https://hooks.example.com"}); err == nil {
		t.Fatal("notification update without id error = nil, want error")
	}
}

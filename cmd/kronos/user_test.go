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

func TestRunUserAddListInspectGrantRemove(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/users":
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"users":[{"id":"user-1","email":"ops@example.com","role":"viewer"}]}`)
			case http.MethodPost:
				defer r.Body.Close()
				var body bytes.Buffer
				if _, err := body.ReadFrom(r.Body); err != nil {
					t.Fatalf("ReadFrom(request) error = %v", err)
				}
				text := body.String()
				if !strings.Contains(text, `"id":"user-1"`) || !strings.Contains(text, `"email":"ops@example.com"`) || !strings.Contains(text, `"role":"viewer"`) {
					t.Fatalf("user add request = %q", text)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"user-1","email":"ops@example.com","role":"viewer"}`)
			default:
				http.Error(w, "bad method", http.StatusMethodNotAllowed)
			}
		case "/api/v1/bootstrap/admin":
			if r.Method != http.MethodPost {
				t.Fatalf("user bootstrap method = %s", r.Method)
			}
			defer r.Body.Close()
			var body bytes.Buffer
			if _, err := body.ReadFrom(r.Body); err != nil {
				t.Fatalf("ReadFrom(bootstrap request) error = %v", err)
			}
			text := body.String()
			if !strings.Contains(text, `"id":"admin"`) || !strings.Contains(text, `"email":"admin@example.com"`) || !strings.Contains(text, `"token_name":"setup"`) {
				t.Fatalf("user bootstrap request = %q", text)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"user":{"id":"admin","email":"admin@example.com","role":"admin"},"token":{"token":{"id":"token-1","user_id":"admin","name":"setup","scopes":["admin:*"]},"secret":"kro_secret"}}`)
		case "/api/v1/users/user-1/grant":
			if r.Method != http.MethodPost {
				t.Fatalf("user grant method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"user-1","email":"ops@example.com","role":"operator"}`)
		case "/api/v1/users/user-1":
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"user-1","email":"ops@example.com","display_name":"Ops","role":"viewer"}`)
			case http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			default:
				http.Error(w, "bad method", http.StatusMethodNotAllowed)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"user", "bootstrap", "--server", server.URL, "--id", "admin", "--email", "admin@example.com", "--display-name", "Admin", "--token-name", "setup"}); err != nil {
		t.Fatalf("user bootstrap error = %v", err)
	}
	if !strings.Contains(out.String(), `"secret":"kro_secret"`) {
		t.Fatalf("user bootstrap output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"user", "add", "--server", server.URL, "--id", "user-1", "--email", "ops@example.com", "--display-name", "Ops"}); err != nil {
		t.Fatalf("user add error = %v", err)
	}
	if !strings.Contains(out.String(), `"id":"user-1"`) {
		t.Fatalf("user add output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"user", "list", "--server", server.URL}); err != nil {
		t.Fatalf("user list error = %v", err)
	}
	if !strings.Contains(out.String(), `"users":[{"id":"user-1"`) {
		t.Fatalf("user list output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"user", "inspect", "--server", server.URL, "--id", "user-1"}); err != nil {
		t.Fatalf("user inspect error = %v", err)
	}
	if !strings.Contains(out.String(), `"display_name":"Ops"`) {
		t.Fatalf("user inspect output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"user", "grant", "--server", server.URL, "--id", "user-1", "--role", "operator"}); err != nil {
		t.Fatalf("user grant error = %v", err)
	}
	if !strings.Contains(out.String(), `"role":"operator"`) {
		t.Fatalf("user grant output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"user", "remove", "--server", server.URL, "--id", "user-1"}); err != nil {
		t.Fatalf("user remove error = %v", err)
	}
	if out.String() != "" {
		t.Fatalf("user remove output = %q, want empty", out.String())
	}
}

func TestRunUserRequiresFields(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"user", "bootstrap", "--display-name", "Admin"}); err == nil {
		t.Fatal("user bootstrap without email error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"user", "bootstrap", "--email", "admin@example.com"}); err == nil {
		t.Fatal("user bootstrap without display name error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"user", "add", "--display-name", "Ops"}); err == nil {
		t.Fatal("user add without email error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"user", "add", "--email", "ops@example.com"}); err == nil {
		t.Fatal("user add without display name error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"user", "grant", "--role", "operator"}); err == nil {
		t.Fatal("user grant without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"user", "grant", "--id", "user-1"}); err == nil {
		t.Fatal("user grant without role error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"user", "inspect"}); err == nil {
		t.Fatal("user inspect without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"user", "remove"}); err == nil {
		t.Fatal("user remove without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"user", "missing"}); err == nil {
		t.Fatal("user missing error = nil, want error")
	}
}

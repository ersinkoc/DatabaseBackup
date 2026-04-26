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

func TestRunTokenCreateListInspectRevoke(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tokens":
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"tokens":[{"id":"token-1","name":"ci"}]}`)
			case http.MethodPost:
				defer r.Body.Close()
				var body bytes.Buffer
				if _, err := body.ReadFrom(r.Body); err != nil {
					t.Fatalf("ReadFrom(request) error = %v", err)
				}
				text := body.String()
				if !strings.Contains(text, `"name":"ci"`) || !strings.Contains(text, `"user_id":"user-1"`) || !strings.Contains(text, `"backup:read"`) {
					t.Fatalf("token create request = %q", text)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"token":{"id":"token-1","name":"ci"},"secret":"kro_secret"}`)
			default:
				http.Error(w, "bad method", http.StatusMethodNotAllowed)
			}
		case "/api/v1/tokens/token-1/revoke":
			if r.Method != http.MethodPost {
				t.Fatalf("token revoke method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"token-1","name":"ci","revoked_at":"2026-04-25T12:00:00Z"}`)
		case "/api/v1/tokens/token-1":
			if r.Method != http.MethodGet {
				t.Fatalf("token inspect method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"token-1","name":"ci","user_id":"user-1","scopes":["backup:read"]}`)
		case "/api/v1/auth/verify":
			if r.Method != http.MethodPost {
				t.Fatalf("token verify method = %s", r.Method)
			}
			if r.Header.Get("Authorization") != "Bearer kro_secret" {
				t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("X-Kronos-Request-ID") == "" {
				t.Fatal("token verify request id header is empty")
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"token":{"id":"token-1","name":"ci"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"token", "create", "--server", server.URL, "--name", "ci", "--user", "user-1", "--scope", "backup:read,backup:write"}); err != nil {
		t.Fatalf("token create error = %v", err)
	}
	if !strings.Contains(out.String(), `"secret":"kro_secret"`) {
		t.Fatalf("token create output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"token", "list", "--server", server.URL}); err != nil {
		t.Fatalf("token list error = %v", err)
	}
	if !strings.Contains(out.String(), `"tokens":[{"id":"token-1"`) {
		t.Fatalf("token list output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"token", "inspect", "--server", server.URL, "--id", "token-1"}); err != nil {
		t.Fatalf("token inspect error = %v", err)
	}
	if !strings.Contains(out.String(), `"user_id":"user-1"`) {
		t.Fatalf("token inspect output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"token", "revoke", "--server", server.URL, "--id", "token-1"}); err != nil {
		t.Fatalf("token revoke error = %v", err)
	}
	if !strings.Contains(out.String(), `"revoked_at"`) {
		t.Fatalf("token revoke output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"token", "verify", "--server", server.URL, "--secret", "kro_secret"}); err != nil {
		t.Fatalf("token verify error = %v", err)
	}
	if !strings.Contains(out.String(), `"token":{"id":"token-1"`) {
		t.Fatalf("token verify output = %q", out.String())
	}
}

func TestRunTokenVerifyErrorIncludesRequestID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Kronos-Request-ID"); got != "req-token-1" {
			t.Fatalf("X-Kronos-Request-ID = %q, want req-token-1", got)
		}
		w.Header().Set("X-Kronos-Request-ID", "req-token-1")
		http.Error(w, "too many auth attempts", http.StatusTooManyRequests)
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), &out, []string{"token", "verify", "--server", server.URL, "--secret", "kro_secret", "--request-id", "req-token-1"})
	if err == nil {
		t.Fatal("token verify error = nil, want error")
	}
	if text := err.Error(); !strings.Contains(text, "request_id=req-token-1") || !strings.Contains(text, "too many auth attempts") {
		t.Fatalf("token verify error = %q", text)
	}
}

func TestRunTokenRequiresFields(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"token", "create", "--user", "user-1", "--scope", "backup:read"}); err == nil {
		t.Fatal("token create without name error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"token", "create", "--name", "ci", "--scope", "backup:read"}); err == nil {
		t.Fatal("token create without user error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"token", "create", "--name", "ci", "--user", "user-1"}); err == nil {
		t.Fatal("token create without scope error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"token", "inspect"}); err == nil {
		t.Fatal("token inspect without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"token", "revoke"}); err == nil {
		t.Fatal("token revoke without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"token", "missing"}); err == nil {
		t.Fatal("token missing error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"token", "verify"}); err == nil {
		t.Fatal("token verify without secret error = nil, want error")
	}
}

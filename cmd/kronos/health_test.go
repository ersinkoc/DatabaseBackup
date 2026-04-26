package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kronos/kronos/internal/obs"
)

func TestRunHealth(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/healthz" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get(obs.RequestIDHeader) != "req-health-1" {
			t.Fatalf("%s = %q", obs.RequestIDHeader, r.Header.Get(obs.RequestIDHeader))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","projects":1}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"--server", server.URL, "--request-id", "req-health-1", "--output", "table", "health"}); err != nil {
		t.Fatalf("health error = %v", err)
	}
	text := out.String()
	for _, want := range []string{"KEY", "VALUE", "projects", "1", "status", "ok"} {
		if !strings.Contains(text, want) {
			t.Fatalf("health output missing %q: %q", want, text)
		}
	}
}

func TestRunHealthRejectsArgs(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"health", "extra"}); err == nil {
		t.Fatal("health extra arg error = nil, want error")
	}
}

func TestRunReady(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/readyz" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get(obs.RequestIDHeader) != "req-ready-1" {
			t.Fatalf("%s = %q", obs.RequestIDHeader, r.Header.Get(obs.RequestIDHeader))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","checks":{"jobs":"ok","audit":"ok"}}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"--server", server.URL, "--request-id", "req-ready-1", "--output", "table", "ready"}); err != nil {
		t.Fatalf("ready error = %v", err)
	}
	text := out.String()
	for _, want := range []string{"KEY", "VALUE", "checks", `"audit":"ok"`, "status"} {
		if !strings.Contains(text, want) {
			t.Fatalf("ready output missing %q: %q", want, text)
		}
	}
}

func TestRunReadyRejectsArgs(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"ready", "extra"}); err == nil {
		t.Fatal("ready extra arg error = nil, want error")
	}
}

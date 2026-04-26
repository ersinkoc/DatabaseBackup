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

func TestRunMetrics(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/metrics" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer metrics-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get(obs.RequestIDHeader) != "req-metrics-1" {
			t.Fatalf("%s = %q", obs.RequestIDHeader, r.Header.Get(obs.RequestIDHeader))
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Write([]byte("kronos_agents{status=\"healthy\"} 1\n"))
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"--server", server.URL, "--token", "metrics-token", "--request-id", "req-metrics-1", "metrics"}); err != nil {
		t.Fatalf("metrics error = %v", err)
	}
	if !strings.Contains(out.String(), `kronos_agents{status="healthy"} 1`) {
		t.Fatalf("metrics output = %q", out.String())
	}
}

func TestRunMetricsUsesGlobalServer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Write([]byte("kronos_backups_total 2\n"))
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"--server", server.URL, "metrics"}); err != nil {
		t.Fatalf("metrics error = %v", err)
	}
	if !strings.Contains(out.String(), "kronos_backups_total 2") {
		t.Fatalf("metrics output = %q", out.String())
	}
}

func TestRunMetricsRejectsArgs(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"metrics", "extra"}); err == nil {
		t.Fatal("metrics extra arg error = nil, want error")
	}
}

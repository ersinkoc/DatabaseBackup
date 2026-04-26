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

func TestRunOverview(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/overview" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer overview-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get(obs.RequestIDHeader) != "req-overview-1" {
			t.Fatalf("%s = %q", obs.RequestIDHeader, r.Header.Get(obs.RequestIDHeader))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"agents":{"healthy":1},"jobs":{"active":2}}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"--server", server.URL, "--token", "overview-token", "--request-id", "req-overview-1", "overview"}); err != nil {
		t.Fatalf("overview error = %v", err)
	}
	if !strings.Contains(out.String(), `"healthy":1`) || !strings.Contains(out.String(), `"active":2`) {
		t.Fatalf("overview output = %q", out.String())
	}
}

func TestRunOverviewRejectsArgs(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"overview", "extra"}); err == nil {
		t.Fatal("overview extra arg error = nil, want error")
	}
}

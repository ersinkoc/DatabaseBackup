package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/kvstore"
)

func TestNotificationRuleStoreSaveListDelete(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	store, err := NewNotificationRuleStore(db)
	if err != nil {
		t.Fatalf("NewNotificationRuleStore() error = %v", err)
	}
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	for _, rule := range []core.NotificationRule{
		{ID: "rule-b", Name: "bravo", Events: []core.NotificationEvent{core.NotificationJobFailed}, WebhookURL: "https://hooks.example.com/bravo", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "rule-a", Name: "alpha", Events: []core.NotificationEvent{core.NotificationJobSucceeded}, WebhookURL: "https://hooks.example.com/alpha", Enabled: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.Save(rule); err != nil {
			t.Fatalf("Save(%s) error = %v", rule.ID, err)
		}
	}
	rules, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rules) != 2 || rules[0].ID != "rule-a" || rules[1].ID != "rule-b" {
		t.Fatalf("rules = %#v", rules)
	}
	got, ok, err := store.Get("rule-a")
	if err != nil || !ok || got.Name != "alpha" {
		t.Fatalf("Get(rule-a) = %#v ok=%v err=%v", got, ok, err)
	}
	if err := store.Delete("rule-a"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, ok, err := store.Get("rule-a"); err != nil || ok {
		t.Fatalf("Get(deleted) ok=%v err=%v, want false nil", ok, err)
	}
}

func TestNotificationDispatcherPostsMatchingJobEvent(t *testing.T) {
	t.Parallel()

	var got map[string]any
	var signature string
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		signature = r.Header.Get("X-Kronos-Signature")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer webhook.Close()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	store, err := NewNotificationRuleStore(db)
	if err != nil {
		t.Fatalf("NewNotificationRuleStore() error = %v", err)
	}
	if err := store.Save(core.NotificationRule{
		ID:         "rule-1",
		Name:       "ops",
		Events:     []core.NotificationEvent{core.NotificationJobFailed},
		WebhookURL: webhook.URL,
		Secret:     "shared-secret",
		Enabled:    true,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save(rule) error = %v", err)
	}

	deliveries := NotificationDispatcher{Store: store, Client: webhook.Client()}.DispatchJobTerminal(context.Background(), core.Job{
		ID:        "job-1",
		Operation: core.JobOperationBackup,
		TargetID:  "target-1",
		StorageID: "storage-1",
		Status:    core.JobStatusFailed,
		Error:     "boom",
	})
	if len(deliveries) != 1 || deliveries[0].RuleID != "rule-1" || deliveries[0].StatusCode != http.StatusAccepted || deliveries[0].Error != "" {
		t.Fatalf("deliveries = %#v", deliveries)
	}
	if got["event"] != string(core.NotificationJobFailed) || got["job_id"] != "job-1" || got["error"] != "boom" {
		t.Fatalf("payload = %#v", got)
	}
	if signature == "" || !strings.HasPrefix(signature, "sha256=") {
		t.Fatalf("signature = %q, want sha256 signature", signature)
	}
}

func TestNotificationRuleValidation(t *testing.T) {
	t.Parallel()

	db, err := kvstore.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	store, err := NewNotificationRuleStore(db)
	if err != nil {
		t.Fatalf("NewNotificationRuleStore() error = %v", err)
	}
	if err := store.Save(core.NotificationRule{Name: "ops", Events: []core.NotificationEvent{core.NotificationJobFailed}, WebhookURL: "https://hooks.example.com"}); err == nil {
		t.Fatal("Save(no id) error = nil, want error")
	}
	if err := store.Save(core.NotificationRule{ID: "rule", Events: []core.NotificationEvent{core.NotificationJobFailed}, WebhookURL: "https://hooks.example.com"}); err == nil {
		t.Fatal("Save(no name) error = nil, want error")
	}
	if err := store.Save(core.NotificationRule{ID: "rule", Name: "ops", WebhookURL: "https://hooks.example.com"}); err == nil {
		t.Fatal("Save(no events) error = nil, want error")
	}
	if err := store.Save(core.NotificationRule{ID: "rule", Name: "ops", Events: []core.NotificationEvent{"job.started"}, WebhookURL: "https://hooks.example.com"}); err == nil {
		t.Fatal("Save(bad event) error = nil, want error")
	}
	if err := store.Save(core.NotificationRule{ID: "rule", Name: "ops", Events: []core.NotificationEvent{core.NotificationJobFailed}, WebhookURL: "file:///tmp/hook"}); err == nil {
		t.Fatal("Save(bad webhook) error = nil, want error")
	}
}

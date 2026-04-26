package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/kvstore"
)

var notificationsBucket = []byte("notifications")

// NotificationRuleStore persists outbound notification rules.
type NotificationRuleStore struct {
	db *kvstore.DB
}

// NewNotificationRuleStore returns a notification rule store backed by db.
func NewNotificationRuleStore(db *kvstore.DB) (*NotificationRuleStore, error) {
	if db == nil {
		return nil, fmt.Errorf("kv database is required")
	}
	return &NotificationRuleStore{db: db}, nil
}

// Save inserts or replaces a notification rule.
func (s *NotificationRuleStore) Save(rule core.NotificationRule) error {
	if err := validateNotificationRule(rule); err != nil {
		return err
	}
	return saveJSON(s.db, notificationsBucket, []byte(rule.ID), rule)
}

// Get fetches one notification rule.
func (s *NotificationRuleStore) Get(id core.ID) (core.NotificationRule, bool, error) {
	var rule core.NotificationRule
	ok, err := getJSON(s.db, notificationsBucket, []byte(id), &rule)
	return rule, ok, err
}

// List returns all notification rules ordered by name, then ID.
func (s *NotificationRuleStore) List() ([]core.NotificationRule, error) {
	var rules []core.NotificationRule
	if err := listJSON(s.db, notificationsBucket, func(data []byte) error {
		var rule core.NotificationRule
		if err := json.Unmarshal(data, &rule); err != nil {
			return err
		}
		rules = append(rules, rule)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Name == rules[j].Name {
			return rules[i].ID < rules[j].ID
		}
		return rules[i].Name < rules[j].Name
	})
	return rules, nil
}

// Delete removes a notification rule.
func (s *NotificationRuleStore) Delete(id core.ID) error {
	return deleteKey(s.db, notificationsBucket, []byte(id))
}

// NotificationDelivery records one delivery attempt.
type NotificationDelivery struct {
	RuleID     core.ID `json:"rule_id"`
	StatusCode int     `json:"status_code,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// NotificationDispatcher delivers terminal job events to matching rules.
type NotificationDispatcher struct {
	Store  *NotificationRuleStore
	Client *http.Client
}

// DispatchJobTerminal posts webhook notifications for a terminal job.
func (d NotificationDispatcher) DispatchJobTerminal(ctx context.Context, job core.Job) []NotificationDelivery {
	if d.Store == nil {
		return nil
	}
	event, ok := notificationEventForJob(job)
	if !ok {
		return nil
	}
	rules, err := d.Store.List()
	if err != nil {
		return []NotificationDelivery{{Error: err.Error()}}
	}
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	payload, _ := json.Marshal(map[string]any{
		"event":      event,
		"job_id":     job.ID,
		"operation":  job.Operation,
		"status":     job.Status,
		"target_id":  job.TargetID,
		"storage_id": job.StorageID,
		"agent_id":   job.AgentID,
		"error":      job.Error,
		"queued_at":  job.QueuedAt,
		"started_at": job.StartedAt,
		"ended_at":   job.EndedAt,
	})
	deliveries := make([]NotificationDelivery, 0)
	for _, rule := range rules {
		if !rule.Enabled || !notificationRuleMatches(rule, event) {
			continue
		}
		delivery := NotificationDelivery{RuleID: rule.ID}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, rule.WebhookURL, strings.NewReader(string(payload)))
		if err != nil {
			delivery.Error = err.Error()
			deliveries = append(deliveries, delivery)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if rule.Secret != "" {
			req.Header.Set("X-Kronos-Signature", notificationSignature(rule.Secret, payload))
		}
		resp, err := client.Do(req)
		if err != nil {
			delivery.Error = err.Error()
			deliveries = append(deliveries, delivery)
			continue
		}
		delivery.StatusCode = resp.StatusCode
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			delivery.Error = fmt.Sprintf("webhook returned %s", resp.Status)
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries
}

func notificationSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func validateNotificationRule(rule core.NotificationRule) error {
	if rule.ID.IsZero() {
		return fmt.Errorf("notification rule id is required")
	}
	if strings.TrimSpace(rule.Name) == "" {
		return fmt.Errorf("notification rule name is required")
	}
	if len(rule.Events) == 0 {
		return fmt.Errorf("notification rule events are required")
	}
	for _, event := range rule.Events {
		switch event {
		case core.NotificationJobSucceeded, core.NotificationJobFailed, core.NotificationJobCanceled:
		default:
			return fmt.Errorf("unsupported notification event %q", event)
		}
	}
	parsed, err := url.Parse(rule.WebhookURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("notification webhook_url must be an absolute URL")
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("notification webhook_url scheme must be http or https")
	}
	return nil
}

func notificationEventForJob(job core.Job) (core.NotificationEvent, bool) {
	switch job.Status {
	case core.JobStatusSucceeded:
		return core.NotificationJobSucceeded, true
	case core.JobStatusFailed:
		return core.NotificationJobFailed, true
	case core.JobStatusCanceled:
		return core.NotificationJobCanceled, true
	default:
		return "", false
	}
}

func notificationRuleMatches(rule core.NotificationRule, event core.NotificationEvent) bool {
	for _, candidate := range rule.Events {
		if candidate == event {
			return true
		}
	}
	return false
}

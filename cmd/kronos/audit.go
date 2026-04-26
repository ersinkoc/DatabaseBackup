package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/kronos/kronos/internal/core"
)

func runAudit(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("audit subcommand is required")
	}
	switch args[0] {
	case "list":
		return runAuditList(ctx, out, args[1:])
	case "search":
		return runAuditSearch(ctx, out, args[1:])
	case "tail":
		return runAuditTail(ctx, out, args[1:])
	case "verify":
		return runAuditVerify(ctx, out, args[1:])
	default:
		return fmt.Errorf("unknown audit subcommand %q", args[0])
	}
}

func runAuditList(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("audit list", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	limit := fs.Int("limit", 0, "maximum number of events")
	filters := newAuditFilterFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < 0 {
		return fmt.Errorf("--limit must be non-negative")
	}
	query := auditFilterQuery(filters)
	if *limit > 0 {
		query.Set("limit", fmt.Sprint(*limit))
	}
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, pathWithQuery("/api/v1/audit", query), out)
}

func runAuditVerify(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("audit verify", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/audit/verify", nil, out)
}

func runAuditTail(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("audit tail", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	limit := fs.Int("limit", 20, "number of events")
	filters := newAuditFilterFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < 0 {
		return fmt.Errorf("--limit must be non-negative")
	}
	events, err := fetchAuditEvents(ctx, http.DefaultClient, *serverAddr, auditFilterQuery(filters))
	if err != nil {
		return err
	}
	if *limit < len(events) {
		events = events[len(events)-*limit:]
	}
	return writeAuditEvents(ctx, out, events)
}

func runAuditSearch(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("audit search", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	query := fs.String("query", "", "case-insensitive search text")
	filters := newAuditFilterFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *query == "" {
		return fmt.Errorf("--query is required")
	}
	events, err := fetchAuditEvents(ctx, http.DefaultClient, *serverAddr, auditFilterQuery(filters))
	if err != nil {
		return err
	}
	needle := strings.ToLower(*query)
	filtered := events[:0]
	for _, event := range events {
		if auditEventMatches(event, needle) {
			filtered = append(filtered, event)
		}
	}
	return writeAuditEvents(ctx, out, filtered)
}

type auditListResponse struct {
	Events []core.AuditEvent `json:"events"`
}

type auditFilterFlags struct {
	ActorID      *string
	Action       *string
	ResourceType *string
	ResourceID   *string
	Since        *string
	Until        *string
}

func newAuditFilterFlags(fs *flag.FlagSet) auditFilterFlags {
	return auditFilterFlags{
		ActorID:      fs.String("actor", "", "actor id filter"),
		Action:       fs.String("action", "", "action filter"),
		ResourceType: fs.String("resource-type", "", "resource type filter"),
		ResourceID:   fs.String("resource-id", "", "resource id filter"),
		Since:        fs.String("since", "", "occurred-at lower bound; RFC3339 or duration such as 7d"),
		Until:        fs.String("until", "", "occurred-at upper bound; RFC3339 or duration such as 24h"),
	}
}

func auditFilterQuery(filters auditFilterFlags) url.Values {
	query := url.Values{}
	query.Set("actor_id", *filters.ActorID)
	query.Set("action", *filters.Action)
	query.Set("resource_type", *filters.ResourceType)
	query.Set("resource_id", *filters.ResourceID)
	query.Set("since", *filters.Since)
	query.Set("until", *filters.Until)
	return query
}

func fetchAuditEvents(ctx context.Context, client *http.Client, serverAddr string, query url.Values) ([]core.AuditEvent, error) {
	serverAddr = controlServerAddr(ctx, serverAddr)
	endpoint, err := controlEndpoint(serverAddr, pathWithQuery("/api/v1/audit", query))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	setControlAuth(ctx, req)
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body strings.Builder
		_, _ = io.Copy(&body, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GET /api/v1/audit failed: %s%s: %s", resp.Status, responseRequestID(resp), strings.TrimSpace(body.String()))
	}
	var payload auditListResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 16*1024*1024))
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Events, nil
}

func writeAuditEvents(ctx context.Context, out io.Writer, events []core.AuditEvent) error {
	return writeCommandJSON(ctx, out, auditListResponse{Events: events})
}

func auditEventMatches(event core.AuditEvent, needle string) bool {
	values := []string{
		string(event.ActorID),
		event.Action,
		event.ResourceType,
		string(event.ResourceID),
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), needle) {
			return true
		}
	}
	for key, value := range event.Metadata {
		if strings.Contains(strings.ToLower(key), needle) || strings.Contains(strings.ToLower(fmt.Sprint(value)), needle) {
			return true
		}
	}
	return false
}

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kronos/kronos/internal/core"
)

func runNotification(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("notification subcommand is required")
	}
	switch args[0] {
	case "add":
		return runNotificationAdd(ctx, out, args[1:])
	case "inspect":
		return runNotificationInspect(ctx, out, args[1:])
	case "list":
		return runNotificationList(ctx, out, args[1:])
	case "remove":
		return runNotificationRemove(ctx, out, args[1:])
	case "update":
		return runNotificationUpdate(ctx, out, args[1:])
	default:
		return fmt.Errorf("unknown notification subcommand %q", args[0])
	}
}

func runNotificationList(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("notification list", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/notifications", out)
}

func runNotificationInspect(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("notification inspect", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "notification rule id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/notifications/"+*id, out)
}

func runNotificationAdd(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("notification add", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "notification rule id")
	name := fs.String("name", "", "notification rule name")
	eventsText := fs.String("event", "", "comma-separated events: job.succeeded,job.failed,job.canceled")
	webhookURL := fs.String("webhook-url", "", "webhook URL")
	secret := fs.String("secret", "", "optional HMAC signing secret")
	enabled := fs.Bool("enabled", true, "enable the notification rule")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rule, err := notificationRuleFromFlags(*id, *name, *eventsText, *webhookURL, *secret, *enabled)
	if err != nil {
		return err
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/notifications", rule, out)
}

func runNotificationUpdate(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("notification update", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "notification rule id")
	name := fs.String("name", "", "notification rule name")
	eventsText := fs.String("event", "", "comma-separated events: job.succeeded,job.failed,job.canceled")
	webhookURL := fs.String("webhook-url", "", "webhook URL")
	secret := fs.String("secret", "", "optional HMAC signing secret")
	enabled := fs.Bool("enabled", true, "enable the notification rule")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	rule, err := notificationRuleFromFlags(*id, *name, *eventsText, *webhookURL, *secret, *enabled)
	if err != nil {
		return err
	}
	return putControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/notifications/"+*id, rule, out)
}

func runNotificationRemove(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("notification remove", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "notification rule id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return deleteControl(ctx, http.DefaultClient, *serverAddr, "/api/v1/notifications/"+*id, out)
}

func notificationRuleFromFlags(id, name, eventsText, webhookURL, secret string, enabled bool) (core.NotificationRule, error) {
	if strings.TrimSpace(name) == "" {
		return core.NotificationRule{}, fmt.Errorf("--name is required")
	}
	if strings.TrimSpace(eventsText) == "" {
		return core.NotificationRule{}, fmt.Errorf("--event is required")
	}
	if strings.TrimSpace(webhookURL) == "" {
		return core.NotificationRule{}, fmt.Errorf("--webhook-url is required")
	}
	events := splitNotificationEvents(eventsText)
	if len(events) == 0 {
		return core.NotificationRule{}, fmt.Errorf("--event is required")
	}
	return core.NotificationRule{
		ID:         core.ID(strings.TrimSpace(id)),
		Name:       strings.TrimSpace(name),
		Events:     events,
		WebhookURL: strings.TrimSpace(webhookURL),
		Secret:     secret,
		Enabled:    enabled,
	}, nil
}

func splitNotificationEvents(value string) []core.NotificationEvent {
	parts := strings.Split(value, ",")
	events := make([]core.NotificationEvent, 0, len(parts))
	seen := make(map[core.NotificationEvent]struct{}, len(parts))
	for _, part := range parts {
		event := core.NotificationEvent(strings.TrimSpace(part))
		if event == "" {
			continue
		}
		if _, ok := seen[event]; ok {
			continue
		}
		seen[event] = struct{}{}
		events = append(events, event)
	}
	return events
}

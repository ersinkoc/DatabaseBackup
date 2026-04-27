package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kronos/kronos/internal/core"
)

func runRestore(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("restore subcommand is required")
	}
	switch args[0] {
	case "preview":
		return runRestorePreview(ctx, out, args[1:])
	case "start":
		return runRestoreStart(ctx, out, args[1:])
	default:
		return fmt.Errorf("unknown restore subcommand %q", args[0])
	}
}

func runRestorePreview(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("restore preview", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	backupID := fs.String("backup", "", "backup id")
	targetID := fs.String("target", "", "restore target id")
	atText := fs.String("at", "", "point-in-time target timestamp")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *backupID == "" {
		return fmt.Errorf("--backup is required")
	}
	payload := map[string]any{"backup_id": core.ID(*backupID)}
	if *targetID != "" {
		payload["target_id"] = core.ID(*targetID)
	}
	if *atText != "" {
		at, err := parseRestoreAt(*atText)
		if err != nil {
			return err
		}
		payload["at"] = at
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/restore/preview", payload, out)
}

func runRestoreStart(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("restore start", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	backupID := fs.String("backup", "", "backup id")
	targetID := fs.String("target", "", "restore target id")
	atText := fs.String("at", "", "point-in-time target timestamp")
	dryRun := fs.Bool("dry-run", false, "validate and execute restore in dry-run mode")
	replaceExisting := fs.Bool("replace-existing", false, "allow replacing existing destination data")
	yes := fs.Bool("yes", false, "confirm a non-dry-run restore")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *backupID == "" {
		return fmt.Errorf("--backup is required")
	}
	if !*dryRun && !*yes {
		return fmt.Errorf("non-dry-run restore requires --yes")
	}
	payload := map[string]any{"backup_id": core.ID(*backupID)}
	if *targetID != "" {
		payload["target_id"] = core.ID(*targetID)
	}
	if *dryRun {
		payload["dry_run"] = true
	}
	if *replaceExisting {
		payload["replace_existing"] = true
	}
	if *atText != "" {
		at, err := parseRestoreAt(*atText)
		if err != nil {
			return err
		}
		payload["at"] = at
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/restore", payload, out)
}

func parseRestoreAt(value string) (time.Time, error) {
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse --at: %w", err)
	}
	if at.After(time.Now().UTC()) {
		return time.Time{}, fmt.Errorf("--at must not be in the future")
	}
	return at, nil
}

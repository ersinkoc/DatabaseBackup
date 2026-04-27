package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

func runJobs(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("jobs subcommand is required")
	}
	switch args[0] {
	case "cancel":
		return runJobsCancel(ctx, out, args[1:])
	case "inspect":
		return runJobsInspect(ctx, out, args[1:])
	case "list":
		return runJobsList(ctx, out, args[1:])
	case "retry":
		return runJobsRetry(ctx, out, args[1:])
	default:
		return fmt.Errorf("unknown jobs subcommand %q", args[0])
	}
}

func runJobsList(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("jobs list", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	status := fs.String("status", "", "job status filter")
	operation := fs.String("operation", "", "job operation filter")
	targetID := fs.String("target", "", "target id filter")
	storageID := fs.String("storage", "", "storage id filter")
	agentID := fs.String("agent", "", "agent id filter")
	since := fs.String("since", "", "queued-at lower bound; RFC3339 or duration such as 7d")
	until := fs.String("until", "", "queued-at upper bound; RFC3339 or duration such as 24h")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateListTimeFlags(*since, *until); err != nil {
		return err
	}
	query := url.Values{}
	query.Set("status", *status)
	query.Set("operation", *operation)
	query.Set("target_id", *targetID)
	query.Set("storage_id", *storageID)
	query.Set("agent_id", *agentID)
	query.Set("since", *since)
	query.Set("until", *until)
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, pathWithQuery("/api/v1/jobs", query), out)
}

func runJobsInspect(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("jobs inspect", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "job id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/jobs/"+*id, out)
}

func runJobsCancel(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("jobs cancel", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "job id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/jobs/"+*id+"/cancel", nil, out)
}

func runJobsRetry(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("jobs retry", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "job id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/jobs/"+*id+"/retry", nil, out)
}

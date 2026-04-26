package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

func runHealth(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("health", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("health does not accept positional arguments")
	}
	server := controlServerAddr(ctx, *serverAddr)
	endpoint, err := controlEndpoint(server, "/healthz")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	setControlAuth(ctx, req)
	return doControlRequest(http.DefaultClient, req, out)
}

func runReady(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("ready", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("ready does not accept positional arguments")
	}
	server := controlServerAddr(ctx, *serverAddr)
	endpoint, err := controlEndpoint(server, "/readyz")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	setControlAuth(ctx, req)
	return doControlRequest(http.DefaultClient, req, out)
}

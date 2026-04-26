package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kronos/kronos/internal/core"
)

func runToken(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("token subcommand is required")
	}
	switch args[0] {
	case "create":
		return runTokenCreate(ctx, out, args[1:])
	case "inspect":
		return runTokenInspect(ctx, out, args[1:])
	case "list":
		return runTokenList(ctx, out, args[1:])
	case "revoke":
		return runTokenRevoke(ctx, out, args[1:])
	case "verify":
		return runTokenVerify(ctx, out, args[1:])
	default:
		return fmt.Errorf("unknown token subcommand %q", args[0])
	}
}

func runTokenCreate(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("token create", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	name := fs.String("name", "", "token name")
	userID := fs.String("user", "", "user id")
	scopesText := fs.String("scope", "", "comma-separated scopes")
	expiresText := fs.String("expires-at", "", "RFC3339 expiry timestamp")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	if *userID == "" {
		return fmt.Errorf("--user is required")
	}
	scopes := splitScopes(*scopesText)
	if len(scopes) == 0 {
		return fmt.Errorf("--scope is required")
	}
	payload := map[string]any{
		"name":    *name,
		"user_id": core.ID(*userID),
		"scopes":  scopes,
	}
	if *expiresText != "" {
		expiresAt, err := time.Parse(time.RFC3339, *expiresText)
		if err != nil {
			return fmt.Errorf("parse --expires-at: %w", err)
		}
		payload["expires_at"] = expiresAt
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/tokens", payload, out)
}

func runTokenList(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("token list", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/tokens", out)
}

func runTokenInspect(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("token inspect", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "token id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/tokens/"+*id, out)
}

func runTokenRevoke(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("token revoke", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "token id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/tokens/"+*id+"/revoke", nil, out)
}

func runTokenVerify(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("token verify", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	secret := fs.String("secret", "", "bearer token secret")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *secret == "" {
		*secret = strings.TrimSpace(os.Getenv("KRONOS_TOKEN"))
	}
	if *secret == "" {
		return fmt.Errorf("--secret is required or KRONOS_TOKEN must be set")
	}
	endpoint, err := controlEndpoint(*serverAddr, "/api/v1/auth/verify")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+*secret)
	setControlRequestID(ctx, req)
	return doControlRequest(http.DefaultClient, req, out)
}

func splitScopes(value string) []string {
	parts := strings.Split(value, ",")
	scopes := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			scopes = append(scopes, part)
		}
	}
	return scopes
}

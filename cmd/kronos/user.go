package main

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/kronos/kronos/internal/core"
)

func runUser(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("user subcommand is required")
	}
	switch args[0] {
	case "add":
		return runUserAdd(ctx, out, args[1:])
	case "bootstrap":
		return runUserBootstrap(ctx, out, args[1:])
	case "grant":
		return runUserGrant(ctx, out, args[1:])
	case "inspect":
		return runUserInspect(ctx, out, args[1:])
	case "list":
		return runUserList(ctx, out, args[1:])
	case "remove":
		return runUserRemove(ctx, out, args[1:])
	default:
		return fmt.Errorf("unknown user subcommand %q", args[0])
	}
}

func runUserBootstrap(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("user bootstrap", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "admin user id")
	email := fs.String("email", "", "admin user email")
	displayName := fs.String("display-name", "", "admin display name")
	tokenName := fs.String("token-name", "initial-admin", "initial admin token name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return fmt.Errorf("--email is required")
	}
	if *displayName == "" {
		return fmt.Errorf("--display-name is required")
	}
	payload := map[string]any{
		"id":           core.ID(*id),
		"email":        *email,
		"display_name": *displayName,
		"token_name":   *tokenName,
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/bootstrap/admin", payload, out)
}

func runUserAdd(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("user add", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "user id")
	email := fs.String("email", "", "user email")
	displayName := fs.String("display-name", "", "display name")
	role := fs.String("role", string(core.RoleViewer), "role: admin, operator, or viewer")
	totp := fs.Bool("totp", false, "require TOTP for this user")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return fmt.Errorf("--email is required")
	}
	if *displayName == "" {
		return fmt.Errorf("--display-name is required")
	}
	payload := core.User{
		ID:           core.ID(*id),
		Email:        *email,
		DisplayName:  *displayName,
		Role:         core.RoleName(*role),
		TOTPEnforced: *totp,
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/users", payload, out)
}

func runUserList(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("user list", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/users", out)
}

func runUserInspect(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("user inspect", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "user id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/users/"+*id, out)
}

func runUserGrant(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("user grant", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "user id")
	role := fs.String("role", "", "role: admin, operator, or viewer")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	if *role == "" {
		return fmt.Errorf("--role is required")
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/users/"+*id+"/grant", map[string]string{"role": *role}, out)
}

func runUserRemove(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("user remove", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "user id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return deleteControl(ctx, http.DefaultClient, *serverAddr, "/api/v1/users/"+*id, out)
}

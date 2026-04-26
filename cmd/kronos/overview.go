package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

func runOverview(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("overview", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("overview does not accept positional arguments")
	}
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/overview", out)
}

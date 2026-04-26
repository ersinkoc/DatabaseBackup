package main

import (
	"context"
	"fmt"
	"io"

	"github.com/kronos/kronos/internal/buildinfo"
)

func runVersion(_ context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("version", out)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("version does not accept positional arguments")
	}
	fmt.Fprintf(out, "kronos %s\ncommit: %s\nbuilt: %s\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
	return nil
}

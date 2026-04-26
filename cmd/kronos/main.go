package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/kronos/kronos/internal/obs"
)

type command struct {
	name        string
	description string
	run         func(context.Context, io.Writer, []string) error
}

type cliOptions struct {
	Token     string
	Server    string
	Output    string
	RequestID string
	Color     bool
}

type cliOptionsKey struct{}

func main() {
	if err := run(context.Background(), os.Stdout, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "kronos: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out io.Writer, args []string) error {
	commands := registry()
	global := flag.NewFlagSet("kronos", flag.ContinueOnError)
	global.SetOutput(out)
	server := global.String("server", "", "control-plane server address")
	token := global.String("token", os.Getenv("KRONOS_TOKEN"), "bearer token for control-plane API requests")
	output := global.String("output", "json", "output format: json, pretty, yaml, or table")
	requestID := global.String("request-id", "", "request correlation id to send to the control plane")
	noColor := global.Bool("no-color", false, "disable colorized output")
	args = hoistGlobalFlags(args)
	if err := global.Parse(args); err != nil {
		return err
	}
	if *output != "json" && *output != "pretty" && *output != "yaml" && *output != "table" {
		return fmt.Errorf("unsupported --output %q", *output)
	}
	args = global.Args()
	color := colorEnabled(out, *noColor)
	ctx = context.WithValue(ctx, cliOptionsKey{}, cliOptions{Token: *token, Server: *server, Output: *output, RequestID: *requestID, Color: color})
	if strings.TrimSpace(*requestID) != "" {
		ctx = obs.WithRequestID(ctx, *requestID)
	} else {
		ctx = obs.EnsureRequestID(ctx)
	}
	if len(args) == 0 {
		return runHelp(out, commands, color)
	}

	name := args[0]
	if name == "help" {
		if len(args) > 1 {
			return runCommandHelp(out, commands, args[1], color)
		}
		return runHelp(out, commands, color)
	}
	if name == "-h" || name == "--help" {
		return runHelp(out, commands, color)
	}

	cmd, ok := commands[name]
	if !ok {
		return fmt.Errorf("unknown command %q", name)
	}
	if len(args) > 1 && (args[1] == "-h" || args[1] == "--help") {
		return runCommandHelp(out, commands, name, color)
	}
	return cmd.run(ctx, out, args[1:])
}

func hoistGlobalFlags(args []string) []string {
	if len(args) == 0 {
		return args
	}
	hoisted := make([]string, 0, len(args))
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if arg == "--no-color" || arg == "-no-color" {
			hoisted = append(hoisted, arg)
			continue
		}
		if hasGlobalFlagValue(arg, "output") || hasGlobalFlagValue(arg, "token") {
			hoisted = append(hoisted, arg)
			continue
		}
		if hasGlobalFlagValue(arg, "request-id") {
			hoisted = append(hoisted, arg)
			continue
		}
		if arg == "--output" || arg == "-output" || arg == "--token" || arg == "-token" || arg == "--request-id" || arg == "-request-id" {
			hoisted = append(hoisted, arg)
			if i+1 < len(args) {
				i++
				hoisted = append(hoisted, args[i])
			}
			continue
		}
		rest = append(rest, arg)
	}
	if len(hoisted) == 0 {
		return args
	}
	return append(hoisted, rest...)
}

func hasGlobalFlagValue(arg string, name string) bool {
	return strings.HasPrefix(arg, "--"+name+"=") || strings.HasPrefix(arg, "-"+name+"=")
}

func registry() map[string]command {
	return map[string]command{
		"agent": {
			name:        "agent",
			description: "run, list, and inspect agents",
			run:         runAgent,
		},
		"audit": {
			name:        "audit",
			description: "list and verify audit events",
			run:         runAudit,
		},
		"backup": {
			name:        "backup",
			description: "inspect, verify, and manage backups",
			run:         runBackup,
		},
		"config": {
			name:        "config",
			description: "inspect and validate configuration",
			run:         runConfig,
		},
		"completion": {
			name:        "completion",
			description: "generate shell completion scripts",
			run:         runCompletion,
		},
		"gc": {
			name:        "gc",
			description: "collect unreferenced repository chunks",
			run:         runGC,
		},
		"health": {
			name:        "health",
			description: "check control-plane health",
			run:         runHealth,
		},
		"jobs": {
			name:        "jobs",
			description: "list and manage queued or running jobs",
			run:         runJobs,
		},
		"keygen": {
			name:        "keygen",
			description: "generate manifest and chunk keys",
			run:         runKeygen,
		},
		"key": {
			name:        "key",
			description: "manage repository key slots",
			run:         runKey,
		},
		"local": {
			name:        "local",
			description: "run server and agent in one local process",
			run:         runLocal,
		},
		"metrics": {
			name:        "metrics",
			description: "fetch Prometheus metrics",
			run:         runMetrics,
		},
		"notification": {
			name:        "notification",
			description: "list and manage notification rules",
			run:         runNotification,
		},
		"ready": {
			name:        "ready",
			description: "check control-plane readiness",
			run:         runReady,
		},
		"repair-db": {
			name:        "repair-db",
			description: "check and repair the embedded state database",
			run:         runRepairDB,
		},
		"retention": {
			name:        "retention",
			description: "plan backup retention decisions",
			run:         runRetention,
		},
		"restore": {
			name:        "restore",
			description: "preview and start restore jobs",
			run:         runRestore,
		},
		"schedule": {
			name:        "schedule",
			description: "list and manage backup schedules",
			run:         runSchedule,
		},
		"server": {
			name:        "server",
			description: "run the control-plane server",
			run:         runServer,
		},
		"storage": {
			name:        "storage",
			description: "list and manage storage repositories",
			run:         runStorage,
		},
		"target": {
			name:        "target",
			description: "list and manage database targets",
			run:         runTarget,
		},
		"token": {
			name:        "token",
			description: "create, list, inspect, and revoke API tokens",
			run:         runToken,
		},
		"user": {
			name:        "user",
			description: "add, list, inspect, remove, and grant users",
			run:         runUser,
		},
		"version": {
			name:        "version",
			description: "print build information",
			run:         runVersion,
		},
	}
}

func runHelp(out io.Writer, commands map[string]command, color bool) error {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Fprintln(out, colorize("Kronos", ansiBold, color))
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Time devours. Kronos preserves.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, colorize("Usage:", ansiCyan, color))
	fmt.Fprintln(out, "  kronos [global flags] <command> [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  --output, --token, --request-id, and --no-color may also be placed with command flags.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, colorize("Global Flags:", ansiCyan, color))
	fmt.Fprintln(out, "  --server string   control-plane server address")
	fmt.Fprintln(out, "  --token string    bearer token for control-plane API requests")
	fmt.Fprintln(out, "  --output string   output format: json, pretty, yaml, or table")
	fmt.Fprintln(out, "  --request-id string request correlation id to send to the control plane")
	fmt.Fprintln(out, "  --no-color        disable colorized output")
	fmt.Fprintln(out)
	fmt.Fprintln(out, colorize("Commands:", ansiCyan, color))
	for _, name := range names {
		cmd := commands[name]
		fmt.Fprintf(out, "  %-10s %s\n", cmd.name, cmd.description)
	}
	return nil
}

func runCommandHelp(out io.Writer, commands map[string]command, name string, color bool) error {
	cmd, ok := commands[name]
	if !ok {
		return fmt.Errorf("unknown command %q", name)
	}
	fmt.Fprintln(out, colorize(cmd.name, ansiBold, color))
	fmt.Fprintln(out)
	fmt.Fprintln(out, cmd.description)
	fmt.Fprintln(out)
	subcommands := completionSubcommands()[name]
	if len(subcommands) == 0 {
		fmt.Fprintln(out, colorize("Usage:", ansiCyan, color))
		fmt.Fprintf(out, "  kronos [global flags] %s [flags]\n", name)
		return nil
	}
	fmt.Fprintln(out, colorize("Usage:", ansiCyan, color))
	fmt.Fprintf(out, "  kronos [global flags] %s <subcommand> [flags]\n", name)
	fmt.Fprintln(out)
	fmt.Fprintln(out, colorize("Subcommands:", ansiCyan, color))
	for _, subcommand := range subcommands {
		fmt.Fprintf(out, "  %s\n", subcommand)
	}
	return nil
}

func newFlagSet(name string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	return fs
}

const (
	ansiBold  = "\x1b[1m"
	ansiCyan  = "\x1b[36m"
	ansiReset = "\x1b[0m"
)

func colorEnabled(out io.Writer, noColor bool) bool {
	if noColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	force := strings.TrimSpace(os.Getenv("CLICOLOR_FORCE"))
	if force != "" && force != "0" {
		return true
	}
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func colorize(value string, code string, enabled bool) string {
	if !enabled {
		return value
	}
	return code + value + ansiReset
}

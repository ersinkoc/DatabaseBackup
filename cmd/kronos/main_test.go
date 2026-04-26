package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kronos/kronos/internal/config"
)

func TestRunHelp(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, nil); err != nil {
		t.Fatalf("run help error = %v", err)
	}

	text := out.String()
	for _, want := range []string{"Kronos", "Global Flags", "--server", "--token", "--output", "--request-id", "json, pretty, yaml, or table", "--no-color", "server", "agent", "audit", "health", "ready", "jobs", "local", "metrics", "notification", "overview", "schedule", "storage", "target", "config", "version"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help output missing %q:\n%s", want, text)
		}
	}
}

func TestRunHelpColorHonorsEnvironment(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("NO_COLOR", "")

	var out bytes.Buffer
	if err := run(context.Background(), &out, nil); err != nil {
		t.Fatalf("run help error = %v", err)
	}
	if !strings.Contains(out.String(), "\x1b[1mKronos\x1b[0m") {
		t.Fatalf("help output is not colorized: %q", out.String())
	}

	out.Reset()
	if err := run(context.Background(), &out, []string{"--no-color", "help"}); err != nil {
		t.Fatalf("run help --no-color error = %v", err)
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("help --no-color output has ansi escapes: %q", out.String())
	}
}

func TestColorEnabled(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if colorEnabled(&bytes.Buffer{}, false) {
		t.Fatal("colorEnabled(NO_COLOR) = true, want false")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "1")
	if !colorEnabled(&bytes.Buffer{}, false) {
		t.Fatal("colorEnabled(CLICOLOR_FORCE) = false, want true")
	}
	if colorEnabled(&bytes.Buffer{}, true) {
		t.Fatal("colorEnabled(noColor) = true, want false")
	}
	if colorize("x", ansiBold, true) != ansiBold+"x"+ansiReset {
		t.Fatal("colorize(enabled) did not wrap value")
	}
}

func TestRunCommandHelp(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"help", "token"}); err != nil {
		t.Fatalf("run help token error = %v", err)
	}
	text := out.String()
	for _, want := range []string{"token", "create, list, inspect", "Subcommands:", "create", "inspect", "verify"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help token output missing %q:\n%s", want, text)
		}
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"backup", "--help"}); err != nil {
		t.Fatalf("run backup --help error = %v", err)
	}
	if !strings.Contains(out.String(), "Subcommands:") || !strings.Contains(out.String(), "unprotect") {
		t.Fatalf("backup --help output = %q", out.String())
	}
}

func TestRunCommandHelpRejectsUnknown(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"help", "missing"}); err == nil {
		t.Fatal("help missing error = nil, want error")
	}
}

func TestRunRejectsUnknownGlobalOutput(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"--output", "xml", "version"}); err == nil {
		t.Fatal("run --output xml error = nil, want error")
	}
}

func TestRunGlobalFlagParsingVariants(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"version", "--output=pretty", "--token=abc", "--no-color"}); err != nil {
		t.Fatalf("run(version with global flags after command) error = %v", err)
	}
	if !strings.Contains(out.String(), "kronos") {
		t.Fatalf("version output = %q", out.String())
	}
	hoisted := hoistGlobalFlags([]string{"version", "--", "--output", "yaml"})
	if fmt.Sprint(hoisted) != fmt.Sprint([]string{"version", "--", "--output", "yaml"}) {
		t.Fatalf("hoistGlobalFlags with -- = %v", hoisted)
	}
	if !hasGlobalFlagValue("-output=json", "output") || !hasGlobalFlagValue("--token=abc", "token") || !hasGlobalFlagValue("--request-id=req-1", "request-id") || hasGlobalFlagValue("--server=x", "token") {
		t.Fatal("hasGlobalFlagValue returned unexpected result")
	}
}

func TestRunAcceptsGlobalOutputAfterCommand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"storage", "test", "--uri", "file://" + dir, "--output", "table"}); err != nil {
		t.Fatalf("storage test with trailing --output error = %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "KEY") || !strings.Contains(text, "backend") || !strings.Contains(text, "local") {
		t.Fatalf("storage test table output = %q", text)
	}
}

func TestHoistGlobalFlagsLeavesServerLocal(t *testing.T) {
	t.Parallel()

	got := hoistGlobalFlags([]string{"backup", "list", "--server", "http://127.0.0.1:8500", "--output", "yaml", "--token=secret", "--request-id", "req-1"})
	want := []string{"--output", "yaml", "--token=secret", "--request-id", "req-1", "backup", "list", "--server", "http://127.0.0.1:8500"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("hoistGlobalFlags() = %#v, want %#v", got, want)
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"version"}); err != nil {
		t.Fatalf("run version error = %v", err)
	}
	for _, want := range []string{"kronos ", "commit:", "built:"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("version output missing %q: %q", want, out.String())
		}
	}
}

func TestRunVersionRejectsArgs(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"version", "extra"}); err == nil {
		t.Fatal("run version extra arg error = nil, want error")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"missing"}); err == nil {
		t.Fatal("run unknown command succeeded, want error")
	}
}

func TestRunConfigValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kronos.yaml")
	data := []byte(`
server:
  listen: "127.0.0.1:8500"
  data_dir: "/tmp/kronos"
projects:
  - name: default
    storages:
      - name: local
        backend: local
        path: "/tmp/repo"
    targets:
      - name: redis
        driver: redis
        connection:
          host: "127.0.0.1"
          port: 6379
    schedules:
      - name: redis-nightly
        target: redis
        type: full
        cron: "0 2 * * *"
        storage: local
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var out bytes.Buffer
	err := run(context.Background(), &out, []string{"config", "validate", "--config", path})
	if err != nil {
		t.Fatalf("run config validate error = %v", err)
	}
	if !strings.Contains(out.String(), `"ok":true`) || !strings.Contains(out.String(), `"projects":1`) {
		t.Fatalf("config validate output = %q", out.String())
	}
}

func TestRunConfigInspectRedactsSecrets(t *testing.T) {
	t.Setenv("KRONOS_TEST_OIDC_SECRET", "oidc-secret")
	t.Setenv("KRONOS_TEST_MASTER", "master-secret")
	t.Setenv("KRONOS_TEST_STORAGE_KEY", "storage-secret")
	t.Setenv("KRONOS_TEST_PG_PASSWORD", "pg-secret")

	path := filepath.Join(t.TempDir(), "kronos.yaml")
	data := []byte(`
server:
  listen: "127.0.0.1:8500"
  data_dir: "/tmp/kronos"
  auth:
    oidc:
      issuer: "https://id.example.com"
      client_id: "kronos"
      client_secret: "${env:KRONOS_TEST_OIDC_SECRET}"
  master_passphrase: "${env:KRONOS_TEST_MASTER}"
projects:
  - name: default
    storages:
      - name: local
        backend: local
        path: "/tmp/repo"
        credentials: "${env:KRONOS_TEST_STORAGE_KEY}"
        encryption_key: "age-secret"
        options:
          secret_key: "${env:KRONOS_TEST_STORAGE_KEY}"
    targets:
      - name: redis
        driver: redis
        connection:
          host: "127.0.0.1"
          port: 6379
          password: "${env:KRONOS_TEST_PG_PASSWORD}"
        options:
          api_token: "target-token"
    schedules:
      - name: redis-nightly
        target: redis
        type: full
        cron: "0 2 * * *"
        storage: local
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"--output", "pretty", "config", "inspect", "--config", path}); err != nil {
		t.Fatalf("config inspect error = %v", err)
	}
	text := out.String()
	for _, leaked := range []string{"oidc-secret", "master-secret", "storage-secret", "age-secret", "pg-secret", "target-token"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("config inspect leaked %q:\n%s", leaked, text)
		}
	}
	if !strings.Contains(text, "***REDACTED***") || !strings.Contains(text, `"projects"`) {
		t.Fatalf("config inspect output = %q", text)
	}
}

func TestRunConfigInspectCanIncludeSecrets(t *testing.T) {
	t.Setenv("KRONOS_TEST_PG_PASSWORD", "pg-secret")

	path := filepath.Join(t.TempDir(), "kronos.yaml")
	data := []byte(`
server:
  listen: "127.0.0.1:8500"
  data_dir: "/tmp/kronos"
projects:
  - name: default
    storages:
      - name: local
        backend: local
        path: "/tmp/repo"
    targets:
      - name: redis
        driver: redis
        connection:
          host: "127.0.0.1"
          port: 6379
          password: "${env:KRONOS_TEST_PG_PASSWORD}"
    schedules:
      - name: redis-nightly
        target: redis
        type: full
        cron: "0 2 * * *"
        storage: local
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"config", "inspect", "--config", path, "--include-secrets"}); err != nil {
		t.Fatalf("config inspect --include-secrets error = %v", err)
	}
	if !strings.Contains(out.String(), "pg-secret") {
		t.Fatalf("config inspect --include-secrets output = %q", out.String())
	}
}

func TestRunConfigHelpAndValidationErrors(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := runConfig(context.Background(), &out, nil); err != nil {
		t.Fatalf("runConfig(help) error = %v", err)
	}
	if !strings.Contains(out.String(), "kronos config validate") {
		t.Fatalf("help output = %q", out.String())
	}
	if err := runConfig(context.Background(), &out, []string{"missing"}); err == nil {
		t.Fatal("runConfig(unknown) error = nil, want error")
	}
	if err := runConfigValidate(context.Background(), &out, nil); err == nil {
		t.Fatal("runConfigValidate(missing config) error = nil, want error")
	}
	if err := runConfigInspect(context.Background(), &out, nil); err == nil {
		t.Fatal("runConfigInspect(missing config) error = nil, want error")
	}
}

func TestRedactConfigLeavesEmptySecretsEmpty(t *testing.T) {
	t.Parallel()

	cfg := redactConfig(config.Config{
		Projects: []config.ProjectConfig{{
			Storages: []config.StorageConfig{{Options: map[string]any{"secret_key": "", "endpoint": "http://localhost"}}},
			Targets:  []config.TargetConfig{{Options: map[string]any{"password": "", "tls": "disable"}}},
		}},
	})
	if cfg.Server.MasterPassphrase != "" || cfg.Projects[0].Storages[0].Options["secret_key"] != "***REDACTED***" || cfg.Projects[0].Targets[0].Options["password"] != "***REDACTED***" {
		t.Fatalf("redacted config = %#v", cfg)
	}
	if cfg.Projects[0].Storages[0].Options["endpoint"] != "http://localhost" || cfg.Projects[0].Targets[0].Options["tls"] != "disable" {
		t.Fatalf("non-secret options redacted: %#v", cfg)
	}
}

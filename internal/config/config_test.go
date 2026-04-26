package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/secret"
)

func TestLoadFileSample(t *testing.T) {
	t.Setenv("KRONOS_TEST_OIDC_SECRET", "oidc-secret")
	t.Setenv("KRONOS_TEST_PG_PASSWORD", "pg-secret")
	t.Setenv("KRONOS_TEST_KEY_SUFFIX", "suffix")

	cfg, err := LoadFile(context.Background(), "testdata/sample.yaml", secret.NewRegistry())
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}

	if cfg.Server.Auth.OIDC.ClientSecret != "oidc-secret" {
		t.Fatalf("OIDC client secret = %q, want oidc-secret", cfg.Server.Auth.OIDC.ClientSecret)
	}
	if cfg.Server.MasterPassphrase != "from-file-master" {
		t.Fatalf("master passphrase = %q, want from-file-master", cfg.Server.MasterPassphrase)
	}

	project := cfg.Projects[0]
	if project.Storages[0].EncryptionKey != "age-key-suffix" {
		t.Fatalf("encryption key = %q, want age-key-suffix", project.Storages[0].EncryptionKey)
	}
	if project.Targets[0].Connection.Password != "pg-secret" {
		t.Fatalf("target password = %q, want pg-secret", project.Targets[0].Connection.Password)
	}
}

func TestLoadUnknownSecretScheme(t *testing.T) {
	_, err := Load(context.Background(), []byte(`
server:
  listen: "127.0.0.1:8500"
  data_dir: "/tmp/kronos"
projects:
  - name: default
    storages:
      - name: local
        backend: local
        path: "${vault:secret/path#field}"
`), secret.NewRegistry())
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Load() error = %v, want ErrNotFound", err)
	}
}

func TestValidateReportsMissingScheduleReferences(t *testing.T) {
	err := Config{
		Server: ServerConfig{Listen: "127.0.0.1:8500", DataDir: "/tmp/kronos"},
		Projects: []ProjectConfig{
			{
				Name: "default",
				Schedules: []ScheduleConfig{
					{Name: "nightly", Target: "missing-target", Storage: "missing-storage", Cron: "0 2 * * *"},
				},
			},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want missing reference errors")
	}
	if !strings.Contains(err.Error(), "missing-target") || !strings.Contains(err.Error(), "missing-storage") {
		t.Fatalf("Validate() error = %v, want missing target and storage", err)
	}
}

func TestValidateAuthRateLimitSettings(t *testing.T) {
	t.Parallel()

	base := Config{
		Server: ServerConfig{Listen: "127.0.0.1:8500", DataDir: "/tmp/kronos"},
		Projects: []ProjectConfig{{
			Name:     "default",
			Storages: []StorageConfig{{Name: "local", Backend: "local", Path: "/tmp/repo"}},
			Targets:  []TargetConfig{{Name: "redis", Driver: "redis", Connection: ConnectionConfig{Host: "127.0.0.1"}}},
		}},
	}
	valid := base
	valid.Server.Auth.TokenVerifyRateLimit = 3
	valid.Server.Auth.TokenVerifyRateWindow = "30s"
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate(valid auth rate limit) error = %v", err)
	}

	negative := base
	negative.Server.Auth.TokenVerifyRateLimit = -1
	if err := negative.Validate(); err == nil || !strings.Contains(err.Error(), "token_verify_rate_limit") {
		t.Fatalf("Validate(negative rate limit) error = %v, want token_verify_rate_limit", err)
	}

	badWindow := base
	badWindow.Server.Auth.TokenVerifyRateWindow = "soon"
	if err := badWindow.Validate(); err == nil || !strings.Contains(err.Error(), "token_verify_rate_window") {
		t.Fatalf("Validate(bad window) error = %v, want token_verify_rate_window", err)
	}
}

func TestLoadRejectsInvalidYAMLAndPlaceholders(t *testing.T) {
	t.Parallel()

	if _, err := Load(context.Background(), []byte("server: ["), secret.NewRegistry()); err == nil {
		t.Fatal("Load(invalid yaml) error = nil, want error")
	}
	if _, err := Load(context.Background(), []byte(`
server:
  listen: "127.0.0.1:8500"
  data_dir: "/tmp/kronos"
projects:
  - name: default
    storages:
      - name: local
        backend: local
        path: "${env:KRONOS_MISSING"
`), secret.NewRegistry()); err == nil {
		t.Fatal("Load(unterminated placeholder) error = nil, want error")
	}
}

func TestLoadFileResolvesRelativeFileSecrets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("relative-secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
server:
  listen: "127.0.0.1:8500"
  data_dir: "/tmp/kronos"
  master_passphrase: "${file:secret.txt}"
projects:
  - name: default
    storages:
      - name: local
        backend: local
        path: "/tmp/kronos/repo"
    targets:
      - name: redis
        driver: redis
        connection:
          host: "127.0.0.1"
    schedules:
      - name: nightly
        target: redis
        storage: local
        type: full
        cron: "0 2 * * *"
`), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	cfg, err := LoadFile(context.Background(), configPath, secret.NewRegistry())
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if cfg.Server.MasterPassphrase != "relative-secret" {
		t.Fatalf("master passphrase = %q, want relative-secret", cfg.Server.MasterPassphrase)
	}
}

func TestExpandValueRejectsNonStringMapKey(t *testing.T) {
	t.Parallel()

	if _, err := expandValue(context.Background(), map[any]any{1: "value"}, secret.NewRegistry()); err == nil {
		t.Fatal("expandValue(non-string key) error = nil, want error")
	}
	got, err := expandValue(context.Background(), []any{"plain", 42, map[string]any{"nested": "value"}}, secret.NewRegistry())
	if err != nil {
		t.Fatalf("expandValue(slice) error = %v", err)
	}
	values, ok := got.([]any)
	if !ok || len(values) != 3 {
		t.Fatalf("expandValue(slice) = %#v", got)
	}
}

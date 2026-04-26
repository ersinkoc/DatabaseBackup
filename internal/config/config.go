package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/secret"
	"gopkg.in/yaml.v3"
)

// Config is the top-level Kronos configuration file.
type Config struct {
	Server        ServerConfig         `json:"server" yaml:"server"`
	Projects      []ProjectConfig      `json:"projects" yaml:"projects"`
	Notifications []NotificationConfig `json:"notifications,omitempty" yaml:"notifications,omitempty"`
}

// ServerConfig configures the control-plane server.
type ServerConfig struct {
	Listen           string     `json:"listen" yaml:"listen"`
	ListenWebUI      string     `json:"listen_webui,omitempty" yaml:"listen_webui,omitempty"`
	DataDir          string     `json:"data_dir" yaml:"data_dir"`
	TLS              TLSConfig  `json:"tls,omitempty" yaml:"tls,omitempty"`
	Auth             AuthConfig `json:"auth,omitempty" yaml:"auth,omitempty"`
	MasterPassphrase string     `json:"master_passphrase,omitempty" yaml:"master_passphrase,omitempty"`
}

// TLSConfig holds mTLS file paths.
type TLSConfig struct {
	Cert     string `json:"cert,omitempty" yaml:"cert,omitempty"`
	Key      string `json:"key,omitempty" yaml:"key,omitempty"`
	ClientCA string `json:"client_ca,omitempty" yaml:"client_ca,omitempty"`
}

// AuthConfig configures server authentication.
type AuthConfig struct {
	OIDC                  OIDCConfig `json:"oidc,omitempty" yaml:"oidc,omitempty"`
	TokenVerifyRateLimit  int        `json:"token_verify_rate_limit,omitempty" yaml:"token_verify_rate_limit,omitempty"`
	TokenVerifyRateWindow string     `json:"token_verify_rate_window,omitempty" yaml:"token_verify_rate_window,omitempty"`
}

// OIDCConfig configures an OpenID Connect identity provider.
type OIDCConfig struct {
	Issuer       string `json:"issuer,omitempty" yaml:"issuer,omitempty"`
	ClientID     string `json:"client_id,omitempty" yaml:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty" yaml:"client_secret,omitempty"`
}

// ProjectConfig groups targets, storage, and schedules.
type ProjectConfig struct {
	Name      string           `json:"name" yaml:"name"`
	Storages  []StorageConfig  `json:"storages,omitempty" yaml:"storages,omitempty"`
	Targets   []TargetConfig   `json:"targets,omitempty" yaml:"targets,omitempty"`
	Schedules []ScheduleConfig `json:"schedules,omitempty" yaml:"schedules,omitempty"`
}

// StorageConfig configures one storage backend.
type StorageConfig struct {
	Name          string         `json:"name" yaml:"name"`
	Backend       string         `json:"backend" yaml:"backend"`
	Path          string         `json:"path,omitempty" yaml:"path,omitempty"`
	Bucket        string         `json:"bucket,omitempty" yaml:"bucket,omitempty"`
	Region        string         `json:"region,omitempty" yaml:"region,omitempty"`
	Endpoint      string         `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Credentials   string         `json:"credentials,omitempty" yaml:"credentials,omitempty"`
	EncryptionKey string         `json:"encryption_key,omitempty" yaml:"encryption_key,omitempty"`
	Options       map[string]any `json:"options,omitempty" yaml:"options,omitempty"`
}

// TargetConfig configures one database target.
type TargetConfig struct {
	Name       string           `json:"name" yaml:"name"`
	Driver     string           `json:"driver" yaml:"driver"`
	Connection ConnectionConfig `json:"connection" yaml:"connection"`
	Agent      string           `json:"agent,omitempty" yaml:"agent,omitempty"`
	Tier       string           `json:"tier,omitempty" yaml:"tier,omitempty"`
	Options    map[string]any   `json:"options,omitempty" yaml:"options,omitempty"`
}

// ConnectionConfig holds database connection fields common to first drivers.
type ConnectionConfig struct {
	Host     string `json:"host,omitempty" yaml:"host,omitempty"`
	Port     int    `json:"port,omitempty" yaml:"port,omitempty"`
	User     string `json:"user,omitempty" yaml:"user,omitempty"`
	Password string `json:"password,omitempty" yaml:"password,omitempty"`
	Database string `json:"database,omitempty" yaml:"database,omitempty"`
	TLS      string `json:"tls,omitempty" yaml:"tls,omitempty"`
}

// ScheduleConfig configures a recurring backup.
type ScheduleConfig struct {
	Name      string      `json:"name" yaml:"name"`
	Target    string      `json:"target" yaml:"target"`
	Type      string      `json:"type" yaml:"type"`
	Cron      string      `json:"cron" yaml:"cron"`
	Storage   string      `json:"storage" yaml:"storage"`
	Retention string      `json:"retention,omitempty" yaml:"retention,omitempty"`
	Hooks     HooksConfig `json:"hooks,omitempty" yaml:"hooks,omitempty"`
}

// HooksConfig configures job lifecycle hooks.
type HooksConfig struct {
	PreBackup []HookAction `json:"pre_backup,omitempty" yaml:"pre_backup,omitempty"`
	OnFailure []HookAction `json:"on_failure,omitempty" yaml:"on_failure,omitempty"`
}

// HookAction configures one shell or webhook hook.
type HookAction struct {
	Shell   string `json:"shell,omitempty" yaml:"shell,omitempty"`
	Webhook string `json:"webhook,omitempty" yaml:"webhook,omitempty"`
}

// NotificationConfig configures event routing.
type NotificationConfig struct {
	Name     string   `json:"name,omitempty" yaml:"name,omitempty"`
	When     string   `json:"when" yaml:"when"`
	Events   []string `json:"events,omitempty" yaml:"events,omitempty"`
	Webhook  string   `json:"webhook,omitempty" yaml:"webhook,omitempty"`
	Secret   string   `json:"secret,omitempty" yaml:"secret,omitempty"`
	Channels []string `json:"channels,omitempty" yaml:"channels,omitempty"`
}

// LoadFile reads, expands, and validates a YAML config file.
func LoadFile(ctx context.Context, path string, resolver secret.Resolver) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	cfg, err := Load(ctx, data, withBaseDirResolver(filepath.Dir(path), resolver))
	if err != nil {
		return nil, fmt.Errorf("load config %q: %w", path, err)
	}
	return cfg, nil
}

func withBaseDirResolver(baseDir string, resolver secret.Resolver) secret.Resolver {
	if resolver == nil {
		resolver = secret.NewRegistry()
	}
	return secret.ResolverFunc(func(ctx context.Context, ref secret.Reference) (string, error) {
		if ref.Scheme == "file" && !filepath.IsAbs(ref.Path) {
			ref.Path = filepath.Join(baseDir, ref.Path)
		}
		return resolver.Resolve(ctx, ref)
	})
}

// Load expands placeholders, parses YAML, and validates the resulting config.
func Load(ctx context.Context, data []byte, resolver secret.Resolver) (*Config, error) {
	if resolver == nil {
		resolver = secret.NewRegistry()
	}

	expanded, err := expandYAML(ctx, data, resolver)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks the required structure of cfg.
func (c Config) Validate() error {
	var errs []error
	if c.Server.Listen == "" {
		errs = append(errs, errors.New("server.listen is required"))
	}
	if c.Server.DataDir == "" {
		errs = append(errs, errors.New("server.data_dir is required"))
	}
	if c.Server.Auth.TokenVerifyRateLimit < 0 {
		errs = append(errs, errors.New("server.auth.token_verify_rate_limit must be non-negative"))
	}
	if c.Server.Auth.TokenVerifyRateWindow != "" {
		if _, err := time.ParseDuration(c.Server.Auth.TokenVerifyRateWindow); err != nil {
			errs = append(errs, fmt.Errorf("server.auth.token_verify_rate_window: %w", err))
		}
	}
	if len(c.Projects) == 0 {
		errs = append(errs, errors.New("at least one project is required"))
	}
	for i, notification := range c.Notifications {
		events := notification.Events
		if notification.When != "" {
			events = append(events, notification.When)
		}
		if len(events) == 0 {
			errs = append(errs, fmt.Errorf("notifications[%d].when or events is required", i))
		}
		for _, event := range events {
			switch core.NotificationEvent(event) {
			case core.NotificationJobSucceeded, core.NotificationJobFailed, core.NotificationJobCanceled:
			default:
				errs = append(errs, fmt.Errorf("notifications[%d].event %q is not supported", i, event))
			}
		}
		if notification.Webhook == "" && len(notification.Channels) == 0 {
			errs = append(errs, fmt.Errorf("notifications[%d].webhook or channels is required", i))
		}
	}

	for i, project := range c.Projects {
		if project.Name == "" {
			errs = append(errs, fmt.Errorf("projects[%d].name is required", i))
		}
		storages := make(map[string]struct{}, len(project.Storages))
		for j, storage := range project.Storages {
			if storage.Name == "" {
				errs = append(errs, fmt.Errorf("projects[%d].storages[%d].name is required", i, j))
			}
			if storage.Backend == "" {
				errs = append(errs, fmt.Errorf("projects[%d].storages[%d].backend is required", i, j))
			}
			storages[storage.Name] = struct{}{}
		}

		targets := make(map[string]struct{}, len(project.Targets))
		for j, target := range project.Targets {
			if target.Name == "" {
				errs = append(errs, fmt.Errorf("projects[%d].targets[%d].name is required", i, j))
			}
			if target.Driver == "" {
				errs = append(errs, fmt.Errorf("projects[%d].targets[%d].driver is required", i, j))
			}
			targets[target.Name] = struct{}{}
		}

		for j, schedule := range project.Schedules {
			if schedule.Name == "" {
				errs = append(errs, fmt.Errorf("projects[%d].schedules[%d].name is required", i, j))
			}
			if schedule.Cron == "" {
				errs = append(errs, fmt.Errorf("projects[%d].schedules[%d].cron is required", i, j))
			}
			if _, ok := targets[schedule.Target]; schedule.Target == "" || !ok {
				errs = append(errs, fmt.Errorf("projects[%d].schedules[%d].target %q is not defined", i, j, schedule.Target))
			}
			if _, ok := storages[schedule.Storage]; schedule.Storage == "" || !ok {
				errs = append(errs, fmt.Errorf("projects[%d].schedules[%d].storage %q is not defined", i, j, schedule.Storage))
			}
		}
	}

	return errors.Join(errs...)
}

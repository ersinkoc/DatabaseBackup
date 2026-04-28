package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	kaudit "github.com/kronos/kronos/internal/audit"
	"github.com/kronos/kronos/internal/config"
	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/kvstore"
	"github.com/kronos/kronos/internal/obs"
	krestore "github.com/kronos/kronos/internal/restore"
	"github.com/kronos/kronos/internal/retention"
	sched "github.com/kronos/kronos/internal/schedule"
	"github.com/kronos/kronos/internal/secret"
	control "github.com/kronos/kronos/internal/server"
	"github.com/kronos/kronos/internal/webui"
)

const defaultSchedulerInterval = time.Minute
const authVerifyRateLimit = 10
const authVerifyRateWindow = time.Minute

func runServer(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("server", out)
	configPath := fs.String("config", "", "path to kronos YAML config")
	listenAddr := fs.String("listen", "127.0.0.1:8500", "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}

	listenSet := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == "listen" {
			listenSet = true
		}
	})
	var cfg *config.Config
	if *configPath != "" {
		loaded, err := config.LoadFile(ctx, *configPath, secret.NewRegistry())
		if err != nil {
			return err
		}
		cfg = loaded
		if !listenSet && cfg.Server.Listen != "" {
			*listenAddr = cfg.Server.Listen
		}
	}
	return serveControlPlane(ctx, out, *listenAddr, cfg)
}

type controlPlaneOptions struct {
	OnListen func(addr string) error
}

func serveControlPlane(ctx context.Context, out io.Writer, listenAddr string, cfg *config.Config) error {
	return serveControlPlaneWithOptions(ctx, out, listenAddr, cfg, controlPlaneOptions{})
}

func serveControlPlaneWithOptions(ctx context.Context, out io.Writer, listenAddr string, cfg *config.Config, opts controlPlaneOptions) error {
	if listenAddr == "" {
		return fmt.Errorf("listen address is required")
	}
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	var stateDB *kvstore.DB
	var stores apiStores
	if cfg != nil && cfg.Server.DataDir != "" {
		db, recovered, err := openServerState(cfg.Server.DataDir)
		if err != nil {
			listener.Close()
			return err
		}
		stateDB = db
		openedStores, err := newAPIStores(db)
		if err != nil {
			db.Close()
			listener.Close()
			return err
		}
		stores = openedStores
		if err := seedAPIStoresFromConfig(stores, cfg, time.Now().UTC()); err != nil {
			db.Close()
			listener.Close()
			return err
		}
		if recovered > 0 {
			fmt.Fprintf(out, "recovered_failed_jobs=%d\n", recovered)
		}
	}
	defer func() {
		if stateDB != nil {
			stateDB.Close()
		}
	}()

	registry := control.NewAgentRegistry(nil, 30*time.Second)
	server := &http.Server{
		Handler:           newServerHandlerWithStores(cfg, registry, stores),
		ReadHeaderTimeout: 5 * time.Second,
	}
	startSchedulerLoop(ctx, out, stores, registry, defaultSchedulerInterval)
	errCh := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if err == http.ErrServerClosed {
			err = nil
		}
		errCh <- err
	}()
	fmt.Fprintf(out, "kronos-server listening=%s\n", listener.Addr().String())
	if cfg != nil {
		fmt.Fprintf(out, "projects=%d\n", len(cfg.Projects))
	}
	if opts.OnListen != nil {
		if err := opts.OnListen(listener.Addr().String()); err != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
				return shutdownErr
			}
			if serveErr := <-errCh; serveErr != nil {
				return serveErr
			}
			return err
		}
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if err := <-errCh; err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func startSchedulerLoop(ctx context.Context, out io.Writer, stores apiStores, registry *control.AgentRegistry, interval time.Duration) {
	if stores.schedules == nil || stores.scheduleStates == nil || stores.jobs == nil {
		return
	}
	if interval <= 0 {
		interval = defaultSchedulerInterval
	}
	if out == nil {
		out = io.Discard
	}
	go func() {
		tickScheduler(ctx, out, stores, registry)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tickScheduler(ctx, out, stores, registry)
			}
		}
	}()
}

func tickScheduler(ctx context.Context, out io.Writer, stores apiStores, registry *control.AgentRegistry) {
	if err := ctx.Err(); err != nil {
		return
	}
	if failed, _, err := failLostAgentJobs(stores.jobs, registry, time.Now().UTC()); err != nil {
		fmt.Fprintf(out, "scheduler_error=%q\n", err.Error())
		return
	} else if failed > 0 {
		fmt.Fprintf(out, "agent_lost_jobs=%d\n", failed)
	}
	runner, err := control.NewSchedulerRunner(stores.schedules, stores.scheduleStates, stores.jobs, core.RealClock{})
	if err != nil {
		fmt.Fprintf(out, "scheduler_error=%q\n", err.Error())
		return
	}
	runner.Backups = stores.backups
	jobs, err := runner.Tick()
	if err != nil {
		fmt.Fprintf(out, "scheduler_error=%q\n", err.Error())
		return
	}
	if len(jobs) > 0 {
		fmt.Fprintf(out, "scheduler_enqueued_jobs=%d\n", len(jobs))
	}
}

func openServerState(dataDir string) (*kvstore.DB, int, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, 0, err
	}
	db, err := kvstore.Open(filepath.Join(dataDir, "state.db"))
	if err != nil {
		return nil, 0, err
	}
	store, err := control.NewJobStore(db)
	if err != nil {
		db.Close()
		return nil, 0, err
	}
	recovered, err := store.FailActive(time.Now().UTC(), "server_lost")
	if err != nil {
		db.Close()
		return nil, 0, err
	}
	return db, recovered, nil
}

func newServerHandler(cfg *config.Config) http.Handler {
	return newServerHandlerWithStores(cfg, control.NewAgentRegistry(nil, 30*time.Second), apiStores{})
}

func newServerHandlerWithRegistry(cfg *config.Config, registry *control.AgentRegistry) http.Handler {
	return newServerHandlerWithStores(cfg, registry, apiStores{})
}

type apiStores struct {
	jobs           *control.JobStore
	audit          *kaudit.Log
	tokens         *control.TokenStore
	users          *control.UserStore
	targets        *control.TargetStore
	storages       *control.StorageStore
	schedules      *control.ScheduleStore
	scheduleStates *control.ScheduleStateStore
	backups        *control.BackupStore
	policies       *control.RetentionPolicyStore
	notifications  *control.NotificationRuleStore
}

type authRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	clients map[string]authRateWindow
	ticks   uint64
	limited uint64
}

type authRateWindow struct {
	start time.Time
	count int
}

func newAuthRateLimiter(limit int, window time.Duration) *authRateLimiter {
	if limit <= 0 {
		limit = authVerifyRateLimit
	}
	if window <= 0 {
		window = authVerifyRateWindow
	}
	return &authRateLimiter{
		limit:   limit,
		window:  window,
		clients: make(map[string]authRateWindow),
	}
}

func (l *authRateLimiter) Allow(r *http.Request) bool {
	if l == nil {
		return true
	}
	key := authRateLimitKey(r)
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.ticks++
	if l.ticks%uint64(l.limit) == 0 {
		l.pruneLocked(now)
	}

	window := l.clients[key]
	if window.start.IsZero() || now.Sub(window.start) >= l.window {
		l.clients[key] = authRateWindow{start: now, count: 1}
		return true
	}
	if window.count >= l.limit {
		l.limited++
		return false
	}
	window.count++
	l.clients[key] = window
	return true
}

func (l *authRateLimiter) LimitedTotal() uint64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limited
}

func (l *authRateLimiter) pruneLocked(now time.Time) {
	for key, window := range l.clients {
		if window.start.IsZero() || now.Sub(window.start) >= l.window {
			delete(l.clients, key)
		}
	}
}

func authRateLimitKey(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get(obs.RequestIDHeader))
		if requestID == "" {
			requestID = obs.NewRequestID()
			r.Header.Set(obs.RequestIDHeader, requestID)
		}
		w.Header().Set(obs.RequestIDHeader, requestID)
		next.ServeHTTP(w, r.WithContext(obs.WithRequestID(r.Context(), requestID)))
	})
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := w.Header()
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		headers.Set("Referrer-Policy", "no-referrer")
		headers.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		headers.Set("Cross-Origin-Opener-Policy", "same-origin")
		headers.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func withControlPlaneCacheHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isControlPlanePath(r.URL.Path) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Pragma", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func isControlPlanePath(path string) bool {
	return path == "/healthz" || path == "/readyz" || path == "/metrics" || strings.HasPrefix(path, "/api/")
}

func authRateLimitSettings(cfg *config.Config) (int, time.Duration) {
	limit := authVerifyRateLimit
	window := authVerifyRateWindow
	if cfg == nil {
		return limit, window
	}
	if cfg.Server.Auth.TokenVerifyRateLimit > 0 {
		limit = cfg.Server.Auth.TokenVerifyRateLimit
	}
	if cfg.Server.Auth.TokenVerifyRateWindow != "" {
		if parsed, err := time.ParseDuration(cfg.Server.Auth.TokenVerifyRateWindow); err == nil && parsed > 0 {
			window = parsed
		}
	}
	return limit, window
}

func newAPIStores(db *kvstore.DB) (apiStores, error) {
	jobs, err := control.NewJobStore(db)
	if err != nil {
		return apiStores{}, err
	}
	auditLog, err := kaudit.New(db, core.RealClock{})
	if err != nil {
		return apiStores{}, err
	}
	tokens, err := control.NewTokenStore(db, core.RealClock{})
	if err != nil {
		return apiStores{}, err
	}
	users, err := control.NewUserStore(db)
	if err != nil {
		return apiStores{}, err
	}
	targets, err := control.NewTargetStore(db)
	if err != nil {
		return apiStores{}, err
	}
	storages, err := control.NewStorageStore(db)
	if err != nil {
		return apiStores{}, err
	}
	schedules, err := control.NewScheduleStore(db)
	if err != nil {
		return apiStores{}, err
	}
	scheduleStates, err := control.NewScheduleStateStore(db)
	if err != nil {
		return apiStores{}, err
	}
	backups, err := control.NewBackupStore(db)
	if err != nil {
		return apiStores{}, err
	}
	policies, err := control.NewRetentionPolicyStore(db)
	if err != nil {
		return apiStores{}, err
	}
	notifications, err := control.NewNotificationRuleStore(db)
	if err != nil {
		return apiStores{}, err
	}
	return apiStores{jobs: jobs, audit: auditLog, tokens: tokens, users: users, targets: targets, storages: storages, schedules: schedules, scheduleStates: scheduleStates, backups: backups, policies: policies, notifications: notifications}, nil
}

func seedAPIStoresFromConfig(stores apiStores, cfg *config.Config, now time.Time) error {
	if cfg == nil || stores.targets == nil || stores.storages == nil || stores.schedules == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if stores.notifications != nil {
		for i, notification := range cfg.Notifications {
			name := notification.Name
			if name == "" {
				name = fmt.Sprintf("notification-%d", i+1)
			}
			events := notificationEvents(notification)
			webhook := notification.Webhook
			if webhook == "" && len(notification.Channels) > 0 {
				webhook = notification.Channels[0]
			}
			if err := stores.notifications.Save(core.NotificationRule{
				ID:          core.ID("config/" + name),
				Name:        name,
				Events:      events,
				WebhookURL:  webhook,
				Secret:      notification.Secret,
				MaxAttempts: notification.MaxAttempts,
				Enabled:     true,
				CreatedAt:   now,
				UpdatedAt:   now,
			}); err != nil {
				return fmt.Errorf("seed notification %s: %w", name, err)
			}
		}
	}
	for _, project := range cfg.Projects {
		targetIDs := make(map[string]core.ID, len(project.Targets))
		storageIDs := make(map[string]core.ID, len(project.Storages))
		for _, storage := range project.Storages {
			id := configResourceID(project.Name, storage.Name)
			storageIDs[storage.Name] = id
			if err := stores.storages.Save(core.Storage{
				ID:        id,
				Name:      storage.Name,
				Kind:      core.StorageKind(storage.Backend),
				URI:       storageURI(storage),
				Options:   storageOptions(storage),
				CreatedAt: now,
				UpdatedAt: now,
				Labels:    map[string]string{"project": project.Name},
			}); err != nil {
				return fmt.Errorf("seed storage %s/%s: %w", project.Name, storage.Name, err)
			}
		}
		for _, target := range project.Targets {
			id := configResourceID(project.Name, target.Name)
			targetIDs[target.Name] = id
			labels := map[string]string{"project": project.Name}
			if target.Agent != "" {
				labels["agent"] = target.Agent
			}
			if target.Tier != "" {
				labels["tier"] = target.Tier
			}
			if err := stores.targets.Save(core.Target{
				ID:        id,
				Name:      target.Name,
				Driver:    core.TargetDriver(target.Driver),
				Endpoint:  connectionEndpoint(target.Connection),
				Database:  target.Connection.Database,
				Options:   targetOptions(target),
				CreatedAt: now,
				UpdatedAt: now,
				Labels:    labels,
			}); err != nil {
				return fmt.Errorf("seed target %s/%s: %w", project.Name, target.Name, err)
			}
		}
		for _, schedule := range project.Schedules {
			backupType := core.BackupType(schedule.Type)
			if backupType == "" {
				backupType = core.BackupTypeFull
			}
			if err := stores.schedules.Save(core.Schedule{
				ID:              configResourceID(project.Name, schedule.Name),
				Name:            schedule.Name,
				TargetID:        targetIDs[schedule.Target],
				StorageID:       storageIDs[schedule.Storage],
				BackupType:      backupType,
				Expression:      schedule.Cron,
				RetentionPolicy: core.ID(schedule.Retention),
				CreatedAt:       now,
				UpdatedAt:       now,
				Labels:          map[string]string{"project": project.Name},
			}); err != nil {
				return fmt.Errorf("seed schedule %s/%s: %w", project.Name, schedule.Name, err)
			}
		}
	}
	return nil
}

func notificationEvents(notification config.NotificationConfig) []core.NotificationEvent {
	values := notification.Events
	if notification.When != "" {
		values = append(values, notification.When)
	}
	events := make([]core.NotificationEvent, 0, len(values))
	seen := make(map[core.NotificationEvent]struct{}, len(values))
	for _, value := range values {
		event := core.NotificationEvent(strings.TrimSpace(value))
		if event == "" {
			continue
		}
		if _, ok := seen[event]; ok {
			continue
		}
		seen[event] = struct{}{}
		events = append(events, event)
	}
	return events
}

func configResourceID(project string, name string) core.ID {
	if project == "" {
		return core.ID(name)
	}
	return core.ID(project + "/" + name)
}

func connectionEndpoint(connection config.ConnectionConfig) string {
	if connection.Host == "" {
		return ""
	}
	if connection.Port <= 0 {
		return connection.Host
	}
	return fmt.Sprintf("%s:%d", connection.Host, connection.Port)
}

func storageURI(storage config.StorageConfig) string {
	switch storage.Backend {
	case string(core.StorageKindLocal):
		if storage.Path == "" {
			return ""
		}
		return "file://" + storage.Path
	case string(core.StorageKindS3):
		if storage.Bucket == "" {
			return ""
		}
		return "s3://" + storage.Bucket
	default:
		if storage.Path != "" {
			return storage.Backend + "://" + storage.Path
		}
		if storage.Bucket != "" {
			return storage.Backend + "://" + storage.Bucket
		}
		return ""
	}
}

func storageOptions(storage config.StorageConfig) map[string]any {
	out := cloneOptions(storage.Options)
	if out == nil {
		out = map[string]any{}
	}
	setOption := func(key string, value string) {
		if value != "" {
			out[key] = value
		}
	}
	setOption("region", storage.Region)
	setOption("endpoint", storage.Endpoint)
	setOption("credentials", storage.Credentials)
	setOption("encryption_key", storage.EncryptionKey)
	if len(out) == 0 {
		return nil
	}
	return out
}

func targetOptions(target config.TargetConfig) map[string]any {
	out := cloneOptions(target.Options)
	if out == nil {
		out = map[string]any{}
	}
	setOption := func(key string, value string) {
		if value != "" {
			out[key] = value
		}
	}
	setOption("username", target.Connection.User)
	setOption("password", target.Connection.Password)
	setOption("tls", target.Connection.TLS)
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneOptions(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func newServerHandlerWithStores(cfg *config.Config, registry *control.AgentRegistry, stores apiStores) http.Handler {
	if registry == nil {
		registry = control.NewAgentRegistry(nil, 30*time.Second)
	}
	startedAt := time.Now().UTC()
	authLimiter := newAuthRateLimiter(authRateLimitSettings(cfg))
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if cfg == nil {
			fmt.Fprint(w, `{"status":"ok"}`)
			return
		}
		fmt.Fprintf(w, `{"status":"ok","projects":%d}`, len(cfg.Projects))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
			return
		}
		handleReadiness(w, r, stores)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
			return
		}
		if !requireScope(w, r, stores.tokens, "metrics:read") {
			return
		}
		handleMetrics(w, r, registry, stores, authLimiter, startedAt)
	})
	mux.HandleFunc("/api/v1/overview", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
			return
		}
		if !requireScope(w, r, stores.tokens, "metrics:read") {
			return
		}
		handleOverview(w, r, registry, stores)
	})
	mux.HandleFunc("/api/v1/agents/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodPost) {
			return
		}
		if !requireScope(w, r, stores.tokens, "agent:write") {
			return
		}
		var heartbeat control.AgentHeartbeat
		if err := decodeJSONRequest(w, r, &heartbeat); err != nil {
			http.Error(w, "invalid heartbeat", http.StatusBadRequest)
			return
		}
		if heartbeat.ID == "" {
			http.Error(w, "agent id is required", http.StatusBadRequest)
			return
		}
		writeJSON(w, registry.Heartbeat(heartbeat))
	})
	mux.HandleFunc("/api/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodGet) {
			return
		}
		if !requireScope(w, r, stores.tokens, "agent:read") {
			return
		}
		writeJSON(w, map[string]any{"agents": registry.List()})
	})
	mux.HandleFunc("/api/v1/agents/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/agents/")
		if !allowMethods(w, r, http.MethodGet) {
			return
		}
		if !requireScope(w, r, stores.tokens, "agent:read") {
			return
		}
		agent, ok := registry.Get(id)
		if !ok {
			http.NotFound(w, nil)
			return
		}
		writeJSON(w, agent)
	})
	mux.HandleFunc("/api/v1/audit", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodGet) {
			return
		}
		if !requireScope(w, r, stores.tokens, "audit:read") {
			return
		}
		handleListAudit(w, r, stores.audit)
	})
	mux.HandleFunc("/api/v1/audit/verify", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodPost) {
			return
		}
		if !requireScope(w, r, stores.tokens, "audit:verify") {
			return
		}
		handleVerifyAudit(w, r, stores.audit)
	})
	mux.HandleFunc("/api/v1/auth/verify", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodPost) {
			return
		}
		if !authLimiter.Allow(r) {
			w.Header().Set("Retry-After", strconv.Itoa(int(authVerifyRateWindow/time.Second)))
			http.Error(w, "too many auth attempts", http.StatusTooManyRequests)
			return
		}
		handleVerifyBearerToken(w, r, stores.tokens)
	})
	mux.HandleFunc("/api/v1/tokens", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !requireScope(w, r, stores.tokens, "token:read") {
				return
			}
			handleListTokens(w, stores.tokens)
		case http.MethodPost:
			if !requireScope(w, r, stores.tokens, "token:write") {
				return
			}
			handleCreateToken(w, r, stores.tokens, stores.users, stores.audit)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	})
	mux.HandleFunc("/api/v1/tokens/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/tokens/")
		if path == "prune" {
			if !allowMethods(w, r, http.MethodPost) {
				return
			}
			if !requireScope(w, r, stores.tokens, "token:write") {
				return
			}
			handlePruneTokens(w, r, stores.tokens, stores.audit)
			return
		}
		id, action, _ := strings.Cut(path, "/")
		switch {
		case r.Method == http.MethodGet && action == "":
			if !requireScope(w, r, stores.tokens, "token:read") {
				return
			}
			handleGetToken(w, stores.tokens, core.ID(id))
		case r.Method == http.MethodPost && action == "revoke":
			if !requireScope(w, r, stores.tokens, "token:write") {
				return
			}
			handleRevokeToken(w, r, stores.tokens, stores.audit, core.ID(id))
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	})
	mux.HandleFunc("/api/v1/users", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !requireScope(w, r, stores.tokens, "user:read") {
				return
			}
			handleListUsers(w, stores.users)
		case http.MethodPost:
			if !requireScope(w, r, stores.tokens, "user:write") {
				return
			}
			handleCreateUser(w, r, stores.users, stores.audit)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	})
	mux.HandleFunc("/api/v1/users/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
		idText, action, _ := strings.Cut(path, "/")
		id := core.ID(idText)
		switch {
		case r.Method == http.MethodGet && action == "":
			if !requireScope(w, r, stores.tokens, "user:read") {
				return
			}
			handleGetUser(w, stores.users, id)
		case r.Method == http.MethodDelete && action == "":
			if !requireScope(w, r, stores.tokens, "user:write") {
				return
			}
			handleDeleteUser(w, r, stores.users, stores.audit, id)
		case r.Method == http.MethodPost && action == "grant":
			if !requireScope(w, r, stores.tokens, "user:write") {
				return
			}
			handleGrantUser(w, r, stores.users, stores.audit, id)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodDelete, http.MethodPost)
		}
	})
	mux.HandleFunc("/api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodGet) {
			return
		}
		if !requireScope(w, r, stores.tokens, "job:read") {
			return
		}
		if stores.jobs == nil {
			writeJSON(w, map[string]any{"jobs": []any{}})
			return
		}
		filters, err := parseJobListFilters(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jobs, err := stores.jobs.List()
		if err != nil {
			http.Error(w, "list jobs", http.StatusInternalServerError)
			return
		}
		jobs = filterJobs(jobs, filters)
		writeJSON(w, map[string]any{"jobs": jobs})
	})
	mux.HandleFunc("/api/v1/jobs/claim", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodPost) {
			return
		}
		if !requireScope(w, r, stores.tokens, "job:write") {
			return
		}
		handleClaimJob(w, r, stores.jobs, stores.targets, stores.audit, registry)
	})
	mux.HandleFunc("/api/v1/jobs/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
		id, action, _ := strings.Cut(path, "/")
		switch {
		case r.Method == http.MethodGet && action == "":
			if !requireScope(w, r, stores.tokens, "job:read") {
				return
			}
			handleGetJob(w, stores.jobs, core.ID(id))
		case r.Method == http.MethodPost && action == "cancel":
			if !requireScope(w, r, stores.tokens, "job:write") {
				return
			}
			handleCancelJob(w, r, stores.jobs, stores.audit, core.ID(id))
		case r.Method == http.MethodPost && action == "retry":
			if !requireScope(w, r, stores.tokens, "job:write") {
				return
			}
			handleRetryJob(w, r, stores.jobs, stores.audit, core.ID(id))
		case r.Method == http.MethodPost && action == "finish":
			if !requireScope(w, r, stores.tokens, "job:write") {
				return
			}
			handleFinishJob(w, r, stores.jobs, stores.backups, stores.audit, stores.notifications, core.ID(id))
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	})
	mux.HandleFunc("/api/v1/scheduler/tick", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodPost) {
			return
		}
		if !requireScope(w, r, stores.tokens, "schedule:write") {
			return
		}
		handleSchedulerTick(w, r, stores, registry)
	})
	mux.HandleFunc("/api/v1/backups/now", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodPost) {
			return
		}
		if !requireScope(w, r, stores.tokens, "backup:write") {
			return
		}
		handleBackupNow(w, r, stores.jobs, stores.audit)
	})
	mux.HandleFunc("/api/v1/backups", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodGet) {
			return
		}
		if !requireScope(w, r, stores.tokens, "backup:read") {
			return
		}
		handleListBackups(w, r, stores.backups)
	})
	mux.HandleFunc("/api/v1/backups/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/backups/")
		id, action, _ := strings.Cut(path, "/")
		switch {
		case r.Method == http.MethodGet && action == "":
			if !requireScope(w, r, stores.tokens, "backup:read") {
				return
			}
			handleGetBackup(w, stores.backups, core.ID(id))
		case r.Method == http.MethodPost && action == "protect":
			if !requireScope(w, r, stores.tokens, "backup:write") {
				return
			}
			handleProtectBackup(w, r, stores.backups, stores.audit, core.ID(id), true)
		case r.Method == http.MethodPost && action == "unprotect":
			if !requireScope(w, r, stores.tokens, "backup:write") {
				return
			}
			handleProtectBackup(w, r, stores.backups, stores.audit, core.ID(id), false)
		case r.Method == http.MethodPost && action == "verify":
			if !requireScope(w, r, stores.tokens, "backup:write") {
				return
			}
			handleBackupVerify(w, r, stores.backups, stores.jobs, stores.audit, core.ID(id))
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	})
	mux.HandleFunc("/api/v1/retention/plan", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodPost) {
			return
		}
		if !requireScope(w, r, stores.tokens, "retention:read") {
			return
		}
		handleRetentionPlan(w, r, stores.backups)
	})
	mux.HandleFunc("/api/v1/retention/apply", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodPost) {
			return
		}
		if !requireScope(w, r, stores.tokens, "retention:write") {
			return
		}
		handleRetentionApply(w, r, stores.backups, stores.audit)
	})
	mux.HandleFunc("/api/v1/retention/policies", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !requireScope(w, r, stores.tokens, "retention:read") {
				return
			}
			handleListRetentionPolicies(w, stores.policies)
		case http.MethodPost:
			if !requireScope(w, r, stores.tokens, "retention:write") {
				return
			}
			handleCreateRetentionPolicy(w, r, stores.policies, stores.audit)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	})
	mux.HandleFunc("/api/v1/retention/policies/", func(w http.ResponseWriter, r *http.Request) {
		id := core.ID(strings.TrimPrefix(r.URL.Path, "/api/v1/retention/policies/"))
		switch r.Method {
		case http.MethodGet:
			if !requireScope(w, r, stores.tokens, "retention:read") {
				return
			}
			handleGetRetentionPolicy(w, stores.policies, id)
		case http.MethodPut:
			if !requireScope(w, r, stores.tokens, "retention:write") {
				return
			}
			handleUpdateRetentionPolicy(w, r, stores.policies, stores.audit, id)
		case http.MethodDelete:
			if !requireScope(w, r, stores.tokens, "retention:write") {
				return
			}
			handleDeleteRetentionPolicy(w, r, stores.policies, stores.audit, id)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete)
		}
	})
	mux.HandleFunc("/api/v1/notifications", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !requireScope(w, r, stores.tokens, "notification:read") {
				return
			}
			if wantsSecrets(r) && !requireScope(w, r, stores.tokens, "notification:write") {
				return
			}
			handleListNotifications(w, r, stores.notifications)
		case http.MethodPost:
			if !requireScope(w, r, stores.tokens, "notification:write") {
				return
			}
			handleCreateNotification(w, r, stores.notifications, stores.audit)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	})
	mux.HandleFunc("/api/v1/notifications/", func(w http.ResponseWriter, r *http.Request) {
		id := core.ID(strings.TrimPrefix(r.URL.Path, "/api/v1/notifications/"))
		switch r.Method {
		case http.MethodGet:
			if !requireScope(w, r, stores.tokens, "notification:read") {
				return
			}
			if wantsSecrets(r) && !requireScope(w, r, stores.tokens, "notification:write") {
				return
			}
			handleGetNotification(w, r, stores.notifications, id)
		case http.MethodPut:
			if !requireScope(w, r, stores.tokens, "notification:write") {
				return
			}
			handleUpdateNotification(w, r, stores.notifications, stores.audit, id)
		case http.MethodDelete:
			if !requireScope(w, r, stores.tokens, "notification:write") {
				return
			}
			handleDeleteNotification(w, r, stores.notifications, stores.audit, id)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete)
		}
	})
	mux.HandleFunc("/api/v1/restore", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodPost) {
			return
		}
		if !requireScope(w, r, stores.tokens, "restore:write") {
			return
		}
		handleRestoreStart(w, r, stores.backups, stores.jobs, stores.audit)
	})
	mux.HandleFunc("/api/v1/restore/preview", func(w http.ResponseWriter, r *http.Request) {
		if !allowMethods(w, r, http.MethodPost) {
			return
		}
		if !requireScope(w, r, stores.tokens, "restore:read") {
			return
		}
		handleRestorePreview(w, r, stores.backups)
	})
	mux.HandleFunc("/api/v1/targets", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !requireScope(w, r, stores.tokens, "target:read") {
				return
			}
			if wantsSecrets(r) && !requireScope(w, r, stores.tokens, "agent:write") {
				return
			}
			handleListTargets(w, r, stores.targets)
		case http.MethodPost:
			if !requireScope(w, r, stores.tokens, "target:write") {
				return
			}
			handleCreateTarget(w, r, stores.targets, stores.audit)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	})
	mux.HandleFunc("/api/v1/targets/", func(w http.ResponseWriter, r *http.Request) {
		id := core.ID(strings.TrimPrefix(r.URL.Path, "/api/v1/targets/"))
		switch r.Method {
		case http.MethodGet:
			if !requireScope(w, r, stores.tokens, "target:read") {
				return
			}
			if wantsSecrets(r) && !requireScope(w, r, stores.tokens, "agent:write") {
				return
			}
			handleGetTarget(w, r, stores.targets, id)
		case http.MethodPut:
			if !requireScope(w, r, stores.tokens, "target:write") {
				return
			}
			handleUpdateTarget(w, r, stores.targets, stores.audit, id)
		case http.MethodDelete:
			if !requireScope(w, r, stores.tokens, "target:write") {
				return
			}
			handleDeleteTarget(w, r, stores.targets, stores.audit, id)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete)
		}
	})
	mux.HandleFunc("/api/v1/storages", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !requireScope(w, r, stores.tokens, "storage:read") {
				return
			}
			if wantsSecrets(r) && !requireScope(w, r, stores.tokens, "agent:write") {
				return
			}
			handleListStorages(w, r, stores.storages)
		case http.MethodPost:
			if !requireScope(w, r, stores.tokens, "storage:write") {
				return
			}
			handleCreateStorage(w, r, stores.storages, stores.audit)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	})
	mux.HandleFunc("/api/v1/storages/", func(w http.ResponseWriter, r *http.Request) {
		id := core.ID(strings.TrimPrefix(r.URL.Path, "/api/v1/storages/"))
		switch r.Method {
		case http.MethodGet:
			if !requireScope(w, r, stores.tokens, "storage:read") {
				return
			}
			if wantsSecrets(r) && !requireScope(w, r, stores.tokens, "agent:write") {
				return
			}
			handleGetStorage(w, r, stores.storages, id)
		case http.MethodPut:
			if !requireScope(w, r, stores.tokens, "storage:write") {
				return
			}
			handleUpdateStorage(w, r, stores.storages, stores.audit, id)
		case http.MethodDelete:
			if !requireScope(w, r, stores.tokens, "storage:write") {
				return
			}
			handleDeleteStorage(w, r, stores.storages, stores.audit, id)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete)
		}
	})
	mux.HandleFunc("/api/v1/schedules", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !requireScope(w, r, stores.tokens, "schedule:read") {
				return
			}
			handleListSchedules(w, stores.schedules)
		case http.MethodPost:
			if !requireScope(w, r, stores.tokens, "schedule:write") {
				return
			}
			handleCreateSchedule(w, r, stores.schedules, stores.audit)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	})
	mux.HandleFunc("/api/v1/schedules/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/schedules/")
		idText, action, _ := strings.Cut(path, "/")
		id := core.ID(idText)
		switch {
		case r.Method == http.MethodGet && action == "":
			if !requireScope(w, r, stores.tokens, "schedule:read") {
				return
			}
			handleGetSchedule(w, stores.schedules, id)
		case r.Method == http.MethodPut && action == "":
			if !requireScope(w, r, stores.tokens, "schedule:write") {
				return
			}
			handleUpdateSchedule(w, r, stores.schedules, stores.audit, id)
		case r.Method == http.MethodDelete && action == "":
			if !requireScope(w, r, stores.tokens, "schedule:write") {
				return
			}
			handleDeleteSchedule(w, r, stores.schedules, stores.audit, id)
		case r.Method == http.MethodPost && action == "pause":
			if !requireScope(w, r, stores.tokens, "schedule:write") {
				return
			}
			handlePauseSchedule(w, r, stores.schedules, stores.audit, id, true)
		case r.Method == http.MethodPost && action == "resume":
			if !requireScope(w, r, stores.tokens, "schedule:write") {
				return
			}
			handlePauseSchedule(w, r, stores.schedules, stores.audit, id, false)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPost)
		}
	})
	mux.Handle("/", webui.Handler())
	return withSecurityHeaders(withControlPlaneCacheHeaders(withRequestID(mux)))
}

func allowMethods(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, method := range methods {
		if r.Method == method {
			return true
		}
	}
	methodNotAllowed(w, methods...)
	return false
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

func handleMetrics(w http.ResponseWriter, r *http.Request, registry *control.AgentRegistry, stores apiStores, authLimiter *authRateLimiter, startedAt time.Time) {
	now := time.Now().UTC()
	uptime := int64(0)
	if !startedAt.IsZero() {
		uptime = int64(now.Sub(startedAt).Seconds())
		if uptime < 0 {
			uptime = 0
		}
	}
	snapshot := obs.MetricsSnapshot{
		ProcessStartedAt:       startedAt.Unix(),
		ProcessUptimeSeconds:   uptime,
		TargetsByDriver:        make(map[core.TargetDriver]int),
		StoragesByKind:         make(map[core.StorageKind]int),
		SchedulesByType:        make(map[core.BackupType]int),
		JobsByStatus:           make(map[core.JobStatus]int),
		JobsByOperation:        make(map[core.JobOperation]int),
		JobsActiveByOperation:  make(map[core.JobOperation]int),
		JobsActiveByAgent:      make(map[string]int),
		BackupsByType:          make(map[core.BackupType]int),
		BackupsByTarget:        make(map[core.ID]int),
		BackupsByStorage:       make(map[core.ID]int),
		BackupsBytesByTarget:   make(map[core.ID]int64),
		BackupsBytesByStorage:  make(map[core.ID]int64),
		BackupsLatestByTarget:  make(map[core.ID]int64),
		BackupsLatestByStorage: make(map[core.ID]int64),
		AuthRateLimitedTotal:   authLimiter.LimitedTotal(),
	}
	if registry != nil {
		for _, agent := range registry.List() {
			switch agent.Status {
			case control.AgentHealthy:
				snapshot.AgentsHealthy++
				capacity := agent.Capacity
				if capacity <= 0 {
					capacity = 1
				}
				snapshot.AgentsCapacity += capacity
			case control.AgentDegraded:
				snapshot.AgentsDegraded++
			}
		}
	}
	if stores.targets != nil {
		targets, err := stores.targets.List()
		if err != nil {
			http.Error(w, "list targets", http.StatusInternalServerError)
			return
		}
		snapshot.TargetsTotal = len(targets)
		for _, target := range targets {
			snapshot.TargetsByDriver[target.Driver]++
		}
	}
	if stores.storages != nil {
		storages, err := stores.storages.List()
		if err != nil {
			http.Error(w, "list storages", http.StatusInternalServerError)
			return
		}
		snapshot.StoragesTotal = len(storages)
		for _, storage := range storages {
			snapshot.StoragesByKind[storage.Kind]++
		}
	}
	if stores.schedules != nil {
		schedules, err := stores.schedules.List()
		if err != nil {
			http.Error(w, "list schedules", http.StatusInternalServerError)
			return
		}
		snapshot.SchedulesTotal = len(schedules)
		for _, schedule := range schedules {
			snapshot.SchedulesByType[schedule.BackupType]++
			if schedule.Paused {
				snapshot.SchedulesPaused++
			}
		}
	}
	if stores.jobs != nil {
		jobs, err := stores.jobs.List()
		if err != nil {
			http.Error(w, "list jobs", http.StatusInternalServerError)
			return
		}
		for _, job := range jobs {
			snapshot.JobsByStatus[job.Status]++
			if job.Operation != "" {
				snapshot.JobsByOperation[job.Operation]++
			}
			if job.Status == core.JobStatusRunning || job.Status == core.JobStatusFinalizing {
				snapshot.JobsActive++
				if job.Operation != "" {
					snapshot.JobsActiveByOperation[job.Operation]++
				}
				if job.AgentID != "" {
					snapshot.JobsActiveByAgent[job.AgentID]++
				}
			}
		}
	}
	if stores.backups != nil {
		backups, err := stores.backups.List()
		if err != nil {
			http.Error(w, "list backups", http.StatusInternalServerError)
			return
		}
		snapshot.BackupsTotal = len(backups)
		for _, backup := range backups {
			snapshot.BackupsByType[backup.Type]++
			snapshot.BackupsByTarget[backup.TargetID]++
			snapshot.BackupsByStorage[backup.StorageID]++
			snapshot.BackupsBytesTotal += backup.SizeBytes
			snapshot.BackupsBytesByTarget[backup.TargetID] += backup.SizeBytes
			snapshot.BackupsBytesByStorage[backup.StorageID] += backup.SizeBytes
			snapshot.BackupsChunksTotal += backup.ChunkCount
			completedAt := backup.EndedAt.Unix()
			if completedAt > snapshot.BackupsLatestCompleted {
				snapshot.BackupsLatestCompleted = completedAt
			}
			if completedAt > snapshot.BackupsLatestByTarget[backup.TargetID] {
				snapshot.BackupsLatestByTarget[backup.TargetID] = completedAt
			}
			if completedAt > snapshot.BackupsLatestByStorage[backup.StorageID] {
				snapshot.BackupsLatestByStorage[backup.StorageID] = completedAt
			}
			if backup.Protected {
				snapshot.BackupsProtected++
			}
		}
	}
	if stores.policies != nil {
		policies, err := stores.policies.List()
		if err != nil {
			http.Error(w, "list retention policies", http.StatusInternalServerError)
			return
		}
		snapshot.RetentionPoliciesTotal = len(policies)
	}
	if stores.notifications != nil {
		rules, err := stores.notifications.List()
		if err != nil {
			http.Error(w, "list notification rules", http.StatusInternalServerError)
			return
		}
		snapshot.NotificationRulesTotal = len(rules)
		for _, rule := range rules {
			if rule.Enabled {
				snapshot.NotificationRulesEnabled++
			} else {
				snapshot.NotificationRulesDisabled++
			}
		}
	}
	if stores.users != nil {
		users, err := stores.users.List()
		if err != nil {
			http.Error(w, "list users", http.StatusInternalServerError)
			return
		}
		snapshot.UsersTotal = len(users)
	}
	if stores.tokens != nil {
		tokens, err := stores.tokens.List()
		if err != nil {
			http.Error(w, "list tokens", http.StatusInternalServerError)
			return
		}
		snapshot.TokensTotal = len(tokens)
		now := time.Now().UTC()
		for _, token := range tokens {
			if !token.RevokedAt.IsZero() {
				snapshot.TokensRevoked++
			}
			if !token.ExpiresAt.IsZero() && !token.ExpiresAt.After(now) {
				snapshot.TokensExpired++
			}
		}
	}
	if stores.audit != nil {
		events, err := stores.audit.List(r.Context(), 0)
		if err != nil {
			http.Error(w, "list audit events", http.StatusInternalServerError)
			return
		}
		snapshot.AuditEventsTotal = len(events)
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	_ = obs.WritePrometheus(w, snapshot)
}

type overviewResponse struct {
	GeneratedAt   time.Time         `json:"generated_at"`
	Agents        overviewAgents    `json:"agents"`
	Inventory     overviewInventory `json:"inventory"`
	Jobs          overviewJobs      `json:"jobs"`
	Backups       overviewBackups   `json:"backups"`
	Health        readinessResponse `json:"health"`
	Attention     overviewAttention `json:"attention"`
	LatestJobs    []core.Job        `json:"latest_jobs,omitempty"`
	LatestBackups []core.Backup     `json:"latest_backups,omitempty"`
}

type overviewAgents struct {
	Healthy  int `json:"healthy"`
	Degraded int `json:"degraded"`
	Capacity int `json:"capacity"`
}

type overviewInventory struct {
	Targets                  int `json:"targets"`
	Storages                 int `json:"storages"`
	Schedules                int `json:"schedules"`
	SchedulesPaused          int `json:"schedules_paused"`
	RetentionPolicies        int `json:"retention_policies"`
	NotificationRules        int `json:"notification_rules"`
	NotificationRulesEnabled int `json:"notification_rules_enabled"`
	Users                    int `json:"users"`
}

type overviewJobs struct {
	Active   int                    `json:"active"`
	ByStatus map[core.JobStatus]int `json:"by_status"`
}

type overviewBackups struct {
	Total                    int                     `json:"total"`
	Protected                int                     `json:"protected"`
	BytesTotal               int64                   `json:"bytes_total"`
	LatestCompletedTimestamp int64                   `json:"latest_completed_timestamp,omitempty"`
	ByType                   map[core.BackupType]int `json:"by_type"`
}

type overviewAttention struct {
	DegradedAgents            int `json:"degraded_agents"`
	FailedJobs                int `json:"failed_jobs"`
	ReadinessErrors           int `json:"readiness_errors"`
	UnprotectedBackups        int `json:"unprotected_backups"`
	DisabledNotificationRules int `json:"disabled_notification_rules"`
}

func handleOverview(w http.ResponseWriter, r *http.Request, registry *control.AgentRegistry, stores apiStores) {
	response := overviewResponse{
		GeneratedAt: time.Now().UTC(),
		Jobs:        overviewJobs{ByStatus: make(map[core.JobStatus]int)},
		Backups:     overviewBackups{ByType: make(map[core.BackupType]int)},
	}
	readiness, _ := readinessSnapshot(r.Context(), stores)
	response.Health = readiness
	for _, status := range readiness.Checks {
		if status == "error" {
			response.Attention.ReadinessErrors++
		}
	}
	if registry != nil {
		for _, agent := range registry.List() {
			switch agent.Status {
			case control.AgentHealthy:
				response.Agents.Healthy++
				capacity := agent.Capacity
				if capacity <= 0 {
					capacity = 1
				}
				response.Agents.Capacity += capacity
			case control.AgentDegraded:
				response.Agents.Degraded++
				response.Attention.DegradedAgents++
			}
		}
	}
	if stores.targets != nil {
		targets, err := stores.targets.List()
		if err != nil {
			http.Error(w, "list targets", http.StatusInternalServerError)
			return
		}
		response.Inventory.Targets = len(targets)
	}
	if stores.storages != nil {
		storages, err := stores.storages.List()
		if err != nil {
			http.Error(w, "list storages", http.StatusInternalServerError)
			return
		}
		response.Inventory.Storages = len(storages)
	}
	if stores.schedules != nil {
		schedules, err := stores.schedules.List()
		if err != nil {
			http.Error(w, "list schedules", http.StatusInternalServerError)
			return
		}
		response.Inventory.Schedules = len(schedules)
		for _, schedule := range schedules {
			if schedule.Paused {
				response.Inventory.SchedulesPaused++
			}
		}
	}
	if stores.jobs != nil {
		jobs, err := stores.jobs.List()
		if err != nil {
			http.Error(w, "list jobs", http.StatusInternalServerError)
			return
		}
		sort.Slice(jobs, func(i, j int) bool {
			return jobs[i].QueuedAt.After(jobs[j].QueuedAt)
		})
		for _, job := range jobs {
			response.Jobs.ByStatus[job.Status]++
			if job.Status == core.JobStatusFailed {
				response.Attention.FailedJobs++
			}
			if job.Status == core.JobStatusRunning || job.Status == core.JobStatusFinalizing {
				response.Jobs.Active++
			}
		}
		response.LatestJobs = limitJobs(jobs, 5)
	}
	if stores.backups != nil {
		backups, err := stores.backups.List()
		if err != nil {
			http.Error(w, "list backups", http.StatusInternalServerError)
			return
		}
		sort.Slice(backups, func(i, j int) bool {
			return backups[i].EndedAt.After(backups[j].EndedAt)
		})
		response.Backups.Total = len(backups)
		for _, backup := range backups {
			response.Backups.ByType[backup.Type]++
			response.Backups.BytesTotal += backup.SizeBytes
			if backup.Protected {
				response.Backups.Protected++
			} else {
				response.Attention.UnprotectedBackups++
			}
			if completedAt := backup.EndedAt.Unix(); completedAt > response.Backups.LatestCompletedTimestamp {
				response.Backups.LatestCompletedTimestamp = completedAt
			}
		}
		response.LatestBackups = limitBackups(backups, 5)
	}
	if stores.policies != nil {
		policies, err := stores.policies.List()
		if err != nil {
			http.Error(w, "list retention policies", http.StatusInternalServerError)
			return
		}
		response.Inventory.RetentionPolicies = len(policies)
	}
	if stores.notifications != nil {
		rules, err := stores.notifications.List()
		if err != nil {
			http.Error(w, "list notification rules", http.StatusInternalServerError)
			return
		}
		response.Inventory.NotificationRules = len(rules)
		for _, rule := range rules {
			if rule.Enabled {
				response.Inventory.NotificationRulesEnabled++
			} else {
				response.Attention.DisabledNotificationRules++
			}
		}
	}
	if stores.users != nil {
		users, err := stores.users.List()
		if err != nil {
			http.Error(w, "list users", http.StatusInternalServerError)
			return
		}
		response.Inventory.Users = len(users)
	}
	writeJSON(w, response)
}

func limitJobs(jobs []core.Job, limit int) []core.Job {
	if len(jobs) <= limit {
		return jobs
	}
	return jobs[:limit]
}

func limitBackups(backups []core.Backup, limit int) []core.Backup {
	if len(backups) <= limit {
		return backups
	}
	return backups[:limit]
}

type readinessResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
	Error  string            `json:"error,omitempty"`
}

func handleReadiness(w http.ResponseWriter, r *http.Request, stores apiStores) {
	response, status := readinessSnapshot(r.Context(), stores)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func readinessSnapshot(ctx context.Context, stores apiStores) (readinessResponse, int) {
	checks := make(map[string]string)
	status := http.StatusOK
	var firstErr error

	check := func(name string, fn func() error) {
		if fn == nil {
			return
		}
		if err := fn(); err != nil {
			checks[name] = "error"
			status = http.StatusServiceUnavailable
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", name, err)
			}
			return
		}
		checks[name] = "ok"
	}

	check("jobs", func() error {
		if stores.jobs == nil {
			return nil
		}
		_, err := stores.jobs.List()
		return err
	})
	check("audit", func() error {
		if stores.audit == nil {
			return nil
		}
		_, err := stores.audit.List(ctx, 1)
		return err
	})
	check("tokens", func() error {
		if stores.tokens == nil {
			return nil
		}
		_, err := stores.tokens.List()
		return err
	})
	check("users", func() error {
		if stores.users == nil {
			return nil
		}
		_, err := stores.users.List()
		return err
	})
	check("targets", func() error {
		if stores.targets == nil {
			return nil
		}
		_, err := stores.targets.List()
		return err
	})
	check("storages", func() error {
		if stores.storages == nil {
			return nil
		}
		_, err := stores.storages.List()
		return err
	})
	check("schedules", func() error {
		if stores.schedules == nil {
			return nil
		}
		_, err := stores.schedules.List()
		return err
	})
	check("schedule_states", func() error {
		if stores.scheduleStates == nil {
			return nil
		}
		_, err := stores.scheduleStates.List()
		return err
	})
	check("backups", func() error {
		if stores.backups == nil {
			return nil
		}
		_, err := stores.backups.List()
		return err
	})
	check("retention_policies", func() error {
		if stores.policies == nil {
			return nil
		}
		_, err := stores.policies.List()
		return err
	})
	check("notifications", func() error {
		if stores.notifications == nil {
			return nil
		}
		_, err := stores.notifications.List()
		return err
	})

	response := readinessResponse{Status: "ok", Checks: checks}
	if firstErr != nil {
		response.Status = "error"
		response.Error = firstErr.Error()
	}
	return response, status
}

func handleSchedulerTick(w http.ResponseWriter, r *http.Request, stores apiStores, registry *control.AgentRegistry) {
	if stores.schedules == nil || stores.scheduleStates == nil || stores.jobs == nil {
		http.Error(w, "scheduler stores are not configured", http.StatusServiceUnavailable)
		return
	}
	if _, failedJobIDs, err := failLostAgentJobs(stores.jobs, registry, time.Now().UTC()); err != nil {
		http.Error(w, "fail lost agent jobs", http.StatusInternalServerError)
		return
	} else if len(failedJobIDs) > 0 {
		if handleAuditAppendError(w, appendAuditEvent(r, stores.audit, "agent_lost.jobs_failed", "job", "", map[string]any{
			"job_count": len(failedJobIDs),
			"job_ids":   failedJobIDs,
		})) {
			return
		}
	}
	runner, err := control.NewSchedulerRunner(stores.schedules, stores.scheduleStates, stores.jobs, core.RealClock{})
	if err != nil {
		http.Error(w, "create scheduler runner", http.StatusInternalServerError)
		return
	}
	runner.Backups = stores.backups
	jobs, err := runner.Tick()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(jobs) > 0 {
		jobIDs := make([]core.ID, 0, len(jobs))
		for _, job := range jobs {
			jobIDs = append(jobIDs, job.ID)
		}
		if handleAuditAppendError(w, appendAuditEvent(r, stores.audit, "schedule.tick", "schedule", "", map[string]any{
			"job_count": len(jobs),
			"job_ids":   jobIDs,
		})) {
			return
		}
	}
	writeJSON(w, map[string]any{"jobs": jobs})
}

func handleListAudit(w http.ResponseWriter, r *http.Request, log *kaudit.Log) {
	if log == nil {
		http.Error(w, "audit log is not configured", http.StatusServiceUnavailable)
		return
	}
	filters, err := parseAuditListFilters(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	events, err := log.List(r.Context(), 0)
	if err != nil {
		http.Error(w, "list audit events", http.StatusInternalServerError)
		return
	}
	events = filterAuditEvents(events, filters)
	writeJSON(w, map[string]any{"events": events})
}

type auditListFilters struct {
	ActorID      core.ID
	Action       string
	ResourceType string
	ResourceID   core.ID
	Since        time.Time
	Until        time.Time
	Limit        int
}

func parseAuditListFilters(r *http.Request) (auditListFilters, error) {
	query := r.URL.Query()
	filters := auditListFilters{
		ActorID:      core.ID(query.Get("actor_id")),
		Action:       query.Get("action"),
		ResourceType: query.Get("resource_type"),
		ResourceID:   core.ID(query.Get("resource_id")),
	}
	var err error
	if query.Get("since") != "" {
		filters.Since, err = parseBackupListTime(query.Get("since"), time.Now().UTC())
		if err != nil {
			return auditListFilters{}, fmt.Errorf("invalid since filter: %w", err)
		}
	}
	if query.Get("until") != "" {
		filters.Until, err = parseBackupListTime(query.Get("until"), time.Now().UTC())
		if err != nil {
			return auditListFilters{}, fmt.Errorf("invalid until filter: %w", err)
		}
	}
	if err := validateTimeRange(filters.Since, filters.Until); err != nil {
		return auditListFilters{}, err
	}
	if query.Get("limit") != "" {
		filters.Limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || filters.Limit < 0 {
			return auditListFilters{}, fmt.Errorf("invalid limit filter %q", query.Get("limit"))
		}
	}
	return filters, nil
}

func filterAuditEvents(events []core.AuditEvent, filters auditListFilters) []core.AuditEvent {
	out := events[:0]
	for _, event := range events {
		if filters.ActorID != "" && event.ActorID != filters.ActorID {
			continue
		}
		if filters.Action != "" && event.Action != filters.Action {
			continue
		}
		if filters.ResourceType != "" && event.ResourceType != filters.ResourceType {
			continue
		}
		if filters.ResourceID != "" && event.ResourceID != filters.ResourceID {
			continue
		}
		if !filters.Since.IsZero() && event.OccurredAt.Before(filters.Since) {
			continue
		}
		if !filters.Until.IsZero() && event.OccurredAt.After(filters.Until) {
			continue
		}
		out = append(out, event)
		if filters.Limit > 0 && len(out) >= filters.Limit {
			break
		}
	}
	return out
}

func handleVerifyAudit(w http.ResponseWriter, r *http.Request, log *kaudit.Log) {
	if log == nil {
		http.Error(w, "audit log is not configured", http.StatusServiceUnavailable)
		return
	}
	if err := log.Verify(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, log, "audit.verified", "audit", "", nil)) {
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func appendAuditEvent(r *http.Request, log *kaudit.Log, action string, resourceType string, resourceID core.ID, metadata map[string]any) error {
	if log == nil {
		return nil
	}
	metadata = auditMetadataWithRequest(r, metadata)
	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	_, err := log.Append(ctx, core.AuditEvent{
		ActorID:      core.ID(r.Header.Get("X-Kronos-Actor")),
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Metadata:     metadata,
	})
	return err
}

func auditMetadataWithRequest(r *http.Request, metadata map[string]any) map[string]any {
	out := make(map[string]any, len(metadata)+2)
	for key, value := range metadata {
		out[key] = value
	}
	if r != nil {
		if requestID, ok := obs.RequestIDFromContext(r.Context()); ok {
			out["request_id"] = requestID
		} else if requestID := strings.TrimSpace(r.Header.Get(obs.RequestIDHeader)); requestID != "" {
			out["request_id"] = requestID
		}
		if agentID := strings.TrimSpace(r.Header.Get("X-Kronos-Agent-ID")); agentID != "" {
			out["agent_id"] = agentID
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func handleAuditAppendError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	http.Error(w, "write audit event", http.StatusInternalServerError)
	return true
}

type createTokenRequest struct {
	Name      string    `json:"name"`
	UserID    core.ID   `json:"user_id"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

func handleListTokens(w http.ResponseWriter, store *control.TokenStore) {
	if store == nil {
		writeJSON(w, map[string]any{"tokens": []any{}})
		return
	}
	tokens, err := store.List()
	if err != nil {
		http.Error(w, "list tokens", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"tokens": tokens})
}

func handleGetToken(w http.ResponseWriter, store *control.TokenStore, id core.ID) {
	if store == nil {
		http.Error(w, "token store is not configured", http.StatusServiceUnavailable)
		return
	}
	token, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get token", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	writeJSON(w, token)
}

func handleCreateToken(w http.ResponseWriter, r *http.Request, store *control.TokenStore, users *control.UserStore, auditLog *kaudit.Log) {
	if store == nil {
		http.Error(w, "token store is not configured", http.StatusServiceUnavailable)
		return
	}
	var request createTokenRequest
	if err := decodeJSONRequest(w, r, &request); err != nil {
		http.Error(w, "invalid token request", http.StatusBadRequest)
		return
	}
	if err := validateTokenUserScopes(users, request.UserID, request.Scopes); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	created, err := store.Create(request.Name, request.UserID, request.Scopes, request.ExpiresAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "token.created", "token", created.Token.ID, map[string]any{
		"user_id": created.Token.UserID,
		"scopes":  created.Token.Scopes,
	})) {
		return
	}
	writeJSON(w, created)
}

func handleVerifyBearerToken(w http.ResponseWriter, r *http.Request, store *control.TokenStore) {
	if store == nil {
		http.Error(w, "token store is not configured", http.StatusServiceUnavailable)
		return
	}
	secret, ok := bearerToken(r)
	if !ok {
		http.Error(w, "bearer token is required", http.StatusUnauthorized)
		return
	}
	token, ok, err := store.Verify(secret)
	if err != nil {
		http.Error(w, "verify token", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "invalid bearer token", http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"token": token})
}

func handleRevokeToken(w http.ResponseWriter, r *http.Request, store *control.TokenStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "token store is not configured", http.StatusServiceUnavailable)
		return
	}
	token, err := store.Revoke(id)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			http.NotFound(w, nil)
			return
		}
		http.Error(w, "revoke token", http.StatusInternalServerError)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "token.revoked", "token", token.ID, map[string]any{"user_id": token.UserID})) {
		return
	}
	writeJSON(w, token)
}

type pruneTokensRequest struct {
	DryRun bool `json:"dry_run"`
}

func handlePruneTokens(w http.ResponseWriter, r *http.Request, store *control.TokenStore, auditLog *kaudit.Log) {
	if store == nil {
		http.Error(w, "token store is not configured", http.StatusServiceUnavailable)
		return
	}
	var request pruneTokensRequest
	if err := decodeOptionalJSONRequest(w, r, &request); err != nil {
		http.Error(w, "invalid token prune request", http.StatusBadRequest)
		return
	}
	if request.DryRun {
		inactive, err := store.Inactive()
		if err != nil {
			http.Error(w, "list inactive tokens", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"deleted": len(inactive), "dry_run": true, "tokens": inactive})
		return
	}
	deleted, err := store.PruneInactive()
	if err != nil {
		http.Error(w, "prune tokens", http.StatusInternalServerError)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "token.pruned", "token", "", map[string]any{"deleted": len(deleted)})) {
		return
	}
	writeJSON(w, map[string]any{"deleted": len(deleted), "dry_run": false, "tokens": deleted})
}

func validateTokenUserScopes(users *control.UserStore, userID core.ID, scopes []string) error {
	if users == nil {
		return nil
	}
	user, ok, err := users.Get(userID)
	if err != nil {
		return err
	}
	if !ok {
		return core.WrapKind(core.ErrorKindNotFound, "create token", fmt.Errorf("user %q not found", userID))
	}
	for _, scope := range scopes {
		if !roleAllowsScope(user.Role, scope) {
			return fmt.Errorf("scope %q exceeds user role %q", scope, user.Role)
		}
	}
	return nil
}

func roleAllowsScope(role core.RoleName, scope string) bool {
	return tokenAllowsScope(core.Token{Scopes: roleScopes(role)}, scope)
}

func roleScopes(role core.RoleName) []string {
	switch role {
	case core.RoleAdmin:
		return []string{"admin:*"}
	case core.RoleOperator:
		return []string{
			"agent:read",
			"audit:read",
			"backup:*",
			"job:*",
			"metrics:read",
			"notification:*",
			"restore:*",
			"retention:*",
			"schedule:read",
			"storage:read",
			"target:read",
		}
	case core.RoleViewer:
		return []string{
			"agent:read",
			"audit:read",
			"backup:read",
			"job:read",
			"metrics:read",
			"notification:read",
			"restore:read",
			"retention:read",
			"schedule:read",
			"storage:read",
			"target:read",
		}
	default:
		return nil
	}
}

func requireScope(w http.ResponseWriter, r *http.Request, store *control.TokenStore, scope string) bool {
	if strings.TrimSpace(r.Header.Get("Authorization")) == "" {
		return true
	}
	secret, ok := bearerToken(r)
	if !ok {
		http.Error(w, "invalid bearer token", http.StatusUnauthorized)
		return false
	}
	if store == nil {
		http.Error(w, "token store is not configured", http.StatusServiceUnavailable)
		return false
	}
	token, ok, err := store.Verify(secret)
	if err != nil {
		http.Error(w, "verify token", http.StatusInternalServerError)
		return false
	}
	if !ok {
		http.Error(w, "invalid bearer token", http.StatusUnauthorized)
		return false
	}
	if !tokenAllowsScope(token, scope) {
		http.Error(w, "insufficient scope", http.StatusForbidden)
		return false
	}
	if r.Header.Get("X-Kronos-Actor") == "" && !token.UserID.IsZero() {
		r.Header.Set("X-Kronos-Actor", token.UserID.String())
	}
	return true
}

func tokenAllowsScope(token core.Token, required string) bool {
	if required == "" {
		return true
	}
	for _, scope := range token.Scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if scope == "*" || scope == "admin:*" || scope == required {
			return true
		}
		if strings.HasSuffix(scope, ":*") {
			prefix := strings.TrimSuffix(scope, "*")
			if strings.HasPrefix(required, prefix) {
				return true
			}
		}
	}
	return false
}

func bearerToken(r *http.Request) (string, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return "", false
	}
	scheme, value, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(value) == "" {
		return "", false
	}
	return strings.TrimSpace(value), true
}

type grantUserRequest struct {
	Role core.RoleName `json:"role"`
}

func handleListUsers(w http.ResponseWriter, store *control.UserStore) {
	if store == nil {
		writeJSON(w, map[string]any{"users": []any{}})
		return
	}
	users, err := store.List()
	if err != nil {
		http.Error(w, "list users", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"users": users})
}

func handleCreateUser(w http.ResponseWriter, r *http.Request, store *control.UserStore, auditLog *kaudit.Log) {
	if store == nil {
		http.Error(w, "user store is not configured", http.StatusServiceUnavailable)
		return
	}
	var user core.User
	if err := decodeJSONRequest(w, r, &user); err != nil {
		http.Error(w, "invalid user", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	if user.ID.IsZero() {
		id, err := core.NewID(core.RealClock{})
		if err != nil {
			http.Error(w, "create user id", http.StatusInternalServerError)
			return
		}
		user.ID = id
	}
	user.CreatedAt, user.UpdatedAt = stampTimes(user.CreatedAt, now)
	if err := store.Save(user); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "user.created", "user", user.ID, map[string]any{"role": user.Role})) {
		return
	}
	writeJSON(w, user)
}

func handleGetUser(w http.ResponseWriter, store *control.UserStore, id core.ID) {
	if store == nil {
		http.Error(w, "user store is not configured", http.StatusServiceUnavailable)
		return
	}
	user, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get user", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	writeJSON(w, user)
}

func handleGrantUser(w http.ResponseWriter, r *http.Request, store *control.UserStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "user store is not configured", http.StatusServiceUnavailable)
		return
	}
	var request grantUserRequest
	if err := decodeJSONRequest(w, r, &request); err != nil {
		http.Error(w, "invalid grant request", http.StatusBadRequest)
		return
	}
	user, err := store.Grant(id, request.Role, time.Now().UTC())
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			http.NotFound(w, nil)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "user.role_granted", "user", user.ID, map[string]any{"role": user.Role})) {
		return
	}
	writeJSON(w, user)
}

func handleDeleteUser(w http.ResponseWriter, r *http.Request, store *control.UserStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "user store is not configured", http.StatusServiceUnavailable)
		return
	}
	if _, ok, err := store.Get(id); err != nil {
		http.Error(w, "get user", http.StatusInternalServerError)
		return
	} else if !ok {
		http.NotFound(w, nil)
		return
	}
	if err := store.Delete(id); err != nil {
		http.Error(w, "delete user", http.StatusInternalServerError)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "user.deleted", "user", id, nil)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type backupNowRequest struct {
	TargetID  core.ID         `json:"target_id"`
	StorageID core.ID         `json:"storage_id"`
	Type      core.BackupType `json:"type,omitempty"`
	ParentID  core.ID         `json:"parent_id,omitempty"`
}

type backupVerifyRequest struct {
	Level core.JobVerificationLevel `json:"level,omitempty"`
}

type claimJobResponse struct {
	Job *core.Job `json:"job,omitempty"`
}

type finishJobRequest struct {
	Status       core.JobStatus           `json:"status"`
	Error        string                   `json:"error,omitempty"`
	Backup       *core.Backup             `json:"backup,omitempty"`
	Verification *core.VerificationReport `json:"verification,omitempty"`
}

func handleBackupNow(w http.ResponseWriter, r *http.Request, store *control.JobStore, auditLog *kaudit.Log) {
	if store == nil {
		http.Error(w, "job store is not configured", http.StatusServiceUnavailable)
		return
	}
	var request backupNowRequest
	if err := decodeJSONRequest(w, r, &request); err != nil {
		http.Error(w, "invalid backup request", http.StatusBadRequest)
		return
	}
	if request.TargetID.IsZero() {
		http.Error(w, "target_id is required", http.StatusBadRequest)
		return
	}
	if request.StorageID.IsZero() {
		http.Error(w, "storage_id is required", http.StatusBadRequest)
		return
	}
	if request.Type == "" {
		if request.ParentID.IsZero() {
			request.Type = core.BackupTypeFull
		} else {
			request.Type = core.BackupTypeIncremental
		}
	}
	if request.Type != core.BackupTypeFull && request.Type != core.BackupTypeIncremental && request.Type != core.BackupTypeDifferential {
		http.Error(w, "unsupported backup type", http.StatusBadRequest)
		return
	}
	if request.Type == core.BackupTypeFull && !request.ParentID.IsZero() {
		http.Error(w, "parent_id requires incremental or differential backup type", http.StatusBadRequest)
		return
	}
	if backupTypeNeedsParent(request.Type) && request.ParentID.IsZero() {
		http.Error(w, "parent_id is required for incremental or differential backup", http.StatusBadRequest)
		return
	}
	orchestrator, err := control.NewOrchestrator(store, core.RealClock{})
	if err != nil {
		http.Error(w, "create orchestrator", http.StatusInternalServerError)
		return
	}
	jobs, err := orchestrator.EnqueueDue([]sched.DueJob{{
		TargetID:  request.TargetID,
		StorageID: request.StorageID,
		Type:      request.Type,
		QueuedAt:  time.Now().UTC(),
	}})
	if err != nil {
		http.Error(w, "enqueue backup", http.StatusInternalServerError)
		return
	}
	job := jobs[0]
	if !request.ParentID.IsZero() {
		job.ParentBackupID = request.ParentID
		if err := store.Save(job); err != nil {
			http.Error(w, "save backup job", http.StatusInternalServerError)
			return
		}
	}
	auditMetadata := map[string]any{
		"target_id":  request.TargetID,
		"storage_id": request.StorageID,
		"type":       request.Type,
	}
	if !request.ParentID.IsZero() {
		auditMetadata["parent_id"] = request.ParentID
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "backup.requested", "job", job.ID, auditMetadata)) {
		return
	}
	writeJSON(w, job)
}

func backupTypeNeedsParent(backupType core.BackupType) bool {
	return backupType == core.BackupTypeIncremental || backupType == core.BackupTypeDifferential
}

func handleClaimJob(w http.ResponseWriter, r *http.Request, store *control.JobStore, targets *control.TargetStore, auditLog *kaudit.Log, registry *control.AgentRegistry) {
	if store == nil {
		http.Error(w, "job store is not configured", http.StatusServiceUnavailable)
		return
	}
	if _, failedJobIDs, err := failLostAgentJobs(store, registry, time.Now().UTC()); err != nil {
		http.Error(w, "fail lost agent jobs", http.StatusInternalServerError)
		return
	} else if len(failedJobIDs) > 0 {
		if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "agent_lost.jobs_failed", "job", "", map[string]any{
			"job_count": len(failedJobIDs),
			"job_ids":   failedJobIDs,
		})) {
			return
		}
	}
	jobs, err := store.List()
	if err != nil {
		http.Error(w, "list jobs", http.StatusInternalServerError)
		return
	}
	orchestrator, err := control.NewOrchestrator(store, core.RealClock{})
	if err != nil {
		http.Error(w, "create orchestrator", http.StatusInternalServerError)
		return
	}
	if agentCapacityReached(registry, jobs) {
		writeJSON(w, claimJobResponse{})
		return
	}
	agentID := strings.TrimSpace(r.Header.Get("X-Kronos-Agent-ID"))
	for _, job := range jobs {
		if job.Status != core.JobStatusQueued {
			continue
		}
		if targetHasActiveJob(jobs, job) {
			continue
		}
		if !jobMatchesAgent(job, agentID, targets) {
			continue
		}
		started, err := orchestrator.StartOnAgent(job.ID, agentID)
		if err != nil {
			http.Error(w, "claim job", http.StatusInternalServerError)
			return
		}
		if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "job.claimed", "job", started.ID, map[string]any{
			"target_id":  started.TargetID,
			"storage_id": started.StorageID,
			"type":       started.Type,
			"operation":  started.Operation,
		})) {
			return
		}
		writeJSON(w, claimJobResponse{Job: &started})
		return
	}
	writeJSON(w, claimJobResponse{})
}

func jobMatchesAgent(job core.Job, agentID string, targets *control.TargetStore) bool {
	if agentID == "" || targets == nil || job.TargetID.IsZero() {
		return true
	}
	target, ok, err := targets.Get(job.TargetID)
	if err != nil || !ok {
		return true
	}
	assigned := strings.TrimSpace(target.Labels["agent"])
	return assigned == "" || assigned == agentID
}

func agentCapacityReached(registry *control.AgentRegistry, jobs []core.Job) bool {
	capacity, ok := healthyAgentCapacity(registry)
	if !ok {
		return false
	}
	active := 0
	for _, job := range jobs {
		if job.Status == core.JobStatusRunning || job.Status == core.JobStatusFinalizing {
			active++
		}
	}
	return active >= capacity
}

func healthyAgentCapacity(registry *control.AgentRegistry) (int, bool) {
	if registry == nil {
		return 0, false
	}
	total := 0
	for _, agent := range registry.List() {
		if agent.Status != control.AgentHealthy {
			continue
		}
		capacity := agent.Capacity
		if capacity <= 0 {
			capacity = 1
		}
		total += capacity
	}
	if total <= 0 {
		return 0, false
	}
	return total, true
}

func failLostAgentJobs(store *control.JobStore, registry *control.AgentRegistry, now time.Time) (int, []core.ID, error) {
	if store == nil || registry == nil {
		return 0, nil, nil
	}
	lostAgents := make(map[string]bool)
	for _, agent := range registry.List() {
		if agent.Status == control.AgentDegraded {
			lostAgents[agent.ID] = true
		}
	}
	if len(lostAgents) == 0 {
		return 0, nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	jobs, err := store.List()
	if err != nil {
		return 0, nil, err
	}
	failed := 0
	failedJobIDs := make([]core.ID, 0)
	for _, job := range jobs {
		if job.AgentID == "" || !lostAgents[job.AgentID] {
			continue
		}
		if job.Status != core.JobStatusRunning && job.Status != core.JobStatusFinalizing {
			continue
		}
		job.Status = core.JobStatusFailed
		job.EndedAt = now.UTC()
		job.Error = "agent_lost"
		if err := store.Save(job); err != nil {
			return failed, failedJobIDs, err
		}
		failed++
		failedJobIDs = append(failedJobIDs, job.ID)
	}
	return failed, failedJobIDs, nil
}

func targetHasActiveJob(jobs []core.Job, candidate core.Job) bool {
	if candidate.TargetID.IsZero() {
		return false
	}
	for _, job := range jobs {
		if job.ID == candidate.ID || job.TargetID != candidate.TargetID {
			continue
		}
		if job.Status == core.JobStatusRunning || job.Status == core.JobStatusFinalizing {
			return true
		}
	}
	return false
}

type jobListFilters struct {
	Status         core.JobStatus
	Operation      core.JobOperation
	TargetID       core.ID
	StorageID      core.ID
	AgentID        string
	VerifyBackupID core.ID
	Since          time.Time
	Until          time.Time
}

func parseJobListFilters(r *http.Request) (jobListFilters, error) {
	query := r.URL.Query()
	filters := jobListFilters{
		Status:         core.JobStatus(query.Get("status")),
		Operation:      core.JobOperation(query.Get("operation")),
		TargetID:       core.ID(query.Get("target_id")),
		StorageID:      core.ID(query.Get("storage_id")),
		AgentID:        query.Get("agent_id"),
		VerifyBackupID: core.ID(query.Get("verify_backup_id")),
	}
	var err error
	if query.Get("since") != "" {
		filters.Since, err = parseBackupListTime(query.Get("since"), time.Now().UTC())
		if err != nil {
			return jobListFilters{}, fmt.Errorf("invalid since filter: %w", err)
		}
	}
	if query.Get("until") != "" {
		filters.Until, err = parseBackupListTime(query.Get("until"), time.Now().UTC())
		if err != nil {
			return jobListFilters{}, fmt.Errorf("invalid until filter: %w", err)
		}
	}
	if err := validateTimeRange(filters.Since, filters.Until); err != nil {
		return jobListFilters{}, err
	}
	return filters, nil
}

func filterJobs(jobs []core.Job, filters jobListFilters) []core.Job {
	out := jobs[:0]
	for _, job := range jobs {
		if filters.Status != "" && job.Status != filters.Status {
			continue
		}
		if filters.Operation != "" && job.Operation != filters.Operation {
			continue
		}
		if filters.TargetID != "" && job.TargetID != filters.TargetID {
			continue
		}
		if filters.StorageID != "" && job.StorageID != filters.StorageID {
			continue
		}
		if filters.AgentID != "" && job.AgentID != filters.AgentID {
			continue
		}
		if filters.VerifyBackupID != "" && job.VerifyBackupID != filters.VerifyBackupID {
			continue
		}
		if !filters.Since.IsZero() && job.QueuedAt.Before(filters.Since) {
			continue
		}
		if !filters.Until.IsZero() && job.QueuedAt.After(filters.Until) {
			continue
		}
		out = append(out, job)
	}
	return out
}

func handleGetJob(w http.ResponseWriter, store *control.JobStore, id core.ID) {
	if store == nil {
		http.Error(w, "job store is not configured", http.StatusServiceUnavailable)
		return
	}
	job, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get job", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	writeJSON(w, job)
}

func handleFinishJob(w http.ResponseWriter, r *http.Request, jobs *control.JobStore, backups *control.BackupStore, auditLog *kaudit.Log, notifications *control.NotificationRuleStore, id core.ID) {
	if jobs == nil {
		http.Error(w, "job store is not configured", http.StatusServiceUnavailable)
		return
	}
	var request finishJobRequest
	if err := decodeJSONRequest(w, r, &request); err != nil {
		http.Error(w, "invalid finish request", http.StatusBadRequest)
		return
	}
	orchestrator, err := control.NewOrchestrator(jobs, core.RealClock{})
	if err != nil {
		http.Error(w, "create orchestrator", http.StatusInternalServerError)
		return
	}
	current, ok, err := jobs.Get(id)
	if err != nil {
		http.Error(w, "get job", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	if request.Backup != nil && request.Status != core.JobStatusSucceeded {
		http.Error(w, "backup metadata requires succeeded status", http.StatusBadRequest)
		return
	}
	if request.Backup != nil && current.Operation != "" && current.Operation != core.JobOperationBackup {
		http.Error(w, "non-backup jobs cannot attach backup metadata", http.StatusBadRequest)
		return
	}
	if request.Backup != nil && backups == nil {
		http.Error(w, "backup store is not configured", http.StatusServiceUnavailable)
		return
	}
	if request.Verification != nil && request.Status != core.JobStatusSucceeded {
		http.Error(w, "verification report requires succeeded status", http.StatusBadRequest)
		return
	}
	if request.Verification != nil && current.Operation != core.JobOperationVerify {
		http.Error(w, "verification reports require verify jobs", http.StatusBadRequest)
		return
	}
	job, err := orchestrator.Finish(id, request.Status, request.Error)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			http.NotFound(w, nil)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var backupID core.ID
	if request.Backup != nil {
		backup := *request.Backup
		if backup.JobID.IsZero() {
			backup.JobID = job.ID
		}
		if backup.TargetID.IsZero() {
			backup.TargetID = job.TargetID
		}
		if backup.StorageID.IsZero() {
			backup.StorageID = job.StorageID
		}
		if backup.Type == "" {
			backup.Type = job.Type
		}
		if backup.StartedAt.IsZero() {
			backup.StartedAt = job.StartedAt
		}
		if backup.EndedAt.IsZero() {
			backup.EndedAt = job.EndedAt
		}
		if err := backups.Save(backup); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		backupID = backup.ID
	}
	if request.Verification != nil {
		report := *request.Verification
		if report.BackupID.IsZero() {
			report.BackupID = job.VerifyBackupID
		}
		if report.Level == "" {
			report.Level = job.VerifyLevel
		}
		if len(report.ManifestIDs) == 0 {
			report.ManifestIDs = append([]core.ID(nil), job.VerifyManifestIDs...)
		}
		job.VerifyReport = &report
		if err := jobs.Save(job); err != nil {
			http.Error(w, "save verification report", http.StatusInternalServerError)
			return
		}
	}
	metadata := map[string]any{"status": request.Status}
	if request.Error != "" {
		metadata["error"] = request.Error
	}
	if !backupID.IsZero() {
		metadata["backup_id"] = backupID
	}
	if request.Verification != nil {
		metadata["verification"] = request.Verification
	}
	notificationCtx, cancelNotifications := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancelNotifications()
	deliveries := control.NotificationDispatcher{Store: notifications}.DispatchJobTerminal(notificationCtx, job)
	if len(deliveries) > 0 {
		metadata["notifications"] = deliveries
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "job.finished", "job", job.ID, metadata)) {
		return
	}
	writeJSON(w, job)
}

func handleCancelJob(w http.ResponseWriter, r *http.Request, store *control.JobStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "job store is not configured", http.StatusServiceUnavailable)
		return
	}
	orchestrator, err := control.NewOrchestrator(store, core.RealClock{})
	if err != nil {
		http.Error(w, "create orchestrator", http.StatusInternalServerError)
		return
	}
	job, err := orchestrator.Finish(id, core.JobStatusCanceled, "canceled")
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			http.NotFound(w, nil)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "job.canceled", "job", job.ID, nil)) {
		return
	}
	writeJSON(w, job)
}

func handleRetryJob(w http.ResponseWriter, r *http.Request, store *control.JobStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "job store is not configured", http.StatusServiceUnavailable)
		return
	}
	orchestrator, err := control.NewOrchestrator(store, core.RealClock{})
	if err != nil {
		http.Error(w, "create orchestrator", http.StatusInternalServerError)
		return
	}
	job, err := orchestrator.Retry(id)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			http.NotFound(w, nil)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "job.retried", "job", job.ID, nil)) {
		return
	}
	writeJSON(w, job)
}

type backupListFilters struct {
	TargetID     core.ID
	StorageID    core.ID
	Type         core.BackupType
	Since        time.Time
	Until        time.Time
	Protected    bool
	HasProtected bool
}

func handleListBackups(w http.ResponseWriter, r *http.Request, store *control.BackupStore) {
	if store == nil {
		writeJSON(w, map[string]any{"backups": []any{}})
		return
	}
	filters, err := parseBackupListFilters(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	backups, err := store.List()
	if err != nil {
		http.Error(w, "list backups", http.StatusInternalServerError)
		return
	}
	backups = filterBackups(backups, filters)
	writeJSON(w, map[string]any{"backups": backups})
}

func parseBackupListFilters(r *http.Request) (backupListFilters, error) {
	query := r.URL.Query()
	filters := backupListFilters{
		TargetID:  core.ID(query.Get("target_id")),
		StorageID: core.ID(query.Get("storage_id")),
		Type:      core.BackupType(query.Get("type")),
	}
	var err error
	if query.Get("since") != "" {
		filters.Since, err = parseBackupListTime(query.Get("since"), time.Now().UTC())
		if err != nil {
			return backupListFilters{}, fmt.Errorf("invalid since filter: %w", err)
		}
	}
	if query.Get("until") != "" {
		filters.Until, err = parseBackupListTime(query.Get("until"), time.Now().UTC())
		if err != nil {
			return backupListFilters{}, fmt.Errorf("invalid until filter: %w", err)
		}
	}
	if err := validateTimeRange(filters.Since, filters.Until); err != nil {
		return backupListFilters{}, err
	}
	if query.Get("protected") != "" {
		filters.HasProtected = true
		filters.Protected, err = strconv.ParseBool(query.Get("protected"))
		if err != nil {
			return backupListFilters{}, fmt.Errorf("invalid protected filter %q", query.Get("protected"))
		}
	}
	return filters, nil
}

func validateTimeRange(since time.Time, until time.Time) error {
	if !since.IsZero() && !until.IsZero() && since.After(until) {
		return fmt.Errorf("since filter must be before or equal to until filter")
	}
	return nil
}

func parseBackupListTime(value string, now time.Time) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), nil
	}
	duration, err := parseRelativeDuration(value)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(-duration).UTC(), nil
}

func parseRelativeDuration(value string) (time.Duration, error) {
	if strings.HasSuffix(value, "d") || strings.HasSuffix(value, "w") {
		multiplier := 24 * time.Hour
		number := strings.TrimSuffix(value, "d")
		if strings.HasSuffix(value, "w") {
			multiplier = 7 * 24 * time.Hour
			number = strings.TrimSuffix(value, "w")
		}
		parsed, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return 0, err
		}
		if parsed < 0 {
			return 0, fmt.Errorf("duration must be non-negative")
		}
		return time.Duration(parsed * float64(multiplier)), nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if duration < 0 {
		return 0, fmt.Errorf("duration must be non-negative")
	}
	return duration, nil
}

func filterBackups(backups []core.Backup, filters backupListFilters) []core.Backup {
	out := backups[:0]
	for _, backup := range backups {
		if filters.TargetID != "" && backup.TargetID != filters.TargetID {
			continue
		}
		if filters.StorageID != "" && backup.StorageID != filters.StorageID {
			continue
		}
		if filters.Type != "" && backup.Type != filters.Type {
			continue
		}
		if filters.HasProtected && backup.Protected != filters.Protected {
			continue
		}
		filterTime := backup.EndedAt
		if filterTime.IsZero() {
			filterTime = backup.StartedAt
		}
		if !filters.Since.IsZero() && filterTime.Before(filters.Since) {
			continue
		}
		if !filters.Until.IsZero() && filterTime.After(filters.Until) {
			continue
		}
		out = append(out, backup)
	}
	return out
}

func handleGetBackup(w http.ResponseWriter, store *control.BackupStore, id core.ID) {
	if store == nil {
		http.Error(w, "backup store is not configured", http.StatusServiceUnavailable)
		return
	}
	backup, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get backup", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	writeJSON(w, backup)
}

func handleProtectBackup(w http.ResponseWriter, r *http.Request, store *control.BackupStore, auditLog *kaudit.Log, id core.ID, protected bool) {
	if store == nil {
		http.Error(w, "backup store is not configured", http.StatusServiceUnavailable)
		return
	}
	backup, err := store.Protect(id, protected)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			http.NotFound(w, nil)
			return
		}
		http.Error(w, "protect backup", http.StatusInternalServerError)
		return
	}
	action := "backup.unprotected"
	if protected {
		action = "backup.protected"
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, action, "backup", backup.ID, nil)) {
		return
	}
	writeJSON(w, backup)
}

func handleBackupVerify(w http.ResponseWriter, r *http.Request, backups *control.BackupStore, jobs *control.JobStore, auditLog *kaudit.Log, id core.ID) {
	if backups == nil {
		http.Error(w, "backup store is not configured", http.StatusServiceUnavailable)
		return
	}
	if jobs == nil {
		http.Error(w, "job store is not configured", http.StatusServiceUnavailable)
		return
	}
	var request backupVerifyRequest
	if err := decodeJSONRequest(w, r, &request); err != nil {
		http.Error(w, "invalid backup verify request", http.StatusBadRequest)
		return
	}
	level := request.Level
	if level == "" {
		level = core.JobVerificationChunk
	}
	if level != core.JobVerificationManifest && level != core.JobVerificationChunk {
		http.Error(w, "unsupported verification level", http.StatusBadRequest)
		return
	}
	list, err := backups.List()
	if err != nil {
		http.Error(w, "list backups", http.StatusInternalServerError)
		return
	}
	plan, err := krestore.BuildPlan(list, krestore.Request{BackupID: id})
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			http.NotFound(w, nil)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(plan.Steps) == 0 {
		http.Error(w, "backup verification plan is empty", http.StatusBadRequest)
		return
	}
	jobID, err := core.NewID(core.RealClock{})
	if err != nil {
		http.Error(w, "create verify job id", http.StatusInternalServerError)
		return
	}
	selected := plan.Steps[len(plan.Steps)-1]
	manifestIDs := make([]core.ID, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		manifestIDs = append(manifestIDs, step.ManifestID)
	}
	job := core.Job{
		ID:                jobID,
		Operation:         core.JobOperationVerify,
		TargetID:          plan.TargetID,
		StorageID:         plan.StorageID,
		Type:              selected.Type,
		VerifyBackupID:    plan.BackupID,
		VerifyManifestID:  selected.ManifestID,
		VerifyManifestIDs: manifestIDs,
		VerifyLevel:       level,
		Status:            core.JobStatusQueued,
		QueuedAt:          time.Now().UTC(),
	}
	if err := jobs.Save(job); err != nil {
		http.Error(w, "save verify job", http.StatusInternalServerError)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "backup.verify.requested", "job", job.ID, map[string]any{
		"backup_id":    plan.BackupID,
		"storage_id":   plan.StorageID,
		"level":        level,
		"manifest_ids": manifestIDs,
	})) {
		return
	}
	writeJSON(w, job)
}

type retentionPlanRequest struct {
	Policy core.RetentionPolicy `json:"policy"`
	Now    time.Time            `json:"now,omitempty"`
}

type retentionApplyRequest struct {
	Policy core.RetentionPolicy `json:"policy"`
	Now    time.Time            `json:"now,omitempty"`
	DryRun bool                 `json:"dry_run,omitempty"`
}

type retentionApplyResponse struct {
	Plan    retention.Plan `json:"plan"`
	Deleted []core.ID      `json:"deleted"`
	DryRun  bool           `json:"dry_run"`
}

func handleRetentionPlan(w http.ResponseWriter, r *http.Request, store *control.BackupStore) {
	if store == nil {
		http.Error(w, "backup store is not configured", http.StatusServiceUnavailable)
		return
	}
	var request retentionPlanRequest
	if err := decodeJSONRequest(w, r, &request); err != nil {
		http.Error(w, "invalid retention plan request", http.StatusBadRequest)
		return
	}
	backups, err := store.List()
	if err != nil {
		http.Error(w, "list backups", http.StatusInternalServerError)
		return
	}
	plan, err := retention.Resolve(backups, request.Policy, request.Now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, plan)
}

func handleRetentionApply(w http.ResponseWriter, r *http.Request, store *control.BackupStore, auditLog *kaudit.Log) {
	if store == nil {
		http.Error(w, "backup store is not configured", http.StatusServiceUnavailable)
		return
	}
	var request retentionApplyRequest
	if err := decodeJSONRequest(w, r, &request); err != nil {
		http.Error(w, "invalid retention apply request", http.StatusBadRequest)
		return
	}
	backups, err := store.List()
	if err != nil {
		http.Error(w, "list backups", http.StatusInternalServerError)
		return
	}
	plan, err := retention.Resolve(backups, request.Policy, request.Now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	deleted := make([]core.ID, 0)
	for _, item := range plan.Items {
		if item.Keep {
			continue
		}
		deleted = append(deleted, item.Backup.ID)
		if request.DryRun {
			continue
		}
		if err := store.Delete(item.Backup.ID); err != nil {
			http.Error(w, "delete backup metadata", http.StatusInternalServerError)
			return
		}
	}
	if !request.DryRun && handleAuditAppendError(w, appendAuditEvent(r, auditLog, "retention.applied", "retention", "", map[string]any{
		"deleted":       deleted,
		"deleted_count": len(deleted),
	})) {
		return
	}
	writeJSON(w, retentionApplyResponse{Plan: plan, Deleted: deleted, DryRun: request.DryRun})
}

func handleRestorePreview(w http.ResponseWriter, r *http.Request, store *control.BackupStore) {
	if store == nil {
		http.Error(w, "backup store is not configured", http.StatusServiceUnavailable)
		return
	}
	var request krestore.Request
	if err := decodeJSONRequest(w, r, &request); err != nil {
		http.Error(w, "invalid restore preview request", http.StatusBadRequest)
		return
	}
	if err := validateRestoreAt(request.At, time.Now().UTC()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	backups, err := store.List()
	if err != nil {
		http.Error(w, "list backups", http.StatusInternalServerError)
		return
	}
	plan, err := krestore.BuildPlan(backups, request)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			http.NotFound(w, nil)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, plan)
}

type restoreStartResponse struct {
	Job  core.Job      `json:"job"`
	Plan krestore.Plan `json:"plan"`
}

func handleRestoreStart(w http.ResponseWriter, r *http.Request, backups *control.BackupStore, jobs *control.JobStore, auditLog *kaudit.Log) {
	if backups == nil {
		http.Error(w, "backup store is not configured", http.StatusServiceUnavailable)
		return
	}
	if jobs == nil {
		http.Error(w, "job store is not configured", http.StatusServiceUnavailable)
		return
	}
	var request krestore.Request
	if err := decodeJSONRequest(w, r, &request); err != nil {
		http.Error(w, "invalid restore request", http.StatusBadRequest)
		return
	}
	if err := validateRestoreAt(request.At, time.Now().UTC()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	list, err := backups.List()
	if err != nil {
		http.Error(w, "list backups", http.StatusInternalServerError)
		return
	}
	plan, err := krestore.BuildPlan(list, request)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			http.NotFound(w, nil)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := core.NewID(core.RealClock{})
	if err != nil {
		http.Error(w, "create restore job id", http.StatusInternalServerError)
		return
	}
	selected := plan.Steps[len(plan.Steps)-1]
	manifestIDs := make([]core.ID, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		manifestIDs = append(manifestIDs, step.ManifestID)
	}
	job := core.Job{
		ID:                     id,
		Operation:              core.JobOperationRestore,
		TargetID:               plan.TargetID,
		StorageID:              plan.StorageID,
		Type:                   selected.Type,
		RestoreBackupID:        plan.BackupID,
		RestoreManifestID:      selected.ManifestID,
		RestoreManifestIDs:     manifestIDs,
		RestoreTargetID:        plan.TargetID,
		RestoreAt:              plan.At,
		RestoreDryRun:          request.DryRun,
		RestoreReplaceExisting: request.ReplaceExisting,
		Status:                 core.JobStatusQueued,
		QueuedAt:               time.Now().UTC(),
	}
	if err := jobs.Save(job); err != nil {
		http.Error(w, "save restore job", http.StatusInternalServerError)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "restore.requested", "job", job.ID, map[string]any{
		"backup_id":        plan.BackupID,
		"target_id":        plan.TargetID,
		"storage_id":       plan.StorageID,
		"at":               plan.At,
		"dry_run":          request.DryRun,
		"replace_existing": request.ReplaceExisting,
	})) {
		return
	}
	writeJSON(w, restoreStartResponse{Job: job, Plan: plan})
}

func validateRestoreAt(at time.Time, now time.Time) error {
	if at.IsZero() {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if at.After(now.UTC()) {
		return fmt.Errorf("restore at must not be in the future")
	}
	return nil
}

func handleListRetentionPolicies(w http.ResponseWriter, store *control.RetentionPolicyStore) {
	if store == nil {
		writeJSON(w, map[string]any{"policies": []any{}})
		return
	}
	policies, err := store.List()
	if err != nil {
		http.Error(w, "list retention policies", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"policies": policies})
}

func handleCreateRetentionPolicy(w http.ResponseWriter, r *http.Request, store *control.RetentionPolicyStore, auditLog *kaudit.Log) {
	if store == nil {
		http.Error(w, "retention policy store is not configured", http.StatusServiceUnavailable)
		return
	}
	var policy core.RetentionPolicy
	if err := decodeJSONRequest(w, r, &policy); err != nil {
		http.Error(w, "invalid retention policy", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	if policy.ID.IsZero() {
		id, err := core.NewID(core.RealClock{})
		if err != nil {
			http.Error(w, "create retention policy id", http.StatusInternalServerError)
			return
		}
		policy.ID = id
	}
	policy.CreatedAt, policy.UpdatedAt = stampTimes(policy.CreatedAt, now)
	if err := store.Save(policy); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "retention_policy.created", "retention_policy", policy.ID, map[string]any{"rules": len(policy.Rules)})) {
		return
	}
	writeJSON(w, policy)
}

func handleGetRetentionPolicy(w http.ResponseWriter, store *control.RetentionPolicyStore, id core.ID) {
	if store == nil {
		http.Error(w, "retention policy store is not configured", http.StatusServiceUnavailable)
		return
	}
	policy, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get retention policy", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	writeJSON(w, policy)
}

func handleUpdateRetentionPolicy(w http.ResponseWriter, r *http.Request, store *control.RetentionPolicyStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "retention policy store is not configured", http.StatusServiceUnavailable)
		return
	}
	var policy core.RetentionPolicy
	if err := decodeJSONRequest(w, r, &policy); err != nil {
		http.Error(w, "invalid retention policy", http.StatusBadRequest)
		return
	}
	existing, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get retention policy", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	policy.ID = id
	policy.CreatedAt, policy.UpdatedAt = stampTimes(existing.CreatedAt, time.Now().UTC())
	if err := store.Save(policy); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "retention_policy.updated", "retention_policy", policy.ID, map[string]any{"rules": len(policy.Rules)})) {
		return
	}
	writeJSON(w, policy)
}

func handleDeleteRetentionPolicy(w http.ResponseWriter, r *http.Request, store *control.RetentionPolicyStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "retention policy store is not configured", http.StatusServiceUnavailable)
		return
	}
	if _, ok, err := store.Get(id); err != nil {
		http.Error(w, "get retention policy", http.StatusInternalServerError)
		return
	} else if !ok {
		http.NotFound(w, nil)
		return
	}
	if err := store.Delete(id); err != nil {
		http.Error(w, "delete retention policy", http.StatusInternalServerError)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "retention_policy.deleted", "retention_policy", id, nil)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleListNotifications(w http.ResponseWriter, r *http.Request, store *control.NotificationRuleStore) {
	if store == nil {
		writeJSON(w, map[string]any{"notifications": []any{}})
		return
	}
	rules, err := store.List()
	if err != nil {
		http.Error(w, "list notifications", http.StatusInternalServerError)
		return
	}
	if !wantsSecrets(r) {
		for i := range rules {
			rules[i] = redactNotification(rules[i])
		}
	}
	writeJSON(w, map[string]any{"notifications": rules})
}

func handleCreateNotification(w http.ResponseWriter, r *http.Request, store *control.NotificationRuleStore, auditLog *kaudit.Log) {
	if store == nil {
		http.Error(w, "notification store is not configured", http.StatusServiceUnavailable)
		return
	}
	var rule core.NotificationRule
	if err := decodeJSONRequest(w, r, &rule); err != nil {
		http.Error(w, "invalid notification", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	if rule.ID.IsZero() {
		id, err := core.NewID(core.RealClock{})
		if err != nil {
			http.Error(w, "create notification id", http.StatusInternalServerError)
			return
		}
		rule.ID = id
	}
	rule.CreatedAt, rule.UpdatedAt = stampTimes(rule.CreatedAt, now)
	if err := store.Save(rule); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "notification.created", "notification", rule.ID, map[string]any{"events": len(rule.Events)})) {
		return
	}
	writeJSON(w, redactNotification(rule))
}

func handleGetNotification(w http.ResponseWriter, r *http.Request, store *control.NotificationRuleStore, id core.ID) {
	if store == nil {
		http.Error(w, "notification store is not configured", http.StatusServiceUnavailable)
		return
	}
	rule, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get notification", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	if !wantsSecrets(r) {
		rule = redactNotification(rule)
	}
	writeJSON(w, rule)
}

func handleUpdateNotification(w http.ResponseWriter, r *http.Request, store *control.NotificationRuleStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "notification store is not configured", http.StatusServiceUnavailable)
		return
	}
	var rule core.NotificationRule
	if err := decodeJSONRequest(w, r, &rule); err != nil {
		http.Error(w, "invalid notification", http.StatusBadRequest)
		return
	}
	existing, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get notification", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	rule.ID = id
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = existing.CreatedAt
	}
	rule.UpdatedAt = time.Now().UTC()
	if err := store.Save(rule); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "notification.updated", "notification", rule.ID, map[string]any{"events": len(rule.Events)})) {
		return
	}
	writeJSON(w, redactNotification(rule))
}

func handleDeleteNotification(w http.ResponseWriter, r *http.Request, store *control.NotificationRuleStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "notification store is not configured", http.StatusServiceUnavailable)
		return
	}
	if _, ok, err := store.Get(id); err != nil {
		http.Error(w, "get notification", http.StatusInternalServerError)
		return
	} else if !ok {
		http.NotFound(w, nil)
		return
	}
	if err := store.Delete(id); err != nil {
		http.Error(w, "delete notification", http.StatusInternalServerError)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "notification.deleted", "notification", id, nil)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func wantsSecrets(r *http.Request) bool {
	return r != nil && strings.EqualFold(r.URL.Query().Get("include_secrets"), "true")
}

func redactTarget(target core.Target) core.Target {
	target.Options = redactOptions(target.Options)
	return target
}

func redactStorage(storage core.Storage) core.Storage {
	storage.Options = redactOptions(storage.Options)
	return storage
}

func redactNotification(rule core.NotificationRule) core.NotificationRule {
	if rule.Secret != "" {
		rule.Secret = "***REDACTED***"
	}
	return rule
}

func redactOptions(options map[string]any) map[string]any {
	if len(options) == 0 {
		return options
	}
	out := make(map[string]any, len(options))
	for key, value := range options {
		if isSecretOptionKey(key) {
			out[key] = "***REDACTED***"
			continue
		}
		out[key] = value
	}
	return out
}

func isSecretOptionKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), ".", "_"))
	for _, marker := range []string{"password", "secret", "token", "passphrase", "credential", "private_key", "access_key", "session_key", "encryption_key", "api_key", "apikey"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func handleListTargets(w http.ResponseWriter, r *http.Request, store *control.TargetStore) {
	if store == nil {
		writeJSON(w, map[string]any{"targets": []any{}})
		return
	}
	targets, err := store.List()
	if err != nil {
		http.Error(w, "list targets", http.StatusInternalServerError)
		return
	}
	if !wantsSecrets(r) {
		for i := range targets {
			targets[i] = redactTarget(targets[i])
		}
	}
	writeJSON(w, map[string]any{"targets": targets})
}

func handleCreateTarget(w http.ResponseWriter, r *http.Request, store *control.TargetStore, auditLog *kaudit.Log) {
	if store == nil {
		http.Error(w, "target store is not configured", http.StatusServiceUnavailable)
		return
	}
	var target core.Target
	if err := decodeJSONRequest(w, r, &target); err != nil {
		http.Error(w, "invalid target", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	if target.ID.IsZero() {
		id, err := core.NewID(core.RealClock{})
		if err != nil {
			http.Error(w, "create target id", http.StatusInternalServerError)
			return
		}
		target.ID = id
	}
	target.CreatedAt, target.UpdatedAt = stampTimes(target.CreatedAt, now)
	if err := store.Save(target); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "target.created", "target", target.ID, nil)) {
		return
	}
	writeJSON(w, redactTarget(target))
}

func handleGetTarget(w http.ResponseWriter, r *http.Request, store *control.TargetStore, id core.ID) {
	if store == nil {
		http.Error(w, "target store is not configured", http.StatusServiceUnavailable)
		return
	}
	target, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get target", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	if !wantsSecrets(r) {
		target = redactTarget(target)
	}
	writeJSON(w, target)
}

func handleUpdateTarget(w http.ResponseWriter, r *http.Request, store *control.TargetStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "target store is not configured", http.StatusServiceUnavailable)
		return
	}
	var target core.Target
	if err := decodeJSONRequest(w, r, &target); err != nil {
		http.Error(w, "invalid target", http.StatusBadRequest)
		return
	}
	existing, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get target", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	target.ID = id
	target.CreatedAt, target.UpdatedAt = stampTimes(existing.CreatedAt, time.Now().UTC())
	if err := store.Save(target); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "target.updated", "target", target.ID, nil)) {
		return
	}
	writeJSON(w, redactTarget(target))
}

func handleDeleteTarget(w http.ResponseWriter, r *http.Request, store *control.TargetStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "target store is not configured", http.StatusServiceUnavailable)
		return
	}
	if _, ok, err := store.Get(id); err != nil {
		http.Error(w, "get target", http.StatusInternalServerError)
		return
	} else if !ok {
		http.NotFound(w, nil)
		return
	}
	if err := store.Delete(id); err != nil {
		http.Error(w, "delete target", http.StatusInternalServerError)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "target.deleted", "target", id, nil)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleListStorages(w http.ResponseWriter, r *http.Request, store *control.StorageStore) {
	if store == nil {
		writeJSON(w, map[string]any{"storages": []any{}})
		return
	}
	storages, err := store.List()
	if err != nil {
		http.Error(w, "list storages", http.StatusInternalServerError)
		return
	}
	if !wantsSecrets(r) {
		for i := range storages {
			storages[i] = redactStorage(storages[i])
		}
	}
	writeJSON(w, map[string]any{"storages": storages})
}

func handleCreateStorage(w http.ResponseWriter, r *http.Request, store *control.StorageStore, auditLog *kaudit.Log) {
	if store == nil {
		http.Error(w, "storage store is not configured", http.StatusServiceUnavailable)
		return
	}
	var storage core.Storage
	if err := decodeJSONRequest(w, r, &storage); err != nil {
		http.Error(w, "invalid storage", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	if storage.ID.IsZero() {
		id, err := core.NewID(core.RealClock{})
		if err != nil {
			http.Error(w, "create storage id", http.StatusInternalServerError)
			return
		}
		storage.ID = id
	}
	storage.CreatedAt, storage.UpdatedAt = stampTimes(storage.CreatedAt, now)
	if err := store.Save(storage); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "storage.created", "storage", storage.ID, nil)) {
		return
	}
	writeJSON(w, redactStorage(storage))
}

func handleGetStorage(w http.ResponseWriter, r *http.Request, store *control.StorageStore, id core.ID) {
	if store == nil {
		http.Error(w, "storage store is not configured", http.StatusServiceUnavailable)
		return
	}
	storage, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get storage", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	if !wantsSecrets(r) {
		storage = redactStorage(storage)
	}
	writeJSON(w, storage)
}

func handleUpdateStorage(w http.ResponseWriter, r *http.Request, store *control.StorageStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "storage store is not configured", http.StatusServiceUnavailable)
		return
	}
	var storage core.Storage
	if err := decodeJSONRequest(w, r, &storage); err != nil {
		http.Error(w, "invalid storage", http.StatusBadRequest)
		return
	}
	existing, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get storage", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	storage.ID = id
	storage.CreatedAt, storage.UpdatedAt = stampTimes(existing.CreatedAt, time.Now().UTC())
	if err := store.Save(storage); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "storage.updated", "storage", storage.ID, nil)) {
		return
	}
	writeJSON(w, redactStorage(storage))
}

func handleDeleteStorage(w http.ResponseWriter, r *http.Request, store *control.StorageStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "storage store is not configured", http.StatusServiceUnavailable)
		return
	}
	if _, ok, err := store.Get(id); err != nil {
		http.Error(w, "get storage", http.StatusInternalServerError)
		return
	} else if !ok {
		http.NotFound(w, nil)
		return
	}
	if err := store.Delete(id); err != nil {
		http.Error(w, "delete storage", http.StatusInternalServerError)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "storage.deleted", "storage", id, nil)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleListSchedules(w http.ResponseWriter, store *control.ScheduleStore) {
	if store == nil {
		writeJSON(w, map[string]any{"schedules": []any{}})
		return
	}
	schedules, err := store.List()
	if err != nil {
		http.Error(w, "list schedules", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"schedules": schedules})
}

func handleCreateSchedule(w http.ResponseWriter, r *http.Request, store *control.ScheduleStore, auditLog *kaudit.Log) {
	if store == nil {
		http.Error(w, "schedule store is not configured", http.StatusServiceUnavailable)
		return
	}
	var schedule core.Schedule
	if err := decodeJSONRequest(w, r, &schedule); err != nil {
		http.Error(w, "invalid schedule", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	if schedule.ID.IsZero() {
		id, err := core.NewID(core.RealClock{})
		if err != nil {
			http.Error(w, "create schedule id", http.StatusInternalServerError)
			return
		}
		schedule.ID = id
	}
	schedule.CreatedAt, schedule.UpdatedAt = stampTimes(schedule.CreatedAt, now)
	if err := store.Save(schedule); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "schedule.created", "schedule", schedule.ID, nil)) {
		return
	}
	writeJSON(w, schedule)
}

func handleGetSchedule(w http.ResponseWriter, store *control.ScheduleStore, id core.ID) {
	if store == nil {
		http.Error(w, "schedule store is not configured", http.StatusServiceUnavailable)
		return
	}
	schedule, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get schedule", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	writeJSON(w, schedule)
}

func handleUpdateSchedule(w http.ResponseWriter, r *http.Request, store *control.ScheduleStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "schedule store is not configured", http.StatusServiceUnavailable)
		return
	}
	var schedule core.Schedule
	if err := decodeJSONRequest(w, r, &schedule); err != nil {
		http.Error(w, "invalid schedule", http.StatusBadRequest)
		return
	}
	existing, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get schedule", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	schedule.ID = id
	schedule.CreatedAt, schedule.UpdatedAt = stampTimes(existing.CreatedAt, time.Now().UTC())
	if err := store.Save(schedule); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "schedule.updated", "schedule", schedule.ID, nil)) {
		return
	}
	writeJSON(w, schedule)
}

func handleDeleteSchedule(w http.ResponseWriter, r *http.Request, store *control.ScheduleStore, auditLog *kaudit.Log, id core.ID) {
	if store == nil {
		http.Error(w, "schedule store is not configured", http.StatusServiceUnavailable)
		return
	}
	if _, ok, err := store.Get(id); err != nil {
		http.Error(w, "get schedule", http.StatusInternalServerError)
		return
	} else if !ok {
		http.NotFound(w, nil)
		return
	}
	if err := store.Delete(id); err != nil {
		http.Error(w, "delete schedule", http.StatusInternalServerError)
		return
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, "schedule.deleted", "schedule", id, nil)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handlePauseSchedule(w http.ResponseWriter, r *http.Request, store *control.ScheduleStore, auditLog *kaudit.Log, id core.ID, paused bool) {
	if store == nil {
		http.Error(w, "schedule store is not configured", http.StatusServiceUnavailable)
		return
	}
	schedule, ok, err := store.Get(id)
	if err != nil {
		http.Error(w, "get schedule", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	schedule.Paused = paused
	schedule.UpdatedAt = time.Now().UTC()
	if err := store.Save(schedule); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	action := "schedule.resumed"
	if paused {
		action = "schedule.paused"
	}
	if handleAuditAppendError(w, appendAuditEvent(r, auditLog, action, "schedule", schedule.ID, nil)) {
		return
	}
	writeJSON(w, schedule)
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, dst any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func decodeOptionalJSONRequest(w http.ResponseWriter, r *http.Request, dst any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return err
	} else if errors.Is(err, io.EOF) {
		return nil
	}
	return ensureJSONEOF(decoder)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("request body must contain a single JSON value")
}

func stampTimes(createdAt time.Time, now time.Time) (time.Time, time.Time) {
	if createdAt.IsZero() {
		createdAt = now
	}
	return createdAt.UTC(), now.UTC()
}

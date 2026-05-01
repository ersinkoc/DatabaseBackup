package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kronos/kronos/internal/chunk"
	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/drivers"
	"github.com/kronos/kronos/internal/engine"
	"github.com/kronos/kronos/internal/manifest"
	"github.com/kronos/kronos/internal/repository"
	"github.com/kronos/kronos/internal/secret"
	"github.com/kronos/kronos/internal/storage"
	azurestorage "github.com/kronos/kronos/internal/storage/azure"
	gcsstorage "github.com/kronos/kronos/internal/storage/gcs"
	"github.com/kronos/kronos/internal/storage/local"
	"github.com/kronos/kronos/internal/storage/s3"
	sftpstorage "github.com/kronos/kronos/internal/storage/sftp"
	"github.com/kronos/kronos/internal/verify"
)

// PipelineFactory builds a fresh chunk pipeline for a backend.
type PipelineFactory func(backend storage.Backend) (*chunk.Pipeline, error)

// StorageFactory opens a concrete backend from a control-plane storage record.
type StorageFactory func(item core.Storage) (storage.Backend, error)

// BackupExecutor executes backup jobs with registered drivers and backends.
type BackupExecutor struct {
	Drivers         *drivers.Registry
	Targets         map[core.ID]drivers.Target
	Backends        map[core.ID]storage.Backend
	Backups         map[core.ID]core.Backup
	PipelineFactory PipelineFactory
	StorageFactory  StorageFactory
	PublicKey       ed25519.PublicKey
	PrivateKey      ed25519.PrivateKey
	Clock           core.Clock
	SecretResolver  secret.Resolver
}

// SyncResources refreshes targets, storage backends, and backup metadata from the control plane.
func (e *BackupExecutor) SyncResources(ctx context.Context, client *Client) error {
	if e == nil {
		return fmt.Errorf("backup executor is required")
	}
	if client == nil {
		return fmt.Errorf("agent client is required")
	}
	targets, err := client.ListTargets(ctx)
	if err != nil {
		return err
	}
	targetMap := make(map[core.ID]drivers.Target, len(targets))
	for _, target := range targets {
		options, err := resolveResourceOptions(ctx, target.Options, e.SecretResolver)
		if err != nil {
			return fmt.Errorf("target %s secrets: %w", target.ID, err)
		}
		target.Options = options
		targetMap[target.ID] = drivers.Target{
			Name:       target.Name,
			Driver:     string(target.Driver),
			Connection: targetConnection(target),
			Options:    stringOptions(target.Options),
		}
	}
	e.Targets = targetMap

	storages, err := client.ListStorages(ctx)
	if err != nil {
		return err
	}
	storageFactory := e.StorageFactory
	if storageFactory == nil {
		storageFactory = OpenStorageBackend
	}
	backendMap := make(map[core.ID]storage.Backend, len(storages))
	for _, item := range storages {
		options, err := resolveResourceOptions(ctx, item.Options, e.SecretResolver)
		if err != nil {
			return fmt.Errorf("storage %s secrets: %w", item.ID, err)
		}
		item.Options = options
		backend, err := storageFactory(item)
		if err != nil {
			return fmt.Errorf("storage %s: %w", item.ID, err)
		}
		backendMap[item.ID] = backend
	}
	e.Backends = backendMap

	backups, err := client.ListBackups(ctx)
	if err != nil {
		return err
	}
	backupMap := make(map[core.ID]core.Backup, len(backups))
	for _, backup := range backups {
		backupMap[backup.ID] = backup
	}
	e.Backups = backupMap
	return nil
}

// OpenStorageBackend builds a supported repository backend from a storage record.
func OpenStorageBackend(item core.Storage) (storage.Backend, error) {
	switch item.Kind {
	case core.StorageKindLocal:
		root, err := localStorageRoot(item.URI)
		if err != nil {
			return nil, err
		}
		return local.New(item.Name, root)
	case core.StorageKindS3:
		cfg, err := s3StorageConfig(item)
		if err != nil {
			return nil, err
		}
		return s3.New(cfg)
	case core.StorageKindSFTP:
		cfg, err := sftpStorageConfig(item)
		if err != nil {
			return nil, err
		}
		return sftpstorage.New(context.Background(), cfg)
	case core.StorageKindAzure:
		cfg, err := azureStorageConfig(item)
		if err != nil {
			return nil, err
		}
		return azurestorage.New(cfg)
	case core.StorageKindGCS:
		cfg, err := gcsStorageConfig(item)
		if err != nil {
			return nil, err
		}
		return gcsstorage.New(cfg)
	default:
		return nil, fmt.Errorf("storage kind %q is not implemented in this build; supported storage kinds: local, s3, sftp, azure, gcs", item.Kind)
	}
}

// Execute runs one job and returns operation-specific metadata for the control plane.
func (e BackupExecutor) Execute(ctx context.Context, job core.Job) (core.JobResult, error) {
	switch job.Operation {
	case core.JobOperationRestore:
		report, err := e.executeRestore(ctx, job)
		if err != nil {
			err = e.runFailureHooks(ctx, job, err)
		}
		return core.JobResult{Restore: report}, err
	case core.JobOperationVerify:
		report, err := e.executeVerify(ctx, job)
		if err != nil {
			err = e.runFailureHooks(ctx, job, err)
		}
		return core.JobResult{Verification: report}, err
	}
	if err := e.runHooks(ctx, job, "pre_backup", job.Hooks.PreBackup, nil); err != nil {
		return core.JobResult{}, err
	}
	backup, err := e.executeBackup(ctx, job)
	if err != nil {
		err = e.runFailureHooks(ctx, job, err)
	}
	return core.JobResult{Backup: backup}, err
}

func (e BackupExecutor) runFailureHooks(ctx context.Context, job core.Job, jobErr error) error {
	if hookErr := e.runHooks(ctx, job, "on_failure", job.Hooks.OnFailure, jobErr); hookErr != nil {
		return fmt.Errorf("%w; on_failure hooks: %v", jobErr, hookErr)
	}
	return jobErr
}

func (e BackupExecutor) runHooks(ctx context.Context, job core.Job, hookName string, actions []core.JobHookAction, jobErr error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for i, action := range actions {
		if shell := strings.TrimSpace(action.Shell); shell != "" {
			if err := runShellHook(ctx, job, hookName, shell, jobErr); err != nil {
				return fmt.Errorf("%s hook %d shell: %w", hookName, i+1, err)
			}
		}
		if webhookURL := strings.TrimSpace(action.WebhookURL); webhookURL != "" {
			if err := runWebhookHook(ctx, job, hookName, webhookURL, jobErr); err != nil {
				return fmt.Errorf("%s hook %d webhook: %w", hookName, i+1, err)
			}
		}
	}
	return nil
}

func runShellHook(ctx context.Context, job core.Job, hookName string, command string, jobErr error) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = append(os.Environ(), hookEnv(job, hookName, jobErr)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, text)
	}
	return nil
}

func runWebhookHook(ctx context.Context, job core.Job, hookName string, webhookURL string, jobErr error) error {
	payload, err := json.Marshal(hookPayload(job, hookName, jobErr))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}

func hookEnv(job core.Job, hookName string, jobErr error) []string {
	values := []string{
		"KRONOS_HOOK=" + hookName,
		"KRONOS_JOB_ID=" + job.ID.String(),
		"KRONOS_JOB_OPERATION=" + string(job.Operation),
		"KRONOS_TARGET_ID=" + job.TargetID.String(),
		"KRONOS_STORAGE_ID=" + job.StorageID.String(),
		"KRONOS_SCHEDULE_ID=" + job.ScheduleID.String(),
	}
	if jobErr != nil {
		values = append(values, "KRONOS_JOB_ERROR="+jobErr.Error())
	}
	return values
}

func hookPayload(job core.Job, hookName string, jobErr error) map[string]any {
	payload := map[string]any{
		"hook":        hookName,
		"job_id":      job.ID,
		"operation":   job.Operation,
		"target_id":   job.TargetID,
		"storage_id":  job.StorageID,
		"schedule_id": job.ScheduleID,
	}
	if jobErr != nil {
		payload["error"] = jobErr.Error()
	}
	return payload
}

func localStorageRoot(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("local storage uri is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse local storage uri: %w", err)
	}
	if u.Scheme == "" {
		return raw, nil
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("local storage uri must use file scheme")
	}
	switch {
	case u.Path != "":
		return u.Path, nil
	case u.Host != "":
		return u.Host, nil
	case u.Opaque != "":
		return u.Opaque, nil
	default:
		return "", fmt.Errorf("local storage root is required")
	}
}

func s3StorageConfig(item core.Storage) (s3.Config, error) {
	bucket, err := s3Bucket(item.URI)
	if err != nil {
		return s3.Config{}, err
	}
	cfg := s3.Config{
		Name:           item.Name,
		Bucket:         bucket,
		Endpoint:       optionString(item.Options, "endpoint"),
		Region:         optionString(item.Options, "region"),
		ForcePathStyle: optionBool(item.Options, "force_path_style"),
	}
	if accessKey := optionString(item.Options, "access_key", "accessKey", "aws_access_key_id"); accessKey != "" {
		cfg.Credentials = s3.Credentials{
			AccessKey:    accessKey,
			SecretKey:    optionString(item.Options, "secret_key", "secretKey", "aws_secret_access_key"),
			SessionToken: optionString(item.Options, "session_token", "sessionToken", "aws_session_token"),
		}
	} else {
		credentials := strings.TrimSpace(optionString(item.Options, "credentials", "credentials_provider"))
		switch strings.ToLower(credentials) {
		case "", "env", "aws", "aws_env":
			cfg.CredentialsProvider = s3.EnvCredentialsProvider{}
		case "imds", "instance_metadata":
			cfg.CredentialsProvider = s3.IMDSProvider{Endpoint: optionString(item.Options, "imds_endpoint")}
		default:
			parsed, err := parseS3Credentials(credentials)
			if err != nil {
				return s3.Config{}, err
			}
			cfg.Credentials = parsed
		}
	}
	return cfg, nil
}

func parseS3Credentials(value string) (s3.Credentials, error) {
	var raw struct {
		AccessKey       string `json:"access_key"`
		AccessKeyID     string `json:"access_key_id"`
		AWSAccessKeyID  string `json:"aws_access_key_id"`
		SecretKey       string `json:"secret_key"`
		SecretAccessKey string `json:"secret_access_key"`
		AWSSecretKey    string `json:"aws_secret_access_key"`
		SessionToken    string `json:"session_token"`
		AWSSessionToken string `json:"aws_session_token"`
		Token           string `json:"token"`
	}
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return s3.Credentials{}, fmt.Errorf("parse s3 credentials: expected env, aws, imds, or JSON object: %w", err)
	}
	creds := s3.Credentials{
		AccessKey:    firstNonEmpty(raw.AccessKey, raw.AccessKeyID, raw.AWSAccessKeyID),
		SecretKey:    firstNonEmpty(raw.SecretKey, raw.SecretAccessKey, raw.AWSSecretKey),
		SessionToken: firstNonEmpty(raw.SessionToken, raw.AWSSessionToken, raw.Token),
	}
	if creds.AccessKey == "" || creds.SecretKey == "" {
		return s3.Credentials{}, fmt.Errorf("s3 credentials JSON requires access_key and secret_key")
	}
	return creds, nil
}

func sftpStorageConfig(item core.Storage) (sftpstorage.Config, error) {
	u, err := url.Parse(item.URI)
	if err != nil {
		return sftpstorage.Config{}, fmt.Errorf("parse sftp storage uri: %w", err)
	}
	if u.Scheme != "sftp" {
		return sftpstorage.Config{}, fmt.Errorf("sftp storage uri must use sftp scheme")
	}
	if u.Host == "" {
		return sftpstorage.Config{}, fmt.Errorf("sftp storage uri host is required")
	}
	username := optionString(item.Options, "username", "user")
	password := optionString(item.Options, "password")
	if u.User != nil {
		if username == "" {
			username = u.User.Username()
		}
		if value, ok := u.User.Password(); ok && password == "" {
			password = value
		}
	}
	return sftpstorage.Config{
		Name:                  item.Name,
		Address:               u.Host,
		Root:                  u.Path,
		Username:              username,
		Password:              password,
		PrivateKeyPEM:         optionString(item.Options, "private_key", "privateKey"),
		PrivateKeyPath:        optionString(item.Options, "private_key_path", "privateKeyPath"),
		Passphrase:            optionString(item.Options, "passphrase", "private_key_passphrase", "privateKeyPassphrase"),
		AgentSocket:           optionString(item.Options, "agent_socket", "ssh_agent_socket"),
		KnownHostsPath:        optionString(item.Options, "known_hosts", "knownHosts"),
		InsecureIgnoreHostKey: optionBool(item.Options, "insecure_ignore_host_key", "insecureIgnoreHostKey"),
	}, nil
}

func azureStorageConfig(item core.Storage) (azurestorage.Config, error) {
	u, err := url.Parse(item.URI)
	if err != nil {
		return azurestorage.Config{}, fmt.Errorf("parse azure storage uri: %w", err)
	}
	if u.Scheme != "azure" && u.Scheme != "azblob" {
		return azurestorage.Config{}, fmt.Errorf("azure storage uri must use azure or azblob scheme")
	}
	account := optionString(item.Options, "account_name", "accountName", "account")
	container := optionString(item.Options, "container")
	prefix := optionString(item.Options, "prefix")
	if u.Host != "" {
		if account == "" {
			account = u.Host
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) > 0 && parts[0] != "" {
			if container == "" {
				container = parts[0]
			}
			if prefix == "" && len(parts) > 1 {
				prefix = strings.Join(parts[1:], "/")
			}
		}
	}
	return azurestorage.Config{
		Name:        item.Name,
		AccountName: account,
		AccountKey:  optionString(item.Options, "account_key", "accountKey"),
		Container:   container,
		Prefix:      prefix,
		Endpoint:    optionString(item.Options, "endpoint"),
		SASToken:    optionString(item.Options, "sas_token", "sasToken", "sas"),
	}, nil
}

func gcsStorageConfig(item core.Storage) (gcsstorage.Config, error) {
	u, err := url.Parse(item.URI)
	if err != nil {
		return gcsstorage.Config{}, fmt.Errorf("parse gcs storage uri: %w", err)
	}
	if u.Scheme != "gcs" && u.Scheme != "gs" {
		return gcsstorage.Config{}, fmt.Errorf("gcs storage uri must use gcs or gs scheme")
	}
	bucket := optionString(item.Options, "bucket")
	prefix := optionString(item.Options, "prefix")
	if u.Host != "" {
		if bucket == "" {
			bucket = u.Host
		}
		if prefix == "" {
			prefix = strings.Trim(u.Path, "/")
		}
	}
	return gcsstorage.Config{
		Name:        item.Name,
		Bucket:      bucket,
		Prefix:      prefix,
		Endpoint:    optionString(item.Options, "endpoint"),
		BearerToken: optionString(item.Options, "bearer_token", "bearerToken", "access_token", "accessToken"),
		APIKey:      optionString(item.Options, "api_key", "apiKey"),
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func s3Bucket(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse s3 storage uri: %w", err)
	}
	if u.Scheme != "s3" {
		return "", fmt.Errorf("s3 storage uri must use s3 scheme")
	}
	if u.Host != "" {
		return u.Host, nil
	}
	bucket := strings.Trim(strings.Split(strings.TrimPrefix(u.Path, "/"), "/")[0], " ")
	if bucket == "" {
		return "", fmt.Errorf("s3 bucket is required")
	}
	return bucket, nil
}

func targetConnection(target core.Target) map[string]string {
	out := make(map[string]string)
	if target.Endpoint != "" {
		out["addr"] = target.Endpoint
	}
	if target.Database != "" {
		out["database"] = target.Database
	}
	for _, key := range []string{"host", "port", "username", "password", "tls", "sslmode", "dsn"} {
		if value := optionString(target.Options, key); value != "" {
			out[key] = value
		}
	}
	if out["username"] == "" {
		if value := optionString(target.Options, "user"); value != "" {
			out["username"] = value
		}
	}
	return out
}

func optionString(options map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := options[key]
		if !ok || value == nil {
			continue
		}
		switch v := value.(type) {
		case string:
			return v
		default:
			return fmt.Sprint(v)
		}
	}
	return ""
}

func resolveResourceOptions(ctx context.Context, in map[string]any, resolver secret.Resolver) (map[string]any, error) {
	if len(in) == 0 {
		return in, nil
	}
	if resolver == nil {
		resolver = secret.NewRegistry()
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		text, ok := value.(string)
		if !ok {
			out[key] = value
			continue
		}
		ref, found, err := secret.ParsePlaceholder(strings.TrimSpace(text))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if !found {
			out[key] = value
			continue
		}
		resolved, err := resolver.Resolve(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		out[key] = resolved
	}
	return out, nil
}

func optionBool(options map[string]any, keys ...string) bool {
	value := strings.TrimSpace(strings.ToLower(optionString(options, keys...)))
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

func stringOptions(in map[string]any) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = fmt.Sprint(value)
	}
	return out
}

func (e BackupExecutor) executeBackup(ctx context.Context, job core.Job) (*core.Backup, error) {
	backupType := job.Type
	if backupType == "" {
		backupType = core.BackupTypeFull
	}
	if backupType != core.BackupTypeFull && backupType != core.BackupTypeIncremental && backupType != core.BackupTypeDifferential {
		return nil, fmt.Errorf("backup type %s is not supported by agent executor yet", backupType)
	}
	if e.Drivers == nil {
		return nil, fmt.Errorf("driver registry is required")
	}
	if e.PipelineFactory == nil {
		return nil, fmt.Errorf("pipeline factory is required")
	}
	if len(e.PrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("manifest signing key is required")
	}
	clock := e.Clock
	if clock == nil {
		clock = core.RealClock{}
	}
	target, ok := e.Targets[job.TargetID]
	if !ok {
		return nil, fmt.Errorf("target %q is not configured on agent", job.TargetID)
	}
	backend, ok := e.Backends[job.StorageID]
	if !ok {
		return nil, fmt.Errorf("storage %q is not configured on agent", job.StorageID)
	}
	driver, ok := e.Drivers.Get(target.Driver)
	if !ok {
		return nil, fmt.Errorf("target driver %q is not implemented in this build; registered target drivers: %s", target.Driver, strings.Join(e.Drivers.Names(), ", "))
	}
	pipeline, err := e.PipelineFactory(backend)
	if err != nil {
		return nil, err
	}
	if pipeline == nil {
		return nil, fmt.Errorf("pipeline factory returned nil pipeline")
	}
	if pipeline.Cipher == nil {
		return nil, fmt.Errorf("pipeline cipher is required")
	}
	if pipeline.KeyID == "" {
		return nil, fmt.Errorf("pipeline key id is required")
	}
	startedAt := clock.Now().UTC()
	result, parentID, err := e.runBackupEngine(ctx, driver, target, backend, job, backupType, pipeline)
	if err != nil {
		return nil, err
	}
	finishedAt := clock.Now().UTC()
	backupID, err := core.NewID(clock)
	if err != nil {
		return nil, err
	}
	version, err := driver.Version(ctx, target)
	if err != nil {
		return nil, err
	}
	commit, err := repository.CommitManifest(ctx, backend, result, repository.CommitOptions{
		BackupID: string(backupID),
		Target:   target.Name,
		Driver: manifest.Driver{
			Name:    driver.Name(),
			Version: version,
		},
		Type:       backupType,
		ParentID:   stringPtrFromID(parentID),
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Encryption: manifest.Encryption{
			Algorithm: pipeline.Cipher.Algorithm(),
			KeyID:     pipeline.KeyID,
		},
		PrivateKey: e.PrivateKey,
	})
	if err != nil {
		return nil, err
	}
	return &core.Backup{
		ID:         backupID,
		TargetID:   job.TargetID,
		StorageID:  job.StorageID,
		JobID:      job.ID,
		Type:       backupType,
		ParentID:   parentID,
		ManifestID: core.ID(commit.Key),
		StartedAt:  startedAt,
		EndedAt:    finishedAt,
		SizeBytes:  result.Stats.BytesIn,
		ChunkCount: result.Stats.Chunks,
	}, nil
}

func (e BackupExecutor) runBackupEngine(ctx context.Context, driver drivers.Driver, target drivers.Target, backend storage.Backend, job core.Job, backupType core.BackupType, pipeline *chunk.Pipeline) (engine.BackupResult, core.ID, error) {
	if backupType == core.BackupTypeFull {
		result, err := engine.BackupFull(ctx, driver, target, pipeline)
		return result, "", err
	}
	if job.ParentBackupID.IsZero() {
		return engine.BackupResult{}, "", fmt.Errorf("parent backup id is required for %s backup", backupType)
	}
	parent, ok := e.Backups[job.ParentBackupID]
	if !ok {
		return engine.BackupResult{}, "", fmt.Errorf("parent backup %q is not configured on agent", job.ParentBackupID)
	}
	if parent.ManifestID.IsZero() {
		return engine.BackupResult{}, "", fmt.Errorf("parent backup %q manifest id is required", job.ParentBackupID)
	}
	publicKey, err := e.publicKey()
	if err != nil {
		return engine.BackupResult{}, "", err
	}
	parentManifest, _, err := repository.LoadManifest(ctx, backend, string(parent.ManifestID), publicKey)
	if err != nil {
		return engine.BackupResult{}, "", err
	}
	result, err := engine.BackupIncremental(ctx, driver, target, parentManifest, pipeline)
	return result, parent.ID, err
}

func (e BackupExecutor) executeRestore(ctx context.Context, job core.Job) (*core.RestoreReport, error) {
	if e.Drivers == nil {
		return nil, fmt.Errorf("driver registry is required")
	}
	if e.PipelineFactory == nil {
		return nil, fmt.Errorf("pipeline factory is required")
	}
	publicKey, err := e.publicKey()
	if err != nil {
		return nil, err
	}
	if job.RestoreBackupID.IsZero() {
		return nil, fmt.Errorf("restore backup id is required")
	}
	targetID := job.RestoreTargetID
	if targetID.IsZero() {
		targetID = job.TargetID
	}
	target, ok := e.Targets[targetID]
	if !ok {
		return nil, fmt.Errorf("restore target %q is not configured on agent", targetID)
	}
	storageID := job.StorageID
	manifestID := job.RestoreManifestID
	manifestIDs := append([]core.ID(nil), job.RestoreManifestIDs...)
	if backup, ok := e.Backups[job.RestoreBackupID]; ok {
		if storageID.IsZero() {
			storageID = backup.StorageID
		}
		if manifestID.IsZero() {
			manifestID = backup.ManifestID
		}
	}
	if len(manifestIDs) == 0 && !manifestID.IsZero() {
		manifestIDs = append(manifestIDs, manifestID)
	}
	if storageID.IsZero() {
		return nil, fmt.Errorf("restore storage id is required")
	}
	if len(manifestIDs) == 0 {
		return nil, fmt.Errorf("restore manifest ids are required")
	}
	backend, ok := e.Backends[storageID]
	if !ok {
		return nil, fmt.Errorf("storage %q is not configured on agent", storageID)
	}
	driver, ok := e.Drivers.Get(target.Driver)
	if !ok {
		return nil, fmt.Errorf("target driver %q is not implemented in this build; registered target drivers: %s", target.Driver, strings.Join(e.Drivers.Names(), ", "))
	}
	pipeline, err := e.PipelineFactory(backend)
	if err != nil {
		return nil, err
	}
	if pipeline == nil {
		return nil, fmt.Errorf("pipeline factory returned nil pipeline")
	}
	refs, report, err := e.restoreRefs(ctx, backend, publicKey, manifestIDs)
	if err != nil {
		return nil, err
	}
	stats, err := engine.Restore(ctx, driver, target, pipeline, refs, drivers.RestoreOptions{
		ReplaceExisting: job.RestoreReplaceExisting,
		DryRun:          job.RestoreDryRun,
		Metadata: map[string]string{
			"backup_id": string(job.RestoreBackupID),
			"job_id":    string(job.ID),
		},
	})
	if err != nil {
		return nil, err
	}
	report.BackupID = job.RestoreBackupID
	report.TargetID = targetID
	report.ManifestIDs = append([]core.ID(nil), manifestIDs...)
	report.RestoredBytes = stats.BytesIn
	report.DryRun = job.RestoreDryRun
	return report, nil
}

func (e BackupExecutor) executeVerify(ctx context.Context, job core.Job) (*core.VerificationReport, error) {
	publicKey, err := e.publicKey()
	if err != nil {
		return nil, err
	}
	if job.VerifyBackupID.IsZero() {
		return nil, fmt.Errorf("verify backup id is required")
	}
	storageID := job.StorageID
	manifestID := job.VerifyManifestID
	manifestIDs := append([]core.ID(nil), job.VerifyManifestIDs...)
	if backup, ok := e.Backups[job.VerifyBackupID]; ok {
		if storageID.IsZero() {
			storageID = backup.StorageID
		}
		if manifestID.IsZero() {
			manifestID = backup.ManifestID
		}
	}
	if storageID.IsZero() {
		return nil, fmt.Errorf("verify storage id is required")
	}
	if len(manifestIDs) == 0 && !manifestID.IsZero() {
		manifestIDs = append(manifestIDs, manifestID)
	}
	if len(manifestIDs) == 0 {
		return nil, fmt.Errorf("verify manifest ids are required")
	}
	backend, ok := e.Backends[storageID]
	if !ok {
		return nil, fmt.Errorf("storage %q is not configured on agent", storageID)
	}
	level := job.VerifyLevel
	if level == "" {
		level = core.JobVerificationChunk
	}
	report := core.VerificationReport{
		BackupID:    job.VerifyBackupID,
		Level:       level,
		ManifestIDs: append([]core.ID(nil), manifestIDs...),
	}
	switch level {
	case core.JobVerificationManifest:
		for _, manifestID := range manifestIDs {
			committed, _, err := repository.LoadManifest(ctx, backend, string(manifestID), publicKey)
			if err != nil {
				return nil, err
			}
			manifestReport, err := verify.Manifest(ctx, backend, committed, publicKey)
			if err != nil {
				return nil, err
			}
			report.Objects += manifestReport.Objects
			report.Chunks += manifestReport.Chunks
			report.StoredBytes += manifestReport.StoredBytes
		}
	case core.JobVerificationChunk:
		if e.PipelineFactory == nil {
			return nil, fmt.Errorf("pipeline factory is required")
		}
		pipeline, err := e.PipelineFactory(backend)
		if err != nil {
			return nil, err
		}
		if pipeline == nil {
			return nil, fmt.Errorf("pipeline factory returned nil pipeline")
		}
		if pipeline.Cipher == nil {
			return nil, fmt.Errorf("pipeline cipher is required")
		}
		for _, manifestID := range manifestIDs {
			committed, _, err := repository.LoadManifest(ctx, backend, string(manifestID), publicKey)
			if err != nil {
				return nil, err
			}
			chunkReport, err := verify.Chunks(ctx, pipeline, committed, publicKey)
			if err != nil {
				return nil, err
			}
			report.Objects += chunkReport.Objects
			report.Chunks += chunkReport.Chunks
			report.VerifiedChunks += chunkReport.VerifiedChunks
			report.StoredBytes += chunkReport.StoredBytes
			report.RestoredBytes += chunkReport.RestoredBytes
		}
	default:
		return nil, fmt.Errorf("unknown verification level %q", level)
	}
	return &report, nil
}

func (e BackupExecutor) restoreRefs(ctx context.Context, backend storage.Backend, publicKey ed25519.PublicKey, manifestIDs []core.ID) ([]chunk.ChunkRef, *core.RestoreReport, error) {
	var refs []chunk.ChunkRef
	report := &core.RestoreReport{}
	for _, manifestID := range manifestIDs {
		if manifestID.IsZero() {
			return nil, nil, fmt.Errorf("restore manifest id is required")
		}
		committed, _, err := repository.LoadManifest(ctx, backend, string(manifestID), publicKey)
		if err != nil {
			return nil, nil, err
		}
		manifestRefs, err := manifestRefs(committed)
		if err != nil {
			return nil, nil, err
		}
		report.Objects += len(committed.Objects)
		for _, ref := range manifestRefs {
			report.StoredBytes += ref.StoredSize
		}
		refs = append(refs, manifestRefs...)
	}
	report.Chunks = len(refs)
	return refs, report, nil
}

func (e BackupExecutor) publicKey() (ed25519.PublicKey, error) {
	if len(e.PublicKey) == ed25519.PublicKeySize {
		return e.PublicKey, nil
	}
	if len(e.PrivateKey) == ed25519.PrivateKeySize {
		publicKey, ok := e.PrivateKey.Public().(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("derive manifest public key")
		}
		return publicKey, nil
	}
	return nil, fmt.Errorf("manifest public key is required")
}

func manifestRefs(m manifest.Manifest) ([]chunk.ChunkRef, error) {
	var refs []chunk.ChunkRef
	for _, object := range m.Objects {
		objectRefs, err := manifest.PipelineRefs(object.Chunks)
		if err != nil {
			return nil, err
		}
		refs = append(refs, objectRefs...)
	}
	return refs, nil
}

func stringPtrFromID(id core.ID) *string {
	if id.IsZero() {
		return nil
	}
	value := id.String()
	return &value
}

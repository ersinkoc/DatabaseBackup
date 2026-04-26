package core

import "time"

// TargetDriver identifies a database family Kronos can back up.
type TargetDriver string

const (
	// TargetDriverPostgres is PostgreSQL.
	TargetDriverPostgres TargetDriver = "postgres"
	// TargetDriverMySQL is MySQL or MariaDB.
	TargetDriverMySQL TargetDriver = "mysql"
	// TargetDriverMongoDB is MongoDB.
	TargetDriverMongoDB TargetDriver = "mongodb"
	// TargetDriverRedis is Redis, Valkey, or a compatible server.
	TargetDriverRedis TargetDriver = "redis"
)

// StorageKind identifies a repository backend.
type StorageKind string

const (
	// StorageKindLocal stores objects on a local filesystem.
	StorageKindLocal StorageKind = "local"
	// StorageKindS3 stores objects through the S3 API.
	StorageKindS3 StorageKind = "s3"
	// StorageKindSFTP stores objects over SFTP.
	StorageKindSFTP StorageKind = "sftp"
	// StorageKindAzure stores objects in Azure Blob Storage.
	StorageKindAzure StorageKind = "azure"
	// StorageKindGCS stores objects in Google Cloud Storage.
	StorageKindGCS StorageKind = "gcs"
)

// BackupType describes the shape of a backup run.
type BackupType string

const (
	// BackupTypeFull captures a complete snapshot.
	BackupTypeFull BackupType = "full"
	// BackupTypeIncremental uploads changed chunks since a parent backup.
	BackupTypeIncremental BackupType = "incr"
	// BackupTypeDifferential uploads changes since the most recent full backup.
	BackupTypeDifferential BackupType = "diff"
	// BackupTypeStream captures a PITR stream such as WAL, binlog, oplog, or AOF.
	BackupTypeStream BackupType = "stream"
	// BackupTypeSchema captures schema without data.
	BackupTypeSchema BackupType = "schema"
)

// JobStatus is the lifecycle state of a job.
type JobStatus string

const (
	// JobStatusQueued means the job is waiting for capacity.
	JobStatusQueued JobStatus = "queued"
	// JobStatusRunning means an agent is executing the job.
	JobStatusRunning JobStatus = "running"
	// JobStatusFinalizing means data has uploaded and metadata is committing.
	JobStatusFinalizing JobStatus = "finalizing"
	// JobStatusSucceeded means the job completed successfully.
	JobStatusSucceeded JobStatus = "succeeded"
	// JobStatusFailed means the job ended with an error.
	JobStatusFailed JobStatus = "failed"
	// JobStatusCanceled means the job was stopped by a user or shutdown.
	JobStatusCanceled JobStatus = "canceled"
)

// JobOperation identifies what an agent should do for a job.
type JobOperation string

const (
	// JobOperationBackup captures a backup from a target into storage.
	JobOperationBackup JobOperation = "backup"
	// JobOperationRestore restores a backup chain into a target.
	JobOperationRestore JobOperation = "restore"
)

// NotificationEvent identifies an event that can trigger an outbound notification.
type NotificationEvent string

const (
	// NotificationJobSucceeded fires after a job completes successfully.
	NotificationJobSucceeded NotificationEvent = "job.succeeded"
	// NotificationJobFailed fires after a job fails.
	NotificationJobFailed NotificationEvent = "job.failed"
	// NotificationJobCanceled fires after a job is canceled.
	NotificationJobCanceled NotificationEvent = "job.canceled"
)

// RoleName identifies a built-in RBAC role.
type RoleName string

const (
	// RoleAdmin can administer every Kronos resource.
	RoleAdmin RoleName = "admin"
	// RoleOperator can run backups and restores.
	RoleOperator RoleName = "operator"
	// RoleViewer can read state without mutating it.
	RoleViewer RoleName = "viewer"
)

// Target describes a database endpoint and its resolved configuration.
type Target struct {
	ID        ID                `json:"id"`
	Name      string            `json:"name"`
	Driver    TargetDriver      `json:"driver"`
	Endpoint  string            `json:"endpoint"`
	Database  string            `json:"database,omitempty"`
	Options   map[string]any    `json:"options,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// Storage describes a backup repository destination.
type Storage struct {
	ID        ID                `json:"id"`
	Name      string            `json:"name"`
	Kind      StorageKind       `json:"kind"`
	URI       string            `json:"uri"`
	Options   map[string]any    `json:"options,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// Schedule declares recurring backup work for a target.
type Schedule struct {
	ID              ID                `json:"id"`
	Name            string            `json:"name"`
	TargetID        ID                `json:"target_id"`
	StorageID       ID                `json:"storage_id"`
	BackupType      BackupType        `json:"backup_type"`
	Expression      string            `json:"expression"`
	RetentionPolicy ID                `json:"retention_policy_id,omitempty"`
	Paused          bool              `json:"paused"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	Labels          map[string]string `json:"labels,omitempty"`
}

// Job records a single unit of scheduled or manual work.
type Job struct {
	ID                     ID           `json:"id"`
	Operation              JobOperation `json:"operation,omitempty"`
	ScheduleID             ID           `json:"schedule_id,omitempty"`
	TargetID               ID           `json:"target_id"`
	StorageID              ID           `json:"storage_id"`
	Type                   BackupType   `json:"type,omitempty"`
	AgentID                string       `json:"agent_id,omitempty"`
	ParentBackupID         ID           `json:"parent_backup_id,omitempty"`
	RestoreBackupID        ID           `json:"restore_backup_id,omitempty"`
	RestoreManifestID      ID           `json:"restore_manifest_id,omitempty"`
	RestoreManifestIDs     []ID         `json:"restore_manifest_ids,omitempty"`
	RestoreTargetID        ID           `json:"restore_target_id,omitempty"`
	RestoreAt              time.Time    `json:"restore_at,omitempty"`
	RestoreDryRun          bool         `json:"restore_dry_run,omitempty"`
	RestoreReplaceExisting bool         `json:"restore_replace_existing,omitempty"`
	Status                 JobStatus    `json:"status"`
	QueuedAt               time.Time    `json:"queued_at"`
	StartedAt              time.Time    `json:"started_at,omitempty"`
	EndedAt                time.Time    `json:"ended_at,omitempty"`
	Error                  string       `json:"error,omitempty"`
}

// Backup describes a committed backup manifest.
type Backup struct {
	ID         ID         `json:"id"`
	TargetID   ID         `json:"target_id"`
	StorageID  ID         `json:"storage_id"`
	JobID      ID         `json:"job_id"`
	Type       BackupType `json:"type"`
	ParentID   ID         `json:"parent_id,omitempty"`
	ManifestID ID         `json:"manifest_id"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    time.Time  `json:"ended_at"`
	SizeBytes  int64      `json:"size_bytes"`
	ChunkCount int        `json:"chunk_count"`
	Protected  bool       `json:"protected"`
}

// Manifest is the immutable metadata committed after chunks are uploaded.
type Manifest struct {
	ID          ID             `json:"id"`
	BackupID    ID             `json:"backup_id"`
	Version     int            `json:"version"`
	ParentID    ID             `json:"parent_id,omitempty"`
	ChunkHashes []string       `json:"chunk_hashes"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

// RetentionPolicy combines one or more keep rules.
type RetentionPolicy struct {
	ID        ID              `json:"id"`
	Name      string          `json:"name"`
	Rules     []RetentionRule `json:"rules"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// RetentionRule is a serialisable retention rule declaration.
type RetentionRule struct {
	Kind   string         `json:"kind"`
	Params map[string]any `json:"params,omitempty"`
}

// NotificationRule routes matching events to an outbound webhook.
type NotificationRule struct {
	ID          ID                  `json:"id"`
	Name        string              `json:"name"`
	Events      []NotificationEvent `json:"events"`
	WebhookURL  string              `json:"webhook_url"`
	Secret      string              `json:"secret,omitempty"`
	MaxAttempts int                 `json:"max_attempts,omitempty"`
	Enabled     bool                `json:"enabled"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

// User is a local or federated Kronos principal.
type User struct {
	ID           ID        `json:"id"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"display_name"`
	Role         RoleName  `json:"role"`
	TOTPEnforced bool      `json:"totp_enforced"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Role describes a named collection of permissions.
type Role struct {
	Name        RoleName `json:"name"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
}

// Token is a scoped API credential record. Secret material is never stored here.
type Token struct {
	ID        ID        `json:"id"`
	UserID    ID        `json:"user_id"`
	Name      string    `json:"name"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	RevokedAt time.Time `json:"revoked_at,omitempty"`
}

// AuditEvent records an administrative action in the hash-chained audit log.
type AuditEvent struct {
	Seq          uint64         `json:"seq"`
	ID           ID             `json:"id"`
	ActorID      ID             `json:"actor_id,omitempty"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   ID             `json:"resource_id,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	OccurredAt   time.Time      `json:"occurred_at"`
	PrevHash     string         `json:"prev_hash,omitempty"`
	Hash         string         `json:"hash"`
}

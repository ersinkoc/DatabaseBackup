package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/kvstore"
	sched "github.com/kronos/kronos/internal/schedule"
)

var (
	targetsBucket   = []byte("targets")
	storagesBucket  = []byte("storages")
	schedulesBucket = []byte("schedules")
	backupsBucket   = []byte("backups")
	usersBucket     = []byte("users")
	policiesBucket  = []byte("retention_policies")
)

// TargetStore persists target definitions.
type TargetStore struct {
	db        *kvstore.DB
	protector *StateSecretProtector
}

// StorageStore persists storage backend definitions.
type StorageStore struct {
	db        *kvstore.DB
	protector *StateSecretProtector
}

// ScheduleStore persists schedule definitions.
type ScheduleStore struct {
	db *kvstore.DB
}

// BackupStore persists committed backup metadata.
type BackupStore struct {
	db *kvstore.DB
}

// UserStore persists local and federated user metadata.
type UserStore struct {
	db *kvstore.DB
}

// RetentionPolicyStore persists reusable retention policies.
type RetentionPolicyStore struct {
	db *kvstore.DB
}

// NewTargetStore returns a target store backed by db.
func NewTargetStore(db *kvstore.DB) (*TargetStore, error) {
	if db == nil {
		return nil, fmt.Errorf("kv database is required")
	}
	return &TargetStore{db: db}, nil
}

// SetSecretProtector enables at-rest protection for sensitive target options.
func (s *TargetStore) SetSecretProtector(protector *StateSecretProtector) {
	s.protector = protector
}

// NewStorageStore returns a storage store backed by db.
func NewStorageStore(db *kvstore.DB) (*StorageStore, error) {
	if db == nil {
		return nil, fmt.Errorf("kv database is required")
	}
	return &StorageStore{db: db}, nil
}

// SetSecretProtector enables at-rest protection for sensitive storage options.
func (s *StorageStore) SetSecretProtector(protector *StateSecretProtector) {
	s.protector = protector
}

// NewScheduleStore returns a schedule store backed by db.
func NewScheduleStore(db *kvstore.DB) (*ScheduleStore, error) {
	if db == nil {
		return nil, fmt.Errorf("kv database is required")
	}
	return &ScheduleStore{db: db}, nil
}

// NewBackupStore returns a backup store backed by db.
func NewBackupStore(db *kvstore.DB) (*BackupStore, error) {
	if db == nil {
		return nil, fmt.Errorf("kv database is required")
	}
	return &BackupStore{db: db}, nil
}

// NewUserStore returns a user store backed by db.
func NewUserStore(db *kvstore.DB) (*UserStore, error) {
	if db == nil {
		return nil, fmt.Errorf("kv database is required")
	}
	return &UserStore{db: db}, nil
}

// NewRetentionPolicyStore returns a retention policy store backed by db.
func NewRetentionPolicyStore(db *kvstore.DB) (*RetentionPolicyStore, error) {
	if db == nil {
		return nil, fmt.Errorf("kv database is required")
	}
	return &RetentionPolicyStore{db: db}, nil
}

// Save inserts or replaces a target.
func (s *TargetStore) Save(target core.Target) error {
	if err := validateTarget(target); err != nil {
		return err
	}
	var err error
	target.Options, err = s.protector.protectOptions(target.Options)
	if err != nil {
		return err
	}
	return saveJSON(s.db, targetsBucket, []byte(target.ID), target)
}

// Get fetches one target by ID.
func (s *TargetStore) Get(id core.ID) (core.Target, bool, error) {
	var target core.Target
	ok, err := getJSON(s.db, targetsBucket, []byte(id), &target)
	if err != nil || !ok {
		return target, ok, err
	}
	target.Options, err = s.protector.revealOptions(target.Options)
	return target, ok, err
}

// List returns all targets ordered by name, then ID.
func (s *TargetStore) List() ([]core.Target, error) {
	var targets []core.Target
	if err := listJSON(s.db, targetsBucket, func(data []byte) error {
		var target core.Target
		if err := json.Unmarshal(data, &target); err != nil {
			return err
		}
		var err error
		target.Options, err = s.protector.revealOptions(target.Options)
		if err != nil {
			return err
		}
		targets = append(targets, target)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Name == targets[j].Name {
			return targets[i].ID < targets[j].ID
		}
		return targets[i].Name < targets[j].Name
	})
	return targets, nil
}

// Delete removes a target by ID.
func (s *TargetStore) Delete(id core.ID) error {
	return deleteKey(s.db, targetsBucket, []byte(id))
}

// Save inserts or replaces a storage definition.
func (s *StorageStore) Save(storage core.Storage) error {
	if err := validateStorage(storage); err != nil {
		return err
	}
	var err error
	storage.Options, err = s.protector.protectOptions(storage.Options)
	if err != nil {
		return err
	}
	return saveJSON(s.db, storagesBucket, []byte(storage.ID), storage)
}

// Get fetches one storage definition by ID.
func (s *StorageStore) Get(id core.ID) (core.Storage, bool, error) {
	var storage core.Storage
	ok, err := getJSON(s.db, storagesBucket, []byte(id), &storage)
	if err != nil || !ok {
		return storage, ok, err
	}
	storage.Options, err = s.protector.revealOptions(storage.Options)
	return storage, ok, err
}

// List returns all storage definitions ordered by name, then ID.
func (s *StorageStore) List() ([]core.Storage, error) {
	var storages []core.Storage
	if err := listJSON(s.db, storagesBucket, func(data []byte) error {
		var storage core.Storage
		if err := json.Unmarshal(data, &storage); err != nil {
			return err
		}
		var err error
		storage.Options, err = s.protector.revealOptions(storage.Options)
		if err != nil {
			return err
		}
		storages = append(storages, storage)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(storages, func(i, j int) bool {
		if storages[i].Name == storages[j].Name {
			return storages[i].ID < storages[j].ID
		}
		return storages[i].Name < storages[j].Name
	})
	return storages, nil
}

// Delete removes a storage definition by ID.
func (s *StorageStore) Delete(id core.ID) error {
	return deleteKey(s.db, storagesBucket, []byte(id))
}

// Save inserts or replaces a schedule.
func (s *ScheduleStore) Save(schedule core.Schedule) error {
	if err := validateSchedule(schedule); err != nil {
		return err
	}
	return saveJSON(s.db, schedulesBucket, []byte(schedule.ID), schedule)
}

// Get fetches one schedule by ID.
func (s *ScheduleStore) Get(id core.ID) (core.Schedule, bool, error) {
	var schedule core.Schedule
	ok, err := getJSON(s.db, schedulesBucket, []byte(id), &schedule)
	return schedule, ok, err
}

// List returns all schedules ordered by name, then ID.
func (s *ScheduleStore) List() ([]core.Schedule, error) {
	var schedules []core.Schedule
	if err := listJSON(s.db, schedulesBucket, func(data []byte) error {
		var schedule core.Schedule
		if err := json.Unmarshal(data, &schedule); err != nil {
			return err
		}
		schedules = append(schedules, schedule)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(schedules, func(i, j int) bool {
		if schedules[i].Name == schedules[j].Name {
			return schedules[i].ID < schedules[j].ID
		}
		return schedules[i].Name < schedules[j].Name
	})
	return schedules, nil
}

// Delete removes a schedule by ID.
func (s *ScheduleStore) Delete(id core.ID) error {
	return deleteKey(s.db, schedulesBucket, []byte(id))
}

// Save inserts or replaces committed backup metadata.
func (s *BackupStore) Save(backup core.Backup) error {
	if err := validateBackup(backup); err != nil {
		return err
	}
	return saveJSON(s.db, backupsBucket, []byte(backup.ID), backup)
}

// Get fetches one backup by ID.
func (s *BackupStore) Get(id core.ID) (core.Backup, bool, error) {
	var backup core.Backup
	ok, err := getJSON(s.db, backupsBucket, []byte(id), &backup)
	return backup, ok, err
}

// List returns all backups ordered newest first, then ID.
func (s *BackupStore) List() ([]core.Backup, error) {
	var backups []core.Backup
	if err := listJSON(s.db, backupsBucket, func(data []byte) error {
		var backup core.Backup
		if err := json.Unmarshal(data, &backup); err != nil {
			return err
		}
		backups = append(backups, backup)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].EndedAt.Equal(backups[j].EndedAt) {
			return backups[i].ID < backups[j].ID
		}
		return backups[i].EndedAt.After(backups[j].EndedAt)
	})
	return backups, nil
}

// Protect toggles a backup's manual protection flag.
func (s *BackupStore) Protect(id core.ID, protected bool) (core.Backup, error) {
	backup, ok, err := s.Get(id)
	if err != nil {
		return core.Backup{}, err
	}
	if !ok {
		return core.Backup{}, core.WrapKind(core.ErrorKindNotFound, "protect backup", fmt.Errorf("backup %q not found", id))
	}
	backup.Protected = protected
	if err := s.Save(backup); err != nil {
		return core.Backup{}, err
	}
	return backup, nil
}

// Delete removes a backup metadata record by ID.
func (s *BackupStore) Delete(id core.ID) error {
	return deleteKey(s.db, backupsBucket, []byte(id))
}

// Save inserts or replaces a user.
func (s *UserStore) Save(user core.User) error {
	if err := validateUser(user); err != nil {
		return err
	}
	return saveJSON(s.db, usersBucket, []byte(user.ID), user)
}

// Get fetches one user by ID.
func (s *UserStore) Get(id core.ID) (core.User, bool, error) {
	var user core.User
	ok, err := getJSON(s.db, usersBucket, []byte(id), &user)
	return user, ok, err
}

// List returns all users ordered by email, then ID.
func (s *UserStore) List() ([]core.User, error) {
	var users []core.User
	if err := listJSON(s.db, usersBucket, func(data []byte) error {
		var user core.User
		if err := json.Unmarshal(data, &user); err != nil {
			return err
		}
		users = append(users, user)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(users, func(i, j int) bool {
		if users[i].Email == users[j].Email {
			return users[i].ID < users[j].ID
		}
		return users[i].Email < users[j].Email
	})
	return users, nil
}

// Grant changes a user's built-in role.
func (s *UserStore) Grant(id core.ID, role core.RoleName, now time.Time) (core.User, error) {
	if !validRole(role) {
		return core.User{}, fmt.Errorf("invalid role %q", role)
	}
	user, ok, err := s.Get(id)
	if err != nil {
		return core.User{}, err
	}
	if !ok {
		return core.User{}, core.WrapKind(core.ErrorKindNotFound, "grant user role", fmt.Errorf("user %q not found", id))
	}
	user.Role = role
	if now.IsZero() {
		now = time.Now().UTC()
	}
	user.UpdatedAt = now.UTC()
	if err := s.Save(user); err != nil {
		return core.User{}, err
	}
	return user, nil
}

// Delete removes a user by ID.
func (s *UserStore) Delete(id core.ID) error {
	return deleteKey(s.db, usersBucket, []byte(id))
}

// Save inserts or replaces a retention policy.
func (s *RetentionPolicyStore) Save(policy core.RetentionPolicy) error {
	if err := validateRetentionPolicy(policy); err != nil {
		return err
	}
	return saveJSON(s.db, policiesBucket, []byte(policy.ID), policy)
}

// Get fetches one retention policy by ID.
func (s *RetentionPolicyStore) Get(id core.ID) (core.RetentionPolicy, bool, error) {
	var policy core.RetentionPolicy
	ok, err := getJSON(s.db, policiesBucket, []byte(id), &policy)
	return policy, ok, err
}

// List returns all retention policies ordered by name, then ID.
func (s *RetentionPolicyStore) List() ([]core.RetentionPolicy, error) {
	var policies []core.RetentionPolicy
	if err := listJSON(s.db, policiesBucket, func(data []byte) error {
		var policy core.RetentionPolicy
		if err := json.Unmarshal(data, &policy); err != nil {
			return err
		}
		policies = append(policies, policy)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(policies, func(i, j int) bool {
		if policies[i].Name == policies[j].Name {
			return policies[i].ID < policies[j].ID
		}
		return policies[i].Name < policies[j].Name
	})
	return policies, nil
}

// Delete removes a retention policy by ID.
func (s *RetentionPolicyStore) Delete(id core.ID) error {
	return deleteKey(s.db, policiesBucket, []byte(id))
}

func validateTarget(target core.Target) error {
	if target.ID.IsZero() {
		return fmt.Errorf("target id is required")
	}
	if err := validateResourceID("target id", target.ID); err != nil {
		return err
	}
	if target.Name == "" {
		return fmt.Errorf("target name is required")
	}
	if target.Driver == "" {
		return fmt.Errorf("target driver is required")
	}
	if !validTargetDriver(target.Driver) {
		return fmt.Errorf("target driver %q is not supported", target.Driver)
	}
	if target.Endpoint == "" {
		return fmt.Errorf("target endpoint is required")
	}
	if hasControlChars(target.Endpoint) {
		return fmt.Errorf("target endpoint contains control characters")
	}
	if err := validateOptions("target", target.Options, targetOptionKeys); err != nil {
		return err
	}
	return nil
}

func validateStorage(storage core.Storage) error {
	if storage.ID.IsZero() {
		return fmt.Errorf("storage id is required")
	}
	if err := validateResourceID("storage id", storage.ID); err != nil {
		return err
	}
	if storage.Name == "" {
		return fmt.Errorf("storage name is required")
	}
	if storage.Kind == "" {
		return fmt.Errorf("storage kind is required")
	}
	if !validStorageKind(storage.Kind) {
		return fmt.Errorf("storage kind %q is not supported", storage.Kind)
	}
	if storage.URI == "" {
		return fmt.Errorf("storage uri is required")
	}
	if err := validateStorageURI(storage.Kind, storage.URI); err != nil {
		return err
	}
	if err := validateOptions("storage", storage.Options, storageOptionKeys); err != nil {
		return err
	}
	return nil
}

func validateSchedule(schedule core.Schedule) error {
	if schedule.ID.IsZero() {
		return fmt.Errorf("schedule id is required")
	}
	if err := validateResourceID("schedule id", schedule.ID); err != nil {
		return err
	}
	if schedule.Name == "" {
		return fmt.Errorf("schedule name is required")
	}
	if schedule.TargetID.IsZero() {
		return fmt.Errorf("schedule target id is required")
	}
	if err := validateResourceID("schedule target id", schedule.TargetID); err != nil {
		return err
	}
	if schedule.StorageID.IsZero() {
		return fmt.Errorf("schedule storage id is required")
	}
	if err := validateResourceID("schedule storage id", schedule.StorageID); err != nil {
		return err
	}
	if schedule.BackupType == "" {
		return fmt.Errorf("schedule backup type is required")
	}
	if !validBackupType(schedule.BackupType) {
		return fmt.Errorf("schedule backup type %q is not supported", schedule.BackupType)
	}
	if schedule.Expression == "" {
		return fmt.Errorf("schedule expression is required")
	}
	if _, err := sched.ParseCron(schedule.Expression); err != nil {
		if _, windowErr := sched.ParseWindow(schedule.Expression); windowErr != nil {
			return fmt.Errorf("schedule expression: %w", err)
		}
	}
	return nil
}

func validateBackup(backup core.Backup) error {
	if backup.ID.IsZero() {
		return fmt.Errorf("backup id is required")
	}
	if err := validateResourceID("backup id", backup.ID); err != nil {
		return err
	}
	if backup.TargetID.IsZero() {
		return fmt.Errorf("backup target id is required")
	}
	if err := validateResourceID("backup target id", backup.TargetID); err != nil {
		return err
	}
	if backup.StorageID.IsZero() {
		return fmt.Errorf("backup storage id is required")
	}
	if err := validateResourceID("backup storage id", backup.StorageID); err != nil {
		return err
	}
	if backup.JobID.IsZero() {
		return fmt.Errorf("backup job id is required")
	}
	if err := validateResourceID("backup job id", backup.JobID); err != nil {
		return err
	}
	if backup.Type == "" {
		return fmt.Errorf("backup type is required")
	}
	if !validBackupType(backup.Type) {
		return fmt.Errorf("backup type %q is not supported", backup.Type)
	}
	if backup.ManifestID.IsZero() {
		return fmt.Errorf("backup manifest id is required")
	}
	if err := validateResourceID("backup manifest id", backup.ManifestID); err != nil {
		return err
	}
	if backup.EndedAt.IsZero() {
		return fmt.Errorf("backup ended_at is required")
	}
	return nil
}

func validateUser(user core.User) error {
	if user.ID.IsZero() {
		return fmt.Errorf("user id is required")
	}
	if err := validateResourceID("user id", user.ID); err != nil {
		return err
	}
	if user.Email == "" {
		return fmt.Errorf("user email is required")
	}
	if user.DisplayName == "" {
		return fmt.Errorf("user display name is required")
	}
	if !validRole(user.Role) {
		return fmt.Errorf("invalid user role %q", user.Role)
	}
	if user.TOTPEnforced {
		return fmt.Errorf("user totp_enforced is not supported by token-only auth")
	}
	return nil
}

func validateRetentionPolicy(policy core.RetentionPolicy) error {
	if policy.ID.IsZero() {
		return fmt.Errorf("retention policy id is required")
	}
	if err := validateResourceID("retention policy id", policy.ID); err != nil {
		return err
	}
	if policy.Name == "" {
		return fmt.Errorf("retention policy name is required")
	}
	if len(policy.Rules) == 0 {
		return fmt.Errorf("retention policy rules are required")
	}
	for i, rule := range policy.Rules {
		if rule.Kind == "" {
			return fmt.Errorf("retention policy rule %d kind is required", i)
		}
		if !validRetentionRuleKind(rule.Kind) {
			return fmt.Errorf("retention policy rule %d kind %q is not supported", i, rule.Kind)
		}
		if err := validateOptionValues(fmt.Sprintf("retention policy rule %d", i), rule.Params); err != nil {
			return err
		}
	}
	return nil
}

func validateResourceID(label string, id core.ID) error {
	value := strings.TrimSpace(id.String())
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	if value != id.String() {
		return fmt.Errorf("%s must not contain leading or trailing whitespace", label)
	}
	if len(value) > 128 {
		return fmt.Errorf("%s must be at most 128 characters", label)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '-', '_', '.', ':', '/':
			continue
		default:
			return fmt.Errorf("%s contains invalid character %q", label, r)
		}
	}
	return nil
}

func validTargetDriver(driver core.TargetDriver) bool {
	switch driver {
	case core.TargetDriverPostgres, core.TargetDriverMySQL, core.TargetDriverMongoDB, core.TargetDriverRedis:
		return true
	default:
		return false
	}
}

func validStorageKind(kind core.StorageKind) bool {
	switch kind {
	case core.StorageKindLocal, core.StorageKindS3, core.StorageKindSFTP, core.StorageKindAzure, core.StorageKindGCS:
		return true
	default:
		return false
	}
}

func validBackupType(backupType core.BackupType) bool {
	switch backupType {
	case core.BackupTypeFull, core.BackupTypeIncremental, core.BackupTypeDifferential, core.BackupTypeStream, core.BackupTypeSchema:
		return true
	default:
		return false
	}
}

func validRetentionRuleKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "count", "time", "size", "gfs":
		return true
	default:
		return false
	}
}

func validateStorageURI(kind core.StorageKind, raw string) error {
	if hasControlChars(raw) {
		return fmt.Errorf("storage uri contains control characters")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("storage uri: %w", err)
	}
	switch kind {
	case core.StorageKindLocal:
		if parsed.Scheme == "" {
			return nil
		}
		if parsed.Scheme != "file" {
			return fmt.Errorf("local storage uri must use file scheme")
		}
		if parsed.Host != "" {
			return fmt.Errorf("local storage uri must not include a host")
		}
		if parsed.Path == "" {
			return fmt.Errorf("local storage uri path is required")
		}
	case core.StorageKindS3:
		if parsed.Scheme != "s3" {
			return fmt.Errorf("s3 storage uri must use s3 scheme")
		}
		if parsed.Host == "" {
			return fmt.Errorf("s3 storage uri bucket is required")
		}
	case core.StorageKindSFTP:
		if parsed.Scheme != "sftp" {
			return fmt.Errorf("sftp storage uri must use sftp scheme")
		}
		if parsed.Host == "" {
			return fmt.Errorf("sftp storage uri host is required")
		}
	case core.StorageKindAzure:
		if parsed.Scheme != "azure" && parsed.Scheme != "azblob" {
			return fmt.Errorf("azure storage uri must use azure or azblob scheme")
		}
		if parsed.Host == "" {
			return fmt.Errorf("azure storage uri account is required")
		}
		if strings.Trim(parsed.Path, "/") == "" {
			return fmt.Errorf("azure storage uri container is required")
		}
	case core.StorageKindGCS:
		if parsed.Scheme != "gcs" && parsed.Scheme != "gs" {
			return fmt.Errorf("gcs storage uri must use gcs or gs scheme")
		}
		if parsed.Host == "" {
			return fmt.Errorf("gcs storage uri bucket is required")
		}
	}
	return nil
}

var targetOptionKeys = map[string]struct{}{
	"authSource": {}, "auth_source": {}, "connection_test_collection": {}, "database": {}, "dsn": {},
	"dump_set_gtid_purged": {}, "globals": {}, "host": {}, "includeGlobals": {}, "include_globals": {},
	"native": {}, "native_protocol": {}, "password": {}, "port": {}, "protocol": {}, "set_gtid_purged": {}, "ssl": {}, "sslmode": {}, "tls": {}, "uri": {},
	"user": {}, "username": {},
}

var storageOptionKeys = map[string]struct{}{
	"accessKey": {}, "access_key": {}, "aws_access_key_id": {}, "aws_secret_access_key": {},
	"account": {}, "accountKey": {}, "accountName": {}, "account_key": {}, "account_name": {},
	"accessToken": {}, "access_token": {}, "apiKey": {}, "api_key": {}, "aws_session_token": {},
	"bearerToken": {}, "bearer_token": {}, "bucket": {}, "container": {}, "credentials": {}, "credentials_provider": {}, "endpoint": {},
	"force_path_style": {}, "imds_endpoint": {}, "region": {}, "secretKey": {}, "secret_key": {},
	"sas": {}, "sasToken": {}, "sas_token": {}, "sessionToken": {}, "session_token": {},
	"agent_socket": {}, "insecure_ignore_host_key": {}, "knownHosts": {}, "known_hosts": {},
	"passphrase": {}, "password": {}, "privateKey": {}, "privateKeyPassphrase": {},
	"private_key": {}, "private_key_passphrase": {}, "private_key_path": {}, "ssh_agent_socket": {},
	"prefix": {}, "user": {}, "username": {},
}

func validateOptions(label string, options map[string]any, allowed map[string]struct{}) error {
	for key := range options {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%s option %q is not supported", label, key)
		}
	}
	return validateOptionValues(label+" option", options)
}

func validateOptionValues(label string, options map[string]any) error {
	for key, value := range options {
		switch value.(type) {
		case nil, string, bool, int, int64, float64, json.Number:
		default:
			return fmt.Errorf("%s %q must be a scalar value", label, key)
		}
	}
	return nil
}

func hasControlChars(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func validRole(role core.RoleName) bool {
	switch role {
	case core.RoleAdmin, core.RoleOperator, core.RoleViewer:
		return true
	default:
		return false
	}
}

func saveJSON(db *kvstore.DB, bucketName []byte, key []byte, value any) error {
	if db == nil {
		return fmt.Errorf("kv database is required")
	}
	if len(key) == 0 {
		return fmt.Errorf("key is required")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return db.Update(func(tx *kvstore.Tx) error {
		bucket, err := tx.Bucket(bucketName)
		if err != nil {
			return err
		}
		return bucket.Put(key, data)
	})
}

func getJSON(db *kvstore.DB, bucketName []byte, key []byte, value any) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("kv database is required")
	}
	if len(key) == 0 {
		return false, fmt.Errorf("key is required")
	}
	var ok bool
	err := db.View(func(tx *kvstore.Tx) error {
		bucket, err := tx.Bucket(bucketName)
		if err != nil {
			return err
		}
		data, exists, err := bucket.Get(key)
		if err != nil || !exists {
			ok = exists
			return err
		}
		ok = true
		return json.Unmarshal(data, value)
	})
	return ok, err
}

func listJSON(db *kvstore.DB, bucketName []byte, fn func([]byte) error) error {
	if db == nil {
		return fmt.Errorf("kv database is required")
	}
	return db.View(func(tx *kvstore.Tx) error {
		bucket, err := tx.Bucket(bucketName)
		if err != nil {
			return err
		}
		it, err := bucket.Scan([]byte{1}, nil)
		if err != nil {
			return err
		}
		for it.Valid() {
			if err := fn(it.Value()); err != nil {
				return err
			}
			it.Next()
		}
		return it.Err()
	})
}

func deleteKey(db *kvstore.DB, bucketName []byte, key []byte) error {
	if db == nil {
		return fmt.Errorf("kv database is required")
	}
	if len(key) == 0 {
		return fmt.Errorf("key is required")
	}
	return db.Update(func(tx *kvstore.Tx) error {
		bucket, err := tx.Bucket(bucketName)
		if err != nil {
			return err
		}
		return bucket.Delete(key)
	})
}

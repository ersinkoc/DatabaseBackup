//go:build integration

package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kronos/kronos/internal/drivers"
)

func TestPostgresDriverConformanceBackupRestore(t *testing.T) {
	sourceDSN := strings.TrimSpace(os.Getenv("KRONOS_POSTGRES_TEST_DSN"))
	if sourceDSN == "" {
		t.Skip("KRONOS_POSTGRES_TEST_DSN is not set")
	}
	requireCommand(t, "pg_dump")
	requireCommand(t, "pg_dumpall")
	requireCommand(t, "psql")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	suffix := randomHex(t, 4)
	sourceSchema := "kronos_src_" + suffix
	restoreSchema := "kronos_restore_" + suffix
	failureSchema := "kronos_fail_" + suffix
	roleName := "kronos_role_" + suffix
	restoreDSN := firstNonEmpty(strings.TrimSpace(os.Getenv("KRONOS_POSTGRES_RESTORE_DSN")), sourceDSN)
	cleanupSchema(t, ctx, sourceDSN, sourceSchema)
	cleanupSchema(t, ctx, restoreDSN, restoreSchema)
	cleanupSchema(t, ctx, restoreDSN, failureSchema)
	cleanupRole(t, ctx, sourceDSN, roleName)
	defer cleanupSchema(t, context.Background(), sourceDSN, sourceSchema)
	defer cleanupSchema(t, context.Background(), restoreDSN, restoreSchema)
	defer cleanupSchema(t, context.Background(), restoreDSN, failureSchema)
	defer cleanupRole(t, context.Background(), sourceDSN, roleName)

	seedSQL := fmt.Sprintf(`
create extension if not exists pgcrypto;
create schema %s;
create table %s.users(id integer primary key, name text not null);
create table %s.documents(id integer primary key, public_id uuid not null default gen_random_uuid(), payload_oid oid not null);
create table %s.bulk_items(id integer primary key, label text not null, payload jsonb not null, created_at timestamptz not null default now());
insert into %s.users(id, name) values (1, 'Ada'), (2, 'Grace');
insert into %s.documents(id, payload_oid) values (1, lo_from_bytea(0, convert_to('kronos-large-object-%s', 'UTF8')));
insert into %s.bulk_items(id, label, payload, created_at)
select g, 'item-' || g, jsonb_build_object('rank', g, 'bucket', g %% 17, 'tag', 'kronos-%s'), '2026-04-27T00:00:00Z'::timestamptz + (g || ' seconds')::interval
from generate_series(1, 2500) as g;
create index bulk_items_label_idx on %s.bulk_items(label);
`, sourceSchema, sourceSchema, sourceSchema, sourceSchema, sourceSchema, sourceSchema, suffix, sourceSchema, suffix, sourceSchema)
	runPSQL(t, ctx, sourceDSN, seedSQL)
	runPSQL(t, ctx, sourceDSN, fmt.Sprintf("create role %s login password 'kronos-secret-%s';", roleName, suffix))

	driver := NewDriver()
	var backup drivers.MemoryRecordStream
	rp, err := driver.BackupFull(ctx, drivers.Target{
		Connection: map[string]string{"dsn": sourceDSN},
		Options:    map[string]string{"include_globals": "true"},
	}, &backup)
	if err != nil {
		t.Fatalf("BackupFull() error = %v", err)
	}
	records := backup.Records()
	if rp.Position != "pg_dumpall:globals+pg_dump:plain" {
		t.Fatalf("ResumePoint.Position = %q", rp.Position)
	}
	if len(records) < 3 || records[0].Object.Kind != globalsObjectKind || records[2].Object.Kind != databaseObjectKind {
		t.Fatalf("backup records do not include globals then database streams: %#v", records)
	}
	globalsSQL := string(records[0].Payload)
	if !strings.Contains(globalsSQL, roleName) {
		t.Fatalf("globals backup does not contain role %q", roleName)
	}
	if strings.Contains(strings.ToLower(globalsSQL), "kronos-secret") || strings.Contains(strings.ToLower(globalsSQL), "password '") {
		t.Fatalf("globals backup leaked role password material")
	}
	if !strings.Contains(string(records[2].Payload), sourceSchema) || !strings.Contains(string(records[2].Payload), "Ada") {
		t.Fatalf("backup records do not contain expected source data: %#v", records)
	}
	runPSQL(t, ctx, sourceDSN, fmt.Sprintf("select lo_unlink(payload_oid) from %s.documents; drop schema %s cascade;", sourceSchema, sourceSchema))

	var restore drivers.MemoryRecordStream
	rewrite := strings.ReplaceAll(string(records[2].Payload), sourceSchema, restoreSchema)
	if err := restore.WriteRecord(drivers.ObjectRef{Name: restoreSchema, Kind: databaseObjectKind}, []byte(rewrite)); err != nil {
		t.Fatalf("WriteRecord(restore) error = %v", err)
	}
	if err := driver.Restore(ctx, drivers.Target{Connection: map[string]string{"dsn": restoreDSN}}, &restore, drivers.RestoreOptions{ReplaceExisting: true}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	count := queryScalar(t, ctx, restoreDSN, fmt.Sprintf("select count(*) from %s.users;", restoreSchema))
	if count != "2" {
		t.Fatalf("restored row count = %q, want 2", count)
	}
	name := queryScalar(t, ctx, restoreDSN, fmt.Sprintf("select name from %s.users where id = 1;", restoreSchema))
	if name != "Ada" {
		t.Fatalf("restored id=1 name = %q, want Ada", name)
	}
	largeObject := queryScalar(t, ctx, restoreDSN, fmt.Sprintf("select convert_from(lo_get(payload_oid), 'UTF8') from %s.documents where id = 1;", restoreSchema))
	if largeObject != "kronos-large-object-"+suffix {
		t.Fatalf("restored large object = %q, want %q", largeObject, "kronos-large-object-"+suffix)
	}
	publicID := queryScalar(t, ctx, restoreDSN, fmt.Sprintf("select public_id::text <> '' from %s.documents where id = 1;", restoreSchema))
	if publicID != "t" {
		t.Fatalf("restored extension-backed uuid presence = %q, want t", publicID)
	}
	bulkCount := queryScalar(t, ctx, restoreDSN, fmt.Sprintf("select count(*) from %s.bulk_items;", restoreSchema))
	if bulkCount != "2500" {
		t.Fatalf("restored bulk row count = %q, want 2500", bulkCount)
	}
	bulkChecksum := queryScalar(t, ctx, restoreDSN, fmt.Sprintf("select sum(id)::text || ':' || sum((payload->>'rank')::integer)::text from %s.bulk_items;", restoreSchema))
	if bulkChecksum != "3126250:3126250" {
		t.Fatalf("restored bulk checksum = %q, want 3126250:3126250", bulkChecksum)
	}
	bulkIndexPresent := queryScalar(t, ctx, restoreDSN, fmt.Sprintf("select to_regclass('%s.bulk_items_label_idx') is not null;", restoreSchema))
	if bulkIndexPresent != "t" {
		t.Fatalf("restored bulk index presence = %q, want t", bulkIndexPresent)
	}

	var failedRestore drivers.MemoryRecordStream
	failingSQL := fmt.Sprintf(`
create schema %s;
create table %s.created_before_error(id integer primary key);
select kronos_missing_restore_function();
`, failureSchema, failureSchema)
	if err := failedRestore.WriteRecord(drivers.ObjectRef{Name: failureSchema, Kind: databaseObjectKind}, []byte(failingSQL)); err != nil {
		t.Fatalf("WriteRecord(failed restore) error = %v", err)
	}
	if err := driver.Restore(ctx, drivers.Target{Connection: map[string]string{"dsn": restoreDSN}}, &failedRestore, drivers.RestoreOptions{ReplaceExisting: true}); err == nil {
		t.Fatal("Restore(failing SQL) error = nil, want error")
	}
	rolledBack := queryScalar(t, ctx, restoreDSN, fmt.Sprintf("select to_regclass('%s.created_before_error') is null;", failureSchema))
	if rolledBack != "t" {
		t.Fatalf("failing restore rollback = %q, want t", rolledBack)
	}
}

func requireCommand(t *testing.T, name string) {
	t.Helper()

	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not installed: %v", name, err)
	}
}

func randomHex(t *testing.T, n int) string {
	t.Helper()

	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand.Read() error = %v", err)
	}
	return hex.EncodeToString(buf)
}

func cleanupSchema(t *testing.T, ctx context.Context, dsn string, schema string) {
	t.Helper()

	cmd := exec.CommandContext(ctx, "psql", "--set", "ON_ERROR_STOP=1", "--dbname", dsn, "--command", fmt.Sprintf("drop schema if exists %s cascade;", schema))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cleanup schema %s error = %v output=%s", schema, err, strings.TrimSpace(string(out)))
	}
}

func cleanupRole(t *testing.T, ctx context.Context, dsn string, roleName string) {
	t.Helper()

	cmd := exec.CommandContext(ctx, "psql", "--set", "ON_ERROR_STOP=1", "--dbname", dsn, "--command", fmt.Sprintf("drop role if exists %s;", roleName))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cleanup role %s error = %v output=%s", roleName, err, strings.TrimSpace(string(out)))
	}
}

func runPSQL(t *testing.T, ctx context.Context, dsn string, sql string) {
	t.Helper()

	cmd := exec.CommandContext(ctx, "psql", "--set", "ON_ERROR_STOP=1", "--dbname", dsn)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("psql error = %v output=%s", err, strings.TrimSpace(string(out)))
	}
}

func queryScalar(t *testing.T, ctx context.Context, dsn string, sql string) string {
	t.Helper()

	cmd := exec.CommandContext(ctx, "psql", "--no-align", "--tuples-only", "--set", "ON_ERROR_STOP=1", "--dbname", dsn, "--command", sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("query %q error = %v output=%s", sql, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

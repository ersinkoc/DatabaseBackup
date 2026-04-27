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
	requireCommand(t, "psql")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	suffix := randomHex(t, 4)
	sourceSchema := "kronos_src_" + suffix
	restoreSchema := "kronos_restore_" + suffix
	restoreDSN := firstNonEmpty(strings.TrimSpace(os.Getenv("KRONOS_POSTGRES_RESTORE_DSN")), sourceDSN)
	cleanupSchema(t, ctx, sourceDSN, sourceSchema)
	cleanupSchema(t, ctx, restoreDSN, restoreSchema)
	defer cleanupSchema(t, context.Background(), sourceDSN, sourceSchema)
	defer cleanupSchema(t, context.Background(), restoreDSN, restoreSchema)

	seedSQL := fmt.Sprintf(`
create schema %s;
create table %s.users(id integer primary key, name text not null);
insert into %s.users(id, name) values (1, 'Ada'), (2, 'Grace');
`, sourceSchema, sourceSchema, sourceSchema)
	runPSQL(t, ctx, sourceDSN, seedSQL)

	driver := NewDriver()
	var backup drivers.MemoryRecordStream
	_, err := driver.BackupFull(ctx, drivers.Target{Connection: map[string]string{"dsn": sourceDSN}}, &backup)
	if err != nil {
		t.Fatalf("BackupFull() error = %v", err)
	}
	records := backup.Records()
	if len(records) == 0 || !strings.Contains(string(records[0].Payload), sourceSchema) || !strings.Contains(string(records[0].Payload), "Ada") {
		t.Fatalf("backup records do not contain expected source data: %#v", records)
	}

	var restore drivers.MemoryRecordStream
	rewrite := strings.ReplaceAll(string(records[0].Payload), sourceSchema, restoreSchema)
	if err := restore.WriteRecord(drivers.ObjectRef{Name: restoreSchema, Kind: databaseObjectKind}, []byte(rewrite)); err != nil {
		t.Fatalf("WriteRecord(restore) error = %v", err)
	}
	if err := driver.Restore(ctx, drivers.Target{Connection: map[string]string{"dsn": restoreDSN}}, &restore, drivers.RestoreOptions{}); err != nil {
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

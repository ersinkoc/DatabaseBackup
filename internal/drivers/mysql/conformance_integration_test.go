//go:build integration

package mysql

import (
	"bytes"
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

func TestMySQLDriverConformanceBackupRestore(t *testing.T) {
	sourceAddr := strings.TrimSpace(os.Getenv("KRONOS_MYSQL_TEST_ADDR"))
	if sourceAddr == "" {
		t.Skip("KRONOS_MYSQL_TEST_ADDR is not set")
	}
	requireCommand(t, "mysql")
	requireCommand(t, "mysqldump")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	suffix := randomHex(t, 4)
	sourceDB := "kronos_src_" + suffix
	restoreDB := "kronos_restore_" + suffix
	restoreAddr := firstNonEmpty(strings.TrimSpace(os.Getenv("KRONOS_MYSQL_RESTORE_ADDR")), sourceAddr)
	sourceUser := firstNonEmpty(strings.TrimSpace(os.Getenv("KRONOS_MYSQL_TEST_USER")), "root")
	sourcePassword := os.Getenv("KRONOS_MYSQL_TEST_PASSWORD")
	restoreUser := firstNonEmpty(strings.TrimSpace(os.Getenv("KRONOS_MYSQL_RESTORE_USER")), sourceUser)
	restorePassword := firstNonEmpty(os.Getenv("KRONOS_MYSQL_RESTORE_PASSWORD"), sourcePassword)
	sourceAdmin := mysqlTestTarget(sourceAddr, "", sourceUser, sourcePassword)
	restoreAdmin := mysqlTestTarget(restoreAddr, "", restoreUser, restorePassword)
	sourceTarget := mysqlTestTarget(sourceAddr, sourceDB, sourceUser, sourcePassword)
	restoreTarget := mysqlTestTarget(restoreAddr, restoreDB, restoreUser, restorePassword)
	if value := strings.TrimSpace(os.Getenv("KRONOS_MYSQL_DUMP_SET_GTID_PURGED")); value != "" {
		sourceTarget.Options = map[string]string{"set_gtid_purged": value}
	}

	cleanupDatabase(t, ctx, sourceAdmin, sourceDB)
	cleanupDatabase(t, ctx, restoreAdmin, restoreDB)
	defer cleanupDatabase(t, context.Background(), sourceAdmin, sourceDB)
	defer cleanupDatabase(t, context.Background(), restoreAdmin, restoreDB)

	runMySQL(t, ctx, sourceAdmin, fmt.Sprintf("create database %s;", sourceDB))
	runMySQL(t, ctx, sourceTarget, mysqlSeedSQL(suffix))

	driver := NewDriver()
	var backup drivers.MemoryRecordStream
	rp, err := driver.BackupFull(ctx, sourceTarget, &backup)
	if err != nil {
		t.Fatalf("BackupFull() error = %v", err)
	}
	records := backup.Records()
	if rp.Position != "mysqldump:single-transaction" {
		t.Fatalf("ResumePoint.Position = %q", rp.Position)
	}
	if len(records) < 2 || records[0].Object.Kind != databaseObjectKind || records[0].Object.Name != sourceDB {
		t.Fatalf("backup records do not include database stream: %#v", records)
	}
	dump := string(records[0].Payload)
	if !strings.Contains(dump, "bulk_items") || !strings.Contains(dump, "Ada") || strings.Contains(dump, "CREATE DATABASE") {
		t.Fatalf("backup payload does not look like a portable single-database dump")
	}

	cleanupDatabase(t, ctx, sourceAdmin, sourceDB)
	runMySQL(t, ctx, restoreAdmin, fmt.Sprintf("create database %s;", restoreDB))
	var restore drivers.MemoryRecordStream
	if err := restore.WriteRecord(drivers.ObjectRef{Name: restoreDB, Kind: databaseObjectKind}, records[0].Payload); err != nil {
		t.Fatalf("WriteRecord(restore) error = %v", err)
	}
	if err := driver.Restore(ctx, restoreTarget, &restore, drivers.RestoreOptions{ReplaceExisting: true}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	if got := queryMySQLScalar(t, ctx, restoreTarget, "select count(*) from users;"); got != "2" {
		t.Fatalf("restored users count = %q, want 2", got)
	}
	if got := queryMySQLScalar(t, ctx, restoreTarget, "select name from users where id = 1;"); got != "Ada" {
		t.Fatalf("restored id=1 name = %q, want Ada", got)
	}
	if got := queryMySQLScalar(t, ctx, restoreTarget, "select count(*) from bulk_items;"); got != "500" {
		t.Fatalf("restored bulk count = %q, want 500", got)
	}
	if got := queryMySQLScalar(t, ctx, restoreTarget, "select concat(sum(id), ':', sum(cast(json_unquote(json_extract(payload, '$.rank')) as unsigned))) from bulk_items;"); got != "125250:125250" {
		t.Fatalf("restored bulk checksum = %q, want 125250:125250", got)
	}
	indexSQL := fmt.Sprintf("select count(*) from information_schema.statistics where table_schema = '%s' and table_name = 'bulk_items' and index_name = 'bulk_items_label_idx';", restoreDB)
	if got := queryMySQLScalar(t, ctx, restoreTarget, indexSQL); got != "1" {
		t.Fatalf("restored bulk index presence = %q, want 1", got)
	}
}

func mysqlSeedSQL(suffix string) string {
	var b strings.Builder
	b.WriteString(`
create table users(id integer primary key, name varchar(64) not null);
create table bulk_items(
  id integer primary key,
  label varchar(64) not null,
  payload json not null,
  created_at timestamp not null
);
insert into users(id, name) values (1, 'Ada'), (2, 'Grace');
insert into bulk_items(id, label, payload, created_at) values
`)
	for i := 1; i <= 500; i++ {
		if i > 1 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, "(%d, 'item-%d', json_object('rank', %d, 'bucket', %d, 'tag', 'kronos-%s'), timestamp('2026-04-27 00:00:00') + interval %d second)", i, i, i, i%17, suffix, i)
	}
	b.WriteString(";\ncreate index bulk_items_label_idx on bulk_items(label);\n")
	return b.String()
}

func mysqlTestTarget(addr string, database string, username string, password string) drivers.Target {
	connection := map[string]string{
		"addr":     addr,
		"username": username,
	}
	if database != "" {
		connection["database"] = database
	}
	if password != "" {
		connection["password"] = password
	}
	return drivers.Target{Connection: connection}
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

func cleanupDatabase(t *testing.T, ctx context.Context, target drivers.Target, database string) {
	t.Helper()

	runMySQL(t, ctx, target, fmt.Sprintf("drop database if exists %s;", database))
}

func runMySQL(t *testing.T, ctx context.Context, target drivers.Target, sql string) {
	t.Helper()

	args := mysqlTestArgs(target)
	cmd := exec.CommandContext(ctx, "mysql", args...)
	cmd.Stdin = strings.NewReader(sql)
	if env := mysqlEnv(target); len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("mysql error = %v output=%s stderr=%s", err, strings.TrimSpace(string(out)), strings.TrimSpace(stderr.String()))
	}
}

func queryMySQLScalar(t *testing.T, ctx context.Context, target drivers.Target, sql string) string {
	t.Helper()

	args := append(mysqlTestArgs(target), "--batch", "--raw", "--skip-column-names", "--execute", sql)
	cmd := exec.CommandContext(ctx, "mysql", args...)
	if env := mysqlEnv(target); len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("query %q error = %v output=%s stderr=%s", sql, err, strings.TrimSpace(string(out)), strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out))
}

func mysqlTestArgs(target drivers.Target) []string {
	args := mysqlConnectionArgs(target)
	if database := strings.TrimSpace(target.Connection["database"]); database != "" {
		args = append(args, "--database", database)
	}
	return args
}

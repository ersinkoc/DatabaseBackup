//go:build integration

package mongodb

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kronos/kronos/internal/drivers"
)

func TestMongoDBDriverConformanceBackupRestore(t *testing.T) {
	sourceAddr := strings.TrimSpace(os.Getenv("KRONOS_MONGODB_TEST_ADDR"))
	if sourceAddr == "" {
		t.Skip("KRONOS_MONGODB_TEST_ADDR is not set")
	}
	requireCommand(t, "mongodump")
	requireCommand(t, "mongorestore")
	requireCommand(t, "mongosh")

	ctx, cancel := context.WithTimeout(context.Background(), mongoTestTimeout(t))
	defer cancel()

	suffix := randomHex(t, 4)
	bulkRows := mongoBulkRows(t)
	sourceDB := "kronos_src_" + suffix
	restoreDB := "kronos_restore_" + suffix
	restoreAddr := firstNonEmpty(strings.TrimSpace(os.Getenv("KRONOS_MONGODB_RESTORE_ADDR")), sourceAddr)
	sourceUser := strings.TrimSpace(os.Getenv("KRONOS_MONGODB_TEST_USER"))
	sourcePassword := os.Getenv("KRONOS_MONGODB_TEST_PASSWORD")
	sourceAuthSource := firstNonEmpty(strings.TrimSpace(os.Getenv("KRONOS_MONGODB_TEST_AUTH_SOURCE")), "admin")
	restoreUser := firstNonEmpty(strings.TrimSpace(os.Getenv("KRONOS_MONGODB_RESTORE_USER")), sourceUser)
	restorePassword := firstNonEmpty(os.Getenv("KRONOS_MONGODB_RESTORE_PASSWORD"), sourcePassword)
	restoreAuthSource := firstNonEmpty(strings.TrimSpace(os.Getenv("KRONOS_MONGODB_RESTORE_AUTH_SOURCE")), sourceAuthSource)
	sourceAdmin := mongoTestTarget(sourceAddr, "admin", sourceUser, sourcePassword, sourceAuthSource)
	restoreAdmin := mongoTestTarget(restoreAddr, "admin", restoreUser, restorePassword, restoreAuthSource)
	sourceTarget := mongoTestTarget(sourceAddr, sourceDB, sourceUser, sourcePassword, sourceAuthSource)
	restoreTarget := mongoTestTarget(restoreAddr, restoreDB, restoreUser, restorePassword, restoreAuthSource)
	restoreCommandTarget := mongoRestoreCommandTarget(restoreAddr, restoreDB, restoreUser, restorePassword, restoreAuthSource)
	sourceTarget.Options = map[string]string{"connection_test_collection": "users"}

	cleanupMongoDatabase(t, ctx, sourceAdmin, sourceDB)
	cleanupMongoDatabase(t, ctx, restoreAdmin, restoreDB)
	defer cleanupMongoDatabase(t, context.Background(), sourceAdmin, sourceDB)
	defer cleanupMongoDatabase(t, context.Background(), restoreAdmin, restoreDB)

	runMongoShell(t, ctx, sourceTarget, mongoSeedScript(suffix, bulkRows))

	driver := NewDriver()
	if err := driver.Test(ctx, sourceTarget); err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	var backup drivers.MemoryRecordStream
	rp, err := driver.BackupFull(ctx, sourceTarget, &backup)
	if err != nil {
		t.Fatalf("BackupFull() error = %v", err)
	}
	records := backup.Records()
	if rp.Position != "mongodump:archive" {
		t.Fatalf("ResumePoint.Position = %q", rp.Position)
	}
	if len(records) < 2 || records[0].Object.Kind != databaseObjectKind || records[0].Object.Name != sourceDB {
		t.Fatalf("backup records do not include database archive: %#v", records)
	}
	if len(records[0].Payload) == 0 {
		t.Fatal("backup payload is empty")
	}

	cleanupMongoDatabase(t, ctx, sourceAdmin, sourceDB)
	if err := driver.Restore(ctx, restoreCommandTarget, &backup, drivers.RestoreOptions{ReplaceExisting: true}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	if got := queryMongoScalar(t, ctx, restoreTarget, `db.users.countDocuments()`); got != "2" {
		t.Fatalf("restored users count = %q, want 2", got)
	}
	if got := queryMongoScalar(t, ctx, restoreTarget, `db.users.findOne({id: 1}).name`); got != "Ada" {
		t.Fatalf("restored id=1 name = %q, want Ada", got)
	}
	wantBulkRows := strconv.Itoa(bulkRows)
	if got := queryMongoScalar(t, ctx, restoreTarget, `db.bulk_items.countDocuments()`); got != wantBulkRows {
		t.Fatalf("restored bulk count = %q, want %s", got, wantBulkRows)
	}
	wantChecksum := strconv.Itoa((bulkRows * (bulkRows + 1)) / 2)
	wantBulkChecksum := wantBulkRows + ":" + wantChecksum + ":" + wantChecksum
	checksumScript := `(() => { const row = db.bulk_items.aggregate([{$group: {_id: null, count: {$sum: 1}, ids: {$sum: "$id"}, ranks: {$sum: "$payload.rank"}}}]).toArray()[0]; return row.count + ":" + row.ids + ":" + row.ranks; })()`
	if got := queryMongoScalar(t, ctx, restoreTarget, checksumScript); got != wantBulkChecksum {
		t.Fatalf("restored bulk checksum = %q, want %s", got, wantBulkChecksum)
	}
	if got := queryMongoScalar(t, ctx, restoreTarget, `db.bulk_items.getIndexes().some((idx) => idx.name === "bulk_items_label_idx")`); got != "true" {
		t.Fatalf("restored bulk index presence = %q, want true", got)
	}
}

func TestMongoRestoreCommandTargetIncludesAuthSource(t *testing.T) {
	t.Parallel()

	target := mongoRestoreCommandTarget("127.0.0.1:27019", "restore_db", "backup", "secret", "admin")
	uri := target.Connection["uri"]
	for _, want := range []string{"mongodb://backup:secret@127.0.0.1:27019/", "authSource=admin"} {
		if !strings.Contains(uri, want) {
			t.Fatalf("restore command uri = %q, missing %q", uri, want)
		}
	}
}

func mongoSeedScript(suffix string, bulkRows int) string {
	return fmt.Sprintf(`
db.users.insertMany([
  {id: 1, name: "Ada"},
  {id: 2, name: "Grace"}
]);
const suffix = %q;
const totalRows = %d;
const batchSize = 500;
for (let start = 1; start <= totalRows; start += batchSize) {
  const docs = [];
  const end = Math.min(totalRows, start + batchSize - 1);
  for (let id = start; id <= end; id++) {
    docs.push({
      id,
      label: "item-" + id,
      payload: {rank: id, bucket: id %% 17, tag: "kronos-" + suffix},
      created_at: new Date("2026-04-27T00:00:00Z")
    });
  }
  db.bulk_items.insertMany(docs, {ordered: false});
}
db.bulk_items.createIndex({label: 1}, {name: "bulk_items_label_idx"});
`, suffix, bulkRows)
}

func mongoBulkRows(t *testing.T) int {
	t.Helper()

	value := strings.TrimSpace(os.Getenv("KRONOS_MONGODB_BULK_ROWS"))
	if value == "" {
		return 500
	}
	rows, err := strconv.Atoi(value)
	if err != nil || rows < 1 {
		t.Fatalf("KRONOS_MONGODB_BULK_ROWS = %q, want positive integer", value)
	}
	return rows
}

func mongoTestTimeout(t *testing.T) time.Duration {
	t.Helper()

	value := strings.TrimSpace(os.Getenv("KRONOS_MONGODB_TEST_TIMEOUT_SECONDS"))
	if value == "" {
		return 90 * time.Second
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 1 {
		t.Fatalf("KRONOS_MONGODB_TEST_TIMEOUT_SECONDS = %q, want positive integer", value)
	}
	return time.Duration(seconds) * time.Second
}

func mongoTestTarget(addr string, database string, username string, password string, authSource string) drivers.Target {
	connection := map[string]string{
		"addr":     addr,
		"database": database,
	}
	if username != "" {
		connection["username"] = username
	}
	if password != "" {
		connection["password"] = password
	}
	if username != "" && authSource != "" {
		connection["authSource"] = authSource
	}
	return drivers.Target{Connection: connection}
}

func mongoRestoreCommandTarget(addr string, database string, username string, password string, authSource string) drivers.Target {
	target := mongoTestTarget(addr, database, username, password, authSource)
	host, port := splitAddress(addr)
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "27017"
	}
	uri := url.URL{
		Scheme: "mongodb",
		Host:   netJoinHostPort(host, port),
		Path:   "/",
	}
	if username != "" && password != "" {
		uri.User = url.UserPassword(username, password)
	} else if username != "" {
		uri.User = url.User(username)
	}
	if username != "" && authSource != "" {
		query := uri.Query()
		query.Set("authSource", authSource)
		uri.RawQuery = query.Encode()
	}
	target.Connection["uri"] = uri.String()
	return target
}

func netJoinHostPort(host string, port string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
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

func cleanupMongoDatabase(t *testing.T, ctx context.Context, target drivers.Target, database string) {
	t.Helper()

	runMongoShell(t, ctx, target, fmt.Sprintf("db.getSiblingDB(%q).dropDatabase();", database))
}

func runMongoShell(t *testing.T, ctx context.Context, target drivers.Target, script string) {
	t.Helper()

	cmd := exec.CommandContext(ctx, "mongosh", "--quiet", mongoShellURI(target))
	cmd.Stdin = strings.NewReader(mongoShellScript(target, script))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("mongosh error = %v output=%s stderr=%s", err, strings.TrimSpace(string(out)), strings.TrimSpace(stderr.String()))
	}
}

func queryMongoScalar(t *testing.T, ctx context.Context, target drivers.Target, script string) string {
	t.Helper()

	cmd := exec.CommandContext(ctx, "mongosh", "--quiet", mongoShellURI(target))
	cmd.Stdin = strings.NewReader(mongoShellScript(target, `print("KRONOS_RESULT:" + (`+script+`));`))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("query %q error = %v output=%s stderr=%s", script, err, strings.TrimSpace(string(out)), strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		const marker = "KRONOS_RESULT:"
		if index := strings.Index(line, marker); index >= 0 {
			value := strings.TrimSpace(line[index+len(marker):])
			if fields := strings.Fields(value); len(fields) > 0 {
				return fields[0]
			}
			return value
		}
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

func mongoShellURI(target drivers.Target) string {
	uri := mongoURI(target)
	if strings.TrimSpace(mongoPassword(target)) == "" {
		return uri
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	parsed.User = nil
	return parsed.String()
}

func mongoShellScript(target drivers.Target, script string) string {
	username := strings.TrimSpace(firstNonEmpty(target.Connection["username"], target.Connection["user"]))
	password := mongoPassword(target)
	if username == "" || strings.TrimSpace(password) == "" {
		return script
	}
	authSource := firstNonEmpty(
		strings.TrimSpace(target.Connection["authSource"]),
		strings.TrimSpace(target.Connection["auth_source"]),
		strings.TrimSpace(target.Options["authSource"]),
		strings.TrimSpace(target.Options["auth_source"]),
		"admin",
	)
	return fmt.Sprintf("db.getSiblingDB(%q).auth(%q, %q);\n%s", authSource, username, password, script)
}

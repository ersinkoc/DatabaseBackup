package postgres

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/kronos/kronos/internal/drivers"
)

func TestPGNativeConfigFromTarget(t *testing.T) {
	t.Parallel()

	cfg, err := pgNativeConfigFromTarget(drivers.Target{Connection: map[string]string{
		"dsn": "postgres://backup:secret@db.example:5433/app%20db?sslmode=prefer&application_name=custom",
	}})
	if err != nil {
		t.Fatalf("pgNativeConfigFromTarget(dsn) error = %v", err)
	}
	if cfg.Address != "db.example:5433" || cfg.Database != "app db" || cfg.Username != "backup" || cfg.Password != "secret" || cfg.SSLMode != "prefer" || cfg.ApplicationName != "custom" {
		t.Fatalf("dsn config = %#v", cfg)
	}

	cfg, err = pgNativeConfigFromTarget(drivers.Target{
		Connection: map[string]string{"addr": "127.0.0.1:15432", "database": "app", "user": "u"},
		Options:    map[string]string{"password": "p", "tls": "true"},
	})
	if err != nil {
		t.Fatalf("pgNativeConfigFromTarget(fields) error = %v", err)
	}
	if cfg.Address != "127.0.0.1:15432" || cfg.Database != "app" || cfg.Username != "u" || cfg.Password != "p" || cfg.SSLMode != "require" {
		t.Fatalf("field config = %#v", cfg)
	}
}

func TestPGNativeSimpleQueryOverTCP(t *testing.T) {
	t.Parallel()

	endpoint := startPGNativeScriptServer(t, func(t *testing.T, conn net.Conn) {
		payload := readPGStartupPacket(t, conn)
		if !bytes.Contains(payload, []byte("user\x00backup\x00")) || !bytes.Contains(payload, []byte("database\x00app\x00")) {
			t.Fatalf("startup payload = %q", payload)
		}
		writeAuthOK(conn)
		writeReady(conn)
		msg, err := readPGMessage(conn)
		if err != nil {
			t.Fatalf("read query message: %v", err)
		}
		if msg.Type != 'Q' || string(msg.Payload) != "select 42\x00" {
			t.Fatalf("query message = %#v", msg)
		}
		writeRowDescription(conn, []pgField{{Name: "answer", DataTypeOID: 23, DataTypeSize: 4}})
		writeDataRow(conn, []*string{stringPtr("42")})
		writeCommandComplete(conn, "SELECT 1")
		writeReady(conn)
	})

	result, err := pgNativeSimpleQuery(context.Background(), drivers.Target{
		Connection: map[string]string{"addr": endpoint, "database": "app", "username": "backup", "tls": "disable"},
	}, "select 42")
	if err != nil {
		t.Fatalf("pgNativeSimpleQuery() error = %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0][0] == nil || *result.Rows[0][0] != "42" {
		t.Fatalf("result = %#v", result)
	}
}

func TestPGNativeSSLPreferContinuesWhenServerDeclines(t *testing.T) {
	t.Parallel()

	endpoint := startPGNativeScriptServer(t, func(t *testing.T, conn net.Conn) {
		if code := readPGUntypedCode(t, conn); code != pgSSLRequestCode {
			t.Fatalf("ssl request code = %d", code)
		}
		if _, err := conn.Write([]byte{'N'}); err != nil {
			t.Fatalf("write ssl refusal: %v", err)
		}
		_ = readPGStartupPacket(t, conn)
		writeAuthOK(conn)
		writeReady(conn)
		msg, err := readPGMessage(conn)
		if err != nil {
			t.Fatalf("read query message: %v", err)
		}
		if msg.Type != 'Q' {
			t.Fatalf("query type = %q", msg.Type)
		}
		writeCommandComplete(conn, "SELECT 0")
		writeReady(conn)
	})

	_, err := pgNativeSimpleQuery(context.Background(), drivers.Target{
		Connection: map[string]string{"addr": endpoint, "username": "backup", "tls": "prefer"},
	}, "select 0")
	if err != nil {
		t.Fatalf("pgNativeSimpleQuery(ssl prefer) error = %v", err)
	}
}

func TestPGNativeSSLRequireFailsWhenServerDeclines(t *testing.T) {
	t.Parallel()

	endpoint := startPGNativeScriptServer(t, func(t *testing.T, conn net.Conn) {
		if code := readPGUntypedCode(t, conn); code != pgSSLRequestCode {
			t.Fatalf("ssl request code = %d", code)
		}
		if _, err := conn.Write([]byte{'N'}); err != nil {
			t.Fatalf("write ssl refusal: %v", err)
		}
	})

	_, err := pgNativeSimpleQuery(context.Background(), drivers.Target{
		Connection: map[string]string{"addr": endpoint, "username": "backup", "tls": "require"},
	}, "select 1")
	if err == nil || !strings.Contains(err.Error(), "does not support SSL") {
		t.Fatalf("pgNativeSimpleQuery(ssl require) error = %v", err)
	}
}

func TestDriverTestCanUseNativeProtocol(t *testing.T) {
	t.Parallel()

	queryer := &fakePGNativeQueryer{}
	driver := &Driver{native: queryer}
	err := driver.Test(context.Background(), drivers.Target{Options: map[string]string{"protocol": "native"}})
	if err != nil {
		t.Fatalf("Test(native) error = %v", err)
	}
	if len(queryer.queries) != 1 || queryer.queries[0] != "select 1" {
		t.Fatalf("native queries = %q", queryer.queries)
	}
}

func TestDriverBackupFullCanUseNativeProtocol(t *testing.T) {
	t.Parallel()

	queryer := &fakePGNativeQueryer{results: []pgQueryResult{
		pgTestResult([]string{"schema_name", "table_name"}, [][]*string{{stringPtr("public"), stringPtr("users")}}),
		pgTestResult([]string{"extension_name", "schema_name", "extension_version"}, [][]*string{
			{stringPtr("citext"), stringPtr("public"), stringPtr("1.6")},
		}),
		pgTestResult([]string{"schema_name", "type_name", "enum_labels"}, [][]*string{
			{stringPtr("public"), stringPtr("user_status"), stringPtr("'active', 'blocked'")},
		}),
		pgTestResult([]string{"schema_name", "type_name", "base_type", "domain_default", "not_null", "constraints"}, [][]*string{
			{stringPtr("public"), stringPtr("email_address"), stringPtr("text"), nil, stringPtr("true"), stringPtr("CHECK ((VALUE ~~ '%@%'::text))")},
		}),
		pgTestResult([]string{"schema_name", "sequence_name", "data_type", "start_value", "min_value", "max_value", "increment_by", "cache_size", "cycle", "last_value", "owned_schema", "owned_table", "owned_column"}, [][]*string{
			{stringPtr("public"), stringPtr("users_id_seq"), stringPtr("integer"), stringPtr("1"), stringPtr("1"), stringPtr("2147483647"), stringPtr("1"), stringPtr("1"), stringPtr("false"), stringPtr("2"), stringPtr("public"), stringPtr("users"), stringPtr("id")},
		}),
		pgTestResult([]string{"schema_name", "view_name", "relkind", "view_def"}, [][]*string{
			{stringPtr("public"), stringPtr("active_users"), stringPtr("v"), stringPtr(` SELECT users.id,
    users.name
   FROM users
  WHERE (users.name IS NOT NULL)`)},
			{stringPtr("analytics"), stringPtr("user_counts"), stringPtr("m"), stringPtr(` SELECT count(*) AS count
   FROM public.users`)},
		}),
		pgTestResult([]string{"routine_def"}, [][]*string{
			{stringPtr(`CREATE FUNCTION public.touch_updated_at() RETURNS trigger
    LANGUAGE plpgsql
    AS $$ begin new.updated_at = now(); return new; end $$`)},
		}),
		pgTestResult([]string{"column_name", "data_type", "not_null", "column_default"}, [][]*string{
			{stringPtr("id"), stringPtr("integer"), stringPtr("true"), stringPtr("nextval('users_id_seq'::regclass)")},
			{stringPtr("name"), stringPtr("text"), stringPtr("false"), nil},
		}),
		pgTestResult([]string{"id", "name"}, [][]*string{
			{stringPtr("1"), stringPtr("Ada")},
			{stringPtr("2"), nil},
		}),
		pgTestResult([]string{"constraint_name", "constraint_def"}, [][]*string{
			{stringPtr("users_pkey"), stringPtr("PRIMARY KEY (id)")},
			{stringPtr("users_name_check"), stringPtr("CHECK (name <> ''::text)")},
		}),
		pgTestResult([]string{"index_def"}, [][]*string{
			{stringPtr(`CREATE INDEX users_name_idx ON public.users USING btree (name)`)},
		}),
		pgTestResult([]string{"trigger_def"}, [][]*string{
			{stringPtr(`CREATE TRIGGER users_touch_updated_at BEFORE UPDATE ON public.users FOR EACH ROW EXECUTE FUNCTION public.touch_updated_at()`)},
		}),
	}}
	driver := &Driver{native: queryer}
	var stream drivers.MemoryRecordStream
	rp, err := driver.BackupFull(context.Background(), drivers.Target{
		Connection: map[string]string{"database": "app"},
		Options:    map[string]string{"protocol": "native"},
	}, &stream)
	if err != nil {
		t.Fatalf("BackupFull(native) error = %v", err)
	}
	if rp.Driver != "postgres" || rp.Position != "pgwire:native-sql" {
		t.Fatalf("ResumePoint = %#v", rp)
	}
	records := stream.Records()
	if len(records) != 2 || records[0].Object.Kind != databaseObjectKind || records[1].Rows != 2 || !records[1].Done {
		t.Fatalf("records = %#v", records)
	}
	payload := string(records[0].Payload)
	for _, want := range []string{
		`CREATE EXTENSION IF NOT EXISTS "citext" WITH SCHEMA "public" VERSION '1.6';`,
		`CREATE TYPE "public"."user_status" AS ENUM ('active', 'blocked');`,
		`CREATE DOMAIN "public"."email_address" AS text NOT NULL CHECK ((VALUE ~~ '%@%'::text));`,
		`CREATE SEQUENCE "public"."users_id_seq" AS integer START WITH 1 INCREMENT BY 1 MINVALUE 1 MAXVALUE 2147483647 CACHE 1 NO CYCLE;`,
		`ALTER SEQUENCE "public"."users_id_seq" OWNED BY "public"."users"."id";`,
		`CREATE TABLE "public"."users" (`,
		`"id" integer DEFAULT nextval('users_id_seq'::regclass) NOT NULL`,
		`"name" text`,
		`INSERT INTO "public"."users" ("id", "name") VALUES ('1', 'Ada');`,
		`INSERT INTO "public"."users" ("id", "name") VALUES ('2', NULL);`,
		`CREATE FUNCTION public.touch_updated_at() RETURNS trigger`,
		`ALTER TABLE "public"."users" ADD CONSTRAINT "users_pkey" PRIMARY KEY (id);`,
		`ALTER TABLE "public"."users" ADD CONSTRAINT "users_name_check" CHECK (name <> ''::text);`,
		`CREATE INDEX users_name_idx ON public.users USING btree (name);`,
		`CREATE TRIGGER users_touch_updated_at BEFORE UPDATE ON public.users FOR EACH ROW EXECUTE FUNCTION public.touch_updated_at();`,
		`SELECT pg_catalog.setval('"public"."users_id_seq"', 2, true);`,
		`CREATE VIEW "public"."active_users" AS`,
		`WHERE (users.name IS NOT NULL);`,
		`CREATE SCHEMA IF NOT EXISTS "analytics";`,
		`CREATE MATERIALIZED VIEW "analytics"."user_counts" AS`,
		`WITH NO DATA;`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("native backup payload missing %q:\n%s", want, payload)
		}
	}
	routineOffset := strings.Index(payload, `CREATE FUNCTION public.touch_updated_at()`)
	triggerOffset := strings.Index(payload, `CREATE TRIGGER users_touch_updated_at`)
	if routineOffset < 0 || triggerOffset < 0 || routineOffset > triggerOffset {
		t.Fatalf("routine must be dumped before trigger:\n%s", payload)
	}
	if len(queryer.queries) != 12 || !strings.Contains(queryer.queries[0], "pg_catalog.pg_class") || !strings.Contains(queryer.queries[1], "pg_catalog.pg_extension") || !strings.Contains(queryer.queries[2], "pg_catalog.pg_enum") || !strings.Contains(queryer.queries[3], "typtype = 'd'") || !strings.Contains(queryer.queries[4], "pg_catalog.pg_sequences") || !strings.Contains(queryer.queries[5], "pg_get_viewdef") || !strings.Contains(queryer.queries[6], "pg_get_functiondef") || !strings.Contains(queryer.queries[7], "pg_catalog.pg_attribute") || queryer.queries[8] != `select "id", "name" from "public"."users"` || !strings.Contains(queryer.queries[9], "pg_catalog.pg_constraint") || !strings.Contains(queryer.queries[10], "pg_catalog.pg_index") || !strings.Contains(queryer.queries[11], "pg_catalog.pg_trigger") {
		t.Fatalf("native queries = %#v", queryer.queries)
	}
}

func TestDriverBackupFullNativeZeroColumnTable(t *testing.T) {
	t.Parallel()

	queryer := &fakePGNativeQueryer{results: []pgQueryResult{
		pgTestResult([]string{"schema_name", "table_name"}, [][]*string{{stringPtr("scratch"), stringPtr("marker")}}),
		pgTestResult([]string{"extension_name", "schema_name", "extension_version"}, nil),
		pgTestResult([]string{"schema_name", "type_name", "enum_labels"}, nil),
		pgTestResult([]string{"schema_name", "type_name", "base_type", "domain_default", "not_null", "constraints"}, nil),
		pgTestResult([]string{"schema_name", "sequence_name", "data_type", "start_value", "min_value", "max_value", "increment_by", "cache_size", "cycle", "last_value", "owned_schema", "owned_table", "owned_column"}, nil),
		pgTestResult([]string{"schema_name", "view_name", "relkind", "view_def"}, nil),
		pgTestResult([]string{"routine_def"}, nil),
		pgTestResult([]string{"column_name", "data_type", "not_null", "column_default"}, nil),
		pgTestResult([]string{"row_count"}, [][]*string{{stringPtr("2")}}),
		pgTestResult([]string{"constraint_name", "constraint_def"}, nil),
		pgTestResult([]string{"index_def"}, nil),
		pgTestResult([]string{"trigger_def"}, nil),
	}}
	driver := &Driver{native: queryer}
	var stream drivers.MemoryRecordStream
	if _, err := driver.BackupFull(context.Background(), drivers.Target{Options: map[string]string{"protocol": "native"}}, &stream); err != nil {
		t.Fatalf("BackupFull(native zero-column) error = %v", err)
	}
	payload := string(stream.Records()[0].Payload)
	for _, want := range []string{
		`CREATE SCHEMA IF NOT EXISTS "scratch";`,
		`CREATE TABLE "scratch"."marker" ();`,
		`INSERT INTO "scratch"."marker" DEFAULT VALUES;`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("native zero-column payload missing %q:\n%s", want, payload)
		}
	}
}

func TestDriverRestoreCanUseNativeProtocol(t *testing.T) {
	t.Parallel()

	queryer := &fakePGNativeQueryer{}
	driver := &Driver{native: queryer}
	var stream drivers.MemoryRecordStream
	payload := []byte(`
CREATE TABLE "public"."users" ("name" text);
INSERT INTO "public"."users" ("name") VALUES ('Ada; Lovelace');
CREATE FUNCTION "public"."hello"() RETURNS text AS $$ begin return 'hi;'; end $$ LANGUAGE plpgsql;
`)
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "app", Kind: databaseObjectKind}, payload); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}
	if err := driver.Restore(context.Background(), drivers.Target{Options: map[string]string{"protocol": "native"}}, &stream, drivers.RestoreOptions{ReplaceExisting: true}); err != nil {
		t.Fatalf("Restore(native) error = %v", err)
	}
	if len(queryer.queries) != 3 {
		t.Fatalf("native restore queries = %#v", queryer.queries)
	}
	if !strings.Contains(queryer.queries[1], "Ada; Lovelace") || !strings.Contains(queryer.queries[2], "return 'hi;'") {
		t.Fatalf("native restore split queries = %#v", queryer.queries)
	}
}

func TestSplitPGSQLStatementsRejectsUnterminatedStatements(t *testing.T) {
	t.Parallel()

	for _, sql := range []string{"select 'unterminated", `select "unterminated`, "/* unterminated", "select $tag$unterminated"} {
		if _, err := splitPGSQLStatements(sql); err == nil {
			t.Fatalf("splitPGSQLStatements(%q) error = nil, want error", sql)
		}
	}
}

type fakePGNativeQueryer struct {
	queries []string
	results []pgQueryResult
	err     error
}

func (q *fakePGNativeQueryer) SimpleQuery(_ context.Context, _ drivers.Target, query string) (pgQueryResult, error) {
	q.queries = append(q.queries, query)
	if q.err != nil {
		return pgQueryResult{}, q.err
	}
	if len(q.results) == 0 {
		return pgQueryResult{}, nil
	}
	result := q.results[0]
	q.results = q.results[1:]
	return result, nil
}

func pgTestResult(fields []string, rows [][]*string) pgQueryResult {
	result := pgQueryResult{Rows: rows}
	for _, field := range fields {
		result.Fields = append(result.Fields, pgField{Name: field})
	}
	return result
}

func startPGNativeScriptServer(t *testing.T, handle func(*testing.T, net.Conn)) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})
	done := make(chan error, 1)
	go func() {
		var serverErr error
		defer func() {
			done <- serverErr
		}()
		conn, err := listener.Accept()
		if err != nil {
			serverErr = err
			return
		}
		defer conn.Close()
		handle(t, conn)
	}()
	t.Cleanup(func() {
		if err := <-done; err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Errorf("postgres script server error: %v", err)
		}
	})
	return listener.Addr().String()
}

func readPGStartupPacket(t *testing.T, r io.Reader) []byte {
	t.Helper()

	var length int32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		t.Fatalf("read startup length: %v", err)
	}
	if length < 8 {
		t.Fatalf("startup length = %d", length)
	}
	payload := make([]byte, int(length-4))
	if _, err := io.ReadFull(r, payload); err != nil {
		t.Fatalf("read startup payload: %v", err)
	}
	if code := int32(binary.BigEndian.Uint32(payload[:4])); code != pgProtocolVersion30 {
		t.Fatalf("startup protocol = %d", code)
	}
	return payload
}

func readPGUntypedCode(t *testing.T, r io.Reader) int32 {
	t.Helper()

	var length int32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		t.Fatalf("read untyped length: %v", err)
	}
	if length != 8 {
		t.Fatalf("untyped length = %d", length)
	}
	var code int32
	if err := binary.Read(r, binary.BigEndian, &code); err != nil {
		t.Fatalf("read untyped code: %v", err)
	}
	return code
}

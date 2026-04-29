package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kronos/kronos/internal/drivers"
	"github.com/kronos/kronos/internal/manifest"
)

func TestDriverNameVersionTestAndUnsupportedIncremental(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{[]byte("pg_dump (PostgreSQL) 16.2\n"), []byte("-- schema\n")}}
	driver := &Driver{runner: runner}
	if driver.Name() != "postgres" {
		t.Fatalf("Name() = %q", driver.Name())
	}
	version, err := driver.Version(context.Background(), drivers.Target{})
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version != "pg_dump (PostgreSQL) 16.2" {
		t.Fatalf("Version() = %q", version)
	}
	if err := driver.Test(context.Background(), drivers.Target{Connection: map[string]string{"dsn": "postgres://db"}}); err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if _, err := driver.BackupIncremental(context.Background(), drivers.Target{}, manifest.Manifest{}, nil); !errors.Is(err, drivers.ErrIncrementalUnsupported) {
		t.Fatalf("BackupIncremental() error = %v", err)
	}
	if len(runner.calls) != 2 || runner.calls[0].name != "pg_dump" || runner.calls[1].name != "pg_dump" {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestDriverBackupFullUsesPgDumpPlainSQL(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{[]byte("create table public.users(id int);\n")}}
	driver := &Driver{runner: runner}
	target := drivers.Target{
		Name: "pg-prod",
		Connection: map[string]string{
			"addr":     "db.example:5433",
			"database": "app",
			"username": "backup",
			"password": "secret",
			"tls":      "require",
		},
	}
	var stream drivers.MemoryRecordStream
	rp, err := driver.BackupFull(context.Background(), target, &stream)
	if err != nil {
		t.Fatalf("BackupFull() error = %v", err)
	}
	if rp.Driver != "postgres" || rp.Position != "pg_dump:plain" {
		t.Fatalf("ResumePoint = %#v", rp)
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "pg_dump" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	args := strings.Join(runner.calls[0].args, " ")
	for _, want := range []string{"--format=plain", "--no-owner", "--no-privileges", "--dbname", "postgres://backup@db.example:5433/app?sslmode=require"} {
		if !strings.Contains(args, want) {
			t.Fatalf("pg_dump args = %q, missing %q", args, want)
		}
	}
	if strings.Contains(args, "secret") {
		t.Fatalf("pg_dump args leaked password: %q", args)
	}
	if got := strings.Join(runner.calls[0].env, " "); got != "PGPASSWORD=secret" {
		t.Fatalf("pg_dump env = %q, want PGPASSWORD", got)
	}
	records := stream.Records()
	if len(records) != 2 || records[0].Object.Kind != databaseObjectKind || records[0].Object.Name != "app" || string(records[0].Payload) != "create table public.users(id int);\n" || !records[1].Done {
		t.Fatalf("records = %#v", records)
	}
}

func TestDriverBackupFullCanIncludeGlobals(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{
		[]byte("create role kronos_restore;\n"),
		[]byte("create table public.users(id int);\n"),
	}}
	driver := &Driver{runner: runner}
	target := drivers.Target{
		Connection: map[string]string{"dsn": "postgres://backup:secret@db.example/app"},
		Options:    map[string]string{"include_globals": "true"},
	}
	var stream drivers.MemoryRecordStream
	rp, err := driver.BackupFull(context.Background(), target, &stream)
	if err != nil {
		t.Fatalf("BackupFull() error = %v", err)
	}
	if rp.Position != "pg_dumpall:globals+pg_dump:plain" {
		t.Fatalf("ResumePoint.Position = %q", rp.Position)
	}
	if len(runner.calls) != 2 || runner.calls[0].name != "pg_dumpall" || runner.calls[1].name != "pg_dump" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	globalArgs := strings.Join(runner.calls[0].args, " ")
	for _, want := range []string{"--globals-only", "--no-role-passwords", "--dbname", "postgres://backup@db.example/app"} {
		if !strings.Contains(globalArgs, want) {
			t.Fatalf("pg_dumpall args = %q, missing %q", globalArgs, want)
		}
	}
	if strings.Contains(globalArgs, "secret") {
		t.Fatalf("pg_dumpall args leaked password: %q", globalArgs)
	}
	if got := strings.Join(runner.calls[0].env, " "); got != "PGPASSWORD=secret" {
		t.Fatalf("pg_dumpall env = %q, want PGPASSWORD", got)
	}
	records := stream.Records()
	if len(records) != 4 || records[0].Object.Kind != globalsObjectKind || records[0].Object.Name != "globals" || string(records[0].Payload) != "create role kronos_restore;\n" || !records[1].Done || records[2].Object.Kind != databaseObjectKind || !records[3].Done {
		t.Fatalf("records = %#v", records)
	}
}

func TestDriverBackupFullRequiresWriter(t *testing.T) {
	t.Parallel()

	if _, err := NewDriver().BackupFull(context.Background(), drivers.Target{}, nil); err == nil {
		t.Fatal("BackupFull(nil writer) error = nil, want error")
	}
}

func TestDriverRestoreUsesPsql(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{[]byte("restore ok")}}
	driver := &Driver{runner: runner}
	var stream drivers.MemoryRecordStream
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "app", Kind: databaseObjectKind}, []byte("create table public.users(id int);\n")); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}
	if err := driver.Restore(context.Background(), drivers.Target{Connection: map[string]string{"database": "app"}}, &stream, drivers.RestoreOptions{ReplaceExisting: true}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "psql" || string(runner.calls[0].stdin) != "create table public.users(id int);\n" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	args := strings.Join(runner.calls[0].args, " ")
	if !strings.Contains(args, "--single-transaction") || !strings.Contains(args, "--set ON_ERROR_STOP=1") || !strings.Contains(args, "postgres://127.0.0.1:5432/app?sslmode=disable") {
		t.Fatalf("psql args = %q", args)
	}
}

func TestDriverRestoreAppliesGlobals(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{[]byte("globals ok")}}
	driver := &Driver{runner: runner}
	var stream drivers.MemoryRecordStream
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "globals", Kind: globalsObjectKind}, []byte("create role kronos_restore;\n")); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}
	if err := driver.Restore(context.Background(), drivers.Target{Connection: map[string]string{"dsn": "postgres://db"}}, &stream, drivers.RestoreOptions{ReplaceExisting: true}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "psql" || string(runner.calls[0].stdin) != "create role kronos_restore;\n" {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestDriverRestoreRequiresReplaceExisting(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	driver := &Driver{runner: runner}
	var stream drivers.MemoryRecordStream
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "app", Kind: databaseObjectKind}, []byte("create table public.users(id int);\n")); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}
	err := driver.Restore(context.Background(), drivers.Target{Connection: map[string]string{"database": "app"}}, &stream, drivers.RestoreOptions{})
	if err == nil || !strings.Contains(err.Error(), "replace_existing=true") {
		t.Fatalf("Restore() error = %v, want replace_existing guard", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("guarded restore calls = %#v", runner.calls)
	}
}

func TestDriverRestoreDryRunSkipsPsql(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	driver := &Driver{runner: runner}
	var stream drivers.MemoryRecordStream
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "app", Kind: databaseObjectKind}, []byte("select 1;")); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}
	if err := driver.Restore(context.Background(), drivers.Target{}, &stream, drivers.RestoreOptions{DryRun: true}); err != nil {
		t.Fatalf("Restore(dry-run) error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("dry-run calls = %#v", runner.calls)
	}
}

func TestDriverRestoreRequiresReader(t *testing.T) {
	t.Parallel()

	if err := NewDriver().Restore(context.Background(), drivers.Target{}, nil, drivers.RestoreOptions{}); err == nil {
		t.Fatal("Restore(nil reader) error = nil, want error")
	}
}

func TestPostgresDSN(t *testing.T) {
	t.Parallel()

	target := drivers.Target{Connection: map[string]string{"dsn": "postgres://custom/app"}}
	if got := postgresDSN(target); got != "postgres://custom/app" {
		t.Fatalf("postgresDSN(dsn) = %q", got)
	}
	target = drivers.Target{Connection: map[string]string{"addr": "db.internal", "database": "app"}}
	if got := postgresDSN(target); got != "postgres://db.internal:5432/app?sslmode=disable" {
		t.Fatalf("postgresDSN(default port) = %q", got)
	}
}

func TestIncludeGlobals(t *testing.T) {
	t.Parallel()

	if !includeGlobals(drivers.Target{Options: map[string]string{"include_globals": "yes"}}) {
		t.Fatal("includeGlobals(include_globals=yes) = false, want true")
	}
	if includeGlobals(drivers.Target{Options: map[string]string{"include_globals": "false"}}) {
		t.Fatal("includeGlobals(include_globals=false) = true, want false")
	}
}

func TestDriverStreamWaitsForContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := NewDriver().Stream(ctx, drivers.Target{}, drivers.ResumePoint{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream(canceled) error = %v, want context.Canceled", err)
	}
}

func TestDriverReplayStreamUnsupported(t *testing.T) {
	t.Parallel()

	if err := NewDriver().ReplayStream(context.Background(), drivers.Target{}, nil, drivers.ReplayTarget{}); !errors.Is(err, drivers.ErrIncrementalUnsupported) {
		t.Fatalf("ReplayStream() error = %v", err)
	}
}

type fakeRunner struct {
	outputs [][]byte
	err     error
	calls   []runnerCall
}

type runnerCall struct {
	name  string
	args  []string
	stdin []byte
	env   []string
}

func (r *fakeRunner) Run(_ context.Context, name string, args []string, stdin []byte, env []string) ([]byte, error) {
	r.calls = append(r.calls, runnerCall{name: name, args: append([]string(nil), args...), stdin: append([]byte(nil), stdin...), env: append([]string(nil), env...)})
	if r.err != nil {
		return nil, r.err
	}
	if len(r.outputs) == 0 {
		return nil, fmt.Errorf("no fake output for %s", name)
	}
	out := r.outputs[0]
	r.outputs = r.outputs[1:]
	return out, nil
}

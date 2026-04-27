package mysql

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kronos/kronos/internal/drivers"
	"github.com/kronos/kronos/internal/manifest"
)

func TestDriverNameVersionTestAndUnsupportedIncremental(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{[]byte("mysql  Ver 8.4.0\n"), []byte("1\n")}}
	driver := &Driver{runner: runner}
	if driver.Name() != "mysql" {
		t.Fatalf("Name() = %q", driver.Name())
	}
	version, err := driver.Version(context.Background(), drivers.Target{})
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version != "mysql  Ver 8.4.0" {
		t.Fatalf("Version() = %q", version)
	}
	if err := driver.Test(context.Background(), drivers.Target{Connection: map[string]string{"database": "app"}}); err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if _, err := driver.BackupIncremental(context.Background(), drivers.Target{}, manifest.Manifest{}, nil); !errors.Is(err, drivers.ErrIncrementalUnsupported) {
		t.Fatalf("BackupIncremental() error = %v", err)
	}
	if len(runner.calls) != 2 || runner.calls[0].name != "mysql" || runner.calls[1].name != "mysql" {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestDriverBackupFullUsesMysqlDump(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{[]byte("create table users(id int);\n")}}
	driver := &Driver{runner: runner}
	target := drivers.Target{
		Connection: map[string]string{
			"addr":     "db.example:3307",
			"database": "app",
			"username": "backup",
			"password": "secret",
		},
	}
	var stream drivers.MemoryRecordStream
	rp, err := driver.BackupFull(context.Background(), target, &stream)
	if err != nil {
		t.Fatalf("BackupFull() error = %v", err)
	}
	if rp.Driver != "mysql" || rp.Position != "mysqldump:single-transaction" {
		t.Fatalf("ResumePoint = %#v", rp)
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "mysqldump" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	args := strings.Join(runner.calls[0].args, " ")
	for _, want := range []string{"--host db.example", "--port 3307", "--database app", "--user backup", "--single-transaction", "--routines", "--triggers", "--events", "--set-gtid-purged=OFF", "app"} {
		if !strings.Contains(args, want) {
			t.Fatalf("mysqldump args = %q, missing %q", args, want)
		}
	}
	if strings.Contains(args, "secret") {
		t.Fatalf("mysqldump args leaked password: %q", args)
	}
	if len(runner.calls[0].env) != 1 || runner.calls[0].env[0] != "MYSQL_PWD=secret" {
		t.Fatalf("mysqldump env = %#v", runner.calls[0].env)
	}
	records := stream.Records()
	if len(records) != 2 || records[0].Object.Kind != databaseObjectKind || records[0].Object.Name != "app" || string(records[0].Payload) != "create table users(id int);\n" || !records[1].Done {
		t.Fatalf("records = %#v", records)
	}
}

func TestDriverBackupFullRequiresWriter(t *testing.T) {
	t.Parallel()

	if _, err := NewDriver().BackupFull(context.Background(), drivers.Target{}, nil); err == nil {
		t.Fatal("BackupFull(nil writer) error = nil, want error")
	}
}

func TestDriverRestoreUsesMysql(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{[]byte("restore ok")}}
	driver := &Driver{runner: runner}
	var stream drivers.MemoryRecordStream
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "app", Kind: databaseObjectKind}, []byte("create table users(id int);\n")); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}
	if err := driver.Restore(context.Background(), drivers.Target{Connection: map[string]string{"database": "app"}}, &stream, drivers.RestoreOptions{ReplaceExisting: true}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "mysql" || string(runner.calls[0].stdin) != "create table users(id int);\n" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	args := strings.Join(runner.calls[0].args, " ")
	if !strings.Contains(args, "--host 127.0.0.1") || !strings.Contains(args, "--port 3306") || !strings.Contains(args, "--database app") {
		t.Fatalf("mysql args = %q", args)
	}
}

func TestDriverRestoreRequiresReplaceExisting(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	driver := &Driver{runner: runner}
	var stream drivers.MemoryRecordStream
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "app", Kind: databaseObjectKind}, []byte("create table users(id int);\n")); err != nil {
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

func TestDriverRestoreDryRunSkipsMysql(t *testing.T) {
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
		t.Fatalf("ReplayStream() error = %v, want incremental unsupported", err)
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
		return nil, nil
	}
	out := r.outputs[0]
	r.outputs = r.outputs[1:]
	return out, nil
}

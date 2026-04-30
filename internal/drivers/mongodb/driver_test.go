package mongodb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kronos/kronos/internal/drivers"
	"github.com/kronos/kronos/internal/manifest"
)

func TestDriverNameVersionTestAndUnsupportedIncremental(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{[]byte("mongodump version: 100.12.0\n"), []byte("archive")}}
	driver := &Driver{runner: runner}
	if driver.Name() != "mongodb" {
		t.Fatalf("Name() = %q", driver.Name())
	}
	version, err := driver.Version(context.Background(), drivers.Target{})
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version != "mongodump version: 100.12.0" {
		t.Fatalf("Version() = %q", version)
	}
	if err := driver.Test(context.Background(), drivers.Target{Connection: map[string]string{"database": "app"}}); err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if _, err := driver.BackupIncremental(context.Background(), drivers.Target{}, manifest.Manifest{}, nil); !errors.Is(err, drivers.ErrIncrementalUnsupported) {
		t.Fatalf("BackupIncremental() error = %v", err)
	}
	if len(runner.calls) != 2 || runner.calls[0].name != "mongodump" || runner.calls[1].name != "mongodump" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	testArgs := strings.Join(runner.calls[1].args, " ")
	if !strings.Contains(testArgs, "--collection __kronos_connection_test__") {
		t.Fatalf("Test mongodump args = %q, missing connection test collection", testArgs)
	}
}

func TestDriverBackupFullUsesMongoDumpArchive(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{[]byte("archive-bytes")}}
	driver := &Driver{runner: runner}
	target := drivers.Target{
		Connection: map[string]string{
			"addr":     "mongo.example:27018",
			"database": "app",
			"username": "backup",
			"password": "secret",
		},
		Options: map[string]string{"authSource": "admin", "tls": "true"},
	}
	var stream drivers.MemoryRecordStream
	rp, err := driver.BackupFull(context.Background(), target, &stream)
	if err != nil {
		t.Fatalf("BackupFull() error = %v", err)
	}
	if rp.Driver != "mongodb" || rp.Position != "mongodump:archive" {
		t.Fatalf("ResumePoint = %#v", rp)
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "mongodump" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	args := strings.Join(runner.calls[0].args, " ")
	for _, want := range []string{"--config ", "--db app", "--archive"} {
		if !strings.Contains(args, want) {
			t.Fatalf("mongodump args = %q, missing %q", args, want)
		}
	}
	if strings.Contains(args, "secret") {
		t.Fatalf("mongodump args leaked password: %q", args)
	}
	if !strings.Contains(runner.calls[0].config, `uri: "mongodb://backup@mongo.example:27018/app?authSource=admin&tls=true"`) || !strings.Contains(runner.calls[0].config, `password: "secret"`) {
		t.Fatalf("mongodump config = %q", runner.calls[0].config)
	}
	records := stream.Records()
	if len(records) != 2 || records[0].Object.Kind != databaseObjectKind || records[0].Object.Name != "app" || string(records[0].Payload) != "archive-bytes" || !records[1].Done {
		t.Fatalf("records = %#v", records)
	}
}

func TestDriverBackupFullUsesOplogReplicaSetArchive(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{[]byte("archive-with-oplog")}}
	driver := &Driver{runner: runner}
	target := drivers.Target{
		Connection: map[string]string{
			"addr":     "mongo.example:27018",
			"username": "backup",
			"password": "secret",
		},
		Options: map[string]string{"authSource": "admin", "oplog": "true"},
	}
	var stream drivers.MemoryRecordStream
	rp, err := driver.BackupFull(context.Background(), target, &stream)
	if err != nil {
		t.Fatalf("BackupFull() error = %v", err)
	}
	if rp.Driver != "mongodb" || rp.Position != "mongodump:archive+oplog" {
		t.Fatalf("ResumePoint = %#v", rp)
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "mongodump" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	args := strings.Join(runner.calls[0].args, " ")
	for _, want := range []string{"--config ", "--archive", "--oplog"} {
		if !strings.Contains(args, want) {
			t.Fatalf("mongodump args = %q, missing %q", args, want)
		}
	}
	if strings.Contains(args, "--db") {
		t.Fatalf("oplog mongodump args = %q, must not include --db", args)
	}
	if !strings.Contains(runner.calls[0].config, `uri: "mongodb://backup@mongo.example:27018/?authSource=admin"`) {
		t.Fatalf("mongodump config = %q, want deployment-scoped URI", runner.calls[0].config)
	}
	records := stream.Records()
	if len(records) != 2 ||
		records[0].Object.Kind != deploymentObjectKind ||
		records[0].Object.Name != "replica-set" ||
		string(records[0].Payload) != "archive-with-oplog" ||
		!records[1].Done {
		t.Fatalf("records = %#v", records)
	}
}

func TestDriverBackupFullRejectsDatabaseScopedOplogTarget(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	driver := &Driver{runner: runner}
	target := drivers.Target{
		Connection: map[string]string{"database": "app"},
		Options:    map[string]string{"oplog": "true"},
	}
	var stream drivers.MemoryRecordStream
	_, err := driver.BackupFull(context.Background(), target, &stream)
	if err == nil || !strings.Contains(err.Error(), "full replica-set dump") {
		t.Fatalf("BackupFull() error = %v, want full replica-set dump guard", err)
	}
}

func TestDriverBackupFullRejectsURIPathScopedOplogTarget(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	driver := &Driver{runner: runner}
	target := drivers.Target{
		Connection: map[string]string{"uri": "mongodb://backup:secret@mongo.example:27018/app?authSource=admin"},
		Options:    map[string]string{"oplog": "true"},
	}
	var stream drivers.MemoryRecordStream
	_, err := driver.BackupFull(context.Background(), target, &stream)
	if err == nil || !strings.Contains(err.Error(), `remove database "app"`) {
		t.Fatalf("BackupFull() error = %v, want URI database scope guard", err)
	}
}

func TestDriverBackupFullRequiresWriter(t *testing.T) {
	t.Parallel()

	if _, err := NewDriver().BackupFull(context.Background(), drivers.Target{}, nil); err == nil {
		t.Fatal("BackupFull(nil writer) error = nil, want error")
	}
}

func TestDriverRestoreUsesMongoRestoreArchive(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{[]byte("restore ok")}}
	driver := &Driver{runner: runner}
	var stream drivers.MemoryRecordStream
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "source", Kind: databaseObjectKind}, []byte("archive-bytes")); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}
	target := drivers.Target{Connection: map[string]string{"database": "target"}}
	if err := driver.Restore(context.Background(), target, &stream, drivers.RestoreOptions{ReplaceExisting: true}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "mongorestore" || string(runner.calls[0].stdin) != "archive-bytes" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	args := strings.Join(runner.calls[0].args, " ")
	for _, want := range []string{"--uri mongodb://127.0.0.1:27017/target", "--archive", "--drop", "--nsFrom source.*", "--nsTo target.*"} {
		if !strings.Contains(args, want) {
			t.Fatalf("mongorestore args = %q, missing %q", args, want)
		}
	}
}

func TestDriverRestoreUsesOplogReplayForReplicaSetArchive(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: [][]byte{[]byte("restore ok")}}
	driver := &Driver{runner: runner}
	var stream drivers.MemoryRecordStream
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "replica-set", Kind: deploymentObjectKind}, []byte("archive-bytes")); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}
	target := drivers.Target{Connection: map[string]string{"addr": "mongo.example:27018"}}
	if err := driver.Restore(context.Background(), target, &stream, drivers.RestoreOptions{ReplaceExisting: true}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "mongorestore" || string(runner.calls[0].stdin) != "archive-bytes" {
		t.Fatalf("calls = %#v", runner.calls)
	}
	args := strings.Join(runner.calls[0].args, " ")
	for _, want := range []string{"--uri mongodb://mongo.example:27018/", "--archive", "--drop", "--oplogReplay"} {
		if !strings.Contains(args, want) {
			t.Fatalf("mongorestore args = %q, missing %q", args, want)
		}
	}
	if strings.Contains(args, "--nsFrom") || strings.Contains(args, "--nsTo") {
		t.Fatalf("oplog mongorestore args = %q, must not rewrite namespaces", args)
	}
}

func TestDriverRestoreRejectsDatabaseScopedOplogTarget(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	driver := &Driver{runner: runner}
	var stream drivers.MemoryRecordStream
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "replica-set", Kind: deploymentObjectKind}, []byte("archive-bytes")); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}
	target := drivers.Target{Connection: map[string]string{"database": "target"}}
	err := driver.Restore(context.Background(), target, &stream, drivers.RestoreOptions{ReplaceExisting: true})
	if err == nil || !strings.Contains(err.Error(), "full replica-set target") {
		t.Fatalf("Restore() error = %v, want full replica-set target guard", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("guarded restore calls = %#v", runner.calls)
	}
}

func TestDriverRestoreFailureCleansTemporaryConfig(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{err: fmt.Errorf("restore failed")}
	driver := &Driver{runner: runner}
	var stream drivers.MemoryRecordStream
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "source", Kind: databaseObjectKind}, []byte("archive-bytes")); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}
	target := drivers.Target{
		Connection: map[string]string{
			"addr":     "mongo.example:27018",
			"database": "target",
			"username": "backup",
			"password": "secret",
		},
		Options: map[string]string{"authSource": "admin"},
	}
	err := driver.Restore(context.Background(), target, &stream, drivers.RestoreOptions{ReplaceExisting: true})
	if err == nil || !strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("Restore() error = %v, want restore failed", err)
	}
	if len(runner.calls) != 1 || runner.calls[0].configPath == "" {
		t.Fatalf("calls = %#v, want one config-backed mongorestore call", runner.calls)
	}
	if _, statErr := os.Stat(runner.calls[0].configPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("temporary config %s still exists or stat failed with %v", runner.calls[0].configPath, statErr)
	}
}

func TestDriverRestoreRequiresReplaceExisting(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	driver := &Driver{runner: runner}
	var stream drivers.MemoryRecordStream
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "app", Kind: databaseObjectKind}, []byte("archive")); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}
	err := driver.Restore(context.Background(), drivers.Target{}, &stream, drivers.RestoreOptions{})
	if err == nil || !strings.Contains(err.Error(), "replace_existing=true") {
		t.Fatalf("Restore() error = %v, want replace_existing guard", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("guarded restore calls = %#v", runner.calls)
	}
}

func TestDriverRestoreDryRunSkipsMongoRestore(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	driver := &Driver{runner: runner}
	var stream drivers.MemoryRecordStream
	if err := stream.WriteRecord(drivers.ObjectRef{Name: "app", Kind: databaseObjectKind}, []byte("archive")); err != nil {
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
	name       string
	args       []string
	stdin      []byte
	config     string
	configPath string
}

func (r *fakeRunner) Run(_ context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	call := runnerCall{name: name, args: append([]string(nil), args...), stdin: append([]byte(nil), stdin...)}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--config" {
			call.configPath = args[i+1]
			data, _ := os.ReadFile(args[i+1])
			call.config = string(data)
			break
		}
	}
	r.calls = append(r.calls, call)
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

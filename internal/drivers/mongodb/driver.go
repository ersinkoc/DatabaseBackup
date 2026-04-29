package mongodb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kronos/kronos/internal/drivers"
	"github.com/kronos/kronos/internal/manifest"
)

const databaseObjectKind = "database"

// Driver implements MongoDB logical backups with mongodump archive output.
type Driver struct {
	runner commandRunner
}

type commandRunner interface {
	Run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error)
}

type execRunner struct{}

// NewDriver returns a MongoDB driver.
func NewDriver() *Driver {
	return &Driver{runner: execRunner{}}
}

// Name returns the driver name.
func (d *Driver) Name() string {
	return "mongodb"
}

// Version returns the local mongodump version string.
func (d *Driver) Version(ctx context.Context, target drivers.Target) (string, error) {
	out, err := d.run(ctx, "mongodump", []string{"--version"}, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Test validates that mongodump can connect to the target database.
func (d *Driver) Test(ctx context.Context, target drivers.Target) error {
	args, cleanup, err := mongoDumpArgs(target)
	if err != nil {
		return err
	}
	defer cleanup()
	args = append(args, "--collection", connectionTestCollection(target))
	_, err = d.run(ctx, "mongodump", args, nil)
	return err
}

// BackupFull emits one MongoDB archive record from mongodump.
func (d *Driver) BackupFull(ctx context.Context, target drivers.Target, w drivers.RecordWriter) (drivers.ResumePoint, error) {
	if w == nil {
		return drivers.ResumePoint{}, fmt.Errorf("record writer is required")
	}
	database := databaseName(target)
	args, cleanup, err := mongoDumpArgs(target)
	if err != nil {
		return drivers.ResumePoint{}, err
	}
	defer cleanup()
	payload, err := d.run(ctx, "mongodump", args, nil)
	if err != nil {
		return drivers.ResumePoint{}, err
	}
	obj := drivers.ObjectRef{Name: database, Kind: databaseObjectKind}
	if err := w.WriteRecord(obj, payload); err != nil {
		return drivers.ResumePoint{}, err
	}
	if err := w.FinishObject(obj, 0); err != nil {
		return drivers.ResumePoint{}, err
	}
	return drivers.ResumePoint{Driver: d.Name(), Position: "mongodump:archive"}, nil
}

// BackupIncremental is not supported by mongodump logical backups.
func (d *Driver) BackupIncremental(context.Context, drivers.Target, manifest.Manifest, drivers.RecordWriter) (drivers.ResumePoint, error) {
	return drivers.ResumePoint{}, drivers.ErrIncrementalUnsupported
}

// Stream is reserved for oplog/change-stream capture.
func (d *Driver) Stream(ctx context.Context, _ drivers.Target, _ drivers.ResumePoint, _ drivers.StreamWriter) error {
	<-ctx.Done()
	return ctx.Err()
}

// Restore applies MongoDB archive records through mongorestore.
func (d *Driver) Restore(ctx context.Context, target drivers.Target, r drivers.RecordReader, opts drivers.RestoreOptions) error {
	if r == nil {
		return fmt.Errorf("record reader is required")
	}
	for {
		record, err := r.NextRecord()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if record.Done || record.Object.Kind != databaseObjectKind {
			continue
		}
		if opts.DryRun {
			continue
		}
		if !opts.ReplaceExisting {
			return fmt.Errorf("mongodb restore requires replace_existing=true because archive restore can overwrite existing collections")
		}
		args, cleanup, err := mongoRestoreArgs(target, record.Object.Name)
		if err != nil {
			return err
		}
		if _, err := d.run(ctx, "mongorestore", args, record.Payload); err != nil {
			cleanup()
			return err
		}
		cleanup()
	}
}

// ReplayStream is reserved for oplog/change-stream replay.
func (d *Driver) ReplayStream(context.Context, drivers.Target, drivers.StreamReader, drivers.ReplayTarget) error {
	return drivers.ErrIncrementalUnsupported
}

func (d *Driver) run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	runner := d.runner
	if runner == nil {
		runner = execRunner{}
	}
	return runner.Run(ctx, name, args, stdin)
}

func (execRunner) Run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%s: %w: %s", name, err, message)
	}
	return out, nil
}

func mongoDumpArgs(target drivers.Target) ([]string, func(), error) {
	args, cleanup, err := mongoConnectionArgs(target)
	if err != nil {
		return nil, nil, err
	}
	return append(args, "--db", databaseName(target), "--archive"), cleanup, nil
}

func mongoRestoreArgs(target drivers.Target, sourceDatabase string) ([]string, func(), error) {
	args, cleanup, err := mongoConnectionArgs(target)
	if err != nil {
		return nil, nil, err
	}
	args = append(args, "--archive", "--drop")
	targetDatabase := databaseName(target)
	if sourceDatabase != "" && targetDatabase != "" && sourceDatabase != targetDatabase {
		args = append(args, "--nsFrom", sourceDatabase+".*", "--nsTo", targetDatabase+".*")
	}
	return args, cleanup, nil
}

func mongoConnectionArgs(target drivers.Target) ([]string, func(), error) {
	password := mongoPassword(target)
	if strings.TrimSpace(password) == "" {
		return []string{"--uri", mongoURI(target)}, func() {}, nil
	}
	path, err := writeMongoConfig(mongoURI(target), password)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		_ = os.Remove(path)
	}
	return []string{"--config", path}, cleanup, nil
}

func mongoURI(target drivers.Target) string {
	if value := strings.TrimSpace(firstNonEmpty(target.Connection["uri"], target.Connection["dsn"], target.Options["uri"], target.Options["dsn"])); value != "" {
		return mongoURIWithoutPassword(value)
	}
	database := databaseName(target)
	host, port := splitAddress(target.Connection["addr"])
	if value := strings.TrimSpace(target.Connection["host"]); value != "" {
		host = value
	}
	if value := strings.TrimSpace(target.Connection["port"]); value != "" {
		port = value
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "27017"
	}
	u := url.URL{
		Scheme: "mongodb",
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + database,
	}
	username := firstNonEmpty(target.Connection["username"], target.Connection["user"])
	if username != "" {
		u.User = url.User(username)
	}
	query := u.Query()
	if authSource := strings.TrimSpace(firstNonEmpty(target.Connection["authSource"], target.Connection["auth_source"], target.Options["authSource"], target.Options["auth_source"])); authSource != "" {
		query.Set("authSource", authSource)
	}
	tlsMode := strings.ToLower(strings.TrimSpace(firstNonEmpty(target.Connection["tls"], target.Options["tls"], target.Connection["ssl"], target.Options["ssl"])))
	switch tlsMode {
	case "true", "on", "require", "required":
		query.Set("tls", "true")
	case "false", "off", "disable", "disabled":
		query.Set("tls", "false")
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func writeMongoConfig(uri, password string) (string, error) {
	file, err := os.CreateTemp("", "kronos-mongo-*.yaml")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	content := "uri: " + strconv.Quote(uri) + "\npassword: " + strconv.Quote(password) + "\n"
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func mongoPassword(target drivers.Target) string {
	password := firstNonEmpty(target.Connection["password"], target.Options["password"])
	if strings.TrimSpace(password) != "" {
		return password
	}
	for _, value := range []string{target.Connection["uri"], target.Connection["dsn"], target.Options["uri"], target.Options["dsn"]} {
		if password := passwordFromURL(value); password != "" {
			return password
		}
	}
	return ""
}

func mongoURIWithoutPassword(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.User == nil {
		return value
	}
	username := u.User.Username()
	if username == "" {
		u.User = nil
	} else {
		u.User = url.User(username)
	}
	return u.String()
}

func passwordFromURL(value string) string {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.User == nil {
		return ""
	}
	password, _ := u.User.Password()
	return password
}

func databaseName(target drivers.Target) string {
	if value := strings.TrimSpace(target.Connection["database"]); value != "" {
		return value
	}
	if value := strings.TrimSpace(target.Options["database"]); value != "" {
		return value
	}
	return "admin"
}

func connectionTestCollection(target drivers.Target) string {
	if value := strings.TrimSpace(firstNonEmpty(target.Options["connection_test_collection"], target.Connection["connection_test_collection"])); value != "" {
		return value
	}
	return "__kronos_connection_test__"
}

func splitAddress(address string) (string, string) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", ""
	}
	host, port, err := net.SplitHostPort(address)
	if err == nil {
		return host, port
	}
	if strings.Count(address, ":") == 1 {
		parts := strings.SplitN(address, ":", 2)
		return parts[0], parts[1]
	}
	return address, ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

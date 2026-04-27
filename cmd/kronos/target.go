package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/drivers"
	mysqldriver "github.com/kronos/kronos/internal/drivers/mysql"
	postgresdriver "github.com/kronos/kronos/internal/drivers/postgres"
	redisdriver "github.com/kronos/kronos/internal/drivers/redis"
)

func runTarget(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("target subcommand is required")
	}
	switch args[0] {
	case "add":
		return runTargetAdd(ctx, out, args[1:])
	case "inspect":
		return runTargetInspect(ctx, out, args[1:])
	case "list":
		return runTargetList(ctx, out, args[1:])
	case "remove":
		return runTargetRemove(ctx, out, args[1:])
	case "test":
		return runTargetTest(ctx, out, args[1:])
	case "update":
		return runTargetUpdate(ctx, out, args[1:])
	default:
		return fmt.Errorf("unknown target subcommand %q", args[0])
	}
}

func runTargetList(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("target list", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/targets", out)
}

func runTargetInspect(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("target inspect", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "target id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/targets/"+*id, out)
}

func runTargetAdd(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("target add", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "target id")
	name := fs.String("name", "", "target name")
	driver := fs.String("driver", "", "target driver")
	endpoint := fs.String("endpoint", "", "target endpoint")
	database := fs.String("database", "", "database name")
	username := fs.String("user", "", "connection username")
	password := fs.String("password", "", "connection password")
	tlsMode := fs.String("tls", "", "connection TLS mode")
	agentID := fs.String("agent", "", "agent id assigned to this target")
	tier := fs.String("tier", "", "target tier label")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	if *driver == "" {
		return fmt.Errorf("--driver is required")
	}
	if *endpoint == "" {
		return fmt.Errorf("--endpoint is required")
	}
	payload := core.Target{
		ID:       core.ID(*id),
		Name:     *name,
		Driver:   core.TargetDriver(*driver),
		Endpoint: *endpoint,
		Database: *database,
	}
	options := map[string]any{}
	if *username != "" {
		options["username"] = *username
	}
	if *password != "" {
		options["password"] = *password
	}
	if *tlsMode != "" {
		options["tls"] = *tlsMode
	}
	if len(options) > 0 {
		payload.Options = options
	}
	labels := map[string]string{}
	if *agentID != "" {
		labels["agent"] = *agentID
	}
	if *tier != "" {
		labels["tier"] = *tier
	}
	if len(labels) > 0 {
		payload.Labels = labels
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/targets", payload, out)
}

func runTargetUpdate(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("target update", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "target id")
	name := fs.String("name", "", "target name")
	driver := fs.String("driver", "", "target driver")
	endpoint := fs.String("endpoint", "", "target endpoint")
	database := fs.String("database", "", "database name")
	username := fs.String("user", "", "connection username")
	password := fs.String("password", "", "connection password")
	tlsMode := fs.String("tls", "", "connection TLS mode")
	agentID := fs.String("agent", "", "agent id assigned to this target")
	tier := fs.String("tier", "", "target tier label")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	if *driver == "" {
		return fmt.Errorf("--driver is required")
	}
	if *endpoint == "" {
		return fmt.Errorf("--endpoint is required")
	}
	payload := core.Target{
		ID:       core.ID(*id),
		Name:     *name,
		Driver:   core.TargetDriver(*driver),
		Endpoint: *endpoint,
		Database: *database,
	}
	options := map[string]any{}
	if *username != "" {
		options["username"] = *username
	}
	if *password != "" {
		options["password"] = *password
	}
	if *tlsMode != "" {
		options["tls"] = *tlsMode
	}
	if len(options) > 0 {
		payload.Options = options
	}
	labels := map[string]string{}
	if *agentID != "" {
		labels["agent"] = *agentID
	}
	if *tier != "" {
		labels["tier"] = *tier
	}
	if len(labels) > 0 {
		payload.Labels = labels
	}
	return putControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/targets/"+*id, payload, out)
}

func runTargetRemove(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("target remove", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "target id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return deleteControl(ctx, http.DefaultClient, *serverAddr, "/api/v1/targets/"+*id, out)
}

func runTargetTest(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("target test", out)
	name := fs.String("name", "", "target name")
	driverName := fs.String("driver", "", "target driver")
	endpoint := fs.String("endpoint", "", "target endpoint")
	database := fs.String("database", "", "database name")
	username := fs.String("user", "", "connection username")
	password := fs.String("password", "", "connection password")
	tlsMode := fs.String("tls", "", "connection TLS mode")
	timeout := fs.Duration("timeout", 5*time.Second, "connection test timeout")
	positionalName := ""
	parseArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positionalName = args[0]
		parseArgs = args[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return err
	}
	nameValue := *name
	if len(fs.Args()) > 1 {
		return fmt.Errorf("unexpected target test arguments: %v", fs.Args())
	}
	if len(fs.Args()) == 1 {
		if nameValue != "" || positionalName != "" {
			return fmt.Errorf("target name specified more than once")
		}
		nameValue = fs.Args()[0]
	}
	if nameValue == "" {
		nameValue = positionalName
	}
	if *driverName == "" {
		return fmt.Errorf("--driver is required")
	}
	if *endpoint == "" {
		return fmt.Errorf("--endpoint is required")
	}
	registry := drivers.NewRegistry()
	if err := registry.Register(mysqldriver.NewDriver()); err != nil {
		return err
	}
	if err := registry.Register(postgresdriver.NewDriver()); err != nil {
		return err
	}
	if err := registry.Register(redisdriver.NewDriver()); err != nil {
		return err
	}
	driver, ok := registry.Get(*driverName)
	if !ok {
		return fmt.Errorf("target driver %q is not implemented in this build; supported target drivers: %s", *driverName, strings.Join(registry.Names(), ", "))
	}
	target := drivers.Target{
		Name:       nameValue,
		Driver:     *driverName,
		Connection: targetTestConnection(*endpoint, *database, *username, *password),
		Options:    targetTestOptions(*tlsMode),
	}
	testCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	started := time.Now()
	if err := driver.Test(testCtx, target); err != nil {
		return err
	}
	version, versionErr := driver.Version(testCtx, target)
	if version == "" {
		version = "unknown"
	}
	result := map[string]any{
		"ok":              true,
		"driver":          *driverName,
		"endpoint":        *endpoint,
		"version":         version,
		"duration_millis": time.Since(started).Milliseconds(),
	}
	if nameValue != "" {
		result["name"] = nameValue
	}
	if *database != "" {
		result["database"] = *database
	}
	if versionErr != nil {
		result["version_error"] = versionErr.Error()
	}
	return writeCommandJSON(ctx, out, result)
}

func targetTestConnection(endpoint string, database string, username string, password string) map[string]string {
	connection := map[string]string{"addr": endpoint}
	if database != "" {
		connection["database"] = database
	}
	if username != "" {
		connection["username"] = username
	}
	if password != "" {
		connection["password"] = password
	}
	return connection
}

func targetTestOptions(tlsMode string) map[string]string {
	if tlsMode == "" {
		return nil
	}
	return map[string]string{"tls": tlsMode}
}

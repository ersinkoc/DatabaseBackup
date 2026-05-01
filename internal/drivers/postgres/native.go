package postgres

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/kronos/kronos/internal/drivers"
)

const pgSSLRequestCode = 80877103

type pgNativeConfig struct {
	Address         string
	Host            string
	Port            string
	Database        string
	Username        string
	Password        string
	SSLMode         string
	ApplicationName string
}

type pgNativeDialer func(ctx context.Context, network string, address string) (net.Conn, error)

type pgNativeQueryer interface {
	SimpleQuery(ctx context.Context, target drivers.Target, query string) (pgQueryResult, error)
}

type pgNativeRunner struct{}

func (pgNativeRunner) SimpleQuery(ctx context.Context, target drivers.Target, query string) (pgQueryResult, error) {
	return pgNativeSimpleQuery(ctx, target, query)
}

func pgNativeSimpleQuery(ctx context.Context, target drivers.Target, query string) (pgQueryResult, error) {
	cfg, err := pgNativeConfigFromTarget(target)
	if err != nil {
		return pgQueryResult{}, err
	}
	return pgNativeSimpleQueryWithDialer(ctx, cfg, query, defaultPGNativeDialer)
}

func pgNativeSimpleQueryWithDialer(ctx context.Context, cfg pgNativeConfig, query string, dial pgNativeDialer) (pgQueryResult, error) {
	if dial == nil {
		dial = defaultPGNativeDialer
	}
	conn, err := connectPGNative(ctx, cfg, dial)
	if err != nil {
		return pgQueryResult{}, err
	}
	defer conn.Close()

	params := map[string]string{
		"user":     cfg.Username,
		"database": cfg.Database,
	}
	if cfg.ApplicationName != "" {
		params["application_name"] = cfg.ApplicationName
	}
	return pgSimpleQuery(conn, params, cfg.Password, query)
}

func pgNativeConfigFromTarget(target drivers.Target) (pgNativeConfig, error) {
	cfg := pgNativeConfig{
		Host:            "127.0.0.1",
		Port:            "5432",
		Database:        "postgres",
		Username:        "postgres",
		SSLMode:         "disable",
		ApplicationName: "kronos",
	}
	if dsn := strings.TrimSpace(target.Connection["dsn"]); dsn != "" {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return pgNativeConfig{}, fmt.Errorf("parse postgres dsn: %w", err)
		}
		if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
			return pgNativeConfig{}, fmt.Errorf("unsupported postgres dsn scheme %q", parsed.Scheme)
		}
		if host := parsed.Hostname(); host != "" {
			cfg.Host = host
		}
		if port := parsed.Port(); port != "" {
			cfg.Port = port
		}
		if database := strings.TrimPrefix(parsed.EscapedPath(), "/"); database != "" {
			unescaped, err := url.PathUnescape(database)
			if err != nil {
				return pgNativeConfig{}, fmt.Errorf("parse postgres database name: %w", err)
			}
			cfg.Database = unescaped
		}
		if parsed.User != nil {
			if username := parsed.User.Username(); username != "" {
				cfg.Username = username
			}
			if password, ok := parsed.User.Password(); ok {
				cfg.Password = password
			}
		}
		query := parsed.Query()
		if sslMode := strings.TrimSpace(query.Get("sslmode")); sslMode != "" {
			cfg.SSLMode = normalizePGSSLMode(sslMode)
		}
		if app := strings.TrimSpace(query.Get("application_name")); app != "" {
			cfg.ApplicationName = app
		}
	}

	host, port := splitAddress(target.Connection["addr"])
	if host != "" {
		cfg.Host = host
	}
	if port != "" {
		cfg.Port = port
	}
	if value := strings.TrimSpace(target.Connection["host"]); value != "" {
		cfg.Host = value
	}
	if value := strings.TrimSpace(target.Connection["port"]); value != "" {
		cfg.Port = value
	}
	if value := strings.TrimSpace(firstNonEmpty(target.Connection["database"], target.Options["database"])); value != "" {
		cfg.Database = value
	}
	if value := strings.TrimSpace(firstNonEmpty(target.Connection["username"], target.Connection["user"], target.Options["username"], target.Options["user"])); value != "" {
		cfg.Username = value
	}
	if value := firstNonEmpty(target.Connection["password"], target.Options["password"]); strings.TrimSpace(value) != "" {
		cfg.Password = value
	}
	if value := strings.TrimSpace(firstNonEmpty(target.Connection["sslmode"], target.Connection["tls"], target.Options["sslmode"], target.Options["tls"])); value != "" {
		cfg.SSLMode = normalizePGSSLMode(value)
	}
	if value := strings.TrimSpace(firstNonEmpty(target.Connection["application_name"], target.Options["application_name"])); value != "" {
		cfg.ApplicationName = value
	}
	cfg.Address = net.JoinHostPort(cfg.Host, cfg.Port)
	return cfg, nil
}

func connectPGNative(ctx context.Context, cfg pgNativeConfig, dial pgNativeDialer) (net.Conn, error) {
	conn, err := dial(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if pgSSLModeUsesTLS(cfg.SSLMode) {
		tlsConn, err := startPGNativeTLS(ctx, conn, cfg)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		conn = tlsConn
	}
	return conn, nil
}

func startPGNativeTLS(ctx context.Context, conn net.Conn, cfg pgNativeConfig) (net.Conn, error) {
	var request [8]byte
	binary.BigEndian.PutUint32(request[:4], 8)
	binary.BigEndian.PutUint32(request[4:], pgSSLRequestCode)
	if _, err := conn.Write(request[:]); err != nil {
		return nil, err
	}
	var response [1]byte
	if _, err := conn.Read(response[:]); err != nil {
		return nil, err
	}
	if response[0] != 'S' {
		if pgSSLModeRequiresTLS(cfg.SSLMode) {
			return nil, fmt.Errorf("postgres server does not support SSL for sslmode=%s", cfg.SSLMode)
		}
		return conn, nil
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if hostname := cfg.Host; hostname != "" && net.ParseIP(hostname) == nil {
		tlsCfg.ServerName = hostname
	}
	switch cfg.SSLMode {
	case "require", "prefer", "allow":
		tlsCfg.InsecureSkipVerify = true
	}
	tlsConn := tls.Client(conn, tlsCfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	return tlsConn, nil
}

func defaultPGNativeDialer(ctx context.Context, network string, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address)
}

func normalizePGSSLMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "disable", "false", "off":
		return "disable"
	case "true", "on":
		return "require"
	case "allow", "prefer", "require", "verify-ca", "verify-full":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func pgSSLModeUsesTLS(value string) bool {
	switch normalizePGSSLMode(value) {
	case "allow", "prefer", "require", "verify-ca", "verify-full":
		return true
	default:
		return false
	}
}

func pgSSLModeRequiresTLS(value string) bool {
	switch normalizePGSSLMode(value) {
	case "require", "verify-ca", "verify-full":
		return true
	default:
		return false
	}
}

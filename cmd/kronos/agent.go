package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	agentpkg "github.com/kronos/kronos/internal/agent"
	"github.com/kronos/kronos/internal/buildinfo"
	"github.com/kronos/kronos/internal/chunk"
	kcompress "github.com/kronos/kronos/internal/compress"
	kcrypto "github.com/kronos/kronos/internal/crypto"
	"github.com/kronos/kronos/internal/drivers"
	mongodbdriver "github.com/kronos/kronos/internal/drivers/mongodb"
	mysqldriver "github.com/kronos/kronos/internal/drivers/mysql"
	postgresdriver "github.com/kronos/kronos/internal/drivers/postgres"
	redisdriver "github.com/kronos/kronos/internal/drivers/redis"
	"github.com/kronos/kronos/internal/obs"
	control "github.com/kronos/kronos/internal/server"
	"github.com/kronos/kronos/internal/storage"
)

type agentWorkerOptions struct {
	ManifestPrivateKeyHex string
	ChunkKeyHex           string
	ChunkAlgorithm        string
	Compression           string
	KeyID                 string
}

func runAgent(ctx context.Context, out io.Writer, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "inspect":
			return runAgentInspect(ctx, out, args[1:])
		case "list":
			return runAgentList(ctx, out, args[1:])
		}
	}

	fs := newFlagSet("agent", out)
	configPath := fs.String("config", "", "path to kronos YAML config")
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	token := fs.String("token", os.Getenv("KRONOS_TOKEN"), "bearer token for the control plane")
	agentID := fs.String("id", "", "agent identifier")
	capacity := fs.Int("capacity", 1, "maximum concurrent jobs this agent can run")
	interval := fs.Duration("heartbeat-interval", 10*time.Second, "heartbeat interval")
	work := fs.Bool("work", false, "claim and execute jobs instead of heartbeat-only mode")
	tlsCert := fs.String("tls-cert", os.Getenv("KRONOS_TLS_CERT"), "client certificate PEM for mTLS control-plane connections")
	tlsKey := fs.String("tls-key", os.Getenv("KRONOS_TLS_KEY"), "client private key PEM for mTLS control-plane connections")
	tlsCA := fs.String("tls-ca", os.Getenv("KRONOS_TLS_CA"), "CA certificate PEM for verifying the control plane")
	manifestPrivateKey := fs.String("manifest-private-key", os.Getenv("KRONOS_MANIFEST_PRIVATE_KEY"), "hex-encoded Ed25519 private key for manifest signing")
	chunkKey := fs.String("chunk-key", os.Getenv("KRONOS_CHUNK_KEY"), "hex-encoded 32-byte chunk encryption key")
	chunkAlgorithm := fs.String("chunk-algorithm", kcrypto.AlgorithmAES256GCM, "chunk encryption algorithm")
	compression := fs.String("compression", string(kcompress.AlgorithmNone), "chunk compression algorithm")
	keyID := fs.String("key-id", "default", "chunk encryption key id recorded in manifests")
	if err := fs.Parse(args); err != nil {
		return err
	}
	*serverAddr = controlServerAddr(ctx, *serverAddr)
	if *agentID == "" {
		host, err := osHostname()
		if err != nil {
			return err
		}
		*agentID = host
	}
	if *interval <= 0 {
		return fmt.Errorf("--heartbeat-interval must be greater than zero")
	}
	if *capacity <= 0 {
		return fmt.Errorf("--capacity must be greater than zero")
	}
	httpClient, err := controlTLSHTTPClient(*tlsCert, *tlsKey, *tlsCA)
	if err != nil {
		return err
	}

	if *configPath != "" {
		fmt.Fprintf(out, "config=%s\n", *configPath)
	}
	fmt.Fprintf(out, "kronos-agent id=%s server=%s\n", *agentID, *serverAddr)
	heartbeat := control.AgentHeartbeat{
		ID:       *agentID,
		Version:  buildinfo.Version,
		Capacity: *capacity,
	}
	if *work {
		return runAgentWorkerWithToken(ctx, httpClient, *serverAddr, heartbeat, *interval, *token, agentWorkerOptions{
			ManifestPrivateKeyHex: *manifestPrivateKey,
			ChunkKeyHex:           *chunkKey,
			ChunkAlgorithm:        *chunkAlgorithm,
			Compression:           *compression,
			KeyID:                 *keyID,
		})
	}
	if *token != "" {
		return runAgentHeartbeatWithToken(ctx, httpClient, *serverAddr, heartbeat, *interval, *token)
	}
	return runAgentHeartbeat(ctx, httpClient, *serverAddr, heartbeat, *interval)
}

func runAgentList(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("agent list", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	tlsCert := fs.String("tls-cert", os.Getenv("KRONOS_TLS_CERT"), "client certificate PEM for mTLS control-plane connections")
	tlsKey := fs.String("tls-key", os.Getenv("KRONOS_TLS_KEY"), "client private key PEM for mTLS control-plane connections")
	tlsCA := fs.String("tls-ca", os.Getenv("KRONOS_TLS_CA"), "CA certificate PEM for verifying the control plane")
	if err := fs.Parse(args); err != nil {
		return err
	}
	httpClient, err := controlTLSHTTPClient(*tlsCert, *tlsKey, *tlsCA)
	if err != nil {
		return err
	}
	return getControlJSON(ctx, httpClient, *serverAddr, "/api/v1/agents", out)
}

func runAgentInspect(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("agent inspect", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "agent id")
	tlsCert := fs.String("tls-cert", os.Getenv("KRONOS_TLS_CERT"), "client certificate PEM for mTLS control-plane connections")
	tlsKey := fs.String("tls-key", os.Getenv("KRONOS_TLS_KEY"), "client private key PEM for mTLS control-plane connections")
	tlsCA := fs.String("tls-ca", os.Getenv("KRONOS_TLS_CA"), "CA certificate PEM for verifying the control plane")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	httpClient, err := controlTLSHTTPClient(*tlsCert, *tlsKey, *tlsCA)
	if err != nil {
		return err
	}
	return getControlJSON(ctx, httpClient, *serverAddr, "/api/v1/agents/"+*id, out)
}

func controlTLSHTTPClient(certPath string, keyPath string, caPath string) (*http.Client, error) {
	certPath = strings.TrimSpace(certPath)
	keyPath = strings.TrimSpace(keyPath)
	caPath = strings.TrimSpace(caPath)
	if certPath == "" && keyPath == "" && caPath == "" {
		return http.DefaultClient, nil
	}
	if (certPath == "") != (keyPath == "") {
		return nil, fmt.Errorf("--tls-cert and --tls-key are required together")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if certPath != "" {
		certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load agent tls certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	if caPath != "" {
		data, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read --tls-ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("--tls-ca does not contain PEM certificates")
		}
		tlsConfig.RootCAs = pool
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	return &http.Client{Transport: transport}, nil
}

func runAgentHeartbeat(ctx context.Context, client *http.Client, serverAddr string, heartbeat control.AgentHeartbeat, interval time.Duration) error {
	return runAgentHeartbeatWithToken(ctx, client, serverAddr, heartbeat, interval, os.Getenv("KRONOS_TOKEN"))
}

func runAgentHeartbeatWithToken(ctx context.Context, client *http.Client, serverAddr string, heartbeat control.AgentHeartbeat, interval time.Duration, token string) error {
	if client == nil {
		client = http.DefaultClient
	}
	if heartbeat.ID == "" {
		return fmt.Errorf("agent id is required")
	}
	endpoint, err := heartbeatEndpoint(serverAddr)
	if err != nil {
		return err
	}
	if err := postHeartbeat(ctx, client, endpoint, heartbeat, token); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := postHeartbeat(ctx, client, endpoint, heartbeat, token); err != nil {
				return err
			}
		}
	}
}

func runAgentWorkerWithToken(ctx context.Context, httpClient *http.Client, serverAddr string, heartbeat control.AgentHeartbeat, interval time.Duration, token string, opts agentWorkerOptions) error {
	if heartbeat.ID == "" {
		return fmt.Errorf("agent id is required")
	}
	privateKey, err := decodePrivateKey(opts.ManifestPrivateKeyHex)
	if err != nil {
		return err
	}
	cipher, err := cipherFromKey(opts.ChunkKeyHex, opts.ChunkAlgorithm)
	if err != nil {
		return err
	}
	compressor, err := kcompress.New(kcompress.Algorithm(opts.Compression))
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.KeyID) == "" {
		return fmt.Errorf("--key-id is required")
	}
	registry := drivers.NewRegistry()
	if err := registry.Register(mysqldriver.NewDriver()); err != nil {
		return err
	}
	if err := registry.Register(mongodbdriver.NewDriver()); err != nil {
		return err
	}
	if err := registry.Register(postgresdriver.NewDriver()); err != nil {
		return err
	}
	if err := registry.Register(redisdriver.NewDriver()); err != nil {
		return err
	}
	client, err := agentpkg.NewClient(serverAddr, httpClient)
	if err != nil {
		return err
	}
	client.Token = strings.TrimSpace(token)
	executor := &agentpkg.BackupExecutor{
		Drivers:         registry,
		PipelineFactory: agentPipelineFactory(strings.TrimSpace(opts.KeyID), compressor, cipher),
		PrivateKey:      privateKey,
	}
	return agentpkg.Worker{
		Client:         client,
		Executor:       executor,
		Heartbeat:      heartbeat,
		PollInterval:   interval,
		MaxJobsPerTick: heartbeat.Capacity,
	}.Run(ctx)
}

func agentPipelineFactory(keyID string, compressor kcompress.Compressor, cipher kcrypto.Cipher) agentpkg.PipelineFactory {
	return func(backend storage.Backend) (*chunk.Pipeline, error) {
		return &chunk.Pipeline{
			Backend:     backend,
			Compressor:  compressor,
			Cipher:      cipher,
			KeyID:       keyID,
			Concurrency: 0,
		}, nil
	}
}

func decodePrivateKey(value string) (ed25519.PrivateKey, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("--manifest-private-key is required")
	}
	keyBytes, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("decode manifest private key: %w", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("manifest private key must be %d bytes", ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(keyBytes), nil
}

func postHeartbeat(ctx context.Context, client *http.Client, endpoint string, heartbeat control.AgentHeartbeat, token string) error {
	payload, err := json.Marshal(heartbeat)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token = strings.TrimSpace(token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set(obs.RequestIDHeader, agentRequestID(ctx))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("heartbeat failed: %s", resp.Status)
	}
	return nil
}

func agentRequestID(ctx context.Context) string {
	if requestID, ok := obs.RequestIDFromContext(ctx); ok {
		return requestID
	}
	return obs.NewRequestID()
}

func heartbeatEndpoint(serverAddr string) (string, error) {
	if !strings.Contains(serverAddr, "://") {
		serverAddr = "http://" + serverAddr
	}
	u, err := url.Parse(serverAddr)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid server address %q", serverAddr)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/agents/heartbeat"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func osHostname() (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", err
	}
	if host == "" {
		return "", fmt.Errorf("hostname is empty")
	}
	return host, nil
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kronos/kronos/internal/buildinfo"
	"github.com/kronos/kronos/internal/config"
	"github.com/kronos/kronos/internal/secret"
	control "github.com/kronos/kronos/internal/server"
)

func runLocal(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("local", out)
	configPath := fs.String("config", "", "path to kronos YAML config")
	dataDir := fs.String("data-dir", ".kronos", "local data directory")
	listenAddr := fs.String("listen", "127.0.0.1:8500", "local WebUI listen address")
	work := fs.Bool("work", false, "run an embedded worker agent")
	devInsecure := fs.Bool("dev-insecure", false, "allow unauthenticated API requests; development only")
	agentID := fs.String("id", "local", "local worker agent identifier")
	capacity := fs.Int("capacity", 1, "maximum concurrent local worker jobs")
	interval := fs.Duration("heartbeat-interval", 5*time.Second, "local worker heartbeat interval")
	manifestPrivateKey := fs.String("manifest-private-key", "", "hex-encoded Ed25519 private key for manifest signing")
	chunkKey := fs.String("chunk-key", "", "hex-encoded 32-byte chunk encryption key")
	chunkAlgorithm := fs.String("chunk-algorithm", "aes-256-gcm", "chunk encryption algorithm")
	compression := fs.String("compression", "none", "chunk compression algorithm")
	keyID := fs.String("key-id", "default", "chunk encryption key id recorded in manifests")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *capacity <= 0 {
		return fmt.Errorf("--capacity must be greater than zero")
	}
	if *interval <= 0 {
		return fmt.Errorf("--heartbeat-interval must be greater than zero")
	}

	listenSet := false
	dataDirSet := false
	fs.Visit(func(flag *flag.Flag) {
		switch flag.Name {
		case "listen":
			listenSet = true
		case "data-dir":
			dataDirSet = true
		}
	})
	cfg := &config.Config{}
	if *configPath != "" {
		loaded, err := config.LoadFile(ctx, *configPath, secret.NewRegistry())
		if err != nil {
			return err
		}
		cfg = loaded
	}
	if !listenSet && cfg.Server.Listen != "" {
		*listenAddr = cfg.Server.Listen
	}
	if !dataDirSet && cfg.Server.DataDir != "" {
		*dataDir = cfg.Server.DataDir
	}
	cfg.Server.Listen = *listenAddr
	cfg.Server.DataDir = *dataDir
	allowNoAuth := *devInsecure || isLoopbackListenAddress(*listenAddr)
	if !*work {
		return serveControlPlaneWithOptions(ctx, out, *listenAddr, cfg, controlPlaneOptions{InsecureNoAuth: allowNoAuth})
	}

	localCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workerErr := make(chan error, 1)
	startWorker := func(addr string) error {
		serverAddr := addr
		if !strings.Contains(serverAddr, "://") {
			serverAddr = "http://" + serverAddr
		}
		fmt.Fprintf(out, "kronos-local worker=%s\n", *agentID)
		go func() {
			err := runAgentWorkerWithToken(localCtx, http.DefaultClient, serverAddr, control.AgentHeartbeat{
				ID:       *agentID,
				Version:  buildinfo.Version,
				Capacity: *capacity,
			}, *interval, "", agentWorkerOptions{
				ManifestPrivateKeyHex: *manifestPrivateKey,
				ChunkKeyHex:           *chunkKey,
				ChunkAlgorithm:        *chunkAlgorithm,
				Compression:           *compression,
				KeyID:                 *keyID,
			})
			workerErr <- err
			if err != nil && !errors.Is(err, context.Canceled) {
				cancel()
			}
		}()
		return nil
	}
	err := serveControlPlaneWithOptions(localCtx, out, *listenAddr, cfg, controlPlaneOptions{OnListen: startWorker, InsecureNoAuth: allowNoAuth})
	select {
	case workerRunErr := <-workerErr:
		if workerRunErr != nil && !errors.Is(workerRunErr, context.Canceled) {
			return workerRunErr
		}
	default:
	}
	return err
}

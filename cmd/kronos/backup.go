package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kronos/kronos/internal/chunk"
	"github.com/kronos/kronos/internal/core"
	kcrypto "github.com/kronos/kronos/internal/crypto"
	"github.com/kronos/kronos/internal/manifest"
	"github.com/kronos/kronos/internal/obs"
	"github.com/kronos/kronos/internal/repository"
	"github.com/kronos/kronos/internal/storage/local"
	"github.com/kronos/kronos/internal/verify"
)

func runBackup(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("backup subcommand is required")
	}
	switch args[0] {
	case "inspect":
		return runBackupInspect(ctx, out, args[1:])
	case "list":
		return runBackupList(ctx, out, args[1:])
	case "now":
		return runBackupNow(ctx, out, args[1:])
	case "protect":
		return runBackupProtect(ctx, out, args[1:], true)
	case "unprotect":
		return runBackupProtect(ctx, out, args[1:], false)
	case "verify":
		return runBackupVerify(ctx, out, args[1:])
	default:
		return fmt.Errorf("unknown backup subcommand %q", args[0])
	}
}

func runBackupNow(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("backup now", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	targetID := fs.String("target", "", "target id")
	storageID := fs.String("storage", "", "storage id")
	backupType := fs.String("type", string(core.BackupTypeFull), "backup type")
	parentID := fs.String("parent", "", "parent backup id for incremental or differential backups")
	if err := fs.Parse(args); err != nil {
		return err
	}
	typeSet := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == "type" {
			typeSet = true
		}
	})
	if *targetID == "" {
		return fmt.Errorf("--target is required")
	}
	if *storageID == "" {
		return fmt.Errorf("--storage is required")
	}
	if *parentID != "" && !typeSet {
		*backupType = string(core.BackupTypeIncremental)
	}
	payload := map[string]string{
		"target_id":  *targetID,
		"storage_id": *storageID,
		"type":       *backupType,
	}
	if *parentID != "" {
		payload["parent_id"] = *parentID
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/backups/now", payload, out)
}

func runBackupList(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("backup list", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	targetID := fs.String("target", "", "target id filter")
	storageID := fs.String("storage", "", "storage id filter")
	backupType := fs.String("type", "", "backup type filter")
	since := fs.String("since", "", "ended-at lower bound; RFC3339 or duration such as 7d")
	until := fs.String("until", "", "ended-at upper bound; RFC3339 or duration such as 24h")
	protected := fs.String("protected", "", "protected flag filter: true or false")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateListTimeFlags(*since, *until); err != nil {
		return err
	}
	query := url.Values{}
	query.Set("target_id", *targetID)
	query.Set("storage_id", *storageID)
	query.Set("type", *backupType)
	query.Set("since", *since)
	query.Set("until", *until)
	query.Set("protected", *protected)
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, pathWithQuery("/api/v1/backups", query), out)
}

func runBackupInspect(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("backup inspect", out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "backup id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return getControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/backups/"+*id, out)
}

func pathWithQuery(path string, query url.Values) string {
	clean := url.Values{}
	for key, values := range query {
		for _, value := range values {
			if value != "" {
				clean.Add(key, value)
			}
		}
	}
	if len(clean) == 0 {
		return path
	}
	return path + "?" + clean.Encode()
}

func validateListTimeFlags(sinceText string, untilText string) error {
	if sinceText == "" || untilText == "" {
		return nil
	}
	now := time.Now().UTC()
	since, err := parseBackupListTime(sinceText, now)
	if err != nil {
		return fmt.Errorf("invalid --since: %w", err)
	}
	until, err := parseBackupListTime(untilText, now)
	if err != nil {
		return fmt.Errorf("invalid --until: %w", err)
	}
	return validateTimeRange(since, until)
}

func runBackupProtect(ctx context.Context, out io.Writer, args []string, protected bool) error {
	name := "backup protect"
	action := "protect"
	if !protected {
		name = "backup unprotect"
		action = "unprotect"
	}
	fs := newFlagSet(name, out)
	serverAddr := fs.String("server", "127.0.0.1:8500", "server address")
	id := fs.String("id", "", "backup id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	return postControlJSON(ctx, http.DefaultClient, *serverAddr, "/api/v1/backups/"+*id+"/"+action, nil, out)
}

func runBackupVerify(ctx context.Context, out io.Writer, args []string) error {
	fs := newFlagSet("backup verify", out)
	manifestPath := fs.String("manifest", "", "manifest JSON path")
	manifestKey := fs.String("manifest-key", "", "manifest object key in storage")
	level := fs.String("level", "manifest", "verification level: manifest or chunk")
	chunkKeyHex := fs.String("chunk-key", "", "hex-encoded 32-byte chunk decryption key for --level chunk")
	publicKeyHex := fs.String("public-key", "", "hex-encoded Ed25519 public key")
	localRoot := fs.String("storage-local", "", "local storage repository root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*manifestPath == "") == (*manifestKey == "") {
		return fmt.Errorf("exactly one of --manifest or --manifest-key is required")
	}
	if *publicKeyHex == "" {
		return fmt.Errorf("--public-key is required")
	}
	if *localRoot == "" {
		return fmt.Errorf("--storage-local is required")
	}

	publicKey, err := decodePublicKey(*publicKeyHex)
	if err != nil {
		return err
	}
	backend, err := local.New("local", *localRoot)
	if err != nil {
		return err
	}
	var m manifest.Manifest
	if *manifestKey != "" {
		m, _, err = repository.LoadManifest(ctx, backend, *manifestKey, publicKey)
		if err != nil {
			return err
		}
	} else {
		data, err := readFileBounded(*manifestPath, 64*1024*1024)
		if err != nil {
			return err
		}
		m, err = manifest.Parse(data)
		if err != nil {
			return err
		}
	}
	switch *level {
	case "manifest":
		report, err := verify.Manifest(ctx, backend, m, publicKey)
		if err != nil {
			return err
		}
		return writeCommandJSON(ctx, out, map[string]any{
			"ok":           true,
			"level":        "manifest",
			"objects":      report.Objects,
			"chunks":       report.Chunks,
			"stored_bytes": report.StoredBytes,
		})
	case "chunk":
		cipher, err := cipherFromKey(*chunkKeyHex, m.Encryption.Algorithm)
		if err != nil {
			return err
		}
		report, err := verify.Chunks(ctx, &chunk.Pipeline{Backend: backend, Cipher: cipher}, m, publicKey)
		if err != nil {
			return err
		}
		return writeCommandJSON(ctx, out, map[string]any{
			"ok":              true,
			"level":           "chunk",
			"objects":         report.Objects,
			"chunks":          report.Chunks,
			"verified_chunks": report.VerifiedChunks,
			"stored_bytes":    report.StoredBytes,
			"restored_bytes":  report.RestoredBytes,
		})
	default:
		return fmt.Errorf("unknown verification level %q", *level)
	}
}

func postControlJSON(ctx context.Context, client *http.Client, serverAddr string, path string, payload any, out io.Writer) error {
	serverAddr = controlServerAddr(ctx, serverAddr)
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return err
	}
	endpoint, err := controlEndpoint(serverAddr, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	setControlAuth(ctx, req)
	return doControlRequest(client, req, out)
}

func putControlJSON(ctx context.Context, client *http.Client, serverAddr string, path string, payload any, out io.Writer) error {
	serverAddr = controlServerAddr(ctx, serverAddr)
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return err
	}
	endpoint, err := controlEndpoint(serverAddr, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	setControlAuth(ctx, req)
	return doControlRequest(client, req, out)
}

func getControlJSON(ctx context.Context, client *http.Client, serverAddr string, path string, out io.Writer) error {
	serverAddr = controlServerAddr(ctx, serverAddr)
	endpoint, err := controlEndpoint(serverAddr, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	setControlAuth(ctx, req)
	return doControlRequest(client, req, out)
}

func deleteControl(ctx context.Context, client *http.Client, serverAddr string, path string, out io.Writer) error {
	serverAddr = controlServerAddr(ctx, serverAddr)
	endpoint, err := controlEndpoint(serverAddr, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	setControlAuth(ctx, req)
	return doControlRequest(client, req, out)
}

func controlServerAddr(ctx context.Context, fallback string) string {
	if opts, ok := ctx.Value(cliOptionsKey{}).(cliOptions); ok && opts.Server != "" && fallback == "127.0.0.1:8500" {
		return opts.Server
	}
	return fallback
}

func setControlAuth(ctx context.Context, req *http.Request) {
	token := controlToken(ctx)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	setControlRequestID(ctx, req)
}

func setControlRequestID(ctx context.Context, req *http.Request) {
	if req == nil {
		return
	}
	if requestID, ok := obs.RequestIDFromContext(ctx); ok {
		req.Header.Set(obs.RequestIDHeader, requestID)
	}
}

func controlToken(ctx context.Context) string {
	if opts, ok := ctx.Value(cliOptionsKey{}).(cliOptions); ok && opts.Token != "" {
		return opts.Token
	}
	return os.Getenv("KRONOS_TOKEN")
}

func controlOutput(ctx context.Context) string {
	if opts, ok := ctx.Value(cliOptionsKey{}).(cliOptions); ok && opts.Output != "" {
		return opts.Output
	}
	return "json"
}

func doControlRequest(client *http.Client, req *http.Request, out io.Writer) error {
	if client == nil {
		client = http.DefaultClient
	}
	if req.Header.Get("Authorization") == "" {
		if token := strings.TrimSpace(os.Getenv("KRONOS_TOKEN")); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(io.LimitReader(resp.Body, 16*1024*1024)); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s failed: %s%s: %s", req.Method, req.URL.Path, resp.Status, responseRequestID(resp), strings.TrimSpace(body.String()))
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if controlOutput(req.Context()) != "json" {
		data, err := formatStructuredJSONBytes(req.Context(), body.Bytes())
		if err != nil {
			return err
		}
		body.Reset()
		body.Write(data)
	}
	if _, err := out.Write(body.Bytes()); err != nil {
		return err
	}
	if body.Len() == 0 || body.Bytes()[body.Len()-1] != '\n' {
		_, err = fmt.Fprintln(out)
		return err
	}
	return nil
}

func responseRequestID(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	if requestID := strings.TrimSpace(resp.Header.Get(obs.RequestIDHeader)); requestID != "" {
		return " request_id=" + requestID
	}
	return ""
}

func controlEndpoint(serverAddr string, apiPath string) (string, error) {
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
	parsedPath, err := url.Parse(apiPath)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + parsedPath.Path
	u.RawQuery = parsedPath.RawQuery
	u.Fragment = ""
	return u.String(), nil
}

func cipherFromKey(keyHex string, algorithm string) (kcrypto.Cipher, error) {
	if keyHex == "" {
		return nil, fmt.Errorf("--chunk-key is required")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("decode chunk key: %w", err)
	}
	switch algorithm {
	case kcrypto.AlgorithmAES256GCM:
		return kcrypto.NewAES256GCM(key)
	case kcrypto.AlgorithmChaCha20Poly1305:
		return kcrypto.NewChaCha20Poly1305(key)
	default:
		return nil, fmt.Errorf("unsupported manifest encryption algorithm %q", algorithm)
	}
}

func readFileBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var buf bytes.Buffer
	written, err := io.Copy(&buf, io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if written > limit {
		return nil, fmt.Errorf("file %q exceeds %d bytes", path, limit)
	}
	return buf.Bytes(), nil
}

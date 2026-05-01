package sftp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/storage"
)

const defaultPageSize = 1000

// Config configures an SFTP backend.
type Config struct {
	Name                  string
	Address               string
	Root                  string
	Username              string
	Password              string
	PrivateKeyPEM         string
	PrivateKeyPath        string
	Passphrase            string
	AgentSocket           string
	KnownHostsPath        string
	InsecureIgnoreHostKey bool
	Timeout               time.Duration
	Dial                  func(ctx context.Context, network string, address string, config *ssh.ClientConfig) (*ssh.Client, error)
}

// Backend stores objects on an SFTP server.
type Backend struct {
	name   string
	root   string
	client *pkgsftp.Client
}

var _ storage.Backend = (*Backend)(nil)

// New returns a connected SFTP storage backend.
func New(ctx context.Context, cfg Config) (*Backend, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(cfg.Name) == "" {
		cfg.Name = "sftp"
	}
	if strings.TrimSpace(cfg.Address) == "" {
		return nil, fmt.Errorf("sftp address is required")
	}
	if !strings.Contains(cfg.Address, ":") {
		cfg.Address += ":22"
	}
	if strings.TrimSpace(cfg.Username) == "" {
		return nil, fmt.Errorf("sftp username is required")
	}
	auth, err := authMethods(cfg)
	if err != nil {
		return nil, err
	}
	if len(auth) == 0 {
		return nil, fmt.Errorf("sftp authentication requires password, private_key, private_key_path, or agent_socket")
	}
	hostKey, err := hostKeyCallback(cfg)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	sshConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            auth,
		HostKeyCallback: hostKey,
		Timeout:         timeout,
	}
	dial := cfg.Dial
	if dial == nil {
		dial = dialSSH
	}
	sshClient, err := dial(ctx, "tcp", cfg.Address, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("connect sftp: %w", err)
	}
	client, err := pkgsftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("open sftp session: %w", err)
	}
	root := path.Clean("/" + strings.TrimPrefix(cfg.Root, "/"))
	if root == "." {
		root = "/"
	}
	return &Backend{name: cfg.Name, root: root, client: client}, nil
}

// Name returns the configured backend name.
func (b *Backend) Name() string {
	return b.name
}

// Put stores key atomically by writing a temporary remote file and renaming it.
func (b *Backend) Put(ctx context.Context, key string, r io.Reader, size int64) (storage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return storage.ObjectInfo{}, err
	}
	finalPath, err := b.pathForKey(key)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	if _, err := b.client.Stat(finalPath); err == nil {
		return storage.ObjectInfo{}, core.WrapKind(core.ErrorKindConflict, "put sftp object", fmt.Errorf("object %q already exists", key))
	} else if !isNotExist(err) {
		return storage.ObjectInfo{}, err
	}
	parent := path.Dir(finalPath)
	if err := b.client.MkdirAll(parent); err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("create sftp object parent: %w", err)
	}
	tmpPath := path.Join(parent, "."+path.Base(finalPath)+"."+randomSuffix()+".tmp")
	file, err := b.client.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("create sftp temp object: %w", err)
	}
	hash := sha256.New()
	written, copyErr := copyWithContext(ctx, io.MultiWriter(file, hash), r)
	closeErr := file.Close()
	if copyErr != nil {
		_ = b.client.Remove(tmpPath)
		return storage.ObjectInfo{}, copyErr
	}
	if closeErr != nil {
		_ = b.client.Remove(tmpPath)
		return storage.ObjectInfo{}, fmt.Errorf("close sftp temp object: %w", closeErr)
	}
	if size >= 0 && written != size {
		_ = b.client.Remove(tmpPath)
		return storage.ObjectInfo{}, fmt.Errorf("size mismatch for %q: wrote %d bytes, expected %d", key, written, size)
	}
	if err := b.client.Rename(tmpPath, finalPath); err != nil {
		_ = b.client.Remove(tmpPath)
		return storage.ObjectInfo{}, fmt.Errorf("commit sftp object: %w", err)
	}
	info, err := b.client.Stat(finalPath)
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("stat committed sftp object: %w", err)
	}
	object := objectInfo(key, info)
	object.ETag = hex.EncodeToString(hash.Sum(nil))
	return object, nil
}

// Get returns a full object stream.
func (b *Backend) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	objectPath, err := b.pathForKey(key)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	file, err := b.client.Open(objectPath)
	if err != nil {
		if isNotExist(err) {
			return nil, storage.ObjectInfo{}, core.WrapKind(core.ErrorKindNotFound, "get sftp object", fmt.Errorf("object %q not found", key))
		}
		return nil, storage.ObjectInfo{}, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, storage.ObjectInfo{}, err
	}
	return file, objectInfo(key, info), nil
}

// GetRange returns a range stream from key.
func (b *Backend) GetRange(ctx context.Context, key string, off, length int64) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if off < 0 || length < 0 {
		return nil, fmt.Errorf("invalid range off=%d length=%d", off, length)
	}
	objectPath, err := b.pathForKey(key)
	if err != nil {
		return nil, err
	}
	file, err := b.client.Open(objectPath)
	if err != nil {
		if isNotExist(err) {
			return nil, core.WrapKind(core.ErrorKindNotFound, "get sftp object range", fmt.Errorf("object %q not found", key))
		}
		return nil, err
	}
	if _, err := file.Seek(off, io.SeekStart); err != nil {
		file.Close()
		return nil, err
	}
	return rangedReadCloser{reader: io.LimitReader(file, length), closer: file}, nil
}

// Head returns object metadata.
func (b *Backend) Head(ctx context.Context, key string) (storage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return storage.ObjectInfo{}, err
	}
	objectPath, err := b.pathForKey(key)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	info, err := b.client.Stat(objectPath)
	if err != nil {
		if isNotExist(err) {
			return storage.ObjectInfo{}, core.WrapKind(core.ErrorKindNotFound, "head sftp object", fmt.Errorf("object %q not found", key))
		}
		return storage.ObjectInfo{}, err
	}
	return objectInfo(key, info), nil
}

// Exists reports whether key exists.
func (b *Backend) Exists(ctx context.Context, key string) (bool, error) {
	_, err := b.Head(ctx, key)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, core.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// Delete removes key. Missing keys are ignored.
func (b *Backend) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	objectPath, err := b.pathForKey(key)
	if err != nil {
		return err
	}
	if err := b.client.Remove(objectPath); err != nil && !isNotExist(err) {
		return err
	}
	return nil
}

// List returns objects matching prefix after token, ordered lexicographically.
func (b *Backend) List(ctx context.Context, prefix string, token string) (storage.ListPage, error) {
	if err := ctx.Err(); err != nil {
		return storage.ListPage{}, err
	}
	var objects []storage.ObjectInfo
	walker := b.client.Walk(b.root)
	for walker.Step() {
		if err := ctx.Err(); err != nil {
			return storage.ListPage{}, err
		}
		if err := walker.Err(); err != nil {
			return storage.ListPage{}, err
		}
		info := walker.Stat()
		if info == nil || info.IsDir() {
			continue
		}
		key := strings.TrimPrefix(walker.Path(), strings.TrimRight(b.root, "/")+"/")
		key = strings.TrimPrefix(key, "/")
		name := path.Base(key)
		if strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, ".lock") {
			continue
		}
		if !strings.HasPrefix(key, prefix) || (token != "" && key <= token) {
			continue
		}
		objects = append(objects, objectInfo(key, info))
	}
	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})
	if len(objects) <= defaultPageSize {
		return storage.ListPage{Objects: objects}, nil
	}
	next := objects[defaultPageSize-1].Key
	return storage.ListPage{Objects: objects[:defaultPageSize], NextToken: next}, nil
}

func (b *Backend) pathForKey(key string) (string, error) {
	cleaned := path.Clean(key)
	if key == "" || cleaned == "." || path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", storage.InvalidKeyError{Key: key}
	}
	if strings.Contains(key, "\\") {
		return "", storage.InvalidKeyError{Key: key}
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == ".." {
			return "", storage.InvalidKeyError{Key: key}
		}
	}
	return path.Join(b.root, cleaned), nil
}

func authMethods(cfg Config) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if cfg.Password != "" {
		methods = append(methods, ssh.Password(cfg.Password))
	}
	privateKey := strings.TrimSpace(cfg.PrivateKeyPEM)
	if privateKey == "" && strings.TrimSpace(cfg.PrivateKeyPath) != "" {
		data, err := os.ReadFile(cfg.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read sftp private key: %w", err)
		}
		privateKey = string(data)
	}
	if privateKey != "" {
		signer, err := parsePrivateKey([]byte(privateKey), []byte(cfg.Passphrase))
		if err != nil {
			return nil, err
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if strings.TrimSpace(cfg.AgentSocket) != "" {
		conn, err := net.Dial("unix", cfg.AgentSocket)
		if err != nil {
			return nil, fmt.Errorf("connect ssh agent: %w", err)
		}
		methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
	}
	return methods, nil
}

func parsePrivateKey(key []byte, passphrase []byte) (ssh.Signer, error) {
	if len(passphrase) > 0 {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(key, passphrase)
		if err == nil {
			return signer, nil
		}
		return nil, fmt.Errorf("parse encrypted sftp private key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("parse sftp private key: %w", err)
	}
	return signer, nil
}

func hostKeyCallback(cfg Config) (ssh.HostKeyCallback, error) {
	if cfg.InsecureIgnoreHostKey {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	if strings.TrimSpace(cfg.KnownHostsPath) == "" {
		return nil, fmt.Errorf("sftp known_hosts path is required unless insecure_ignore_host_key is true")
	}
	callback, err := knownhosts.New(cfg.KnownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("load sftp known_hosts: %w", err)
	}
	return callback, nil
}

func dialSSH(ctx context.Context, network string, address string, config *ssh.ClientConfig) (*ssh.Client, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	clientConn, chans, reqs, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(clientConn, chans, reqs), nil
}

func isNotExist(err error) bool {
	return err != nil && (errors.Is(err, os.ErrNotExist) || os.IsNotExist(err))
}

func objectInfo(key string, info os.FileInfo) storage.ObjectInfo {
	return storage.ObjectInfo{
		Key:       key,
		Size:      info.Size(),
		UpdatedAt: info.ModTime().UTC(),
	}
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 128*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, nil
			}
			return written, er
		}
	}
}

func randomSuffix() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return hex.EncodeToString(sum[:8])
}

type rangedReadCloser struct {
	reader io.Reader
	closer io.Closer
}

func (r rangedReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r rangedReadCloser) Close() error {
	return r.closer.Close()
}

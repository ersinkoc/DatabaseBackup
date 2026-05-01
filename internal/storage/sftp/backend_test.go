package sftp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/storage"
	"github.com/kronos/kronos/internal/storage/storagetest"
)

func TestBackendConformance(t *testing.T) {
	t.Parallel()

	storagetest.RunBackendConformance(t, func(t *testing.T) storage.Backend {
		t.Helper()
		server := newTestSFTPServer(t)
		knownHosts := filepath.Join(t.TempDir(), "known_hosts")
		if err := os.WriteFile(knownHosts, []byte(knownhosts.Line([]string{server.address}, server.publicKey)+"\n"), 0o600); err != nil {
			t.Fatalf("write known_hosts: %v", err)
		}
		backend, err := New(context.Background(), Config{
			Name:           "test-sftp",
			Address:        server.address,
			Root:           filepath.ToSlash(filepath.Join(server.root, "repo")),
			Username:       "backup",
			Password:       "secret",
			KnownHostsPath: knownHosts,
			Timeout:        5 * time.Second,
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		t.Cleanup(func() { _ = backend.client.Close() })
		return backend
	}, storagetest.ConformanceOptions{InvalidKeyErrors: true})
}

func TestPathForKeyRejectsTraversal(t *testing.T) {
	t.Parallel()

	backend := &Backend{root: "/repo"}
	if got, err := backend.pathForKey("data/chunk"); err != nil || got != "/repo/data/chunk" {
		t.Fatalf("pathForKey(valid) = %q, %v", got, err)
	}
	for _, key := range []string{"", ".", "/abs", "../escape", "a/../b", `a\b`} {
		_, err := backend.pathForKey(key)
		var invalid storage.InvalidKeyError
		if !errors.As(err, &invalid) {
			t.Fatalf("pathForKey(%q) error = %v, want InvalidKeyError", key, err)
		}
	}
}

func TestNewRequiresSecureHostKeyPolicy(t *testing.T) {
	t.Parallel()

	_, err := New(context.Background(), Config{
		Address:  "example.com:22",
		Username: "backup",
		Password: "secret",
	})
	if err == nil {
		t.Fatal("New() error = nil, want known_hosts requirement")
	}
	if !strings.Contains(err.Error(), "known_hosts") {
		t.Fatalf("New() error = %v, want known_hosts context", err)
	}
}

func TestNewRequiresAuthentication(t *testing.T) {
	t.Parallel()

	_, err := New(context.Background(), Config{
		Address:               "example.com:22",
		Username:              "backup",
		InsecureIgnoreHostKey: true,
	})
	if err == nil {
		t.Fatal("New() error = nil, want authentication requirement")
	}
	if !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("New() error = %v, want authentication context", err)
	}
}

func TestBackendAgainstRealSFTPServer(t *testing.T) {
	t.Parallel()

	server := newTestSFTPServer(t)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(knownhosts.Line([]string{server.address}, server.publicKey)+"\n"), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}

	backend, err := New(context.Background(), Config{
		Name:           "test-sftp",
		Address:        server.address,
		Root:           filepath.ToSlash(filepath.Join(server.root, "repo")),
		Username:       "backup",
		Password:       "secret",
		KnownHostsPath: knownHosts,
		Timeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.client.Close() })

	payload := []byte("kronos sftp backend conformance payload")
	info, err := backend.Put(context.Background(), "data/object.txt", bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if info.Key != "data/object.txt" || info.Size != int64(len(payload)) || info.ETag == "" {
		t.Fatalf("Put() info = %#v", info)
	}
	if _, err := backend.Put(context.Background(), "data/object.txt", bytes.NewReader(payload), int64(len(payload))); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("Put(conflict) error = %v, want ErrConflict", err)
	}

	stream, gotInfo, err := backend.Get(context.Background(), "data/object.txt")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	got, err := io.ReadAll(stream)
	closeErr := stream.Close()
	if err != nil {
		t.Fatalf("ReadAll(Get()) error = %v", err)
	}
	if closeErr != nil {
		t.Fatalf("Close(Get()) error = %v", closeErr)
	}
	if !bytes.Equal(got, payload) || gotInfo.Size != int64(len(payload)) {
		t.Fatalf("Get() = %q %#v", got, gotInfo)
	}

	rangeStream, err := backend.GetRange(context.Background(), "data/object.txt", 7, 4)
	if err != nil {
		t.Fatalf("GetRange() error = %v", err)
	}
	gotRange, err := io.ReadAll(rangeStream)
	closeErr = rangeStream.Close()
	if err != nil {
		t.Fatalf("ReadAll(GetRange()) error = %v", err)
	}
	if closeErr != nil {
		t.Fatalf("Close(GetRange()) error = %v", closeErr)
	}
	if string(gotRange) != "sftp" {
		t.Fatalf("GetRange() = %q, want sftp", gotRange)
	}

	head, err := backend.Head(context.Background(), "data/object.txt")
	if err != nil {
		t.Fatalf("Head() error = %v", err)
	}
	if head.Size != int64(len(payload)) {
		t.Fatalf("Head() = %#v", head)
	}
	exists, err := backend.Exists(context.Background(), "data/object.txt")
	if err != nil || !exists {
		t.Fatalf("Exists(existing) = %v, %v; want true, nil", exists, err)
	}
	missing, err := backend.Exists(context.Background(), "data/missing.txt")
	if err != nil || missing {
		t.Fatalf("Exists(missing) = %v, %v; want false, nil", missing, err)
	}

	page, err := backend.List(context.Background(), "data/", "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "data/object.txt" {
		t.Fatalf("List() = %#v", page)
	}
	if err := backend.Delete(context.Background(), "data/object.txt"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	exists, err = backend.Exists(context.Background(), "data/object.txt")
	if err != nil || exists {
		t.Fatalf("Exists(deleted) = %v, %v; want false, nil", exists, err)
	}
}

type testSFTPServer struct {
	address   string
	publicKey ssh.PublicKey
	listener  net.Listener
	root      string
	done      chan struct{}
}

func newTestSFTPServer(t *testing.T) *testSFTPServer {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}
	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if conn.User() == "backup" && string(password) == "secret" {
				return nil, nil
			}
			return nil, errors.New("invalid credentials")
		},
	}
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	root := t.TempDir()
	server := &testSFTPServer{
		address:   listener.Addr().String(),
		publicKey: signer.PublicKey(),
		listener:  listener,
		root:      root,
		done:      make(chan struct{}),
	}
	go server.serve(config, root)
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-server.done:
		case <-time.After(5 * time.Second):
			t.Fatal("sftp test server did not stop")
		}
	})
	return server
}

func (s *testSFTPServer) serve(config *ssh.ServerConfig, root string) {
	defer close(s.done)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go serveSFTPConn(conn, config, root)
	}
}

func serveSFTPConn(conn net.Conn, config *ssh.ServerConfig, root string) {
	sshConn, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		_ = conn.Close()
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(requests)
	for channel := range channels {
		if channel.ChannelType() != "session" {
			_ = channel.Reject(ssh.UnknownChannelType, "session required")
			continue
		}
		accepted, requests, err := channel.Accept()
		if err != nil {
			continue
		}
		go serveSFTPSession(accepted, requests, root)
	}
}

type subsystemRequest struct {
	Name string
}

func serveSFTPSession(channel ssh.Channel, requests <-chan *ssh.Request, root string) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "subsystem" {
			_ = request.Reply(false, nil)
			continue
		}
		var payload subsystemRequest
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil || payload.Name != "sftp" {
			_ = request.Reply(false, nil)
			continue
		}
		_ = request.Reply(true, nil)
		server, err := pkgsftp.NewServer(channel)
		if err != nil {
			return
		}
		_ = server.Serve()
		_ = server.Close()
		return
	}
}

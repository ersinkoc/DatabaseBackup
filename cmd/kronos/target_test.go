package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestRunTargetAddListRemove(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/targets":
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"targets":[{"id":"target-1","name":"redis"}]}`)
			case http.MethodPost:
				defer r.Body.Close()
				var body bytes.Buffer
				if _, err := body.ReadFrom(r.Body); err != nil {
					t.Fatalf("ReadFrom(request) error = %v", err)
				}
				text := body.String()
				if !strings.Contains(text, `"id":"target-1"`) || !strings.Contains(text, `"driver":"redis"`) || !strings.Contains(text, `"endpoint":"127.0.0.1:6379"`) || !strings.Contains(text, `"database":"0"`) || !strings.Contains(text, `"username":"backup"`) || !strings.Contains(text, `"password":"secret"`) || !strings.Contains(text, `"tls":"disable"`) || !strings.Contains(text, `"agent":"agent-1"`) || !strings.Contains(text, `"tier":"tier0"`) {
					t.Fatalf("target add request = %q", text)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"target-1","name":"redis"}`)
			default:
				http.Error(w, "bad method", http.StatusMethodNotAllowed)
			}
		case "/api/v1/targets/target-1":
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"target-1","name":"redis","driver":"redis"}`)
			case http.MethodPut:
				defer r.Body.Close()
				var body bytes.Buffer
				if _, err := body.ReadFrom(r.Body); err != nil {
					t.Fatalf("ReadFrom(update request) error = %v", err)
				}
				text := body.String()
				if !strings.Contains(text, `"name":"redis-prod"`) || !strings.Contains(text, `"endpoint":"127.0.0.1:6380"`) || !strings.Contains(text, `"agent":"agent-2"`) {
					t.Fatalf("target update request = %q", text)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"target-1","name":"redis-prod","driver":"redis"}`)
			case http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("target method = %s", r.Method)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{
		"target", "add", "--server", server.URL, "--id", "target-1", "--name", "redis", "--driver", "redis", "--endpoint", "127.0.0.1:6379", "--database", "0", "--user", "backup", "--password", "secret", "--tls", "disable", "--agent", "agent-1", "--tier", "tier0",
	}); err != nil {
		t.Fatalf("target add error = %v", err)
	}
	if !strings.Contains(out.String(), `"id":"target-1"`) {
		t.Fatalf("target add output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"target", "list", "--server", server.URL}); err != nil {
		t.Fatalf("target list error = %v", err)
	}
	if !strings.Contains(out.String(), `"targets":[{"id":"target-1"`) {
		t.Fatalf("target list output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"target", "inspect", "--server", server.URL, "--id", "target-1"}); err != nil {
		t.Fatalf("target inspect error = %v", err)
	}
	if !strings.Contains(out.String(), `"driver":"redis"`) {
		t.Fatalf("target inspect output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"target", "update", "--server", server.URL, "--id", "target-1", "--name", "redis-prod", "--driver", "redis", "--endpoint", "127.0.0.1:6380", "--agent", "agent-2"}); err != nil {
		t.Fatalf("target update error = %v", err)
	}
	if !strings.Contains(out.String(), `"name":"redis-prod"`) {
		t.Fatalf("target update output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"target", "remove", "--server", server.URL, "--id", "target-1"}); err != nil {
		t.Fatalf("target remove error = %v", err)
	}
	if out.String() != "" {
		t.Fatalf("target remove output = %q, want empty", out.String())
	}
}

func TestRunTargetAddRequiresFields(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"target", "add", "--driver", "redis", "--endpoint", "127.0.0.1:6379"}); err == nil {
		t.Fatal("target add without name error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"target", "add", "--name", "redis", "--endpoint", "127.0.0.1:6379"}); err == nil {
		t.Fatal("target add without driver error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"target", "remove"}); err == nil {
		t.Fatal("target remove without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"target", "inspect"}); err == nil {
		t.Fatal("target inspect without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"target", "update", "--name", "redis", "--driver", "redis", "--endpoint", "127.0.0.1:6379"}); err == nil {
		t.Fatal("target update without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"target", "test", "--endpoint", "127.0.0.1:6379"}); err == nil {
		t.Fatal("target test without driver error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"target", "test", "--driver", "redis"}); err == nil {
		t.Fatal("target test without endpoint error = nil, want error")
	}
}

func TestRunTargetTestRejectsUnimplementedDrivers(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := run(context.Background(), &out, []string{"target", "test", "--driver", "mongodb", "--endpoint", "127.0.0.1:27017"})
	if err == nil {
		t.Fatal("target test mongodb error = nil, want unsupported driver error")
	}
	text := err.Error()
	if !strings.Contains(text, "not implemented") || !strings.Contains(text, "supported target drivers: mysql, postgres, redis") {
		t.Fatalf("target test error = %q", text)
	}
}

func TestRunTargetTestRedis(t *testing.T) {
	t.Parallel()

	endpoint := startRedisProbeServer(t)
	var out bytes.Buffer
	err := run(context.Background(), &out, []string{
		"target", "test",
		"redis-local",
		"--driver", "redis",
		"--endpoint", endpoint,
		"--database", "0",
		"--timeout", "2s",
	})
	if err != nil {
		t.Fatalf("target test error = %v", err)
	}
	text := out.String()
	if !strings.Contains(text, `"ok":true`) || !strings.Contains(text, `"driver":"redis"`) || !strings.Contains(text, `"version":"7.2.0"`) {
		t.Fatalf("target test output = %q", text)
	}
}

func startRedisProbeServer(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleRedisProbeConn(t, conn)
		}
	}()
	return listener.Addr().String()
}

func handleRedisProbeConn(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()

	reader := bufio.NewReader(conn)
	for {
		command, err := readRedisProbeCommand(reader)
		if err != nil {
			if err != io.EOF {
				t.Errorf("readRedisProbeCommand() error = %v", err)
			}
			return
		}
		if len(command) == 0 {
			continue
		}
		switch strings.ToUpper(command[0]) {
		case "HELLO":
			if _, err := fmt.Fprint(conn, "*2\r\n$6\r\nserver\r\n$5\r\nredis\r\n"); err != nil {
				t.Errorf("Write(HELLO) error = %v", err)
				return
			}
		case "PING":
			if _, err := fmt.Fprint(conn, "+PONG\r\n"); err != nil {
				t.Errorf("Write(PING) error = %v", err)
				return
			}
		case "INFO":
			body := "# Server\r\nredis_version:7.2.0\r\n"
			if _, err := fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(body), body); err != nil {
				t.Errorf("Write(INFO) error = %v", err)
				return
			}
		default:
			if _, err := fmt.Fprintf(conn, "-ERR unsupported command %s\r\n", command[0]); err != nil {
				t.Errorf("Write(error) error = %v", err)
			}
			return
		}
	}
}

func readRedisProbeCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("RESP array expected, got %q", line)
	}
	count, err := strconv.Atoi(strings.TrimPrefix(line, "*"))
	if err != nil {
		return nil, err
	}
	command := make([]string, 0, count)
	for i := 0; i < count; i++ {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		header = strings.TrimSuffix(header, "\r\n")
		if !strings.HasPrefix(header, "$") {
			return nil, fmt.Errorf("RESP bulk expected, got %q", header)
		}
		size, err := strconv.Atoi(strings.TrimPrefix(header, "$"))
		if err != nil {
			return nil, err
		}
		data := make([]byte, size+2)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		if string(data[size:]) != "\r\n" {
			return nil, fmt.Errorf("RESP bulk missing CRLF terminator")
		}
		command = append(command, string(data[:size]))
	}
	return command, nil
}

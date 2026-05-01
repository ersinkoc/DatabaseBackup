package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kronos/kronos/internal/storage/local"
)

func TestRunStorageAddListRemove(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/storages":
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"storages":[{"id":"storage-1","name":"local"}]}`)
			case http.MethodPost:
				defer r.Body.Close()
				var body bytes.Buffer
				if _, err := body.ReadFrom(r.Body); err != nil {
					t.Fatalf("ReadFrom(request) error = %v", err)
				}
				text := body.String()
				if !strings.Contains(text, `"id":"storage-1"`) || !strings.Contains(text, `"kind":"local"`) || !strings.Contains(text, `"uri":"file:///repo"`) || !strings.Contains(text, `"region":"eu-north-1"`) || !strings.Contains(text, `"endpoint":"https://s3.example.com"`) || !strings.Contains(text, `"credentials":"env"`) || !strings.Contains(text, `"access_key":"access"`) || !strings.Contains(text, `"secret_key":"${env:S3_SECRET_KEY}"`) || !strings.Contains(text, `"session_token":"${env:S3_SESSION_TOKEN}"`) || !strings.Contains(text, `"force_path_style":true`) {
					t.Fatalf("storage add request = %q", text)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"storage-1","name":"local"}`)
			default:
				http.Error(w, "bad method", http.StatusMethodNotAllowed)
			}
		case "/api/v1/storages/storage-1":
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"storage-1","name":"local","kind":"local"}`)
			case http.MethodPut:
				defer r.Body.Close()
				var body bytes.Buffer
				if _, err := body.ReadFrom(r.Body); err != nil {
					t.Fatalf("ReadFrom(update request) error = %v", err)
				}
				text := body.String()
				if !strings.Contains(text, `"name":"repo2"`) || !strings.Contains(text, `"uri":"file:///repo2"`) || !strings.Contains(text, `"region":"us-east-1"`) {
					t.Fatalf("storage update request = %q", text)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"storage-1","name":"repo2","kind":"local"}`)
			case http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("storage method = %s", r.Method)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{
		"storage", "add", "--server", server.URL, "--id", "storage-1", "--name", "local", "--kind", "local", "--uri", "file:///repo", "--region", "eu-north-1", "--endpoint", "https://s3.example.com", "--credentials", "env", "--access-key", "access", "--secret-key-ref", "${env:S3_SECRET_KEY}", "--session-token-ref", "${env:S3_SESSION_TOKEN}", "--force-path-style",
	}); err != nil {
		t.Fatalf("storage add error = %v", err)
	}
	if !strings.Contains(out.String(), `"id":"storage-1"`) {
		t.Fatalf("storage add output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"storage", "list", "--server", server.URL}); err != nil {
		t.Fatalf("storage list error = %v", err)
	}
	if !strings.Contains(out.String(), `"storages":[{"id":"storage-1"`) {
		t.Fatalf("storage list output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"storage", "inspect", "--server", server.URL, "--id", "storage-1"}); err != nil {
		t.Fatalf("storage inspect error = %v", err)
	}
	if !strings.Contains(out.String(), `"kind":"local"`) {
		t.Fatalf("storage inspect output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"storage", "update", "--server", server.URL, "--id", "storage-1", "--name", "repo2", "--kind", "local", "--uri", "file:///repo2", "--region", "us-east-1"}); err != nil {
		t.Fatalf("storage update error = %v", err)
	}
	if !strings.Contains(out.String(), `"name":"repo2"`) {
		t.Fatalf("storage update output = %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"storage", "remove", "--server", server.URL, "--id", "storage-1"}); err != nil {
		t.Fatalf("storage remove error = %v", err)
	}
	if out.String() != "" {
		t.Fatalf("storage remove output = %q, want empty", out.String())
	}
}

func TestRunStorageAddRequiresFields(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"storage", "add", "--kind", "local", "--uri", "file:///repo"}); err == nil {
		t.Fatal("storage add without name error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"storage", "add", "--name", "local", "--uri", "file:///repo"}); err == nil {
		t.Fatal("storage add without kind error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"storage", "remove"}); err == nil {
		t.Fatal("storage remove without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"storage", "inspect"}); err == nil {
		t.Fatal("storage inspect without id error = nil, want error")
	}
	if err := run(context.Background(), &out, []string{"storage", "update", "--name", "repo", "--kind", "local", "--uri", "file:///repo"}); err == nil {
		t.Fatal("storage update without id error = nil, want error")
	}
}

func TestRunStorageAddSFTPOptions(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/storages" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		defer r.Body.Close()
		var body bytes.Buffer
		if _, err := body.ReadFrom(r.Body); err != nil {
			t.Fatalf("ReadFrom(request) error = %v", err)
		}
		text := body.String()
		for _, want := range []string{
			`"kind":"sftp"`,
			`"uri":"sftp://backup.example.com/repo"`,
			`"username":"backup"`,
			`"password":"${env:SFTP_PASSWORD}"`,
			`"private_key":"${file:/run/keys/sftp}"`,
			`"passphrase":"${env:SFTP_KEY_PASSPHRASE}"`,
			`"private_key_path":"/run/keys/fallback"`,
			`"agent_socket":"/tmp/ssh-agent.sock"`,
			`"known_hosts":"/run/known_hosts"`,
			`"insecure_ignore_host_key":true`,
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("storage add sftp request = %q, missing %s", text, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"sftp-1","name":"sftp","kind":"sftp"}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), &out, []string{
		"storage", "add",
		"--server", server.URL,
		"--id", "sftp-1",
		"--name", "sftp",
		"--kind", "sftp",
		"--uri", "sftp://backup.example.com/repo",
		"--username", "backup",
		"--password-ref", "${env:SFTP_PASSWORD}",
		"--private-key-ref", "${file:/run/keys/sftp}",
		"--private-key-path", "/run/keys/fallback",
		"--passphrase-ref", "${env:SFTP_KEY_PASSPHRASE}",
		"--agent-socket", "/tmp/ssh-agent.sock",
		"--known-hosts", "/run/known_hosts",
		"--insecure-ignore-host-key",
	})
	if err != nil {
		t.Fatalf("storage add sftp error = %v", err)
	}
}

func TestRunStorageAddAzureOptions(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/storages" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		defer r.Body.Close()
		var body bytes.Buffer
		if _, err := body.ReadFrom(r.Body); err != nil {
			t.Fatalf("ReadFrom(request) error = %v", err)
		}
		text := body.String()
		for _, want := range []string{
			`"kind":"azure"`,
			`"uri":"azure://acct/repo/prefix-from-uri"`,
			`"endpoint":"http://127.0.0.1:10000/acct"`,
			`"account_name":"acct"`,
			`"account_key":"${env:AZURE_STORAGE_ACCOUNT_KEY}"`,
			`"sas_token":"${env:AZURE_STORAGE_SAS_TOKEN}"`,
			`"prefix":"cli-prefix"`,
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("storage add azure request = %q, missing %s", text, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"azure-1","name":"azure","kind":"azure"}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), &out, []string{
		"storage", "add",
		"--server", server.URL,
		"--id", "azure-1",
		"--name", "azure",
		"--kind", "azure",
		"--uri", "azure://acct/repo/prefix-from-uri",
		"--endpoint", "http://127.0.0.1:10000/acct",
		"--account-name", "acct",
		"--account-key-ref", "${env:AZURE_STORAGE_ACCOUNT_KEY}",
		"--sas-token-ref", "${env:AZURE_STORAGE_SAS_TOKEN}",
		"--prefix", "cli-prefix",
	})
	if err != nil {
		t.Fatalf("storage add azure error = %v", err)
	}
}

func TestRunStorageAddGCSOptions(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/storages" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		defer r.Body.Close()
		var body bytes.Buffer
		if _, err := body.ReadFrom(r.Body); err != nil {
			t.Fatalf("ReadFrom(request) error = %v", err)
		}
		text := body.String()
		for _, want := range []string{
			`"kind":"gcs"`,
			`"uri":"gcs://bucket/prefix-from-uri"`,
			`"endpoint":"http://127.0.0.1:4443"`,
			`"bearer_token":"${env:GCS_ACCESS_TOKEN}"`,
			`"api_key":"${env:GCS_API_KEY}"`,
			`"prefix":"cli-prefix"`,
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("storage add gcs request = %q, missing %s", text, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"gcs-1","name":"gcs","kind":"gcs"}`)
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), &out, []string{
		"storage", "add",
		"--server", server.URL,
		"--id", "gcs-1",
		"--name", "gcs",
		"--kind", "gcs",
		"--uri", "gcs://bucket/prefix-from-uri",
		"--endpoint", "http://127.0.0.1:4443",
		"--bearer-token-ref", "${env:GCS_ACCESS_TOKEN}",
		"--api-key-ref", "${env:GCS_API_KEY}",
		"--prefix", "cli-prefix",
	})
	if err != nil {
		t.Fatalf("storage add gcs error = %v", err)
	}
}

func TestRunStorageRejectsConflictingSecretFlags(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := run(context.Background(), &out, []string{"storage", "add", "--name", "repo", "--kind", "s3", "--uri", "s3://repo", "--secret-key", "secret", "--secret-key-ref", "${env:S3_SECRET_KEY}"})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("storage add conflicting secret-key flags error = %v, want mutually exclusive", err)
	}
	err = run(context.Background(), &out, []string{"storage", "update", "--id", "storage-1", "--name", "repo", "--kind", "s3", "--uri", "s3://repo", "--access-key-ref", "env:S3_ACCESS_KEY"})
	if err == nil || !strings.Contains(err.Error(), "secret reference syntax") {
		t.Fatalf("storage update malformed access-key ref error = %v, want syntax error", err)
	}
}

func TestRunStorageTestAndDU(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	uri := "file://" + dir
	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"storage", "test", "--uri", uri}); err != nil {
		t.Fatalf("storage test error = %v", err)
	}
	if !strings.Contains(out.String(), `"ok":true`) || !strings.Contains(out.String(), `"backend":"local"`) || !strings.Contains(out.String(), `"bytes":21`) {
		t.Fatalf("storage test output = %q", out.String())
	}

	backend, err := local.New("local", dir)
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}
	if _, err := backend.Put(context.Background(), "data/a", strings.NewReader("abcd"), 4); err != nil {
		t.Fatalf("Put(data/a) error = %v", err)
	}
	if _, err := backend.Put(context.Background(), "data/b", strings.NewReader("ef"), 2); err != nil {
		t.Fatalf("Put(data/b) error = %v", err)
	}
	if _, err := backend.Put(context.Background(), "meta/c", strings.NewReader("ignored"), 7); err != nil {
		t.Fatalf("Put(meta/c) error = %v", err)
	}
	out.Reset()
	if err := run(context.Background(), &out, []string{"storage", "du", "--uri", uri, "--prefix", "data/"}); err != nil {
		t.Fatalf("storage du error = %v", err)
	}
	if !strings.Contains(out.String(), `"bytes":6`) || !strings.Contains(out.String(), `"objects":2`) {
		t.Fatalf("storage du output = %q", out.String())
	}
}

func TestRunStorageTestS3(t *testing.T) {
	t.Parallel()

	objects := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.URL.Path, "/bucket/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if key == "" && r.URL.Query().Get("list-type") == "2" {
				prefix := r.URL.Query().Get("prefix")
				w.Header().Set("Content-Type", "application/xml")
				fmt.Fprint(w, `<ListBucketResult>`)
				for objectKey, body := range objects {
					if !strings.HasPrefix(objectKey, prefix) {
						continue
					}
					sum := md5.Sum(body)
					fmt.Fprintf(w, `<Contents><Key>%s</Key><LastModified>2026-04-25T12:00:00Z</LastModified><ETag>"%s"</ETag><Size>%d</Size></Contents>`, objectKey, hex.EncodeToString(sum[:]), len(body))
				}
				fmt.Fprint(w, `</ListBucketResult>`)
				return
			}
			body, ok := objects[key]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write(body); err != nil {
				t.Fatalf("Write(GET) error = %v", err)
			}
		case http.MethodPut:
			var body bytes.Buffer
			if _, err := body.ReadFrom(r.Body); err != nil {
				t.Fatalf("ReadFrom(PUT) error = %v", err)
			}
			defer r.Body.Close()
			objects[key] = body.Bytes()
			sum := md5.Sum(body.Bytes())
			w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodHead:
			body, ok := objects[key]
			if !ok {
				http.NotFound(w, r)
				return
			}
			sum := md5.Sum(body)
			w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			delete(objects, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), &out, []string{
		"storage", "test",
		"--uri", "s3://bucket",
		"--region", "us-east-1",
		"--endpoint", server.URL,
		"--access-key", "access",
		"--secret-key", "secret",
		"--force-path-style",
	})
	if err != nil {
		t.Fatalf("storage test s3 error = %v", err)
	}
	if !strings.Contains(out.String(), `"ok":true`) || !strings.Contains(out.String(), `"backend":"s3"`) {
		t.Fatalf("storage test s3 output = %q", out.String())
	}

	objects["data/a"] = []byte("abcd")
	objects["data/b"] = []byte("ef")
	objects["meta/c"] = []byte("ignored")
	out.Reset()
	err = run(context.Background(), &out, []string{
		"storage", "du",
		"--uri", "s3://bucket",
		"--prefix", "data/",
		"--region", "us-east-1",
		"--endpoint", server.URL,
		"--access-key", "access",
		"--secret-key", "secret",
		"--force-path-style",
	})
	if err != nil {
		t.Fatalf("storage du s3 error = %v", err)
	}
	if !strings.Contains(out.String(), `"bytes":6`) || !strings.Contains(out.String(), `"objects":2`) {
		t.Fatalf("storage du s3 output = %q", out.String())
	}
}

func TestRunStorageTestRequiresFileURI(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := run(context.Background(), &out, []string{"storage", "test"}); err == nil {
		t.Fatal("storage test without uri error = nil, want error")
	}
}

func TestRunStorageTestRejectsUnimplementedKinds(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"storage", "test", "--kind", "webdav", "--uri", "webdav://example.com/repo"},
	} {
		args := args
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			err := run(context.Background(), &out, args)
			if err == nil {
				t.Fatal("storage command error = nil, want unsupported kind error")
			}
			text := err.Error()
			if !strings.Contains(text, "not implemented") || !strings.Contains(text, "supported storage kinds: local, s3, sftp, azure, gcs") {
				t.Fatalf("storage command error = %q", text)
			}
		})
	}
}

func TestLocalStorageURIParsing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root, err := localRootFromURI(dir)
	if err != nil {
		t.Fatalf("localRootFromURI(path) error = %v", err)
	}
	if root != dir {
		t.Fatalf("localRootFromURI(path) = %q, want %q", root, dir)
	}
	root, err = localRootFromURI("file://" + dir)
	if err != nil {
		t.Fatalf("localRootFromURI(file) error = %v", err)
	}
	if root != dir {
		t.Fatalf("localRootFromURI(file) = %q, want %q", root, dir)
	}
	backend, err := openLocalStorageURI("file://" + dir)
	if err != nil {
		t.Fatalf("openLocalStorageURI() error = %v", err)
	}
	if backend.Name() != "local" {
		t.Fatalf("backend name = %q, want local", backend.Name())
	}
	for _, uri := range []string{"s3://bucket", "file://host/path", "file://"} {
		uri := uri
		t.Run(uri, func(t *testing.T) {
			t.Parallel()
			if _, err := localRootFromURI(uri); err == nil {
				t.Fatalf("localRootFromURI(%q) error = nil, want error", uri)
			}
		})
	}
}

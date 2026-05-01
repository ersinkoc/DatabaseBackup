package gcs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/storage"
)

const (
	defaultEndpoint     = "https://storage.googleapis.com"
	defaultListPageSize = "1000"
)

// Config configures a Google Cloud Storage backend.
type Config struct {
	Name        string
	Bucket      string
	Prefix      string
	Endpoint    string
	BearerToken string
	APIKey      string
	HTTPClient  *http.Client
}

// Backend stores objects in Google Cloud Storage.
type Backend struct {
	name   string
	bucket string
	prefix string
	base   *url.URL
	token  string
	apiKey string
	client *http.Client
}

var _ storage.Backend = (*Backend)(nil)

// New returns a Google Cloud Storage backend.
func New(cfg Config) (*Backend, error) {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "gcs"
	}
	bucket := strings.Trim(strings.TrimSpace(cfg.Bucket), "/")
	if bucket == "" {
		return nil, fmt.Errorf("gcs bucket is required")
	}
	endpointRaw := strings.TrimSpace(cfg.Endpoint)
	if endpointRaw == "" {
		endpointRaw = defaultEndpoint
	}
	base, err := url.Parse(endpointRaw)
	if err != nil {
		return nil, fmt.Errorf("parse gcs endpoint: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("gcs endpoint must include scheme and host")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Backend{
		name:   name,
		bucket: bucket,
		prefix: cleanPrefix(cfg.Prefix),
		base:   base,
		token:  strings.TrimSpace(cfg.BearerToken),
		apiKey: strings.TrimSpace(cfg.APIKey),
		client: client,
	}, nil
}

// Name returns the configured backend name.
func (b *Backend) Name() string {
	return b.name
}

// Put stores key with a conditional media upload.
func (b *Backend) Put(ctx context.Context, key string, r io.Reader, size int64) (storage.ObjectInfo, error) {
	if err := validateKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	spooled, payloadHash, written, err := spoolAndHash(r)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	defer os.Remove(spooled.Name())
	defer spooled.Close()
	if size >= 0 && written != size {
		return storage.ObjectInfo{}, fmt.Errorf("size mismatch for %q: wrote %d bytes, expected %d", key, written, size)
	}
	if _, err := spooled.Seek(0, io.SeekStart); err != nil {
		return storage.ObjectInfo{}, err
	}
	values := url.Values{}
	values.Set("uploadType", "media")
	values.Set("name", b.objectName(key))
	values.Set("ifGenerationMatch", "0")
	req, err := b.newRequest(ctx, http.MethodPost, "/upload/storage/v1/b/"+url.PathEscape(b.bucket)+"/o", values, spooled)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	req.Body = io.NopCloser(spooled)
	req.ContentLength = written
	req.Header.Set("Content-Length", strconv.FormatInt(written, 10))
	req.Header.Set("Content-Type", "application/octet-stream")
	if err := b.authorize(req); err != nil {
		return storage.ObjectInfo{}, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPreconditionFailed || resp.StatusCode == http.StatusConflict {
		return storage.ObjectInfo{}, core.WrapKind(core.ErrorKindConflict, "put gcs object", fmt.Errorf("object %q already exists", key))
	}
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return storage.ObjectInfo{}, err
	}
	var meta objectResource
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("decode gcs upload response: %w", err)
	}
	info := objectInfoFromResource(key, meta)
	if info.Size == 0 {
		info.Size = written
	}
	if info.ETag == "" {
		info.ETag = payloadHash
	}
	return info, nil
}

// Get returns a full object stream.
func (b *Backend) Get(ctx context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	if err := validateKey(key); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	info, err := b.Head(ctx, key)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	req, err := b.objectRequest(ctx, http.MethodGet, key, url.Values{"alt": []string{"media"}}, nil)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	if err := b.authorize(req); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, storage.ObjectInfo{}, core.WrapKind(core.ErrorKindNotFound, "get gcs object", fmt.Errorf("object %q not found", key))
	}
	if err := expectStatus(resp, http.StatusOK); err != nil {
		resp.Body.Close()
		return nil, storage.ObjectInfo{}, err
	}
	return resp.Body, info, nil
}

// GetRange returns a range stream from key.
func (b *Backend) GetRange(ctx context.Context, key string, off, length int64) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if off < 0 || length < 0 {
		return nil, fmt.Errorf("invalid range off=%d length=%d", off, length)
	}
	req, err := b.objectRequest(ctx, http.MethodGet, key, url.Values{"alt": []string{"media"}}, nil)
	if err != nil {
		return nil, err
	}
	if length == 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off))
	} else {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+length-1))
	}
	if err := b.authorize(req); err != nil {
		return nil, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, core.WrapKind(core.ErrorKindNotFound, "get gcs object range", fmt.Errorf("object %q not found", key))
	}
	if err := expectStatus(resp, http.StatusPartialContent, http.StatusOK); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}

// Head returns object metadata.
func (b *Backend) Head(ctx context.Context, key string) (storage.ObjectInfo, error) {
	if err := validateKey(key); err != nil {
		return storage.ObjectInfo{}, err
	}
	req, err := b.objectRequest(ctx, http.MethodGet, key, nil, nil)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := b.authorize(req); err != nil {
		return storage.ObjectInfo{}, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return storage.ObjectInfo{}, core.WrapKind(core.ErrorKindNotFound, "head gcs object", fmt.Errorf("object %q not found", key))
	}
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return storage.ObjectInfo{}, err
	}
	var meta objectResource
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("decode gcs object metadata: %w", err)
	}
	return objectInfoFromResource(key, meta), nil
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
	if err := validateKey(key); err != nil {
		return err
	}
	req, err := b.objectRequest(ctx, http.MethodDelete, key, nil, nil)
	if err != nil {
		return err
	}
	if err := b.authorize(req); err != nil {
		return err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return expectStatus(resp, http.StatusNoContent, http.StatusOK)
}

// List returns one GCS objects page.
func (b *Backend) List(ctx context.Context, prefix string, token string) (storage.ListPage, error) {
	values := url.Values{}
	values.Set("maxResults", defaultListPageSize)
	if merged := b.objectName(prefix); merged != "" {
		values.Set("prefix", merged)
	}
	if token != "" {
		values.Set("pageToken", token)
	}
	req, err := b.newRequest(ctx, http.MethodGet, "/storage/v1/b/"+url.PathEscape(b.bucket)+"/o", values, nil)
	if err != nil {
		return storage.ListPage{}, err
	}
	if err := b.authorize(req); err != nil {
		return storage.ListPage{}, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return storage.ListPage{}, err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return storage.ListPage{}, err
	}
	var listing listResponse
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return storage.ListPage{}, fmt.Errorf("decode gcs list response: %w", err)
	}
	objects := make([]storage.ObjectInfo, 0, len(listing.Items))
	for _, item := range listing.Items {
		key := strings.TrimPrefix(item.Name, b.prefix)
		objects = append(objects, objectInfoFromResource(key, item))
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	return storage.ListPage{Objects: objects, NextToken: listing.NextPageToken}, nil
}

func (b *Backend) objectRequest(ctx context.Context, method string, key string, values url.Values, body io.Reader) (*http.Request, error) {
	values = cloneValues(values)
	values.Set("alt", firstNonEmpty(values.Get("alt"), "json"))
	return b.newRequest(ctx, method, "/storage/v1/b/"+url.PathEscape(b.bucket)+"/o/"+url.PathEscape(b.objectName(key)), values, body)
}

func (b *Backend) newRequest(ctx context.Context, method string, apiPath string, values url.Values, body io.Reader) (*http.Request, error) {
	u := *b.base
	u.Path = path.Join(strings.TrimRight(u.Path, "/"), apiPath)
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	query := u.Query()
	for key, values := range values {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	if b.apiKey != "" {
		query.Set("key", b.apiKey)
	}
	u.RawQuery = query.Encode()
	return http.NewRequestWithContext(ctx, method, u.String(), body)
}

func (b *Backend) authorize(req *http.Request) error {
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	return nil
}

func (b *Backend) objectName(key string) string {
	return b.prefix + key
}

func cleanPrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return ""
	}
	return prefix + "/"
}

func validateKey(key string) error {
	cleaned := path.Clean(key)
	if key == "" || cleaned == "." || path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return storage.InvalidKeyError{Key: key}
	}
	if strings.Contains(key, "\\") {
		return storage.InvalidKeyError{Key: key}
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == ".." {
			return storage.InvalidKeyError{Key: key}
		}
	}
	return nil
}

func spoolAndHash(r io.Reader) (*os.File, string, int64, error) {
	file, err := os.CreateTemp("", "kronos-gcs-put-*")
	if err != nil {
		return nil, "", 0, err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), r)
	if err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, "", 0, err
	}
	return file, hex.EncodeToString(hash.Sum(nil)), written, nil
}

func objectInfoFromResource(key string, resource objectResource) storage.ObjectInfo {
	size, _ := strconv.ParseInt(resource.Size, 10, 64)
	updatedAt := time.Time{}
	if resource.Updated != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, resource.Updated); err == nil {
			updatedAt = parsed.UTC()
		}
	}
	return storage.ObjectInfo{
		Key:       key,
		Size:      size,
		ETag:      firstNonEmpty(resource.MD5Hash, resource.ETag, resource.CRC32C),
		UpdatedAt: updatedAt,
	}
}

func expectStatus(resp *http.Response, allowed ...int) error {
	for _, status := range allowed {
		if resp.StatusCode == status {
			return nil
		}
	}
	var body bytes.Buffer
	_, _ = io.CopyN(&body, resp.Body, 512)
	text := strings.TrimSpace(body.String())
	if text == "" {
		return fmt.Errorf("gcs request failed: %s", resp.Status)
	}
	return fmt.Errorf("gcs request failed: %s: %s", resp.Status, text)
}

func cloneValues(values url.Values) url.Values {
	out := url.Values{}
	for key, items := range values {
		for _, item := range items {
			out.Add(key, item)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type objectResource struct {
	Name    string `json:"name"`
	Size    string `json:"size"`
	ETag    string `json:"etag"`
	MD5Hash string `json:"md5Hash"`
	CRC32C  string `json:"crc32c"`
	Updated string `json:"updated"`
}

type listResponse struct {
	Items         []objectResource `json:"items"`
	NextPageToken string           `json:"nextPageToken"`
}

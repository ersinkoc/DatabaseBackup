package azure

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
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
	defaultEndpointTemplate = "https://%s.blob.core.windows.net"
	storageVersion          = "2023-11-03"
	defaultListPageSize     = "1000"
)

// Config configures an Azure Blob Storage backend.
type Config struct {
	Name        string
	AccountName string
	AccountKey  string
	Container   string
	Prefix      string
	Endpoint    string
	SASToken    string
	HTTPClient  *http.Client
}

// Backend stores objects in Azure Blob Storage.
type Backend struct {
	name        string
	accountName string
	accountKey  []byte
	container   string
	prefix      string
	endpoint    *url.URL
	sas         url.Values
	client      *http.Client
}

var _ storage.Backend = (*Backend)(nil)

// New returns an Azure Blob Storage backend.
func New(cfg Config) (*Backend, error) {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "azure"
	}
	account := strings.TrimSpace(cfg.AccountName)
	if account == "" && strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("azure account name is required")
	}
	container := strings.Trim(strings.TrimSpace(cfg.Container), "/")
	if container == "" {
		return nil, fmt.Errorf("azure container is required")
	}
	endpointRaw := strings.TrimSpace(cfg.Endpoint)
	if endpointRaw == "" {
		endpointRaw = fmt.Sprintf(defaultEndpointTemplate, account)
	}
	endpoint, err := url.Parse(endpointRaw)
	if err != nil {
		return nil, fmt.Errorf("parse azure endpoint: %w", err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, fmt.Errorf("azure endpoint must include scheme and host")
	}
	var key []byte
	if strings.TrimSpace(cfg.AccountKey) != "" {
		key, err = base64.StdEncoding.DecodeString(strings.TrimSpace(cfg.AccountKey))
		if err != nil {
			return nil, fmt.Errorf("decode azure account key: %w", err)
		}
	}
	sas := url.Values{}
	if token := strings.TrimSpace(cfg.SASToken); token != "" {
		token = strings.TrimPrefix(token, "?")
		sas, err = url.ParseQuery(token)
		if err != nil {
			return nil, fmt.Errorf("parse azure sas token: %w", err)
		}
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Backend{
		name:        name,
		accountName: account,
		accountKey:  key,
		container:   container,
		prefix:      cleanPrefix(cfg.Prefix),
		endpoint:    endpoint,
		sas:         sas,
		client:      client,
	}, nil
}

// Name returns the configured backend name.
func (b *Backend) Name() string {
	return b.name
}

// Put stores key as a block blob.
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
	req, err := b.newRequest(ctx, http.MethodPut, key, nil, spooled)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	req.Body = io.NopCloser(spooled)
	req.ContentLength = written
	req.Header.Set("Content-Length", strconv.FormatInt(written, 10))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("If-None-Match", "*")
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("x-ms-meta-kronos-sha256", payloadHash)
	if err := b.sign(req); err != nil {
		return storage.ObjectInfo{}, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPreconditionFailed || resp.StatusCode == http.StatusConflict {
		return storage.ObjectInfo{}, core.WrapKind(core.ErrorKindConflict, "put azure blob", fmt.Errorf("object %q already exists", key))
	}
	if err := expectStatus(resp, http.StatusCreated, http.StatusOK); err != nil {
		return storage.ObjectInfo{}, err
	}
	info := objectInfoFromHeaders(key, written, resp.Header)
	info.Size = written
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
	req, err := b.newRequest(ctx, http.MethodGet, key, nil, nil)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	if err := b.sign(req); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, storage.ObjectInfo{}, core.WrapKind(core.ErrorKindNotFound, "get azure blob", fmt.Errorf("object %q not found", key))
	}
	if err := expectStatus(resp, http.StatusOK); err != nil {
		resp.Body.Close()
		return nil, storage.ObjectInfo{}, err
	}
	return resp.Body, objectInfoFromHeaders(key, resp.ContentLength, resp.Header), nil
}

// GetRange returns a range stream from key.
func (b *Backend) GetRange(ctx context.Context, key string, off, length int64) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if off < 0 || length < 0 {
		return nil, fmt.Errorf("invalid range off=%d length=%d", off, length)
	}
	req, err := b.newRequest(ctx, http.MethodGet, key, nil, nil)
	if err != nil {
		return nil, err
	}
	if length == 0 {
		req.Header.Set("x-ms-range", fmt.Sprintf("bytes=%d-%d", off, off))
	} else {
		req.Header.Set("x-ms-range", fmt.Sprintf("bytes=%d-%d", off, off+length-1))
	}
	if err := b.sign(req); err != nil {
		return nil, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, core.WrapKind(core.ErrorKindNotFound, "get azure blob range", fmt.Errorf("object %q not found", key))
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
	req, err := b.newRequest(ctx, http.MethodHead, key, nil, nil)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	if err := b.sign(req); err != nil {
		return storage.ObjectInfo{}, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return storage.ObjectInfo{}, core.WrapKind(core.ErrorKindNotFound, "head azure blob", fmt.Errorf("object %q not found", key))
	}
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return storage.ObjectInfo{}, err
	}
	return objectInfoFromHeaders(key, resp.ContentLength, resp.Header), nil
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
	req, err := b.newRequest(ctx, http.MethodDelete, key, nil, nil)
	if err != nil {
		return err
	}
	if err := b.sign(req); err != nil {
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
	return expectStatus(resp, http.StatusAccepted, http.StatusOK, http.StatusNoContent)
}

// List returns one Azure List Blobs page.
func (b *Backend) List(ctx context.Context, prefix string, token string) (storage.ListPage, error) {
	values := url.Values{}
	values.Set("restype", "container")
	values.Set("comp", "list")
	values.Set("maxresults", defaultListPageSize)
	if merged := b.blobName(prefix); merged != "" {
		values.Set("prefix", merged)
	}
	if token != "" {
		values.Set("marker", token)
	}
	req, err := b.newContainerRequest(ctx, http.MethodGet, values, nil)
	if err != nil {
		return storage.ListPage{}, err
	}
	if err := b.sign(req); err != nil {
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
	var listing enumerationResults
	if err := xml.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return storage.ListPage{}, fmt.Errorf("decode azure list response: %w", err)
	}
	objects := make([]storage.ObjectInfo, 0, len(listing.Blobs.Blob))
	for _, item := range listing.Blobs.Blob {
		key := strings.TrimPrefix(item.Name, b.prefix)
		objects = append(objects, storage.ObjectInfo{
			Key:       key,
			Size:      item.Properties.ContentLength,
			ETag:      trimETag(item.Properties.ETag),
			UpdatedAt: item.Properties.LastModified.Time,
		})
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	return storage.ListPage{Objects: objects, NextToken: listing.NextMarker}, nil
}

func (b *Backend) newRequest(ctx context.Context, method string, key string, values url.Values, body io.Reader) (*http.Request, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	return b.newBlobRequest(ctx, method, b.blobName(key), values, body)
}

func (b *Backend) newContainerRequest(ctx context.Context, method string, values url.Values, body io.Reader) (*http.Request, error) {
	return b.newBlobRequest(ctx, method, "", values, body)
}

func (b *Backend) newBlobRequest(ctx context.Context, method string, blob string, values url.Values, body io.Reader) (*http.Request, error) {
	u := *b.endpoint
	parts := []string{strings.TrimRight(u.Path, "/"), b.container}
	if blob != "" {
		parts = append(parts, strings.Split(blob, "/")...)
	}
	u.Path = path.Join(parts...)
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	query := u.Query()
	for key, value := range b.sas {
		for _, item := range value {
			query.Add(key, item)
		}
	}
	for key, value := range values {
		for _, item := range value {
			query.Add(key, item)
		}
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-ms-date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("x-ms-version", storageVersion)
	return req, nil
}

func (b *Backend) sign(req *http.Request) error {
	if len(b.accountKey) == 0 {
		return nil
	}
	account := b.accountName
	if account == "" {
		return fmt.Errorf("azure account name is required for shared key auth")
	}
	stringToSign := azureStringToSign(req, account)
	mac := hmac.New(sha256.New, b.accountKey)
	if _, err := mac.Write([]byte(stringToSign)); err != nil {
		return err
	}
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	req.Header.Set("Authorization", "SharedKey "+account+":"+signature)
	return nil
}

func azureStringToSign(req *http.Request, account string) string {
	headers := req.Header
	contentLength := headers.Get("Content-Length")
	if contentLength == "0" {
		contentLength = ""
	}
	parts := []string{
		req.Method,
		headers.Get("Content-Encoding"),
		headers.Get("Content-Language"),
		contentLength,
		headers.Get("Content-MD5"),
		headers.Get("Content-Type"),
		headers.Get("Date"),
		headers.Get("If-Modified-Since"),
		headers.Get("If-Match"),
		headers.Get("If-None-Match"),
		headers.Get("If-Unmodified-Since"),
		headers.Get("Range"),
	}
	return strings.Join(parts, "\n") + "\n" + canonicalizedHeaders(headers) + canonicalizedResource(req.URL, account)
}

func canonicalizedHeaders(headers http.Header) string {
	var keys []string
	for key := range headers {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "x-ms-") {
			keys = append(keys, lower)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		values := headers.Values(key)
		for i := range values {
			values[i] = strings.Join(strings.Fields(values[i]), " ")
		}
		sort.Strings(values)
		b.WriteString(key)
		b.WriteByte(':')
		b.WriteString(strings.Join(values, ","))
		b.WriteByte('\n')
	}
	return b.String()
}

func canonicalizedResource(u *url.URL, account string) string {
	var b strings.Builder
	b.WriteByte('/')
	b.WriteString(account)
	if u.EscapedPath() == "" {
		b.WriteByte('/')
	} else {
		b.WriteString(u.EscapedPath())
	}
	query := u.Query()
	var keys []string
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "sig") || strings.HasPrefix(lower, "se") || strings.HasPrefix(lower, "sp") || strings.HasPrefix(lower, "sv") || strings.HasPrefix(lower, "sr") {
			continue
		}
		keys = append(keys, lower)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values := query[key]
		sort.Strings(values)
		b.WriteByte('\n')
		b.WriteString(key)
		b.WriteByte(':')
		b.WriteString(strings.Join(values, ","))
	}
	return b.String()
}

func (b *Backend) blobName(key string) string {
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
	file, err := os.CreateTemp("", "kronos-azure-put-*")
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

func objectInfoFromHeaders(key string, fallbackSize int64, header http.Header) storage.ObjectInfo {
	size := fallbackSize
	for _, headerKey := range []string{"Content-Length", "x-ms-blob-content-length"} {
		if value := header.Get(headerKey); value != "" {
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				size = parsed
				break
			}
		}
	}
	updatedAt := time.Time{}
	if value := header.Get("Last-Modified"); value != "" {
		if parsed, err := http.ParseTime(value); err == nil {
			updatedAt = parsed.UTC()
		}
	}
	return storage.ObjectInfo{
		Key:       key,
		Size:      size,
		ETag:      trimETag(header.Get("ETag")),
		UpdatedAt: updatedAt,
	}
}

func trimETag(etag string) string {
	return strings.Trim(etag, `"`)
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
		return fmt.Errorf("azure blob request failed: %s", resp.Status)
	}
	return fmt.Errorf("azure blob request failed: %s: %s", resp.Status, text)
}

type enumerationResults struct {
	Blobs      blobs  `xml:"Blobs"`
	NextMarker string `xml:"NextMarker"`
}

type blobs struct {
	Blob []blob `xml:"Blob"`
}

type blob struct {
	Name       string         `xml:"Name"`
	Properties blobProperties `xml:"Properties"`
}

type blobProperties struct {
	LastModified  azureTime `xml:"Last-Modified"`
	ETag          string    `xml:"Etag"`
	ContentLength int64     `xml:"Content-Length"`
}

type azureTime struct {
	time.Time
}

func (t *azureTime) UnmarshalText(data []byte) error {
	if len(data) == 0 {
		t.Time = time.Time{}
		return nil
	}
	parsed, err := http.ParseTime(string(data))
	if err != nil {
		return err
	}
	t.Time = parsed.UTC()
	return nil
}

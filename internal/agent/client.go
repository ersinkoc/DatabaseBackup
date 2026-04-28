package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/kronos/kronos/internal/core"
	"github.com/kronos/kronos/internal/obs"
	control "github.com/kronos/kronos/internal/server"
)

// Client talks to the Kronos control-plane HTTP API.
type Client struct {
	httpClient *http.Client
	baseURL    *url.URL
	Token      string
	AgentID    string
}

// NewClient returns a control-plane client for serverAddr.
func NewClient(serverAddr string, httpClient *http.Client) (*Client, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if !strings.Contains(serverAddr, "://") {
		serverAddr = "http://" + serverAddr
	}
	parsed, err := url.Parse(serverAddr)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid server address %q", serverAddr)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return &Client{httpClient: httpClient, baseURL: parsed, Token: strings.TrimSpace(os.Getenv("KRONOS_TOKEN"))}, nil
}

// Heartbeat records an agent heartbeat.
func (c *Client) Heartbeat(ctx context.Context, heartbeat control.AgentHeartbeat) (control.AgentSnapshot, error) {
	var snapshot control.AgentSnapshot
	err := c.postJSON(ctx, "/api/v1/agents/heartbeat", heartbeat, &snapshot)
	return snapshot, err
}

// Claim starts the oldest queued job. It returns nil when no job is queued.
func (c *Client) Claim(ctx context.Context) (*core.Job, error) {
	var response claimResponse
	if err := c.postJSON(ctx, "/api/v1/jobs/claim", nil, &response); err != nil {
		return nil, err
	}
	return response.Job, nil
}

// Finish records a terminal job status and optional operation result metadata.
func (c *Client) Finish(ctx context.Context, id core.ID, status core.JobStatus, message string, result core.JobResult) (core.Job, error) {
	var job core.Job
	err := c.postJSON(ctx, "/api/v1/jobs/"+url.PathEscape(string(id))+"/finish", finishRequest{
		Status:       status,
		Error:        message,
		Backup:       result.Backup,
		Restore:      result.Restore,
		Verification: result.Verification,
	}, &job)
	return job, err
}

// ListTargets returns target definitions visible to the agent token.
func (c *Client) ListTargets(ctx context.Context) ([]core.Target, error) {
	var response targetsResponse
	if err := c.getJSON(ctx, "/api/v1/targets?include_secrets=true", &response); err != nil {
		return nil, err
	}
	return response.Targets, nil
}

// GetTarget returns one target definition.
func (c *Client) GetTarget(ctx context.Context, id core.ID) (core.Target, error) {
	var target core.Target
	err := c.getJSON(ctx, "/api/v1/targets/"+url.PathEscape(string(id))+"?include_secrets=true", &target)
	return target, err
}

// ListStorages returns storage definitions visible to the agent token.
func (c *Client) ListStorages(ctx context.Context) ([]core.Storage, error) {
	var response storagesResponse
	if err := c.getJSON(ctx, "/api/v1/storages?include_secrets=true", &response); err != nil {
		return nil, err
	}
	return response.Storages, nil
}

// GetStorage returns one storage definition.
func (c *Client) GetStorage(ctx context.Context, id core.ID) (core.Storage, error) {
	var storage core.Storage
	err := c.getJSON(ctx, "/api/v1/storages/"+url.PathEscape(string(id))+"?include_secrets=true", &storage)
	return storage, err
}

// ListBackups returns committed backup metadata visible to the agent token.
func (c *Client) ListBackups(ctx context.Context) ([]core.Backup, error) {
	var response backupsResponse
	if err := c.getJSON(ctx, "/api/v1/backups", &response); err != nil {
		return nil, err
	}
	return response.Backups, nil
}

// GetBackup returns one committed backup metadata record.
func (c *Client) GetBackup(ctx context.Context, id core.ID) (core.Backup, error) {
	var backup core.Backup
	err := c.getJSON(ctx, "/api/v1/backups/"+url.PathEscape(string(id)), &backup)
	return backup, err
}

func (c *Client) getJSON(ctx context.Context, path string, dst any) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return c.do(req, path, dst)
}

func (c *Client) postJSON(ctx context.Context, path string, payload any, dst any) error {
	if c == nil || c.httpClient == nil || c.baseURL == nil {
		return fmt.Errorf("agent client is not configured")
	}
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			return err
		}
	}
	req, err := c.newRequest(ctx, http.MethodPost, path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, path, dst)
}

func (c *Client) newRequest(ctx context.Context, method string, path string, body *bytes.Buffer) (*http.Request, error) {
	if c == nil || c.httpClient == nil || c.baseURL == nil {
		return nil, fmt.Errorf("agent client is not configured")
	}
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body.Bytes())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), reader)
	if err != nil {
		return nil, err
	}
	if token := strings.TrimSpace(c.Token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if agentID := strings.TrimSpace(c.AgentID); agentID != "" {
		req.Header.Set("X-Kronos-Agent-ID", agentID)
	}
	req.Header.Set(obs.RequestIDHeader, requestIDForContext(ctx))
	return req, nil
}

func (c *Client) do(req *http.Request, path string, dst any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("control-plane request %s failed: %s%s: %s", path, resp.Status, responseRequestID(resp), responseBodySnippet(resp))
	}
	if dst == nil {
		return nil
	}
	decoder := json.NewDecoder(resp.Body)
	return decoder.Decode(dst)
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

func requestIDForContext(ctx context.Context) string {
	if requestID, ok := obs.RequestIDFromContext(ctx); ok {
		return requestID
	}
	return obs.NewRequestID()
}

func responseBodySnippet(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (c *Client) endpoint(path string) string {
	u := *c.baseURL
	apiPath, rawQuery, _ := strings.Cut(path, "?")
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + apiPath
	u.RawQuery = rawQuery
	return u.String()
}

type claimResponse struct {
	Job *core.Job `json:"job,omitempty"`
}

type finishRequest struct {
	Status       core.JobStatus           `json:"status"`
	Error        string                   `json:"error,omitempty"`
	Backup       *core.Backup             `json:"backup,omitempty"`
	Restore      *core.RestoreReport      `json:"restore,omitempty"`
	Verification *core.VerificationReport `json:"verification,omitempty"`
}

type targetsResponse struct {
	Targets []core.Target `json:"targets"`
}

type storagesResponse struct {
	Storages []core.Storage `json:"storages"`
}

type backupsResponse struct {
	Backups []core.Backup `json:"backups"`
}

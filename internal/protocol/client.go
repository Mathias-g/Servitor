package protocol

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a loopback control-plane client. It talks to a running daemon over
// the address given to NewClient. The CLI is one client of the protocol;
// a future MCP adapter would be another (ADR-0005).
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient returns a Client that talks to the daemon at addr (host:port).
func NewClient(addr string) *Client {
	return &Client{
		baseURL: "http://" + addr,
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

// Stop asks the daemon to drain and shut down gracefully.
func (c *Client) Stop(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodPost, PathStop, nil)
	return err
}

// Health reports whether a daemon is reachable at the client's address.
func (c *Client) Health(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, PathHealth, nil)
	return err
}

// Submit registers a workflow from a Wafer (YAML). It returns the daemon's
// response body, which is an empty string on success and a message on failure.
func (c *Client) Submit(ctx context.Context, wafer []byte) (string, error) {
	return c.doBody(ctx, http.MethodPost, PathSubmit, wafer)
}

// Update replaces an already-registered workflow from a Wafer (YAML).
func (c *Client) Update(ctx context.Context, wafer []byte) (string, error) {
	return c.doBody(ctx, http.MethodPost, PathUpdate, wafer)
}

// Enable enables a workflow's triggers.
func (c *Client) Enable(ctx context.Context, name string) error {
	_, err := c.do(ctx, http.MethodPost, PathEnable+"?name="+url.QueryEscape(name), nil)
	return err
}

// Disable disables a workflow's triggers.
func (c *Client) Disable(ctx context.Context, name string) error {
	_, err := c.do(ctx, http.MethodPost, PathDisable+"?name="+url.QueryEscape(name), nil)
	return err
}

// Trigger fires a manual run of a workflow with the given JSON inputs.
func (c *Client) Trigger(ctx context.Context, name string, inputs []byte) error {
	_, err := c.do(ctx, http.MethodPost, PathTrigger+"?name="+url.QueryEscape(name), inputs)
	return err
}

// ListRuns returns the run history as raw JSON text from the daemon.
func (c *Client) ListRuns(ctx context.Context) (string, error) {
	return c.do(ctx, http.MethodGet, PathRuns, nil)
}

// GetRun returns one run (with its node outcomes) as raw JSON text from the daemon.
func (c *Client) GetRun(ctx context.Context, id string) (string, error) {
	return c.do(ctx, http.MethodGet, PathRun+"?id="+url.QueryEscape(id), nil)
}

// Cancel cancels an in-flight run.
func (c *Client) Cancel(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodPost, PathCancel+"?id="+url.QueryEscape(id), nil)
	return err
}

// Resume resumes a parked run by named signal, with an optional JSON payload.
func (c *Client) Resume(ctx context.Context, name string, payload []byte) error {
	_, err := c.doBody(ctx, http.MethodPost, PathResume+"?name="+url.QueryEscape(name), payload)
	return err
}

// Rerun re-runs a dead-lettered (failed) run by mode (continue/restart/discard).
// An empty mode lets the daemon resolve the run's workflow on_failure default.
func (c *Client) Rerun(ctx context.Context, runID, mode string) error {
	_, err := c.do(ctx, http.MethodPost, PathRerun+"?run-id="+url.QueryEscape(runID)+"&mode="+url.QueryEscape(mode), nil)
	return err
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (string, error) {
	return c.doBody(ctx, method, path, body)
}

func (c *Client) doBody(ctx context.Context, method, path string, body []byte) (string, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort on a read body
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("daemon %s failed: %s", path, msg)
	}
	return string(respBody), nil
}

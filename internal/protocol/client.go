package protocol

import (
	"context"
	"fmt"
	"net/http"
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

// Health reports whether a daemon is reachable at the client's address.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, PathHealth)
}

// Stop asks the daemon to drain and shut down gracefully.
func (c *Client) Stop(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, PathStop)
}

func (c *Client) do(ctx context.Context, method, path string) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort on a read body
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon %s failed: %s", path, resp.Status)
	}
	return nil
}

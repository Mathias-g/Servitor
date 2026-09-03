package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPConnector is one declared mcp-http server's reachability: the URL of its
// Streamable HTTP endpoint and its header templates. Header values may
// reference secrets as `$NAME` tokens (for example "Bearer $SEARCH_TOKEN"),
// resolved per use against the node's filtered secret env (SPEC: Secret
// resolution, ADR-0033). It is declared in the config, not compiled in
// (ADR-0047).
type HTTPConnector struct {
	URL     string
	Headers map[string]string
}

// HTTPCallRequest is one tool invocation over Streamable HTTP.
type HTTPCallRequest struct {
	// Connector is the URL and headers of the server to call. Headers must be
	// already resolved (ResolveHeaders); values never carry unresolved `$`
	// references.
	Connector HTTPConnector
	// Mode is the protocol mode to speak. When empty the client probes the
	// server to detect it.
	Mode Mode
	// Tool is the named tool to invoke.
	Tool string
	// Input is the tool arguments.
	Input map[string]any
}

// httpClient is a live JSON-RPC session over Streamable HTTP. A classic server
// returns an Mcp-Session-Id header on initialize that must ride on each later
// request; the stateless revision needs no session and carries `_meta` per
// request instead.
type httpClient struct {
	url     string
	headers map[string]string
	hc      *http.Client
	session string
	nextID  int
}

// newHTTPClient builds a client over a resolved connector.
func newHTTPClient(conn HTTPConnector) *httpClient {
	return &httpClient{
		url:     conn.URL,
		headers: conn.Headers,
		hc:      &http.Client{Timeout: 30 * time.Second},
	}
}

// roundtrip POSTs one JSON-RPC request and returns the matching response,
// handling both the plain-JSON and Server-Sent-Events response forms a
// Streamable HTTP server may use (MCP spec, Transports). It captures an
// Mcp-Session-Id header for classic sessions.
func (c *httpClient) roundtrip(ctx context.Context, req rpcRequest) (rpcResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return rpcResponse{}, err
	}
	resp, err := c.post(ctx, body)
	if err != nil {
		return rpcResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.session = sid
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return readSSE(resp.Body)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return rpcResponse{}, fmt.Errorf("mcp http: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var r rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return rpcResponse{}, fmt.Errorf("mcp http: decode response: %w", err)
	}
	return r, nil
}

// readSSE parses a Server-Sent-Events response body for its JSON-RPC data
// payload. A Streamable HTTP server may stream the response; the JSON-RPC
// message rides in the `data:` lines.
func readSSE(r io.Reader) (rpcResponse, error) {
	sc := bufio.NewScanner(r)
	var data []byte
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if d == "[DONE]" {
				continue
			}
			data = append(data, d...)
		}
	}
	if len(data) == 0 {
		return rpcResponse{}, fmt.Errorf("mcp http: empty SSE response")
	}
	var resp rpcResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return rpcResponse{}, fmt.Errorf("mcp http: parse SSE data: %w", err)
	}
	return resp, nil
}

// send writes one request and returns the matching response.
func (c *httpClient) send(ctx context.Context, method string, params, meta map[string]any) (rpcResponse, error) {
	c.nextID++
	return c.roundtrip(ctx, rpcRequest{JSONRPC: "2.0", ID: &c.nextID, Method: method, Params: params, Meta: meta})
}

// notify writes a request that carries no id (a notification), used by the
// classic handshake. The response body is ignored; only a transport error is
// returned.
func (c *httpClient) notify(ctx context.Context, method string, params map[string]any) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	resp, err := c.post(ctx, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.session = sid
	}
	return nil
}

// post POSTs a raw JSON body to the endpoint with the connector's headers and
// session, returning the response without reading its body.
func (c *httpClient) post(ctx context.Context, body []byte) (*http.Response, error) {
	hreq, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp http: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		hreq.Header.Set(k, v)
	}
	if c.session != "" {
		hreq.Header.Set("Mcp-Session-Id", c.session)
	}
	resp, err := c.hc.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("mcp http: %s: %w", c.url, err)
	}
	return resp, nil
}

// initialize performs the classic handshake. It is only used in ModeClassic.
func (c *httpClient) initialize(ctx context.Context) error {
	resp, err := c.send(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "servitor", "version": "0"},
	}, nil)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("mcp http: initialize: %s", resp.Error.Message)
	}
	return c.notify(ctx, "notifications/initialized", map[string]any{})
}

// probe detects the protocol mode a server expects over HTTP. It tries the
// stateless tools/list first, and on a failure falls back to the classic
// initialize handshake.
func (c *httpClient) probe(ctx context.Context) (Mode, error) {
	resp, err := c.send(ctx, "tools/list", map[string]any{}, meta())
	if err == nil && resp.Error == nil {
		return ModeStateless, nil
	}
	if ierr := c.initialize(ctx); ierr != nil {
		return ModeUnknown, fmt.Errorf("mcp http: probe: neither stateless nor classic: %w", ierr)
	}
	return ModeClassic, nil
}

// listTools returns the server's tools, speaking the given mode.
func (c *httpClient) listTools(ctx context.Context, mode Mode) ([]Tool, error) {
	var params, m map[string]any
	if mode == ModeClassic {
		params = map[string]any{}
	} else {
		m = meta()
	}
	resp, err := c.send(ctx, "tools/list", params, m)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp http: tools/list: %s", resp.Error.Message)
	}
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("mcp http: tools/list result: %w", err)
	}
	return result.Tools, nil
}

// HTTPDiscover probes a URL-based server's mode and lists its tools. It is the
// once-at-refresh capability discovery for an mcp-http server, the HTTP
// counterpart to Discover for a stdio server (ADR-0047).
func HTTPDiscover(ctx context.Context, conn HTTPConnector) (Discovery, error) {
	c := newHTTPClient(conn)
	mode, err := c.probe(ctx)
	if err != nil {
		return Discovery{}, err
	}
	tools, err := c.listTools(ctx, mode)
	if err != nil {
		return Discovery{}, err
	}
	return Discovery{Mode: mode, Tools: tools}, nil
}

// HTTPCall invokes one tool on a URL-based server and returns its structured
// result. When req.Mode is empty it probes the mode first; the clean path
// authors the mode into the Wafer so no probe happens per node (ADR-0015).
func HTTPCall(ctx context.Context, req HTTPCallRequest) (CallResult, error) {
	c := newHTTPClient(req.Connector)
	mode := req.Mode
	switch mode {
	case ModeUnknown:
		m, err := c.probe(ctx)
		if err != nil {
			return CallResult{}, err
		}
		mode = m
	case ModeClassic:
		if err := c.initialize(ctx); err != nil {
			return CallResult{}, err
		}
	}

	var params, m map[string]any
	params = map[string]any{"name": req.Tool, "arguments": req.Input}
	if mode == ModeStateless {
		m = meta()
	}
	resp, err := c.send(ctx, "tools/call", params, m)
	if err != nil {
		return CallResult{}, err
	}
	if resp.Error != nil {
		return CallResult{}, fmt.Errorf("mcp http: tools/call %s: %s", req.Tool, resp.Error.Message)
	}

	var result struct {
		Content []map[string]any `json:"content"`
		IsError bool             `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return CallResult{}, fmt.Errorf("mcp http: tools/call result: %w", err)
	}
	out := CallResult{IsError: result.IsError, Data: result.Content}
	out.Content = textFromContent(result.Content)
	return out, nil
}

// ResolveHeaders substitutes `$NAME` secret references in each header template
// with the value of NAME from env, the filtered secret env of the node's
// subprocess. A reference to a name not present in env is an error, so a header
// that names a secret the node did not declare fails fast (SPEC: Secret
// resolution, ADR-0033).
func ResolveHeaders(headers map[string]string, env []string) (map[string]string, error) {
	if len(headers) == 0 {
		return headers, nil
	}
	vals := map[string]string{}
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok {
			vals[name] = value
		}
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		resolved, err := substitute(v, vals)
		if err != nil {
			return nil, fmt.Errorf("mcp http: header %s: %w", k, err)
		}
		out[k] = resolved
	}
	return out, nil
}

// ReferencedSecrets returns the distinct `$NAME` secret references across the
// header templates, in the order they first appear. Capabilities uses it to
// know which secrets to resolve before probing a URL-based server.
func ReferencedSecrets(headers map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range headers {
		eachRef(v, func(name string) {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		})
	}
	return out
}

// eachRef walks template and calls fn with each `$NAME` reference found.
func eachRef(template string, fn func(name string)) {
	rest := template
	for {
		idx := strings.Index(rest, "$")
		if idx < 0 {
			return
		}
		after := rest[idx+1:]
		j := 0
		for j < len(after) {
			ch := after[j]
			if ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
				j++
			} else {
				break
			}
		}
		if j == 0 {
			rest = after
			continue
		}
		fn(after[:j])
		rest = after[j:]
	}
}

// substitute replaces each `$NAME` token in template with the value of NAME in
// vals. A `$` not followed by an identifier is kept as a literal. A reference
// to a missing name is an error.
func substitute(template string, vals map[string]string) (string, error) {
	var sb strings.Builder
	rest := template
	for {
		idx := strings.Index(rest, "$")
		if idx < 0 {
			sb.WriteString(rest)
			return sb.String(), nil
		}
		sb.WriteString(rest[:idx])
		after := rest[idx+1:]
		j := 0
		for j < len(after) {
			ch := after[j]
			if ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
				j++
			} else {
				break
			}
		}
		if j == 0 {
			sb.WriteString("$")
			rest = after
			continue
		}
		name := after[:j]
		val, ok := vals[name]
		if !ok {
			return "", fmt.Errorf("header references undeclared secret %q", name)
		}
		sb.WriteString(val)
		rest = after[j:]
	}
}

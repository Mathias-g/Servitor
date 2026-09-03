// Package mcp implements the `mcp-stdio` and `mcp-http` node types (ADR-0015,
// ADR-0047): invoking one named tool on one named MCP server. An MCP server
// exposes named tools, each with a JSON Schema for its input, over JSON-RPC 2.0.
// `mcp-stdio` reaches a server as a subprocess over newline delimited JSON-RPC
// on its stdin/stdout; `mcp-http` reaches a remote server over Streamable HTTP
// (http.go). Each node sends a single `tools/call`, reads the structured JSON
// response, and exits (client-mode executor, distinct from the singer
// run-and-read executor).
//
// Protocol modes. The MCP spec was revised on 2026-07-28 to be stateless:
// protocol version and capabilities travel inline in a `_meta` field on each
// request and the `initialize`/`initialized` handshake was removed. Adoption is
// uneven, so a server may still expect the original handshake. This package
// supports both, for either transport, and detects which a server expects once
// at discovery; the detected mode is carried into the Wafer so a node execution
// never re-probes.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Mode is the MCP protocol variant a server expects.
type Mode string

const (
	// ModeUnknown means the mode has not been determined; the client probes on
	// the next interaction.
	ModeUnknown Mode = ""
	// ModeStateless is the revised stateless protocol: version and
	// capabilities ride in a `_meta` field, no handshake.
	ModeStateless Mode = "stateless"
	// ModeClassic is the original protocol with the initialize/initialized
	// handshake.
	ModeClassic Mode = "classic"
)

// Tool is one named tool a server exposes, with its input JSON Schema.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ServerRequest is what a node needs to run or discover a server.
type ServerRequest struct {
	// Command is the server's argv, for example ["atomic-server"]. It must be
	// non-empty.
	Command []string
	// Env is the filtered environment for the server (only declared secrets
	// plus PATH), built with exec.FilteredEnv.
	Env []string
	// Mode is the protocol mode to speak. When empty the client probes the
	// server to detect it.
	Mode Mode
}

// Discovery is the outcome of probing a server: its tools and the mode it
// speaks. It is what `servitor capabilities` caches.
type Discovery struct {
	Mode  Mode   `json:"mode"`
	Tools []Tool `json:"tools"`
}

// CallRequest is one tool invocation.
type CallRequest struct {
	Server ServerRequest
	// Tool is the named tool to invoke.
	Tool string
	// Input is the tool arguments.
	Input map[string]any
}

// CallResult is the outcome of a tool call.
type CallResult struct {
	// Content are the text content blocks the server returned.
	Content string `json:"content"`
	// Data is the raw structured content blocks, for tool results that are not
	// text.
	Data []map[string]any `json:"data,omitempty"`
	// IsError is the server's error flag; when true the caller maps the result
	// onto Servitor's structured error format (ADR-0015).
	IsError bool `json:"isError"`
}

// rpcRequest is one JSON-RPC 2.0 request.
type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      *int           `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
	Meta    map[string]any `json:"_meta,omitempty"`
}

// rpcResponse is one JSON-RPC 2.0 response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// client is a live JSON-RPC session over a server subprocess.
type client struct {
	cmd    *exec.Cmd
	in     *bufio.Writer
	out    *bufio.Scanner
	stderr *bytes.Buffer
	nextID int
}

// start launches the server subprocess and returns a client over its stdio.
func start(ctx context.Context, req ServerRequest) (*client, error) {
	if len(req.Command) == 0 {
		return nil, fmt.Errorf("mcp: empty command")
	}
	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...)
	cmd.Env = req.Env
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %s: %w", req.Command[0], err)
	}
	return &client{
		cmd:    cmd,
		in:     bufio.NewWriter(stdin),
		out:    bufio.NewScanner(stdout),
		stderr: stderr,
		nextID: 1,
	}, nil
}

// close terminates the server.
func (c *client) close() {
	_ = c.cmd.Process.Kill()
	_ = c.cmd.Wait()
}

// send writes one request and returns the matching response.
func (c *client) send(ctx context.Context, method string, params, meta map[string]any) (rpcResponse, error) {
	id := c.nextID
	c.nextID++
	req := rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params, Meta: meta}
	data, err := json.Marshal(req)
	if err != nil {
		return rpcResponse{}, err
	}
	if _, err := c.in.Write(append(data, '\n')); err != nil {
		return rpcResponse{}, fmt.Errorf("mcp: write %s: %w", method, err)
	}
	if err := c.in.Flush(); err != nil {
		return rpcResponse{}, fmt.Errorf("mcp: flush %s: %w", method, err)
	}
	for c.out.Scan() {
		line := bytes.TrimSpace(c.out.Bytes())
		if len(line) == 0 {
			continue
		}
		var resp rpcResponse
		if uerr := json.Unmarshal(line, &resp); uerr != nil {
			continue
		}
		if resp.ID == id {
			return resp, nil
		}
	}
	if err := c.out.Err(); err != nil {
		return rpcResponse{}, err
	}
	if ctx.Err() != nil {
		return rpcResponse{}, ctx.Err()
	}
	return rpcResponse{}, fmt.Errorf("mcp: server closed without responding to %s: %s", method, c.stderr.String())
}

// meta builds the stateless `_meta` payload carried on each request.
func meta() map[string]any {
	return map[string]any{
		"protocolVersion": "2026-07-28",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "servitor", "version": "0"},
	}
}

// initialize performs the classic handshake. It is only used in ModeClassic.
func (c *client) initialize(ctx context.Context) error {
	resp, err := c.send(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "servitor", "version": "0"},
	}, nil)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("mcp: initialize: %s", resp.Error.Message)
	}
	// notifications/initialized carries no id.
	notif := rpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"}
	data, _ := json.Marshal(notif)
	if _, err := c.in.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("mcp: initialized: %w", err)
	}
	return c.in.Flush()
}

// probe detects the protocol mode a server expects (ADR-0015). It tries the
// stateless tools/list first, and on an error (or a classic-handshake-required
// response) falls back to initialize then tools/list.
func probe(ctx context.Context, c *client) (Mode, error) {
	resp, err := c.send(ctx, "tools/list", map[string]any{}, meta())
	if err == nil && resp.Error == nil {
		return ModeStateless, nil
	}
	if ierr := c.initialize(ctx); ierr != nil {
		return ModeUnknown, fmt.Errorf("mcp: probe: neither stateless nor classic: %w", ierr)
	}
	return ModeClassic, nil
}

// listTools returns the server's tools, speaking the given mode (the server is
// already initialized if classic).
func (c *client) listTools(ctx context.Context, mode Mode) ([]Tool, error) {
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
		return nil, fmt.Errorf("mcp: tools/list: %s", resp.Error.Message)
	}
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("mcp: tools/list result: %w", err)
	}
	return result.Tools, nil
}

// Discover probes a server's mode and lists its tools. It is the once-at-refresh
// capability discovery; the returned mode is what a `mcp-stdio` node authors so
// execution does not re-probe (ADR-0015).
func Discover(ctx context.Context, req ServerRequest) (Discovery, error) {
	c, err := start(ctx, req)
	if err != nil {
		return Discovery{}, err
	}
	defer c.close()

	mode, err := probe(ctx, c)
	if err != nil {
		return Discovery{}, err
	}
	tools, err := c.listTools(ctx, mode)
	if err != nil {
		return Discovery{}, err
	}
	return Discovery{Mode: mode, Tools: tools}, nil
}

// Call invokes one tool on a server and returns its structured result. When
// req.Server.Mode is empty, it probes the mode first (used for a first run
// before discovery); the clean path authors the mode into the Wafer so no probe
// happens per node.
func Call(ctx context.Context, req CallRequest) (CallResult, error) {
	mode := req.Server.Mode
	c, err := start(ctx, req.Server)
	if err != nil {
		return CallResult{}, err
	}
	defer c.close()

	switch mode {
	case ModeUnknown:
		mode, err = probe(ctx, c)
		if err != nil {
			return CallResult{}, err
		}
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
		return CallResult{}, fmt.Errorf("mcp: tools/call %s: %s", req.Tool, resp.Error.Message)
	}

	var result struct {
		Content []map[string]any `json:"content"`
		IsError bool             `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return CallResult{}, fmt.Errorf("mcp: tools/call result: %w", err)
	}
	out := CallResult{IsError: result.IsError, Data: result.Content}
	out.Content = textFromContent(result.Content)
	return out, nil
}

// textFromContent concatenates the text content blocks of a tool result.
func textFromContent(blocks []map[string]any) string {
	var parts []string
	for _, b := range blocks {
		if t, ok := b["type"].(string); ok && t == "text" {
			if s, ok := b["text"].(string); ok {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// StructuredError maps an errored tool result onto Servitor's structured
// validation error format (ADR-0015).
type StructuredError struct {
	Path       string `json:"path"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

// AsStructuredError converts a tool call whose result flagged IsError into the
// structured error shape.
func AsStructuredError(tool string, res CallResult) StructuredError {
	msg := strings.TrimSpace(res.Content)
	if msg == "" {
		msg = fmt.Sprintf("mcp tool %q reported an error", tool)
	}
	return StructuredError{
		Path:       "/tool",
		Code:       "mcp_tool_error",
		Message:    msg,
		Suggestion: "check the tool arguments and the server's error output",
	}
}

package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeServer writes a fake MCP server (a python script) that speaks the given
// protocol mode and behaves like a real server: a classic server rejects
// tools/list until initialized, so the client's probe detects classic and falls
// back to the handshake. This is how both modes and the error mapping are
// tested without a real server.
func fakeServer(t *testing.T, dir, mode string) string {
	t.Helper()
	var body string
	if mode == "classic" {
		body = `
import json, sys
initialized = False
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    req = json.loads(line)
    m = req.get("method")
    rid = req.get("id")
    if m == "initialize":
        initialized = True
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{"protocolVersion":"2025-06-18","capabilities":{}}}))
    elif m == "notifications/initialized":
        pass
    elif m == "tools/list":
        if not initialized:
            print(json.dumps({"jsonrpc":"2.0","id":rid,"error":{"code":-32002,"message":"not initialized"}}))
        else:
            print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{"tools":[{"name":"search","description":"search","inputSchema":{"type":"object","properties":{"query":{"type":"string"}}}}]}}))
    elif m == "tools/call":
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{"content":[{"type":"text","text":"found it"}],"isError":False}}))
    sys.stdout.flush()
`
	} else {
		body = `
import json, sys
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    req = json.loads(line)
    m = req.get("method")
    rid = req.get("id")
    if m == "tools/list":
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{"tools":[{"name":"search","description":"search","inputSchema":{"type":"object","properties":{"query":{"type":"string"}}}}]}}))
    elif m == "tools/call":
        print(json.dumps({"jsonrpc":"2.0","id":rid,"result":{"content":[{"type":"text","text":"found it"}],"isError":False}}))
    sys.stdout.flush()
`
	}
	script := "#!/usr/bin/env python3\n" + body
	p := filepath.Join(dir, "mcp-"+mode)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDiscoverStatelessMode(t *testing.T) {
	dir := t.TempDir()
	server := fakeServer(t, dir, "stateless")
	env := []string{"PATH=" + os.Getenv("PATH")}

	d, err := Discover(context.Background(), ServerRequest{Command: []string{server}, Env: env})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if d.Mode != ModeStateless {
		t.Fatalf("mode = %q, want stateless", d.Mode)
	}
	if len(d.Tools) != 1 || d.Tools[0].Name != "search" {
		t.Fatalf("tools = %+v, want search", d.Tools)
	}
}

func TestDiscoverClassicMode(t *testing.T) {
	dir := t.TempDir()
	server := fakeServer(t, dir, "classic")
	env := []string{"PATH=" + os.Getenv("PATH")}

	d, err := Discover(context.Background(), ServerRequest{Command: []string{server}, Env: env})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if d.Mode != ModeClassic {
		t.Fatalf("mode = %q, want classic", d.Mode)
	}
	if len(d.Tools) != 1 || d.Tools[0].Name != "search" {
		t.Fatalf("tools = %+v, want search", d.Tools)
	}
}

func TestCallStatelessWithAuthoredMode(t *testing.T) {
	dir := t.TempDir()
	server := fakeServer(t, dir, "stateless")
	env := []string{"PATH=" + os.Getenv("PATH")}

	res, err := Call(context.Background(), CallRequest{
		Server: ServerRequest{Command: []string{server}, Env: env, Mode: ModeStateless},
		Tool:   "search",
		Input:  map[string]any{"query": "notes"},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.IsError {
		t.Fatal("unexpected isError")
	}
	if res.Content != "found it" {
		t.Fatalf("content = %q, want found it", res.Content)
	}
}

func TestCallClassicWithAuthoredMode(t *testing.T) {
	dir := t.TempDir()
	server := fakeServer(t, dir, "classic")
	env := []string{"PATH=" + os.Getenv("PATH")}

	res, err := Call(context.Background(), CallRequest{
		Server: ServerRequest{Command: []string{server}, Env: env, Mode: ModeClassic},
		Tool:   "search",
		Input:  map[string]any{"query": "notes"},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.Content != "found it" {
		t.Fatalf("content = %q, want found it", res.Content)
	}
}

func TestCallUnknownModeProbes(t *testing.T) {
	dir := t.TempDir()
	server := fakeServer(t, dir, "stateless")
	env := []string{"PATH=" + os.Getenv("PATH")}

	res, err := Call(context.Background(), CallRequest{
		Server: ServerRequest{Command: []string{server}, Env: env, Mode: ModeUnknown},
		Tool:   "search",
		Input:  map[string]any{"query": "notes"},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.Content != "found it" {
		t.Fatalf("content = %q, want found it", res.Content)
	}
}

func TestErrorMapping(t *testing.T) {
	se := AsStructuredError("search", CallResult{IsError: true, Content: "no results"})
	if se.Code != "mcp_tool_error" {
		t.Fatalf("code = %q, want mcp_tool_error", se.Code)
	}
	if !strings.Contains(se.Message, "no results") {
		t.Fatalf("message = %q, want to include server message", se.Message)
	}
	if se.Path == "" {
		t.Fatal("path should be set")
	}
}

func TestDiscoverServersFromDeclaration(t *testing.T) {
	dir := t.TempDir()
	server := fakeServer(t, dir, "stateless")

	// Declared by exact command (ADR-0018): the server may have any name. A
	// non-declared executable is not reported.
	if err := os.WriteFile(filepath.Join(dir, "atomic-server"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Keep python3 (for the fake server's shebang) on PATH for execution.
	t.Setenv("PATH", os.Getenv("PATH"))

	servers := DiscoverServers(map[string][]string{"fake": {server}}, nil)
	if len(servers) != 1 {
		t.Fatalf("servers = %+v, want only the declared fake", servers)
	}
	if servers[0].Name != "fake" {
		t.Fatalf("name = %q, want fake", servers[0].Name)
	}
	if servers[0].Mode != ModeStateless {
		t.Fatalf("mode = %q, want stateless", servers[0].Mode)
	}
	if len(servers[0].Tools) != 1 || servers[0].Tools[0].Name != "search" {
		t.Fatalf("tools = %+v, want search", servers[0].Tools)
	}
}

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeHTTPServer is a Streamable HTTP MCP server used to test the http client
// without a real server. It speaks the requested mode: a stateless server
// answers tools/list and tools/call with a `_meta`-carrying request; a classic
// server requires initialize (returning an Mcp-Session-Id) before tools/list.
func fakeHTTPServer(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "tools/list":
			// A classic server rejects tools/list until initialized.
			if mode == "classic" && r.Header.Get("Mcp-Session-Id") == "" {
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32002,"message":"not initialized"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"search","inputSchema":{"type":"object","properties":{"query":{"type":"string"}}}}]}}`))
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-1")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"found it"}],"isError":false}}`))
		default:
			http.Error(w, "unsupported method "+req.Method, http.StatusBadRequest)
		}
	}))
}

func TestHTTPDiscoverStateless(t *testing.T) {
	srv := fakeHTTPServer(t, "stateless")
	defer srv.Close()

	d, err := HTTPDiscover(context.Background(), HTTPConnector{URL: srv.URL})
	if err != nil {
		t.Fatalf("HTTPDiscover: %v", err)
	}
	if d.Mode != ModeStateless {
		t.Fatalf("mode = %q, want stateless", d.Mode)
	}
	if len(d.Tools) != 1 || d.Tools[0].Name != "search" {
		t.Fatalf("tools = %+v, want search", d.Tools)
	}
}

func TestHTTPDiscoverClassic(t *testing.T) {
	srv := fakeHTTPServer(t, "classic")
	defer srv.Close()

	d, err := HTTPDiscover(context.Background(), HTTPConnector{URL: srv.URL})
	if err != nil {
		t.Fatalf("HTTPDiscover: %v", err)
	}
	if d.Mode != ModeClassic {
		t.Fatalf("mode = %q, want classic", d.Mode)
	}
	if len(d.Tools) != 1 || d.Tools[0].Name != "search" {
		t.Fatalf("tools = %+v, want search", d.Tools)
	}
}

func TestHTTPCallStateless(t *testing.T) {
	srv := fakeHTTPServer(t, "stateless")
	defer srv.Close()

	res, err := HTTPCall(context.Background(), HTTPCallRequest{
		Connector: HTTPConnector{URL: srv.URL},
		Mode:      ModeStateless,
		Tool:      "search",
		Input:     map[string]any{"query": "notes"},
	})
	if err != nil {
		t.Fatalf("HTTPCall: %v", err)
	}
	if res.IsError || res.Content != "found it" {
		t.Fatalf("result = %+v, want found it", res)
	}
}

func TestHTTPCallUnknownModeProbes(t *testing.T) {
	srv := fakeHTTPServer(t, "stateless")
	defer srv.Close()

	res, err := HTTPCall(context.Background(), HTTPCallRequest{
		Connector: HTTPConnector{URL: srv.URL},
		Mode:      ModeUnknown,
		Tool:      "search",
		Input:     map[string]any{"query": "notes"},
	})
	if err != nil {
		t.Fatalf("HTTPCall: %v", err)
	}
	if res.Content != "found it" {
		t.Fatalf("content = %q, want found it", res.Content)
	}
}

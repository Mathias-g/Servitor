package mcp

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoveredServer is an installed MCP server discovered from PATH, with its
// protocol mode and tool schemas (ADR-0017, ADR-0015). A discovery that fails
// records the error rather than failing the whole report, so `capabilities`
// still works when a server is broken (SPEC: How an agent discovers
// integrations).
type DiscoveredServer struct {
	Name     string `json:"name"`
	Mode     Mode   `json:"mode"`
	Tools    []Tool `json:"tools,omitempty"`
	ProbeErr string `json:"probe_error,omitempty"`
}

// DiscoverServers enumerates the `mcp-*` executables on PATH and probes each
// one's protocol mode and tools via `tools/list`. It is invoked once during a
// capabilities refresh, not per step execution (SPEC: Capability discovery,
// ADR-0017).
func DiscoverServers(env []string) []DiscoveredServer {
	names := mcpOnPath()
	servers := make([]DiscoveredServer, 0, len(names))
	for _, name := range names {
		servers = append(servers, discoverServer(name, env))
	}
	return servers
}

// discoverServer probes one MCP server for its mode and tools.
func discoverServer(name string, env []string) DiscoveredServer {
	ds := DiscoveredServer{Name: name}
	d, err := Discover(context.Background(), ServerRequest{Command: []string{name}, Env: env})
	if err != nil {
		ds.ProbeErr = err.Error()
		return ds
	}
	ds.Mode = d.Mode
	ds.Tools = d.Tools
	return ds
}

// mcpOnPath returns the executable names on PATH that look like MCP servers
// (`mcp-*`), sorted.
func mcpOnPath() []string {
	var found []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), "mcp-") {
				continue
			}
			info, err := e.Info()
			if err != nil || info.Mode()&0o111 == 0 {
				continue
			}
			if !seen[e.Name()] {
				seen[e.Name()] = true
				found = append(found, e.Name())
			}
		}
	}
	sort.Strings(found)
	return found
}

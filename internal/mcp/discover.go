package mcp

import (
	"context"
	"sort"
)

// DiscoveredServer is a declared MCP server, with its protocol mode and tool
// schemas (ADR-0015). A discovery that fails records the error rather than
// failing the whole report, so `capabilities` still works when a server is
// broken (SPEC: How an agent discovers integrations).
type DiscoveredServer struct {
	Name     string `json:"name"`
	Mode     Mode   `json:"mode"`
	Tools    []Tool `json:"tools,omitempty"`
	ProbeErr string `json:"probe_error,omitempty"`
}

// DiscoverServers probes each declared server (name to command) for its
// protocol mode and tools via tools/list. It is invoked once during a
// capabilities refresh, not per node execution (SPEC: Capability discovery).
// The declared set comes from the integrations config (ADR-0018); there is no
// PATH scan.
func DiscoverServers(declared map[string][]string, env []string) []DiscoveredServer {
	names := sortedKeys(declared)
	servers := make([]DiscoveredServer, 0, len(names))
	for _, name := range names {
		servers = append(servers, discoverServer(name, declared[name], env))
	}
	return servers
}

// discoverServer probes one MCP server for its mode and tools.
func discoverServer(name string, command, env []string) DiscoveredServer {
	ds := DiscoveredServer{Name: name}
	if len(command) == 0 {
		command = []string{name}
	}
	d, err := Discover(context.Background(), ServerRequest{Command: command, Env: env})
	if err != nil {
		ds.ProbeErr = err.Error()
		return ds
	}
	ds.Mode = d.Mode
	ds.Tools = d.Tools
	return ds
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

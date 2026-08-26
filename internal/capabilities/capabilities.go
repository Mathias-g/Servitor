// Package capabilities materializes the per-server capability set (SPEC: How
// an agent discovers integrations) as files an agent can read on demand. For
// each capability it writes the JSON Schema and a derived example
// fragment, grouped by mechanism (core, webhook, singer, mcp, helper,
// websocket; ADR-0017). Discovered executables sit with their mechanism
// (singer/taps.yaml, mcp/servers.yaml). A pipeline can commit the output
// directory so a remote agent reads capabilities from the repo rather than
// reaching the loopback-only daemon (ADR-0009).
package capabilities

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/Mathias-g/Servitor/internal/integrations"
	"github.com/Mathias-g/Servitor/internal/mcp"
	"github.com/Mathias-g/Servitor/internal/registry"
	"github.com/Mathias-g/Servitor/internal/singer"
)

// DefaultDir is where capabilities writes when no directory is given.
const DefaultDir = ".servitor/capabilities"

// entry is the on-disk shape for one capability: its kind, role, delivery, schema, and
// a derived example fragment.
type entry struct {
	Kind        string         `yaml:"kind"`
	Type        string         `yaml:"type"`
	Mechanism   string         `yaml:"mechanism"`
	Role        string         `yaml:"role"`
	Delivery    string         `yaml:"delivery,omitempty"`
	Description string         `yaml:"description"`
	Schema      map[string]any `yaml:"schema"`
	Example     map[string]any `yaml:"example"`
}

// mechanism is one group in the index: its nodes and triggers.
type mechanism struct {
	Name     string   `yaml:"name"`
	Nodes    []string `yaml:"nodes"`
	Triggers []string `yaml:"triggers"`
}

// index lists the mechanisms and the types each contains.
type index struct {
	Generated  bool        `yaml:"generated"`
	Mechanisms []mechanism `yaml:"mechanisms"`
}

// Write materializes the capability set into dir. It creates the directory and
// one subdirectory per mechanism, with one file per type, plus index.yaml.
func Write(dir string) error {
	if dir == "" {
		dir = DefaultDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("capabilities: create %s: %w", dir, err)
	}

	if err := writeTypes(dir); err != nil {
		return err
	}
	if err := writeIndex(dir); err != nil {
		return err
	}
	if err := writeSecrets(dir); err != nil {
		return err
	}
	if err := writeTaps(dir); err != nil {
		return err
	}
	if err := writeServers(dir); err != nil {
		return err
	}
	return nil
}

// tapsReport is the on-disk shape of the available-taps report.
type tapsReport struct {
	Generated bool                   `yaml:"generated"`
	Note      string                 `yaml:"note,omitempty"`
	Taps      []singer.DiscoveredTap `yaml:"taps"`
}

// writeTaps writes a taps.yaml under the singer/ group reporting the declared
// Singer taps and their discovered schemas (SPEC: How an agent discovers
// integrations, ADR-0018). It sits beside the singer-tap node so an agent
// sees both the type and what is declared to run against it (ADR-0017).
func writeTaps(dir string) error {
	report := tapsReport{Generated: true}
	cfg, err := integrations.Load("")
	if err != nil {
		report.Note = "could not load integrations config: " + err.Error()
	} else {
		declared := map[string][]string{}
		if cfg.Singer != nil {
			for name, t := range cfg.Singer.Taps {
				declared[name] = t.Command
			}
		}
		report.Taps = singer.DiscoverTaps(declared)
	}
	data, err := yaml.Marshal(report)
	if err != nil {
		return fmt.Errorf("capabilities: marshal taps: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, registry.Singer), 0o755); err != nil {
		return fmt.Errorf("capabilities: create singer group: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, registry.Singer, "taps.yaml"), data, 0o644); err != nil {
		return fmt.Errorf("capabilities: write singer/taps.yaml: %w", err)
	}
	return nil
}

// serversReport is the on-disk shape of the available-MCP-servers report.
type serversReport struct {
	Generated bool                   `yaml:"generated"`
	Note      string                 `yaml:"note,omitempty"`
	Servers   []mcp.DiscoveredServer `yaml:"servers"`
}

// writeServers writes a servers.yaml under the mcp/ group reporting the
// declared MCP servers (ADR-0017, ADR-0018): each declared server, its protocol
// mode, and its tool schemas. It sits beside the mcp-call node so an agent
// sees both the type and what is declared. If a server cannot be probed, the
// report records its error rather than failing.
func writeServers(dir string) error {
	report := serversReport{Generated: true}
	cfg, err := integrations.Load("")
	if err != nil {
		report.Note = "could not load integrations config: " + err.Error()
	} else {
		declared := map[string][]string{}
		for name, s := range cfg.MCP {
			declared[name] = s.Command
		}
		report.Servers = mcp.DiscoverServers(declared, nil)
	}
	data, err := yaml.Marshal(report)
	if err != nil {
		return fmt.Errorf("capabilities: marshal servers: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, registry.MCP), 0o755); err != nil {
		return fmt.Errorf("capabilities: create mcp group: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, registry.MCP, "servers.yaml"), data, 0o644); err != nil {
		return fmt.Errorf("capabilities: write mcp/servers.yaml: %w", err)
	}
	return nil
}

// secretEntry is one declared secret and whether it is present. Values are
// never written; only the name and presence, so the report is safe to commit
// (SPEC: How an agent discovers integrations).
type secretEntry struct {
	Name    string `yaml:"name"`
	Present bool   `yaml:"present"`
}

// secretsReport is the on-disk shape of the secrets report.
type secretsReport struct {
	Generated bool          `yaml:"generated"`
	Note      string        `yaml:"note,omitempty"`
	Secrets   []secretEntry `yaml:"secrets"`
}

// writeSecrets writes a secrets.yaml reporting the secrets declared in the
// varlock schema in the working directory (names and presence only, never
// values). If varlock is unavailable or load fails, it writes a note instead
// of failing, so `capabilities` still works without varlock.
func writeSecrets(dir string) error {
	report := secretsReport{Generated: true}
	entries, note, err := declaredSecrets()
	if err != nil {
		report.Note = note
	} else {
		report.Secrets = entries
	}
	data, err := yaml.Marshal(report)
	if err != nil {
		return fmt.Errorf("capabilities: marshal secrets: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.yaml"), data, 0o644); err != nil {
		return fmt.Errorf("capabilities: write secrets.yaml: %w", err)
	}
	return nil
}

// declaredSecrets queries varlock for the declared secrets in the working
// directory and returns each name and whether a value is present. It returns a
// note explaining the absence when varlock is not available or cannot resolve.
func declaredSecrets() (entries []secretEntry, note string, err error) {
	if _, lerr := exec.LookPath("varlock"); lerr != nil {
		return nil, "varlock not available; declared secrets could not be enumerated", lerr
	}
	out, cerr := exec.Command("varlock", "load", "--format", "json-full").Output()
	if cerr != nil {
		return nil, "varlock load failed; declared secrets could not be enumerated", cerr
	}
	var full struct {
		Config map[string]struct {
			Value       string `json:"value"`
			IsSensitive bool   `json:"isSensitive"`
		} `json:"config"`
	}
	if uerr := json.Unmarshal(out, &full); uerr != nil {
		return nil, "could not parse varlock output", uerr
	}
	names := make([]string, 0, len(full.Config))
	for name, c := range full.Config {
		if c.IsSensitive {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		c := full.Config[name]
		entries = append(entries, secretEntry{Name: name, Present: c.Value != ""})
	}
	return entries, "", nil
}

func writeTypes(dir string) error {
	for _, st := range registry.Nodes() {
		e := entry{
			Kind:        "node",
			Type:        st.Name,
			Mechanism:   st.Mechanism,
			Role:        string(st.Role),
			Description: st.Desc,
			Schema:      st.JSONSchema(),
			Example:     st.NodeExample(),
		}
		if err := writeEntry(dir, e); err != nil {
			return err
		}
	}
	for _, tt := range registry.TriggerTypes() {
		e := entry{
			Kind:        "trigger",
			Type:        tt.Name,
			Mechanism:   tt.Mechanism,
			Role:        "trigger",
			Delivery:    tt.Delivery,
			Description: tt.Desc,
			Schema:      tt.JSONSchema(),
			Example:     tt.TriggerExample(),
		}
		if err := writeEntry(dir, e); err != nil {
			return err
		}
	}
	return nil
}

func writeEntry(dir string, e entry) error {
	groupDir := filepath.Join(dir, e.Mechanism)
	if err := os.MkdirAll(groupDir, 0o755); err != nil {
		return fmt.Errorf("capabilities: create %s: %w", groupDir, err)
	}
	data, err := yaml.Marshal(e)
	if err != nil {
		return fmt.Errorf("capabilities: marshal %s: %w", e.Type, err)
	}
	path := filepath.Join(groupDir, e.Type+".yaml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("capabilities: write %s: %w", path, err)
	}
	return nil
}

func writeIndex(dir string) error {
	groups := map[string]*mechanism{}
	var groupNames []string
	add := func(group string) {
		if _, ok := groups[group]; !ok {
			groups[group] = &mechanism{Name: group}
			groupNames = append(groupNames, group)
		}
	}
	for _, st := range registry.Nodes() {
		add(st.Mechanism)
		groups[st.Mechanism].Nodes = append(groups[st.Mechanism].Nodes, st.Name)
	}
	for _, tt := range registry.TriggerTypes() {
		add(tt.Mechanism)
		groups[tt.Mechanism].Triggers = append(groups[tt.Mechanism].Triggers, tt.Name)
	}
	sort.Strings(groupNames)
	idx := index{Generated: true}
	for _, name := range groupNames {
		mech := groups[name]
		sort.Strings(mech.Nodes)
		sort.Strings(mech.Triggers)
		idx.Mechanisms = append(idx.Mechanisms, *mech)
	}
	data, err := yaml.Marshal(idx)
	if err != nil {
		return fmt.Errorf("capabilities: marshal index: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.yaml"), data, 0o644); err != nil {
		return fmt.Errorf("capabilities: write index: %w", err)
	}
	return nil
}

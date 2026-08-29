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
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/Mathias-g/Servitor/internal/components/mcp"
	"github.com/Mathias-g/Servitor/internal/components/secret"
	"github.com/Mathias-g/Servitor/internal/components/singer"
	"github.com/Mathias-g/Servitor/internal/integrations"
	"github.com/Mathias-g/Servitor/internal/registry"
	_ "github.com/Mathias-g/Servitor/internal/registry/mechanisms"
)

// DefaultDir is where capabilities writes when no directory is given.
const DefaultDir = ".servitor/capabilities"

// entry is the on-disk shape for one capability: its kind, role, delivery, schema, and
// a derived example fragment.
type entry struct {
	Kind           string         `yaml:"kind"`
	Type           string         `yaml:"type"`
	MechanismGroup string         `yaml:"mechanism-group"`
	Role           string         `yaml:"role"`
	Delivery       string         `yaml:"delivery,omitempty"`
	Description    string         `yaml:"description"`
	Schema         map[string]any `yaml:"schema"`
	Example        map[string]any `yaml:"example"`
}

// mechanismGroup is one mechanism group in the index: its nodes and triggers.
// The `secret-resolution` group (ADR-0036) has no nodes or triggers; it lists
// the available secret sources instead.
type mechanismGroup struct {
	Name     string   `yaml:"name"`
	Nodes    []string `yaml:"nodes,omitempty"`
	Triggers []string `yaml:"triggers,omitempty"`
	Sources  []string `yaml:"sources,omitempty"`
}

// index lists the mechanism groups and the types each contains.
type index struct {
	Generated       bool             `yaml:"generated"`
	MechanismGroups []mechanismGroup `yaml:"mechanism-groups"`
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
	if err := writeSecretSources(dir); err != nil {
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

// secretEntry is one declared secret and its informational metadata (name,
// account, permissions, expiry). Values are never written; only names and
// metadata, so the report is safe to commit (ADR-0035, SPEC: How an agent
// discovers integrations).
type secretEntry struct {
	Name        string   `yaml:"name"`
	Source      string   `yaml:"source"`
	Account     string   `yaml:"account,omitempty"`
	Permissions []string `yaml:"permissions,omitempty"`
	Expiry      string   `yaml:"expiry,omitempty"`
}

// secretsReport is the on-disk shape of the secrets report.
type secretsReport struct {
	Generated bool          `yaml:"generated"`
	Secrets   []secretEntry `yaml:"secrets"`
}

// writeSecrets writes a secrets.yaml reporting the secrets declared in the
// declared integrations config (ADR-0035) in the working directory (names and
// metadata, never values).
func writeSecrets(dir string) error {
	report := secretsReport{Generated: true}
	cfg, err := integrations.Load("")
	if err != nil {
		return fmt.Errorf("capabilities: load integrations config: %w", err)
	}
	names := make([]string, 0, len(cfg.Secrets))
	for name := range cfg.Secrets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s := cfg.Secrets[name]
		report.Secrets = append(report.Secrets, secretEntry{
			Name:        name,
			Source:      s.Source,
			Account:     s.Account,
			Permissions: s.Permissions,
			Expiry:      s.Expiry,
		})
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

func writeTypes(dir string) error {
	for _, st := range registry.Nodes() {
		e := entry{
			Kind:           "node",
			Type:           st.Name,
			MechanismGroup: st.MechanismGroup,
			Role:           string(st.Role),
			Description:    st.Desc,
			Schema:         st.JSONSchema(),
			Example:        st.NodeExample(),
		}
		if err := writeEntry(dir, e); err != nil {
			return err
		}
	}
	for _, tt := range registry.TriggerTypes() {
		e := entry{
			Kind:           "trigger",
			Type:           tt.Name,
			MechanismGroup: tt.MechanismGroup,
			Role:           "trigger",
			Delivery:       tt.Delivery,
			Description:    tt.Desc,
			Schema:         tt.JSONSchema(),
			Example:        tt.TriggerExample(),
		}
		if err := writeEntry(dir, e); err != nil {
			return err
		}
	}
	return nil
}

func writeEntry(dir string, e entry) error {
	groupDir := filepath.Join(dir, e.MechanismGroup)
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

// secretsSource is one available secret source (a valid `source` value for
// `secrets.yaml`; ADR-0036). Values never appear.
type secretsSource struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
}

// secretSourcesReport is the on-disk shape of the secret-resolution group's
// report: the available secret sources a deployment can name as a secret's
// `source`.
type secretSourcesReport struct {
	Generated bool            `yaml:"generated"`
	Sources   []secretsSource `yaml:"sources"`
}

// writeSecretSources writes the secret-resolution group's sources.yaml (ADR-0036)
// under the secret-resolution/ directory, enumerating the available secret
// sources (providers) for `secrets.yaml`'s `source` field.
func writeSecretSources(dir string) error {
	report := secretSourcesReport{Generated: true}
	for _, name := range secret.DefaultRegistry().SourceNames() {
		report.Sources = append(report.Sources, secretsSource{Name: name, Description: sourceDescription(name)})
	}
	data, err := yaml.Marshal(report)
	if err != nil {
		return fmt.Errorf("capabilities: marshal secret sources: %w", err)
	}
	groupDir := filepath.Join(dir, "secret-resolution")
	if err := os.MkdirAll(groupDir, 0o755); err != nil {
		return fmt.Errorf("capabilities: create secret-resolution: %w", err)
	}
	if err := os.WriteFile(filepath.Join(groupDir, "sources.yaml"), data, 0o644); err != nil {
		return fmt.Errorf("capabilities: write secret sources: %w", err)
	}
	return nil
}

func sourceDescription(source string) string {
	switch source {
	case "env":
		return "plain environment: the operator exports the value (dev/testing fallback)"
	case "varlock":
		return "varlock pull source: loads the resolved set once, serves per node"
	case "onbox":
		return "push-based on-box ciphertext: sealed to the box, decrypted locally (recommended)"
	default:
		return ""
	}
}

// writeIndex builds index.yaml, listing the mechanism groups and the types
// each contains. The `secret-resolution` group (ADR-0036) is added with the
// available secret sources, distinct from the node-capability groups.
func writeIndex(dir string) error {
	groups := map[string]*mechanismGroup{}
	var groupNames []string
	add := func(group string) {
		if _, ok := groups[group]; !ok {
			groups[group] = &mechanismGroup{Name: group}
			groupNames = append(groupNames, group)
		}
	}
	for _, st := range registry.Nodes() {
		add(st.MechanismGroup)
		groups[st.MechanismGroup].Nodes = append(groups[st.MechanismGroup].Nodes, st.Name)
	}
	for _, tt := range registry.TriggerTypes() {
		add(tt.MechanismGroup)
		groups[tt.MechanismGroup].Triggers = append(groups[tt.MechanismGroup].Triggers, tt.Name)
	}
	add("secret-resolution")
	groups["secret-resolution"].Sources = secret.DefaultRegistry().SourceNames()
	sort.Strings(groupNames)
	idx := index{Generated: true}
	for _, name := range groupNames {
		mech := groups[name]
		sort.Strings(mech.Nodes)
		sort.Strings(mech.Triggers)
		sort.Strings(mech.Sources)
		idx.MechanismGroups = append(idx.MechanismGroups, *mech)
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

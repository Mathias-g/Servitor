// Package capabilities materializes the per-server capability set (SPEC: How
// an agent discovers capabilities and connectors) as files an agent can read on demand. For
// each capability it writes the JSON Schema and a derived example
// fragment, grouped by mechanism (core, webhook, singer, mcp, helper,
// websocket; ADR-0017). Discovered executables sit with their mechanism
// (singer/taps.yaml, mcp/servers.yaml). A pipeline can commit the output
// directory so a remote agent reads capabilities from the repo rather than
// reaching the loopback-only daemon (ADR-0009).
package capabilities

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/Mathias-g/Servitor/internal/components/mcp"
	"github.com/Mathias-g/Servitor/internal/components/secret"
	"github.com/Mathias-g/Servitor/internal/components/singer"
	"github.com/Mathias-g/Servitor/internal/config"
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
// resolver, when non-nil, is used to resolve secret-referenced headers when
// probing URL-based (mcp-http) servers, so their tools can be discovered with
// real auth (ADR-0047). A nil resolver probes such servers with only the
// headers that carry no secret reference.
func Write(dir string, resolver *secret.Resolver) error {
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
	if err := writeServers(dir, resolver); err != nil {
		return err
	}
	if err := writeReceivers(dir); err != nil {
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
// capabilities and connectors, ADR-0018). It sits beside the singer-tap node so an agent
// sees both the type and what is declared to run against it (ADR-0017).
func writeTaps(dir string) error {
	report := tapsReport{Generated: true}
	cfg, err := config.Load("")
	if err != nil {
		report.Note = "could not load config: " + err.Error()
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
// mode, and its tool schemas. It sits beside the mcp-stdio node so an agent
// sees both the type and what is declared. If a server cannot be probed, the
// report records its error rather than failing. Command-based (mcp-stdio)
// servers are spawned as subprocesses; URL-based (mcp-http) servers are probed
// over Streamable HTTP, with their `$SECRET` header references resolved from
// the resolver when one is available (ADR-0047).
func writeServers(dir string, resolver *secret.Resolver) error {
	report := serversReport{Generated: true}
	cfg, err := config.Load("")
	if err != nil {
		report.Note = "could not load config: " + err.Error()
	} else {
		declared := map[string][]string{}
		var httpServers []mcp.DiscoveredServer
		for name, s := range cfg.MCP {
			if len(s.Command) > 0 {
				declared[name] = s.Command
			} else if s.URL != "" {
				conn, ok := resolveConnector(name, s, resolver)
				ds := mcp.DiscoveredServer{Name: name}
				if !ok {
					ds.ProbeErr = "could not resolve the connector's secret-referenced headers"
				} else {
					d, derr := mcp.HTTPDiscover(context.Background(), conn)
					if derr != nil {
						ds.ProbeErr = derr.Error()
					} else {
						ds.Mode = d.Mode
						ds.Tools = d.Tools
					}
				}
				httpServers = append(httpServers, ds)
			}
		}
		report.Servers = append(mcp.DiscoverServers(declared, nil), httpServers...)
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

// resolveConnector resolves a URL-based MCP server's header templates against
// the resolver (ADR-0035), so capabilities can probe it with real auth. It
// returns false when there is no resolver or a referenced secret cannot be
// resolved; the probe then cannot authenticate.
func resolveConnector(name string, s *config.Server, resolver *secret.Resolver) (mcp.HTTPConnector, bool) {
	conn := mcp.HTTPConnector{URL: s.URL, Headers: s.Headers}
	names := mcp.ReferencedSecrets(s.Headers)
	if len(names) == 0 {
		return conn, true
	}
	if resolver == nil {
		return conn, false
	}
	values, _, err := resolver.Resolve(context.Background(), name, names)
	if err != nil {
		return conn, false
	}
	env := make([]string, 0, len(values))
	for k, v := range values {
		env = append(env, k+"="+v)
	}
	resolved, err := mcp.ResolveHeaders(s.Headers, env)
	if err != nil {
		return conn, false
	}
	conn.Headers = resolved
	return conn, true
}

// receiverReport is one declared webhook receiver and the signing details a
// workflow author needs: the scheme (which mechanism runs it) and, for hmac
// receivers, the signature header and encoding. The secret name is reported,
// never its value (ADR-0035, ADR-0049).
type receiverEntry struct {
	Path            string `yaml:"path"`
	Scheme          string `yaml:"scheme"`
	Secret          string `yaml:"secret,omitempty"`
	Header          string `yaml:"header,omitempty"`
	Encoding        string `yaml:"encoding,omitempty"`
	TimestampHeader string `yaml:"timestamp_header,omitempty"`
	Prefix          string `yaml:"prefix,omitempty"`
}

// receiversReport is the on-disk shape of the available-webhook-receivers
// report.
type receiversReport struct {
	Generated bool            `yaml:"generated"`
	Note      string          `yaml:"note,omitempty"`
	Receivers []receiverEntry `yaml:"receivers"`
}

// writeReceivers writes a receivers.yaml under the webhook/ group reporting
// the declared webhook receivers (ADR-0049, ADR-0018). It sits beside the
// hmac-webhook and standard-webhook triggers so an agent sees both the
// mechanism and what receivers are declared to run against it. Each receiver
// carries its scheme (the mechanism that runs it) and signing details, so the
// agent copies them verbatim and never reasons about a sender's signing
// scheme from memory.
func writeReceivers(dir string) error {
	report := receiversReport{Generated: true}
	cfg, err := config.Load("")
	if err != nil {
		report.Note = "could not load config: " + err.Error()
	} else {
		for _, path := range cfg.ReceiverPaths() {
			r := cfg.Webhook[path]
			report.Receivers = append(report.Receivers, receiverEntry{
				Path:            path,
				Scheme:          r.Scheme,
				Secret:          r.Secret,
				Header:          r.Header,
				Encoding:        r.Encoding,
				TimestampHeader: r.TimestampHeader,
				Prefix:          r.Prefix,
			})
		}
	}
	data, err := yaml.Marshal(report)
	if err != nil {
		return fmt.Errorf("capabilities: marshal receivers: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, registry.Webhook), 0o755); err != nil {
		return fmt.Errorf("capabilities: create webhook group: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, registry.Webhook, "receivers.yaml"), data, 0o644); err != nil {
		return fmt.Errorf("capabilities: write webhook/receivers.yaml: %w", err)
	}
	return nil
}

// secretEntry is one declared secret and its informational metadata (name,
// account, permissions, expiry). Values are never written; only names and
// metadata, so the report is safe to commit (ADR-0035, SPEC: How an agent
// discovers capabilities and connectors).
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
// declared config (ADR-0035) in the working directory (names and
// metadata, never values).
func writeSecrets(dir string) error {
	report := secretsReport{Generated: true}
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("capabilities: load config: %w", err)
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

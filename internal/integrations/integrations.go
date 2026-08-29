// Package integrations holds the declared integrations config (ADR-0018): the
// single source of truth for what subprocess integrations (MCP servers, Singer
// taps and targets) are available on this box. The operator declares each
// integration with its exact command and the env vars it needs; `servitor
// capabilities` reports what is declared (probing each once at refresh), and a
// management CLI edits the config. There is no PATH scan and no naming
// convention: the config names each integration explicitly, so renames and
// arbitrary vendor names are fine.
//
// The config is a local file the CLI and capabilities read and write directly
// (no daemon round-trip). It is the non-circular source of what is available:
// the runner labels folders and scans nothing; the box declares what it has.
package integrations

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// DefaultFile is where the integrations config lives.
const DefaultFile = "servitor.integrations.yaml"

// Config is the declared integrations config: one section per mechanism.
type Config struct {
	// MCP are the declared MCP servers, keyed by name.
	MCP map[string]*Server `yaml:"mcp"`
	// Singer are the declared Singer taps and targets, keyed by name.
	Singer *SingerSection `yaml:"singer"`
	// Secrets are the declared secrets, keyed by secret name (ADR-0035).
	Secrets map[string]*Secret `yaml:"secrets"`
}

// Secret is one declared secret (ADR-0035). Only Name (the map key) and Source
// are required; the rest are informational metadata an agent uses to reach for
// the right secret. Values never appear here.
type Secret struct {
	// Source is the secret-resolution source (provider) this secret resolves
	// through, a valid value for the `secret-resolution` mechanism group
	// (ADR-0036).
	Source string `yaml:"source"`
	// Account is an informational label (for example the gmail address or
	// GitHub org) for the account this secret belongs to.
	Account string `yaml:"account,omitempty"`
	// Permissions are the operations the operator declared this secret is
	// authorized for. Informational only in v1.
	Permissions []string `yaml:"permissions,omitempty"`
	// Expiry is an informational expiry (for example a date or a note).
	Expiry string `yaml:"expiry,omitempty"`
}

// SingerSection holds the declared Singer taps and targets.
type SingerSection struct {
	Taps    map[string]*Tap    `yaml:"taps"`
	Targets map[string]*Target `yaml:"targets"`
}

// Server is one declared MCP server.
type Server struct {
	// Command is the exact argv to start the server, for example
	// ["atomic-server"]. Where the executable lives is resolved by the OS the
	// same way any command is; a full path is allowed.
	Command []string `yaml:"command"`
	// Env are the env var names the server needs. Values come from the
	// runner's resolved secrets, filtered to these (SPEC: Varlock).
	Env []string `yaml:"env,omitempty"`
}

// Tap is one declared Singer tap.
type Tap struct {
	Command []string `yaml:"command"`
	Env     []string `yaml:"env,omitempty"`
}

// Target is one declared Singer target.
type Target struct {
	Command []string `yaml:"command"`
	Env     []string `yaml:"env,omitempty"`
}

// Load reads the integrations config from path. A missing file yields an empty
// (valid) config, so the runner and capabilities work with nothing declared.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultFile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("integrations: read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("integrations: parse %s: %w", path, err)
	}
	return &c, nil
}

// Save writes the config to path, creating the parent directory if needed.
func Save(c *Config, path string) error {
	if path == "" {
		path = DefaultFile
	}
	if c.MCP == nil {
		c.MCP = map[string]*Server{}
	}
	if c.Singer == nil {
		c.Singer = &SingerSection{}
	}
	if c.Secrets == nil {
		c.Secrets = map[string]*Secret{}
	}
	if c.Singer.Taps == nil {
		c.Singer.Taps = map[string]*Tap{}
	}
	if c.Singer.Targets == nil {
		c.Singer.Targets = map[string]*Target{}
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("integrations: marshal: %w", err)
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("integrations: create dir: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("integrations: write %s: %w", path, err)
	}
	return nil
}

// AddMCPServer declares a new MCP server, replacing one with the same name.
func (c *Config) AddMCPServer(name string, command, env []string) {
	if c.MCP == nil {
		c.MCP = map[string]*Server{}
	}
	c.MCP[name] = &Server{Command: command, Env: env}
}

// RemoveMCPServer deletes a declared MCP server. It reports whether it existed.
func (c *Config) RemoveMCPServer(name string) bool {
	if _, ok := c.MCP[name]; !ok {
		return false
	}
	delete(c.MCP, name)
	return true
}

// AddTap declares a new Singer tap, replacing one with the same name.
func (c *Config) AddTap(name string, command, env []string) {
	if c.Singer == nil {
		c.Singer = &SingerSection{}
	}
	if c.Singer.Taps == nil {
		c.Singer.Taps = map[string]*Tap{}
	}
	c.Singer.Taps[name] = &Tap{Command: command, Env: env}
}

// RemoveTap deletes a declared Singer tap. It reports whether it existed.
func (c *Config) RemoveTap(name string) bool {
	if c.Singer == nil || c.Singer.Taps == nil {
		return false
	}
	if _, ok := c.Singer.Taps[name]; !ok {
		return false
	}
	delete(c.Singer.Taps, name)
	return true
}

// AddTarget declares a new Singer target, replacing one with the same name.
func (c *Config) AddTarget(name string, command, env []string) {
	if c.Singer == nil {
		c.Singer = &SingerSection{}
	}
	if c.Singer.Targets == nil {
		c.Singer.Targets = map[string]*Target{}
	}
	c.Singer.Targets[name] = &Target{Command: command, Env: env}
}

// RemoveTarget deletes a declared Singer target. It reports whether it existed.
func (c *Config) RemoveTarget(name string) bool {
	if c.Singer == nil || c.Singer.Targets == nil {
		return false
	}
	if _, ok := c.Singer.Targets[name]; !ok {
		return false
	}
	delete(c.Singer.Targets, name)
	return true
}

// AddSecret declares a secret, replacing one with the same name (ADR-0035).
func (c *Config) AddSecret(name string, s *Secret) {
	if c.Secrets == nil {
		c.Secrets = map[string]*Secret{}
	}
	c.Secrets[name] = s
}

// RemoveSecret deletes a declared secret. It reports whether it existed.
func (c *Config) RemoveSecret(name string) bool {
	if _, ok := c.Secrets[name]; !ok {
		return false
	}
	delete(c.Secrets, name)
	return true
}

// SecretSources returns the declared secrets as a secret name -> source map,
// the shape a secret.Resolver routes on.
func (c *Config) SecretSources() map[string]string {
	out := map[string]string{}
	for name, s := range c.Secrets {
		out[name] = s.Source
	}
	return out
}

// ServerNames returns the declared MCP server names, sorted.
func (c *Config) ServerNames() []string { return sortedKeys(c.MCP) }

// TapNames returns the declared tap names, sorted.
func (c *Config) TapNames() []string {
	if c.Singer == nil {
		return nil
	}
	return sortedKeys(c.Singer.Taps)
}

// TargetNames returns the declared target names, sorted.
func (c *Config) TargetNames() []string {
	if c.Singer == nil {
		return nil
	}
	return sortedKeys(c.Singer.Targets)
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

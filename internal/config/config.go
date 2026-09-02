// Package config holds the declared config (ADR-0018, ADR-0047): the single
// source of truth for what connectors (MCP servers, Singer taps and targets)
// and secrets are declared on this box. The operator declares each connector
// with its exact command (or, for an MCP server reached over HTTP, its URL and
// secret-referenced headers) and the env vars it needs; `servitor capabilities`
// reports what is declared (probing each once at refresh), and a management CLI
// edits the config. There is no PATH scan and no naming convention: the config
// names each connector explicitly, so renames and arbitrary vendor names are
// fine.
//
// The config is a local file the CLI and capabilities read and write directly
// (no daemon round-trip). It is the non-circular source of what is available:
// the runner labels folders and scans nothing; the box declares what it has.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// DefaultFile is where the declared config lives.
const DefaultFile = "servitor.config.yaml"

// Config is the declared config: one section per mechanism.
type Config struct {
	// MCP are the declared MCP servers, keyed by name.
	MCP map[string]*Server `yaml:"mcp"`
	// Singer are the declared Singer taps and targets, keyed by name.
	Singer *SingerSection `yaml:"singer"`
	// Secrets are the declared secrets, keyed by secret name (ADR-0035).
	Secrets map[string]*Secret `yaml:"secrets"`
	// Webhook are the declared webhook receivers, keyed by their path
	// (ADR-0049). A Wafer's webhook trigger names a receiver by its path, the
	// same way an mcp-stdio node names a server.
	Webhook map[string]*WebhookReceiver `yaml:"webhook"`
}

// WebhookReceiver is one declared webhook receiver (ADR-0049). It names the
// verification scheme (the mechanism to run) and, when it verifies a
// signature, the declared secret holding the shared key. A receiver with no
// secret is an open receiver that accepts any body. Verification details that
// differ per sender (which header, which encoding, whether the body is
// timestamped) are config, not separate mechanisms, so any HMAC signer,
// raw-body or timestamped, is a config entry.
type WebhookReceiver struct {
	// Scheme selects the mechanism: `hmac` (verify HMAC-SHA256 over the body,
	// or a timestamped form of it) or `standard` (the Standard Webhooks
	// envelope). An unknown scheme is rejected at load.
	Scheme string `yaml:"scheme"`
	// Secret is the declared secret name holding the shared key, resolved per
	// use (SPEC: Secret resolution). Empty means an open receiver.
	Secret string `yaml:"secret,omitempty"`
	// Header is the signature header to read, for `hmac` receivers. Default
	// "x-servitor-signature".
	Header string `yaml:"header,omitempty"`
	// Encoding is the digest encoding in the header value, for `hmac`
	// receivers: "hex" or "base64". Default "base64".
	Encoding string `yaml:"encoding,omitempty"`
	// TimestampHeader, when set for an `hmac` receiver, names the header
	// carrying the request timestamp; the receiver then signs
	// `<prefix>:<timestamp>:<body>` and bounds replay with a time window. Empty
	// signs the raw body.
	TimestampHeader string `yaml:"timestamp_header,omitempty"`
	// Prefix is the version token some senders prepend to the digest, for
	// `hmac` receivers: the header value is `<prefix>=<digest>` and, when a
	// timestamp is signed, the message starts `<prefix>:`. Examples: "sha256"
	// or "v0".
	Prefix string `yaml:"prefix,omitempty"`
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

// SchemeHMAC and SchemeStandard are the valid webhook receiver schemes
// (ADR-0049): they select the hmac-webhook and standard-webhook mechanisms.
const (
	SchemeHMAC     = "hmac"
	SchemeStandard = "standard"
)

// Load reads the config from path. A missing file yields an empty
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
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// validate checks the config's invariants. Today it rejects a webhook receiver
// declared with an unknown scheme (ADR-0049), so a typo fails at load rather
// than silently serving a receiver that can never verify.
func (c *Config) validate() error {
	for path, r := range c.Webhook {
		if r.Scheme != SchemeHMAC && r.Scheme != SchemeStandard {
			return fmt.Errorf("config: webhook receiver %q: unknown scheme %q (must be %q or %q)", path, r.Scheme, SchemeHMAC, SchemeStandard)
		}
	}
	return nil
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
	if c.Webhook == nil {
		c.Webhook = map[string]*WebhookReceiver{}
	}
	if c.Singer.Taps == nil {
		c.Singer.Taps = map[string]*Tap{}
	}
	if c.Singer.Targets == nil {
		c.Singer.Targets = map[string]*Target{}
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("config: create dir: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
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

// ReceiverPaths returns the declared webhook receiver paths, sorted.
func (c *Config) ReceiverPaths() []string { return sortedKeys(c.Webhook) }

// AddWebhookReceiver declares a new webhook receiver, replacing one with the
// same path.
func (c *Config) AddWebhookReceiver(path string, r *WebhookReceiver) {
	if c.Webhook == nil {
		c.Webhook = map[string]*WebhookReceiver{}
	}
	c.Webhook[path] = r
}

// RemoveWebhookReceiver deletes a declared webhook receiver. It reports
// whether it existed.
func (c *Config) RemoveWebhookReceiver(path string) bool {
	if _, ok := c.Webhook[path]; !ok {
		return false
	}
	delete(c.Webhook, path)
	return true
}

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

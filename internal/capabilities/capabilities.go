// Package capabilities materializes the per-server capability set (SPEC: How
// an agent discovers integrations) as files an agent can read on demand. For
// each step and trigger type it writes the JSON Schema and a derived example
// fragment, grouped by integration (with a `core` group for Servitor's own
// types). A pipeline can commit the output directory so a remote agent reads
// capabilities from the repo rather than reaching the loopback-only daemon
// (ADR-0009).
package capabilities

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/Mathias-g/Servitor/internal/registry"
)

// DefaultDir is where capabilities writes when no directory is given.
const DefaultDir = ".servitor/capabilities"

// entry is the on-disk shape for one step or trigger type: its schema and a
// derived example fragment.
type entry struct {
	Kind        string         `yaml:"kind"`
	Type        string         `yaml:"type"`
	Group       string         `yaml:"group"`
	Description string         `yaml:"description"`
	Schema      map[string]any `yaml:"schema"`
	Example     map[string]any `yaml:"example"`
}

// integration is one group in the index: its steps and triggers.
type integration struct {
	Name     string   `yaml:"name"`
	Steps    []string `yaml:"steps"`
	Triggers []string `yaml:"triggers"`
}

// index lists the integrations and the types each contains.
type index struct {
	Generated    bool          `yaml:"generated"`
	Integrations []integration `yaml:"integrations"`
}

// Write materializes the capability set into dir. It creates the directory and
// one subdirectory per integration, with one file per type, plus index.yaml.
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
	for _, st := range registry.StepTypes() {
		e := entry{
			Kind:        "step",
			Type:        st.Name,
			Group:       st.Group,
			Description: st.Desc,
			Schema:      st.JSONSchema(),
			Example:     st.StepExample(),
		}
		if err := writeEntry(dir, e); err != nil {
			return err
		}
	}
	for _, tt := range registry.TriggerTypes() {
		e := entry{
			Kind:        "trigger",
			Type:        tt.Name,
			Group:       tt.Group,
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
	groupDir := filepath.Join(dir, e.Group)
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
	groups := map[string]*integration{}
	var groupNames []string
	add := func(group string) {
		if _, ok := groups[group]; !ok {
			groups[group] = &integration{Name: group}
			groupNames = append(groupNames, group)
		}
	}
	for _, st := range registry.StepTypes() {
		add(st.Group)
		groups[st.Group].Steps = append(groups[st.Group].Steps, st.Name)
	}
	for _, tt := range registry.TriggerTypes() {
		add(tt.Group)
		groups[tt.Group].Triggers = append(groups[tt.Group].Triggers, tt.Name)
	}
	sort.Strings(groupNames)
	idx := index{Generated: true}
	for _, name := range groupNames {
		integ := groups[name]
		sort.Strings(integ.Steps)
		sort.Strings(integ.Triggers)
		idx.Integrations = append(idx.Integrations, *integ)
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

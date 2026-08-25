package capabilities

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWriteProducesGroupedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// core group directory exists with the http step.
	if _, err := os.Stat(filepath.Join(dir, "core", "http.yaml")); err != nil {
		t.Fatalf("expected core/http.yaml: %v", err)
	}
	// integration grouping: slack trigger under slack/.
	if _, err := os.Stat(filepath.Join(dir, "slack", "slack_event.yaml")); err != nil {
		t.Fatalf("expected slack/slack_event.yaml: %v", err)
	}
	// index exists.
	if _, err := os.Stat(filepath.Join(dir, "index.yaml")); err != nil {
		t.Fatalf("expected index.yaml: %v", err)
	}
}

func TestIndexListsIntegrations(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.yaml"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	idx := index{}
	if err := yaml.Unmarshal(data, &idx); err != nil {
		t.Fatalf("parse index: %v", err)
	}
	if !idx.Generated {
		t.Fatal("index should be marked generated")
	}
	byName := map[string]*integration{}
	for i := range idx.Integrations {
		byName[idx.Integrations[i].Name] = &idx.Integrations[i]
	}
	core, ok := byName["core"]
	if !ok {
		t.Fatal("index missing core integration")
	}
	found := false
	for _, s := range core.Steps {
		if s == "http" {
			found = true
		}
	}
	if !found {
		t.Fatalf("core integration should list http step, got %v", core.Steps)
	}
	slack, ok := byName["slack"]
	if !ok {
		t.Fatal("index missing slack integration")
	}
	found = false
	for _, tr := range slack.Triggers {
		if tr == "slack_event" {
			found = true
		}
	}
	if !found {
		t.Fatalf("slack integration should list slack_event trigger, got %v", slack.Triggers)
	}
}

func TestEntryContainsSchemaAndExample(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "core", "http.yaml"))
	if err != nil {
		t.Fatalf("read http.yaml: %v", err)
	}
	e := entry{}
	if err := yaml.Unmarshal(data, &e); err != nil {
		t.Fatalf("parse http.yaml: %v", err)
	}
	if e.Kind != "step" || e.Type != "http" || e.Group != "core" {
		t.Fatalf("unexpected entry header: %+v", e)
	}
	if _, ok := e.Schema["properties"]; !ok {
		t.Fatalf("schema missing properties: %+v", e.Schema)
	}
	if e.Example["url"] != "https://api.example.com/things" {
		t.Fatalf("example url = %v, want schema example", e.Example["url"])
	}
}

func TestWriteSecretsWithoutVarlockWritesNote(t *testing.T) {
	// Ensure varlock is not on PATH so this is deterministic.
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	if err := Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "secrets.yaml"))
	if err != nil {
		t.Fatalf("read secrets.yaml: %v", err)
	}
	if !strings.Contains(string(data), "varlock not available") {
		t.Fatalf("secrets.yaml = %q, want a varlock-unavailable note", data)
	}
}

func TestWriteSecretsWithVarlockReportsNamesAndPresence(t *testing.T) {
	if _, err := exec.LookPath("varlock"); err != nil {
		t.Skip("varlock not installed; skipping varlock-backed secrets test")
	}
	// Write a schema + env in the working dir so varlock resolves. MAYBE is
	// optional and empty, so it is declared but not present; TOKEN is present.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"),
		[]byte("# @type=string @optional\nMAYBE=\n# @type=string\nTOKEN=abc\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.schema"),
		[]byte("MAYBE=\nTOKEN=abc\n"), 0o644); err != nil {
		t.Fatalf("write .env.schema: %v", err)
	}
	oldwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	entries, note, err := declaredSecrets()
	if err != nil {
		t.Fatalf("declaredSecrets: %v (%s)", err, note)
	}
	var token, maybe *secretEntry
	for i := range entries {
		if entries[i].Name == "TOKEN" {
			token = &entries[i]
		}
		if entries[i].Name == "MAYBE" {
			maybe = &entries[i]
		}
	}
	if token == nil || !token.Present {
		t.Fatalf("expected TOKEN present, got %+v", entries)
	}
	if maybe == nil || maybe.Present {
		t.Fatalf("expected MAYBE not present, got %+v", entries)
	}
}

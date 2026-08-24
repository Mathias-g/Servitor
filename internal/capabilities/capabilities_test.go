package capabilities

import (
	"os"
	"path/filepath"
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

package capabilities

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Mathias-g/Servitor/internal/integrations"
)

// writeTestConfig writes the integrations config to the working directory
// (where capabilities.Load reads it) and chdirs there.
func writeTestConfig(t *testing.T, cfg *integrations.Config) {
	t.Helper()
	dir := t.TempDir()
	if err := integrations.Save(cfg, filepath.Join(dir, integrations.DefaultFile)); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	oldwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
}

func TestWriteProducesGroupedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// core group directory exists with the http step.
	if _, err := os.Stat(filepath.Join(dir, "core", "http.yaml")); err != nil {
		t.Fatalf("expected core/http.yaml: %v", err)
	}
	// mechanism grouping: webhook triggers under webhook/.
	if _, err := os.Stat(filepath.Join(dir, "webhook", "slack_event.yaml")); err != nil {
		t.Fatalf("expected webhook/slack_event.yaml: %v", err)
	}
	// index exists.
	if _, err := os.Stat(filepath.Join(dir, "index.yaml")); err != nil {
		t.Fatalf("expected index.yaml: %v", err)
	}
}

func TestIndexListsMechanisms(t *testing.T) {
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
	byName := map[string]*mechanism{}
	for i := range idx.Mechanisms {
		byName[idx.Mechanisms[i].Name] = &idx.Mechanisms[i]
	}
	core, ok := byName["core"]
	if !ok {
		t.Fatal("index missing core mechanism")
	}
	found := false
	for _, s := range core.Steps {
		if s == "http" {
			found = true
		}
	}
	if !found {
		t.Fatalf("core mechanism should list http step, got %v", core.Steps)
	}
	webhook, ok := byName["webhook"]
	if !ok {
		t.Fatal("index missing webhook mechanism")
	}
	found = false
	for _, tr := range webhook.Triggers {
		if tr == "slack_event" {
			found = true
		}
	}
	if !found {
		t.Fatalf("webhook mechanism should list slack_event trigger, got %v", webhook.Triggers)
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

func TestEntryEmitsRoleAndDelivery(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// An action-role step carries role=action, no delivery.
	act := entry{}
	if data, err := os.ReadFile(filepath.Join(dir, "core", "http.yaml")); err != nil {
		t.Fatalf("read http.yaml: %v", err)
	} else if err := yaml.Unmarshal(data, &act); err != nil {
		t.Fatalf("parse http.yaml: %v", err)
	}
	if act.Role != "action" {
		t.Fatalf("http role = %q, want action", act.Role)
	}
	// A trigger-role step carries role=trigger and a delivery tag.
	tr := entry{}
	if data, err := os.ReadFile(filepath.Join(dir, "helper", "email_received.yaml")); err != nil {
		t.Fatalf("read email_received.yaml: %v", err)
	} else if err := yaml.Unmarshal(data, &tr); err != nil {
		t.Fatalf("parse email_received.yaml: %v", err)
	}
	if tr.Role != "trigger" {
		t.Fatalf("email_received role = %q, want trigger", tr.Role)
	}
	if tr.Delivery != "polling" {
		t.Fatalf("email_received delivery = %q, want polling", tr.Delivery)
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
	// Canonical varlock layout (SPEC: Varlock): the schema declares each secret
	// with its decorators and an empty value; the present value lives in the
	// git-ignored .env.local. MAYBE is optional and empty, so it is declared
	// but not present; TOKEN has a value, so it is present.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.schema"),
		[]byte("# @sensitive @type=string\nTOKEN=\n# @sensitive @optional @type=string\nMAYBE=\n"), 0o644); err != nil {
		t.Fatalf("write .env.schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"),
		[]byte("TOKEN=abc\n"), 0o600); err != nil {
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

func TestWriteProducesTapsReport(t *testing.T) {
	// A fake tap declared in the integrations config (ADR-0018).
	dir := t.TempDir()
	tap := filepath.Join(dir, "tap-fake")
	script := `#!/bin/sh
case "$1" in
  --about) printf '%s' '{"properties":{"client_id":{"type":"string"}},"required":["client_id"]}';;
  --discover) printf '%s' '[{"stream":"customers","schema":{"type":"object"}}]';;
esac
`
	if err := os.WriteFile(tap, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &integrations.Config{}
	cfg.AddTap("tap-fake", []string{tap}, nil)
	writeTestConfig(t, cfg)

	out := t.TempDir()
	if err := Write(out); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(out, "singer", "taps.yaml"))
	if err != nil {
		t.Fatalf("read singer/taps.yaml: %v", err)
	}
	rep := tapsReport{}
	if err := yaml.Unmarshal(data, &rep); err != nil {
		t.Fatalf("parse singer/taps.yaml: %v", err)
	}
	if !rep.Generated {
		t.Fatal("taps report should be marked generated")
	}
	found := false
	for _, tap := range rep.Taps {
		if tap.Name == "tap-fake" && len(tap.Catalog) == 1 && tap.Catalog[0].Stream == "customers" {
			found = true
		}
	}
	if !found {
		t.Fatalf("taps report = %+v, want tap-fake with customers stream", rep.Taps)
	}
}

func TestWriteProducesServersReport(t *testing.T) {
	// A fake MCP server declared in the integrations config (ADR-0018).
	dir := t.TempDir()
	server := filepath.Join(dir, "atomic-server")
	script := `#!/usr/bin/env python3
import json, sys
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    req = json.loads(line)
    if req.get("method") == "tools/list":
        print(json.dumps({"jsonrpc":"2.0","id":req.get("id"),"result":{"tools":[{"name":"search","inputSchema":{"type":"object"}}]}}))
    sys.stdout.flush()
`
	if err := os.WriteFile(server, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &integrations.Config{}
	cfg.AddMCPServer("atomic", []string{server}, nil)
	writeTestConfig(t, cfg)

	out := t.TempDir()
	if err := Write(out); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(out, "mcp", "servers.yaml"))
	if err != nil {
		t.Fatalf("read mcp/servers.yaml: %v", err)
	}
	rep := serversReport{}
	if err := yaml.Unmarshal(data, &rep); err != nil {
		t.Fatalf("parse mcp/servers.yaml: %v", err)
	}
	if !rep.Generated {
		t.Fatal("servers report should be marked generated")
	}
	found := false
	for _, s := range rep.Servers {
		if s.Name == "atomic" && len(s.Tools) == 1 && s.Tools[0].Name == "search" {
			found = true
		}
	}
	if !found {
		t.Fatalf("servers report = %+v, want atomic with search tool", rep.Servers)
	}
}

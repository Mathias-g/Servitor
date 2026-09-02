package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileIsEmpty(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.ServerNames()) != 0 || len(c.TapNames()) != 0 || len(c.TargetNames()) != 0 {
		t.Fatalf("expected empty config, got %+v", c)
	}
}

func TestRoundTrip(t *testing.T) {
	c := &Config{}
	c.AddMCPServer("atomic", []string{"atomic-server"}, []string{"ATOMIC_TOKEN"})
	c.AddTap("stripe", []string{"tap-stripe"}, []string{"STRIPE_KEY"})
	c.AddTarget("grist", []string{"target-grist"}, nil)

	path := filepath.Join(t.TempDir(), "integrations.yaml")
	if err := Save(c, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.MCP["atomic"].Command[0] != "atomic-server" {
		t.Fatalf("atomic command = %v", got.MCP["atomic"].Command)
	}
	if got.MCP["atomic"].Env[0] != "ATOMIC_TOKEN" {
		t.Fatalf("atomic env = %v", got.MCP["atomic"].Env)
	}
	if got.Singer.Taps["stripe"].Command[0] != "tap-stripe" {
		t.Fatalf("stripe command = %v", got.Singer.Taps["stripe"].Command)
	}
	if got.Singer.Targets["grist"].Command[0] != "target-grist" {
		t.Fatalf("grist command = %v", got.Singer.Targets["grist"].Command)
	}
}

func TestRemoveReportsExistence(t *testing.T) {
	c := &Config{}
	c.AddMCPServer("a", []string{"cmd"}, nil)
	if !c.RemoveMCPServer("a") {
		t.Fatal("existing server should report removal")
	}
	if c.RemoveMCPServer("a") {
		t.Fatal("already-removed server should report false")
	}
	if c.RemoveTap("none") {
		t.Fatal("missing tap should report false")
	}
}

func TestSaveCreatesParentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deep")
	if err := Save(&Config{}, filepath.Join(dir, "c.yaml")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "c.yaml")); err != nil {
		t.Fatalf("file not written: %v", err)
	}
}

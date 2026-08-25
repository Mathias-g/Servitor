package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mathias-g/Servitor/internal/integrations"
)

func TestMCPAddListRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")

	var out, errOut bytes.Buffer
	if code := Run([]string{"mcp", "add", "--file", path, "atomic", "atomic-server", "ATOMIC_TOKEN"}, &out, &errOut); code != exitOK {
		t.Fatalf("add: code=%d err=%q", code, errOut.String())
	}

	out.Reset()
	if code := Run([]string{"mcp", "list", "--file", path}, &out, &errOut); code != exitOK {
		t.Fatalf("list: code=%d err=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "atomic") || !strings.Contains(out.String(), "atomic-server") {
		t.Fatalf("list output = %q, want atomic/atomic-server", out.String())
	}

	out.Reset()
	if code := Run([]string{"mcp", "remove", "--file", path, "atomic"}, &out, &errOut); code != exitOK {
		t.Fatalf("remove: code=%d err=%q", code, errOut.String())
	}

	cfg, err := integrations.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.ServerNames()) != 0 {
		t.Fatalf("expected empty after remove, got %v", cfg.ServerNames())
	}
}

func TestTapAndTargetAdd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")

	var out, errOut bytes.Buffer
	if code := Run([]string{"tap", "add", "--file", path, "stripe", "tap-stripe", "STRIPE_KEY"}, &out, &errOut); code != exitOK {
		t.Fatalf("tap add: code=%d err=%q", code, errOut.String())
	}
	if code := Run([]string{"target", "add", "--file", path, "grist", "target-grist"}, &out, &errOut); code != exitOK {
		t.Fatalf("target add: code=%d err=%q", code, errOut.String())
	}

	cfg, err := integrations.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Singer.Taps["stripe"].Command[0] != "tap-stripe" {
		t.Fatalf("tap command = %v", cfg.Singer.Taps["stripe"].Command)
	}
	if cfg.Singer.Targets["grist"].Command[0] != "target-grist" {
		t.Fatalf("target command = %v", cfg.Singer.Targets["grist"].Command)
	}
}

func TestMCPUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"mcp"}, &out, &errOut); code != exitUsage {
		t.Fatalf("mcp with no subcommand should be usage error, got %d", code)
	}
}

func TestMCPRemoveMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	var out, errOut bytes.Buffer
	if code := Run([]string{"mcp", "remove", "--file", path, "nope"}, &out, &errOut); code != exitFailure {
		t.Fatalf("remove missing should fail, got %d", code)
	}
}

func TestHelpMentionsIntegrations(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"help"}, &out, &errOut); code != exitOK {
		t.Fatalf("help: %d", code)
	}
	if !strings.Contains(out.String(), "servitor.integrations.yaml") {
		t.Fatalf("help should mention integrations config, got %q", out.String())
	}
}

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMainVersionSmoke pins that the entry point delegates to the CLI: running
// the built command with `version` prints the servitor banner. This guards the
// thin main wrapper without requiring a daemon.
func TestMainVersionSmoke(t *testing.T) {
	// Build the binary once so the test exercises the real entry point.
	bin := filepath.Join(t.TempDir(), "servitor")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	run := exec.Command(bin, "version")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run version: %v\n%s", err, out)
	}
	if !strings.HasPrefix(string(out), "servitor ") {
		t.Fatalf("version output %q does not start with 'servitor '", string(out))
	}
}

package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"version"}, &out, &errOut); code != exitOK {
		t.Fatalf("version: exit code %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(out.String(), "servitor ") {
		t.Errorf("version output %q does not start with 'servitor '", out.String())
	}
}

func TestHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"help"}, &out, &errOut); code != exitOK {
		t.Fatalf("help: exit code %d, want %d", code, exitOK)
	}
	if !strings.Contains(out.String(), "capabilities") {
		t.Errorf("help output should mention capabilities, got %q", out.String())
	}
}

func TestUnimplementedCommandFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"submit", "wf.yml"}, &out, &errOut); code != exitFailure {
		t.Fatalf("submit: exit code %d, want %d", code, exitFailure)
	}
}

func TestStopNoDaemon(t *testing.T) {
	// Point stop at an unused loopback port so no real daemon is disturbed.
	var out, errOut bytes.Buffer
	if code := Run([]string{"stop", "--addr", "127.0.0.1:1"}, &out, &errOut); code != exitNoDaemon {
		t.Fatalf("stop: exit code %d, want %d (daemon not running)", code, exitNoDaemon)
	}
}

func TestRunUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"run", "--addr"}, &out, &errOut); code != exitUsage {
		t.Fatalf("run: exit code %d, want %d", code, exitUsage)
	}
}

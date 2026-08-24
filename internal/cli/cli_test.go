package cli

import (
	"bytes"
	"os"
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

func TestSubmitMissingFileFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"submit", "does-not-exist.yml"}, &out, &errOut); code != exitFailure {
		t.Fatalf("submit: exit code %d, want %d", code, exitFailure)
	}
}

func TestTriggerUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"trigger"}, &out, &errOut); code != exitUsage {
		t.Fatalf("trigger: exit code %d, want %d", code, exitUsage)
	}
}

func TestEnableUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"enable"}, &out, &errOut); code != exitUsage {
		t.Fatalf("enable: exit code %d, want %d", code, exitUsage)
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

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "wf-*.yml")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp: %v", err)
	}
	return f.Name()
}

func TestDryRunReadablePlan(t *testing.T) {
	path := writeTemp(t, `
name: demo
on:
  - type: cron
    schedule: "0 * * * *"
steps:
  - type: transform
    name: b
    depends_on: [a]
    expression: x
  - type: http
    name: a
    url: "https://example.com"
    method: GET
    dedupe_key: k
`)
	var out, errOut bytes.Buffer
	if code := Run([]string{"dry-run", path}, &out, &errOut); code != exitOK {
		t.Fatalf("dry-run exit %d: %s", code, errOut.String())
	}
	text := out.String()
	if !strings.Contains(text, "workflow: demo") {
		t.Errorf("plan should name the workflow, got:\n%s", text)
	}
	if !strings.Contains(text, "triggers:") || !strings.Contains(text, "cron") {
		t.Errorf("plan should list triggers, got:\n%s", text)
	}
	if !strings.Contains(text, "after a") {
		t.Errorf("plan should show dependency, got:\n%s", text)
	}
}

func TestDryRunJSONFlag(t *testing.T) {
	path := writeTemp(t, `
name: demo
steps:
  - type: transform
    name: a
    expression: x
`)
	var out, errOut bytes.Buffer
	if code := Run([]string{"dry-run", "--json", path}, &out, &errOut); code != exitOK {
		t.Fatalf("dry-run exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"dag"`) {
		t.Errorf("--json output should include dag, got:\n%s", out.String())
	}
}

func TestDryRunUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"dry-run"}, &out, &errOut); code != exitUsage {
		t.Fatalf("dry-run exit %d, want %d", code, exitUsage)
	}
}

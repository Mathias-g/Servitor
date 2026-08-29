package cli

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestUpdateUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"update"}, &out, &errOut); code != exitUsage {
		t.Fatalf("update with no arg: exit code %d, want %d", code, exitUsage)
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

func TestRunsNoDaemon(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"runs", "--addr", "127.0.0.1:1"}, &out, &errOut); code != exitNoDaemon {
		t.Fatalf("runs: exit code %d, want %d (daemon not running)", code, exitNoDaemon)
	}
}

func TestRunDetailUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	// `run` with a run id but no daemon -> daemon-not-running exit code.
	if code := Run([]string{"run", "--addr", "127.0.0.1:1", "run-1"}, &out, &errOut); code != exitNoDaemon {
		t.Fatalf("run <id> with no daemon: exit code %d, want %d", code, exitNoDaemon)
	}
	if code := Run([]string{"cancel"}, &out, &errOut); code != exitUsage {
		t.Fatalf("cancel with no id: exit code %d, want %d", code, exitUsage)
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
triggers:
  - type: cron
    schedule: "0 * * * *"
nodes:
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
nodes:
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

func TestTransformNodeSubprocess(t *testing.T) {
	input := `{"event":{"id":"e1"},"steps":{"fetch":{"items":[{"amount":10,"active":true},{"amount":100,"active":false},{"amount":5,"active":true}]}}}`

	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })
	f, err := os.CreateTemp(t.TempDir(), "stdin-*.json")
	if err != nil {
		t.Fatalf("create stdin file: %v", err)
	}
	if _, err := f.WriteString(input); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	os.Stdin = f

	var out, errOut bytes.Buffer
	if code := Run([]string{"__transform", `$sum(steps.fetch.items[active=true].amount)`}, &out, &errOut); code != exitOK {
		t.Fatalf("__transform exit %d, want %d (stderr: %s)", code, exitOK, errOut.String())
	}
	if strings.TrimSpace(out.String()) != "15" {
		t.Fatalf("transform output = %q, want 15", out.String())
	}
}

func TestTransformNodeBadExpression(t *testing.T) {
	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })
	f, err := os.CreateTemp(t.TempDir(), "stdin-*.json")
	if err != nil {
		t.Fatalf("create stdin file: %v", err)
	}
	if _, err := f.WriteString(`{}`); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	os.Stdin = f

	var out, errOut bytes.Buffer
	if code := Run([]string{"__transform", `["unterminated`}, &out, &errOut); code != exitFailure {
		t.Fatalf("__transform bad expr exit %d, want %d", code, exitFailure)
	}
}

func TestSwitchNodeSubprocess(t *testing.T) {
	payload := `{"input":{"event":{"id":"e1"},"steps":{"check":"high"}},"cases":{"high":"notify_finance","low":"log_and_done"},"default":"log_unknown"}`

	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })
	f, err := os.CreateTemp(t.TempDir(), "stdin-*.json")
	if err != nil {
		t.Fatalf("create stdin file: %v", err)
	}
	if _, err := f.WriteString(payload); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	os.Stdin = f

	var out, errOut bytes.Buffer
	if code := Run([]string{"__switch", `steps.check`}, &out, &errOut); code != exitOK {
		t.Fatalf("__switch exit %d, want %d (stderr: %s)", code, exitOK, errOut.String())
	}
	if strings.TrimSpace(out.String()) != `"notify_finance"` {
		t.Fatalf("switch output = %q, want notify_finance", out.String())
	}
}

func TestSwitchNodeDefault(t *testing.T) {
	payload := `{"input":{"event":{"id":"e1"},"steps":{"check":"medium"}},"cases":{"high":"notify_finance","low":"log_and_done"},"default":"log_unknown"}`

	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })
	f, err := os.CreateTemp(t.TempDir(), "stdin-*.json")
	if err != nil {
		t.Fatalf("create stdin file: %v", err)
	}
	if _, err := f.WriteString(payload); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	os.Stdin = f

	var out, errOut bytes.Buffer
	if code := Run([]string{"__switch", `steps.check`}, &out, &errOut); code != exitOK {
		t.Fatalf("__switch default exit %d, want %d (stderr: %s)", code, exitOK, errOut.String())
	}
	if strings.TrimSpace(out.String()) != `"log_unknown"` {
		t.Fatalf("switch default output = %q, want log_unknown", out.String())
	}
}

func TestSwitchNodeNoMatchNoDefaultFails(t *testing.T) {
	payload := `{"input":{"event":{"id":"e1"},"steps":{"check":"medium"}},"cases":{"high":"notify_finance","low":"log_and_done"}}`

	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })
	f, err := os.CreateTemp(t.TempDir(), "stdin-*.json")
	if err != nil {
		t.Fatalf("create stdin file: %v", err)
	}
	if _, err := f.WriteString(payload); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	os.Stdin = f

	var out, errOut bytes.Buffer
	if code := Run([]string{"__switch", `steps.check`}, &out, &errOut); code != exitFailure {
		t.Fatalf("__switch no-match-no-default exit %d, want %d", code, exitFailure)
	}
}

func TestForeachNodeSubprocess(t *testing.T) {
	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })
	f, err := os.CreateTemp(t.TempDir(), "stdin-*.json")
	if err != nil {
		t.Fatalf("create stdin file: %v", err)
	}
	if _, err := f.WriteString(`{"event":{"id":"e1"},"steps":{"fetch_ids":[1,2,3]}}`); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	os.Stdin = f

	var out, errOut bytes.Buffer
	if code := Run([]string{"__foreach", `steps.fetch_ids`}, &out, &errOut); code != exitOK {
		t.Fatalf("__foreach exit %d, want %d (stderr: %s)", code, exitOK, errOut.String())
	}
	if strings.TrimSpace(out.String()) != `[1,2,3]` {
		t.Fatalf("__foreach output = %q, want [1,2,3]", out.String())
	}
}

func TestCapabilitiesUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"capabilities", "a", "b"}, &out, &errOut); code != exitUsage {
		t.Fatalf("capabilities with two args exit %d, want %d", code, exitUsage)
	}
}

func TestCapabilitiesWritesFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "caps")
	var out, errOut bytes.Buffer
	if code := Run([]string{"capabilities", dir}, &out, &errOut); code != exitOK {
		t.Fatalf("capabilities exit %d, want %d (stderr: %s)", code, exitOK, errOut.String())
	}
	if !strings.Contains(out.String(), dir) {
		t.Fatalf("capabilities output %q should mention the dir", out.String())
	}
	// The index and at least one type file should exist.
	if _, err := os.Stat(filepath.Join(dir, "index.yaml")); err != nil {
		t.Fatalf("index.yaml not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "core", "shell.yaml")); err != nil {
		t.Fatalf("core/shell.yaml not written: %v", err)
	}
}

func TestEmailPollUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"__email_poll", "extra"}, &out, &errOut); code != exitUsage {
		t.Fatalf("__email_poll with arg exit %d, want %d", code, exitUsage)
	}
}

func TestEmailPollMissingSecretFails(t *testing.T) {
	// Valid config but the referenced secret is not in the environment, so it
	// fails before any network/IMAP attempt.
	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })
	cfg := `{"host":"imap.example.com","username":"me","secret":"NOT_SET_SECRET"}`
	f, err := os.CreateTemp(t.TempDir(), "stdin-*.json")
	if err != nil {
		t.Fatalf("create stdin: %v", err)
	}
	if _, err := f.WriteString(cfg); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	os.Stdin = f

	var out, errOut bytes.Buffer
	if code := Run([]string{"__email_poll"}, &out, &errOut); code != exitFailure {
		t.Fatalf("__email_poll exit %d, want %d", code, exitFailure)
	}
	if !strings.Contains(errOut.String(), "not resolved") {
		t.Fatalf("__email_poll stderr %q should mention the missing secret", errOut.String())
	}
}

package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/wafer"
	"github.com/Mathias-g/Servitor/internal/worker"
)

func extPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("HONKER_EXTENSION_PATH")
	if p == "" {
		t.Skip("HONKER_EXTENSION_PATH not set; skipping Honker-backed runner test")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("HONKER_EXTENSION_PATH %s not readable: %v", p, err)
	}
	return p
}

func openStore(t *testing.T) *honker.Store {
	t.Helper()
	ext := extPath(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := honker.Open(dbPath, ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

const shellYAML = `
name: demo
on:
  - type: cron
    schedule: "@every 1s"
steps:
  - type: shell
    name: a
    command: "printf '{\"ok\":1}'"
  - type: shell
    name: b
    depends_on: [a]
    command: "printf '{\"ok\":2}'"
`

func TestFromWaferBuildsChain(t *testing.T) {
	w, err := wafer.Parse([]byte(shellYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	head, err := FromWafer(w, map[string]any{"trigger": "cron"})
	if err != nil {
		t.Fatalf("FromWafer: %v", err)
	}
	if head == nil {
		t.Fatal("expected a head job")
	}
	if head.StepID != "a" || head.WorkflowID != "demo" {
		t.Fatalf("head = %q/%q, want a/demo", head.StepID, head.WorkflowID)
	}
	if head.Input["trigger"] != "cron" {
		t.Fatalf("head input = %v, want cron event", head.Input)
	}
	if len(head.Downstream) != 1 {
		t.Fatalf("head downstream = %d, want 1 (b)", len(head.Downstream))
	}
	next := head.Downstream[0]
	if next.StepID != "b" {
		t.Fatalf("next step = %q, want b", next.StepID)
	}
	if len(next.Command) == 0 || next.Command[0] != "/bin/sh" {
		t.Fatalf("step b command = %v, want a shell command", next.Command)
	}
}

func TestFromWaferRejectsUnsupportedType(t *testing.T) {
	w, err := wafer.Parse([]byte(`
name: demo
on: []
steps:
  - type: transform
    expression: "input"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := FromWafer(w, nil); err == nil {
		t.Fatal("expected an error for a step type with no handler yet")
	}
}

func TestFromWaferCarriesDeclaredSecrets(t *testing.T) {
	w, err := wafer.Parse([]byte(`
name: demo
on: []
steps:
  - type: shell
    name: a
    secrets: [TOKEN, OTHER]
    command: "echo hi"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	head, err := FromWafer(w, nil)
	if err != nil {
		t.Fatalf("FromWafer: %v", err)
	}
	if len(head.Secrets) != 2 || head.Secrets[0] != "TOKEN" || head.Secrets[1] != "OTHER" {
		t.Fatalf("head secrets = %v, want [TOKEN OTHER]", head.Secrets)
	}
}

func TestStartRunEnqueuesHead(t *testing.T) {
	store := openStore(t)
	q := store.Queue("steps", 30, 3)
	w, err := wafer.Parse([]byte(shellYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	runID, err := StartRun(store, q, w, map[string]any{"trigger": "manual"}, "run-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if runID != "run-1" {
		t.Fatalf("runID = %q, want run-1", runID)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("head not claimable: %v", err)
	}
	var head worker.StepJob
	if err := job.UnmarshalPayload(&head); err != nil {
		t.Fatalf("head payload: %v", err)
	}
	if head.RunID != "run-1" || head.StepID != "a" {
		t.Fatalf("head = %q/%q, want run-1/a", head.RunID, head.StepID)
	}
}

func TestRegisterCronFiresIntoQueue(t *testing.T) {
	store := openStore(t)
	q := store.Queue("steps", 30, 3)
	w, err := wafer.Parse([]byte(shellYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	err = RegisterCron(store, q, w, CronTask{
		Name:     "demo:cron-0",
		Schedule: "@every 1s",
		RunID:    "cron-run",
		Event:    map[string]any{"trigger": "cron"},
	})
	if err != nil {
		t.Fatalf("RegisterCron: %v", err)
	}

	// Fire the scheduler; the task's next fire is on the next second boundary,
	// so wait past it. A cron fire enqueues the head step job.
	soonest, err := store.Scheduler().Soonest()
	if err != nil || soonest <= 0 {
		t.Fatalf("expected a scheduled next fire, got %d (err %v)", soonest, err)
	}
	time.Sleep(1100 * time.Millisecond)
	fires, err := store.Scheduler().Tick()
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(fires) == 0 {
		t.Fatalf("expected a cron fire, got none")
	}

	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("cron-enqueued head not claimable: %v", err)
	}
	var head worker.StepJob
	if err := job.UnmarshalPayload(&head); err != nil {
		t.Fatalf("head payload: %v", err)
	}
	if head.StepID != "a" || head.RunID != "cron-run" {
		t.Fatalf("head = %q/%q, want a/cron-run", head.StepID, head.RunID)
	}
}

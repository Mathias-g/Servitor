package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mathias-g/Servitor/internal/exec"
	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/mcp"
)

func extPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("HONKER_EXTENSION_PATH")
	if p == "" {
		t.Skip("HONKER_EXTENSION_PATH not set; skipping Honker-backed worker test")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("HONKER_EXTENSION_PATH %s not readable: %v", p, err)
	}
	return p
}

func newWorker(t *testing.T, visS, maxAttempts int, secrets map[string]string) (*Worker, *honker.Store, *honker.Queue) {
	t.Helper()
	ext := extPath(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := honker.Open(dbPath, ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	q := store.Queue("steps", visS, maxAttempts)
	w := New(store, q, "worker-1", Config{Secrets: secrets})
	return w, store, q
}

func shellCmd(script string) []string { return []string{"/bin/sh", "-c", script} }

// claimResultJSON returns the decoded step result for a run/step, decoding
// "" as nil.
func claimResultJSON(t *testing.T, store *honker.Store, runID, stepID string) any {
	t.Helper()
	raw, err := store.ResultJSON(runID, stepID)
	if err != nil {
		t.Fatalf("result %s/%s: %v", runID, stepID, err)
	}
	if raw == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("decode result %s/%s: %v", runID, stepID, err)
	}
	return v
}

func TestExecuteShellStepCommitsFanOut(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)

	id, err := q.Enqueue(StepJob{
		RunID:      "run-1",
		WorkflowID: "wf",
		StepID:     "a",
		StepName:   "a",
		StepType:   "shell",
		Input:      map[string]any{"x": 1},
		Command:    shellCmd(`printf '{"ok":true}'`),
		Downstream: []StepJob{{
			RunID: "run-1", WorkflowID: "wf", StepID: "b", StepName: "b",
			StepType: "shell", Command: shellCmd(`printf '{"ok":true}'`),
		}},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}

	// Result row written.
	if res := claimResultJSON(t, store, "run-1", "a"); res == nil {
		t.Fatal("no result written for step a")
	}

	// The parent claim was acked (id 1); the next claimable job is the
	// downstream (id 2, step b). If the parent were still claimable, FIFO
	// would return it first.
	down, err := q.ClaimOne("worker-1")
	if err != nil || down == nil {
		t.Fatalf("downstream not claimable: %v", err)
	}
	if down.ID == id {
		t.Fatalf("parent job %d was not acked; it was re-claimed", id)
	}
	var d StepJob
	if err := down.UnmarshalPayload(&d); err != nil {
		t.Fatalf("downstream payload: %v", err)
	}
	if d.StepID != "b" {
		t.Fatalf("downstream step = %q, want b", d.StepID)
	}
}

func TestDedupeSkipReturnsPriorResult(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)

	// Seed a prior successful dedupe record.
	err := store.CommitStepAtom(honker.StepAtom{
		RunID: "run-1", StepID: "a",
		Result: map[string]any{"ok": true, "from": "prior"},
		Dedupe: &honker.DedupeRecord{WorkflowID: "wf", StepName: "a", Key: "k1", Succeeded: true, Result: map[string]any{"ok": true, "from": "prior"}},
	})
	if err != nil {
		t.Fatalf("seed dedupe: %v", err)
	}

	// A step with the same key; its subprocess would produce a different value.
	if _, err := q.Enqueue(StepJob{
		RunID: "run-2", WorkflowID: "wf", StepID: "a", StepName: "a",
		StepType: "shell", DedupeKey: "k1",
		Command: shellCmd(`printf '{"ok":true,"from":"rerun"}'`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}

	res, ok := claimResultJSON(t, store, "run-2", "a").(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want object", claimResultJSON(t, store, "run-2", "a"))
	}
	if res["from"] != "prior" {
		t.Fatalf("result = %v, want prior result (step skipped)", res)
	}

	// Job acked despite the skip.
	if again, _ := q.ClaimOne("worker-1"); again != nil {
		t.Fatalf("job reclaimed after dedupe skip")
	}
}

func TestDedupeProceedsOnPriorFailure(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)

	err := store.CommitStepAtom(honker.StepAtom{
		RunID: "run-1", StepID: "a",
		Result: map[string]any{"ok": false, "error": "boom"},
		Dedupe: &honker.DedupeRecord{WorkflowID: "wf", StepName: "a", Key: "k1", Succeeded: false, Result: map[string]any{"ok": false}},
	})
	if err != nil {
		t.Fatalf("seed failed dedupe: %v", err)
	}

	if _, err := q.Enqueue(StepJob{
		RunID: "run-2", WorkflowID: "wf", StepID: "a", StepName: "a",
		StepType: "shell", DedupeKey: "k1",
		Command: shellCmd(`printf '{"ok":true,"from":"rerun"}'`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}

	res := claimResultJSON(t, store, "run-2", "a").(map[string]any)
	if res["from"] != "rerun" {
		t.Fatalf("result = %v, want rerun result (failed dedupe must not skip)", res)
	}
}

func TestStepFailureRetriesAndRecordsFailure(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)

	if _, err := q.Enqueue(StepJob{
		RunID: "run-1", WorkflowID: "wf", StepID: "a", StepName: "a",
		StepType: "shell", DedupeKey: "k1",
		Command: shellCmd(`echo boom >&2; exit 1`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err == nil {
		t.Fatal("expected handle to return an error for a failing step")
	}

	// Failed result and failed dedupe recorded.
	res := claimResultJSON(t, store, "run-1", "a").(map[string]any)
	if res["ok"] != false {
		t.Fatalf("result = %v, want ok:false", res)
	}
	out, err := store.LookupDedupe("wf", "a", "k1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if out == nil || out.Succeeded {
		t.Fatalf("expected a failed dedupe record, got %+v", out)
	}

	// The claim was retried, not acked: it is claimable again (max attempts 3).
	if again, err := q.ClaimOne("worker-1"); err != nil || again == nil {
		t.Fatalf("failed step should be re-issued for retry, got %v", err)
	}
}

func TestEnvFilteringToDeclaredSecrets(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, map[string]string{"TOKEN": "abc", "LEAK": "bad"})

	script := `printf '{"TOKEN":"%s","LEAK":"%s"}' "$TOKEN" "${LEAK:-unset}"`
	if _, err := q.Enqueue(StepJob{
		RunID: "run-1", WorkflowID: "wf", StepID: "a", StepName: "a",
		StepType: "shell", Secrets: []string{"TOKEN"},
		Command: shellCmd(script),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}

	res := claimResultJSON(t, store, "run-1", "a").(map[string]any)
	if res["TOKEN"] != "abc" {
		t.Fatalf("TOKEN = %v, want abc", res["TOKEN"])
	}
	if res["LEAK"] != "unset" {
		t.Fatalf("LEAK = %v, want unset (undeclared secret must not reach the subprocess)", res["LEAK"])
	}
}

func TestMissingDeclaredSecretFailsStep(t *testing.T) {
	w, _, q := newWorker(t, 30, 3, map[string]string{})
	if _, err := q.Enqueue(StepJob{
		RunID: "run-1", WorkflowID: "wf", StepID: "a", StepName: "a",
		StepType: "shell", Secrets: []string{"NOPE"},
		Command: shellCmd(`printf '{"ok":true}'`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err == nil {
		t.Fatal("expected error when a declared secret is missing")
	}
}

func TestVisibilityTimeoutReclaimsUnackedClaim(t *testing.T) {
	// A short visibility timeout so a worker that "crashes" mid-job loses its
	// claim and the job is re-issued to another worker (SPEC: Execution model
	// step 9).
	_, _, q := newWorker(t, 1, 3, nil)

	if _, err := q.Enqueue(StepJob{
		RunID: "run-1", WorkflowID: "wf", StepID: "a", StepName: "a",
		StepType: "shell", Command: shellCmd(`printf '{"ok":true}'`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-crashed")
	if err != nil || job == nil {
		t.Fatalf("first claim: %v", err)
	}
	// Simulate a crash: never ack. Wait past the visibility timeout.
	time.Sleep(2500 * time.Millisecond)

	reclaimed, err := q.ClaimOne("worker-2")
	if err != nil || reclaimed == nil {
		t.Fatalf("expired claim not re-issued: %v", err)
	}
	if reclaimed.ID != job.ID {
		t.Fatalf("reclaimed id %d, want the original %d", reclaimed.ID, job.ID)
	}
}

func TestCancelledRunIsSkippedByWorker(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)
	if err := store.CreateRun("run-cancel", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := store.CancelRun("run-cancel"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	if _, err := q.Enqueue(StepJob{
		RunID: "run-cancel", WorkflowID: "wf", StepID: "a", StepName: "a",
		StepType: "shell", Command: shellCmd(`printf '{"ok":true}'`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}

	// The step must not have run: no result written, and the job acked.
	if res := claimResultJSON(t, store, "run-cancel", "a"); res != nil {
		t.Fatalf("cancelled run wrote a result: %v", res)
	}
	if again, _ := q.ClaimOne("worker-1"); again != nil {
		t.Fatalf("cancelled job not acked (id %d)", again.ID)
	}
}

func TestRunMarkedCompletedAfterLastStep(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)
	if err := store.CreateRun("run-done", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	// A single-step run: the only step has no downstream, so completing it
	// marks the run completed.
	if _, err := q.Enqueue(StepJob{
		RunID: "run-done", WorkflowID: "wf", StepID: "a", StepName: "a",
		StepType: "shell", Command: shellCmd(`printf '{"ok":true}'`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if st, _ := store.RunStatus("run-done"); st != honker.RunCompleted {
		t.Fatalf("status = %q, want completed", st)
	}
}

func TestSingerTapPersistsBookmarkAcrossRuns(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, map[string]string{"OUT": filepath.Join(t.TempDir(), "inv.json")})

	// A fake tap that echoes the --state file it receives to $OUT (a declared
	// secret), emits one RECORD and a STATE.
	tap := filepath.Join(t.TempDir(), "tap-fake")
	script := `#!/bin/sh
state=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --state) state="$2"; shift 2;;
    --config) shift 2;;
    *) shift;;
  esac
done
if [ -n "$state" ] && [ -f "$state" ]; then
  cat "$state" > "$OUT"
else
  : > "$OUT"
fi
printf '%s\n' '{"type":"RECORD","stream":"customers","record":{"id":1}}'
printf '%s\n' '{"type":"STATE","value":{"bookmark":"v1"}}'
`
	if err := os.WriteFile(tap, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	runTap := func(runID string) any {
		t.Helper()
		if _, err := q.Enqueue(StepJob{
			RunID: runID, WorkflowID: "wf", StepID: "t", StepName: "t",
			StepType: "singer-tap",
			Config:   map[string]any{"tap": "tap-fake", "config": map[string]any{"client_id": "abc"}},
			Command:  []string{tap},
			Secrets:  []string{"OUT"},
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		job, err := q.ClaimOne("worker-1")
		if err != nil || job == nil {
			t.Fatalf("claim: %v", err)
		}
		if err := w.handle(context.Background(), job); err != nil {
			t.Fatalf("handle: %v", err)
		}
		return claimResultJSON(t, store, runID, "t")
	}

	// First run: no prior state.
	res1 := runTap("run-1").(map[string]any)
	if _, ok := res1["records"]; !ok {
		t.Fatalf("first run result = %v, want records", res1)
	}

	// Second run of the same workflow/step: the prior bookmark must have been
	// passed as a --state file (visible in $OUT).
	runTap("run-2")
	b, err := os.ReadFile(w.secrets["OUT"])
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var prior map[string]any
	if err := json.Unmarshal(b, &prior); err != nil {
		t.Fatalf("state file not valid JSON: %v", err)
	}
	if prior["bookmark"] != "v1" {
		t.Fatalf("prior bookmark = %v, want v1 passed via --state", prior)
	}
}

// stubMCP is a test double for the MCP runner.
type stubMCP struct {
	result mcp.CallResult
	err    error
}

func (s stubMCP) Call(_ context.Context, req mcp.CallRequest) (mcp.CallResult, error) {
	return s.result, s.err
}

func TestMCPStepDispatchesAndMapsError(t *testing.T) {
	ext := extPath(t)
	store, err := honker.Open(filepath.Join(t.TempDir(), "test.db"), ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	q := store.Queue("steps", 30, 3)
	w := New(store, q, "worker-1", Config{MCP: stubMCP{
		result: mcp.CallResult{IsError: true, Content: "boom"},
	}})

	if _, err := q.Enqueue(StepJob{
		RunID: "run-mcp", WorkflowID: "wf", StepID: "m", StepName: "m",
		StepType: "mcp-call",
		Config:   map[string]any{"server": "srv", "tool": "search", "input": map[string]any{"query": "x"}, "mode": "stateless"},
		Command:  []string{"srv"},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}
	res := claimResultJSON(t, store, "run-mcp", "m").(map[string]any)
	if res["ok"] != false || res["code"] != "mcp_tool_error" {
		t.Fatalf("result = %v, want mapped mcp_tool_error", res)
	}
}

// stubRunner is a StepRunner that returns a fixed result without spawning a
// subprocess, so threading of the {event, steps} input can be asserted.
type stubRunner struct {
	out any
}

func (s stubRunner) Run(_ context.Context, req exec.Request) (exec.Result, error) {
	return exec.Result{Output: s.out}, nil
}

func TestDownstreamInputIsThreaded(t *testing.T) {
	w, _, q := newWorker(t, 30, 3, nil)
	// Replace the subprocess runner with a stub.
	w.runner = stubRunner{out: map[string]any{"ok": true}}

	headInput := map[string]any{"event": map[string]any{"id": "e1"}, "steps": map[string]any{}}
	if _, err := q.Enqueue(StepJob{
		RunID: "run-1", WorkflowID: "wf", StepID: "a", StepName: "a",
		StepType: "shell", Input: headInput,
		Command: []string{"ignored"},
		Downstream: []StepJob{{
			RunID: "run-1", WorkflowID: "wf", StepID: "b", StepName: "b",
			StepType: "shell", Command: []string{"ignored"},
		}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}

	down, err := q.ClaimOne("worker-1")
	if err != nil || down == nil {
		t.Fatalf("downstream not claimable: %v", err)
	}
	var d StepJob
	if err := down.UnmarshalPayload(&d); err != nil {
		t.Fatalf("downstream payload: %v", err)
	}
	// The downstream's input must carry the event and step a's result under its name.
	ev, _ := d.Input["event"].(map[string]any)
	if ev["id"] != "e1" {
		t.Fatalf("downstream event = %v, want e1", ev)
	}
	steps, _ := d.Input["steps"].(map[string]any)
	ares, ok := steps["a"].(map[string]any)
	if !ok || ares["ok"] != true {
		t.Fatalf("downstream steps.a = %v, want {ok: true}", steps["a"])
	}
}

// TestDedupeKeyEvaluatedAtExecution pins that a dedupe_key JSONata expression is
// evaluated against the step's {event, steps} input at execution time (ADR-0020,
// ADR-0021), not supplied pre-resolved.
func TestDedupeKeyEvaluatedAtExecution(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)

	// Seed a prior successful dedupe for key derived from event.id = "e-7".
	err := store.CommitStepAtom(honker.StepAtom{
		RunID: "run-1", StepID: "a",
		Result: map[string]any{"ok": true, "from": "prior"},
		Dedupe: &honker.DedupeRecord{WorkflowID: "wf", StepName: "a", Key: "e-7", Succeeded: true, Result: map[string]any{"ok": true, "from": "prior"}},
	})
	if err != nil {
		t.Fatalf("seed dedupe: %v", err)
	}

	// The step's dedupe_key is the expression event.id, evaluated against input.
	if _, err := q.Enqueue(StepJob{
		RunID: "run-2", WorkflowID: "wf", StepID: "a", StepName: "a",
		StepType: "shell", DedupeKey: "event.id",
		Input:   map[string]any{"event": map[string]any{"id": "e-7"}, "steps": map[string]any{}},
		Command: shellCmd(`printf '{"ok":true,"from":"rerun"}'`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}

	res, ok := claimResultJSON(t, store, "run-2", "a").(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want object", claimResultJSON(t, store, "run-2", "a"))
	}
	if res["from"] != "prior" {
		t.Fatalf("result = %v, want prior result (dedupe_key expression matched)", res)
	}
}

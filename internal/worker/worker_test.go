package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mathias-g/Servitor/internal/honker"
)

func extPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("HONKER_EXT_PATH")
	if p == "" {
		t.Skip("HONKER_EXT_PATH not set; skipping Honker-backed worker test")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("HONKER_EXT_PATH %s not readable: %v", p, err)
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

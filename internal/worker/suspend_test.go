package worker

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mathias-g/Servitor/internal/honker"
)

func waitResult(t *testing.T, store *honker.Store, runID string) map[string]any {
	t.Helper()
	res := claimResultJSON(t, store, runID, "w")
	m, _ := res.(map[string]any)
	return m
}

// waitJob builds a wait node with a signal source and one downstream node b.
func waitJob(runID string, signal string) NodeJob {
	b := NodeJob{RunID: runID, WorkflowID: "wf", NodeID: "b", NodeName: "b",
		NodeType: "shell", Command: shellCmd(`printf '{"ok":true}'`)}
	return NodeJob{RunID: runID, WorkflowID: "wf", NodeID: "w", NodeName: "w",
		NodeType: "wait", Config: map[string]any{"signal": signal},
		Input:      map[string]any{"event": map[string]any{"order_id": "1"}, "steps": map[string]any{}},
		Downstream: []NodeJob{b}}
}

func TestWaitParksAndResumesBySignal(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)
	if err := store.CreateRun("run-w", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := q.Enqueue(waitJob("run-w", `"gate"`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}

	// The run is parked (waiting), not completed.
	if st, _ := store.RunStatus("run-w"); st != honker.RunWaiting {
		t.Fatalf("status = %q, want waiting", st)
	}
	cont, err := store.GetContinuation("run-w")
	if err != nil || cont == nil {
		t.Fatalf("continuation: %v", err)
	}
	if cont.SignalName != "gate" {
		t.Fatalf("signal name = %q, want gate", cont.SignalName)
	}

	// Resuming by signal wakes it and fans out the downstream node.
	if err := ResumeBySignal(store, q, "gate", map[string]any{"approved": true}); err != nil {
		t.Fatalf("ResumeBySignal: %v", err)
	}
	if st, _ := store.RunStatus("run-w"); st != honker.RunRunning {
		t.Fatalf("status = %q, want running after resume", st)
	}
	if cont, _ := store.GetContinuation("run-w"); cont != nil {
		t.Fatalf("continuation not cleared after resume")
	}
	res := waitResult(t, store, "run-w")
	if res["source"] != "signal" {
		t.Fatalf("result source = %v, want signal", res["source"])
	}
	if p, _ := res["payload"].(map[string]any); p["approved"] != true {
		t.Fatalf("result payload = %v, want approved:true", res["payload"])
	}

	// The downstream node now runs and the run completes.
	bjob, err := q.ClaimOne("worker-1")
	if err != nil || bjob == nil {
		t.Fatalf("downstream not enqueued: %v", err)
	}
	if err := w.handle(context.Background(), bjob); err != nil {
		t.Fatalf("handle downstream: %v", err)
	}
	if st, _ := store.RunStatus("run-w"); st != honker.RunCompleted {
		t.Fatalf("status = %q, want completed", st)
	}
}

func TestWaitParksAndResumesByTimer(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)
	if err := store.CreateRun("run-t", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	// A timer `at` in the past is claimable immediately, so no sleeping.
	wj := waitJob("run-t", "")
	wj.Config = map[string]any{"timer": map[string]any{"at": time.Now().Add(-time.Minute).Format(time.RFC3339)}}
	if _, err := q.Enqueue(wj); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if st, _ := store.RunStatus("run-t"); st != honker.RunWaiting {
		t.Fatalf("status = %q, want waiting", st)
	}

	// The timer fired: a `resume` job is claimable. Claim and handle it.
	tjob, err := q.ClaimOne("worker-1")
	if err != nil || tjob == nil {
		t.Fatalf("timer resume job not claimable: %v", err)
	}
	if err := w.handle(context.Background(), tjob); err != nil {
		t.Fatalf("handle resume: %v", err)
	}
	res := waitResult(t, store, "run-t")
	if res["source"] != "timer" {
		t.Fatalf("result source = %v, want timer", res["source"])
	}
	if res["payload"] != nil {
		t.Fatalf("result payload = %v, want nil on timer", res["payload"])
	}
}

func TestWaitBufferedSignalResumesImmediately(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)
	if err := store.CreateRun("run-b", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	// A signal sent before the run parks is buffered (ADR-0042 race rule).
	if err := ResumeBySignal(store, q, "gate", map[string]any{"approved": true}); err != nil {
		t.Fatalf("ResumeBySignal (buffer): %v", err)
	}
	if _, err := q.Enqueue(waitJob("run-b", `"gate"`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}
	// The wait consumed the buffered signal and resumed immediately: not parked.
	if st, _ := store.RunStatus("run-b"); st == honker.RunWaiting {
		t.Fatalf("run parked despite a buffered signal")
	}
	res := waitResult(t, store, "run-b")
	if res["source"] != "signal" {
		t.Fatalf("result source = %v, want signal", res["source"])
	}
	if p, _ := res["payload"].(map[string]any); p["approved"] != true {
		t.Fatalf("result payload = %v, want approved:true", res["payload"])
	}
}

func TestWaitRepeatResumeIsNoOp(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)
	if err := store.CreateRun("run-n", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := q.Enqueue(waitJob("run-n", `"gate"`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, _ := q.ClaimOne("worker-1")
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if err := ResumeBySignal(store, q, "gate", "first"); err != nil {
		t.Fatalf("first resume: %v", err)
	}
	// A second resume must not re-run anything: still one downstream job.
	if err := ResumeBySignal(store, q, "gate", "second"); err != nil {
		t.Fatalf("second resume: %v", err)
	}
	// The run is running with exactly one downstream job pending.
	if st, _ := store.RunStatus("run-n"); st != honker.RunRunning {
		t.Fatalf("status = %q, want running", st)
	}
	// Exactly one downstream node remains to claim.
	if j1, _ := q.ClaimOne("worker-1"); j1 == nil {
		t.Fatalf("downstream not enqueued after first resume")
	}
	if j2, _ := q.ClaimOne("worker-1"); j2 != nil {
		t.Fatalf("downstream enqueued twice after repeat resume")
	}
}

func TestWaitAmbiguousSignalRejected(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)
	// Two runs park on the same effective signal name.
	for _, id := range []string{"run-a1", "run-a2"} {
		if err := store.CreateRun(id, "wf"); err != nil {
			t.Fatalf("CreateRun %s: %v", id, err)
		}
		if _, err := q.Enqueue(waitJob(id, `"shared"`)); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
		job, err := q.ClaimOne("worker-1")
		if err != nil || job == nil {
			t.Fatalf("claim %s: %v", id, err)
		}
		if err := w.handle(context.Background(), job); err != nil {
			t.Fatalf("handle %s: %v", id, err)
		}
	}
	if err := ResumeBySignal(store, q, "shared", "x"); err == nil {
		t.Fatalf("ambiguous signal was not rejected")
	}
	// Neither run was resumed.
	if st, _ := store.RunStatus("run-a1"); st != honker.RunWaiting {
		t.Fatalf("run-a1 status = %q, want waiting", st)
	}
	if st, _ := store.RunStatus("run-a2"); st != honker.RunWaiting {
		t.Fatalf("run-a2 status = %q, want waiting", st)
	}
}

func TestSendSignalWakesParkedRun(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)
	if err := store.CreateRun("run-park", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := q.Enqueue(waitJob("run-park", `"approval." & $string(event.order_id)`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, _ := q.ClaimOne("worker-1")
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle park: %v", err)
	}
	if st, _ := store.RunStatus("run-park"); st != honker.RunWaiting {
		t.Fatalf("park status = %q, want waiting", st)
	}

	// A sender workflow's send-signal node wakes the parked run by the same
	// effective name. The name resolves from the sender's own event.
	send := NodeJob{RunID: "run-send", WorkflowID: "wf", NodeID: "s", NodeName: "s",
		NodeType: "send-signal",
		Config:   map[string]any{"signal": `"approval." & $string(event.order_id)`, "payload": `"approve"`},
		Input:    map[string]any{"event": map[string]any{"order_id": "1"}, "steps": map[string]any{}},
	}
	if err := store.CreateRun("run-send", "wf"); err != nil {
		t.Fatalf("CreateRun send: %v", err)
	}
	if _, err := q.Enqueue(send); err != nil {
		t.Fatalf("enqueue send: %v", err)
	}
	sjob, _ := q.ClaimOne("worker-1")
	if err := w.handle(context.Background(), sjob); err != nil {
		t.Fatalf("handle send-signal: %v", err)
	}
	if st, _ := store.RunStatus("run-park"); st != honker.RunRunning {
		t.Fatalf("parked run not resumed: status = %q", st)
	}
	res := waitResult(t, store, "run-park")
	if res["source"] != "signal" || res["payload"] != "approve" {
		t.Fatalf("resumed result = %v, want signal/approve", res)
	}
}

func TestWaitTimerRunAtSurvivesReopen(t *testing.T) {
	ext := extPath(t)
	dbPath := filepath.Join(t.TempDir(), "timer.db")

	store, err := honker.Open(dbPath, ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	q := store.Queue("nodes", 30, 3)
	w := New(store, q, "worker-1", Config{})
	if err := store.CreateRun("run-t", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	// A timer `at` in the past is claimable immediately, so we do not sleep.
	wj := waitJob("run-t", "")
	wj.Config = map[string]any{"timer": map[string]any{"at": time.Now().Add(-time.Minute).Format(time.RFC3339)}}
	if _, err := q.Enqueue(wj); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, _ := q.ClaimOne("worker-1")
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if st, _ := store.RunStatus("run-t"); st != honker.RunWaiting {
		t.Fatalf("status = %q, want waiting", st)
	}
	// Close and reopen the store, as a daemon restart would.
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	store2, err := honker.Open(dbPath, ext)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = store2.Close() }()
	q2 := store2.Queue("nodes", 30, 3)
	w2 := New(store2, q2, "worker-1", Config{})
	// The parked run is still waiting, and its timer resume job is still
	// claimable after reopen.
	if st, _ := store2.RunStatus("run-t"); st != honker.RunWaiting {
		t.Fatalf("status after reopen = %q, want waiting", st)
	}
	tjob, err := q2.ClaimOne("worker-1")
	if err != nil || tjob == nil {
		t.Fatalf("timer resume job not claimable after reopen: %v", err)
	}
	if err := w2.handle(context.Background(), tjob); err != nil {
		t.Fatalf("handle resume after reopen: %v", err)
	}
	res := waitResult(t, store2, "run-t")
	if res["source"] != "timer" {
		t.Fatalf("result source = %v, want timer", res["source"])
	}
}

func TestRerunFailedNodeCallsOnRerun(t *testing.T) {
	var gotRun, gotMode string
	w, store, q := newWorker(t, 30, 3, nil)
	w.onRerun = func(runID, mode string) error {
		gotRun = runID
		gotMode = mode
		return nil
	}
	if err := store.CreateRun("run-watcher", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	// A rerun-failed node, triggered by a failed event carrying from_run.
	node := NodeJob{RunID: "run-watcher", WorkflowID: "wf", NodeID: "r", NodeName: "r",
		NodeType: "rerun-failed",
		Config:   map[string]any{},
		Input:    map[string]any{"event": map[string]any{"trigger": "failed", "from": "demo", "from_run": "demo-123"}, "steps": map[string]any{}},
	}
	if _, err := q.Enqueue(node); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, _ := q.ClaimOne("worker-1")
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if gotRun != "demo-123" {
		t.Fatalf("onRerun run = %q, want demo-123 (from event.from_run)", gotRun)
	}
	if gotMode != "continue" {
		t.Fatalf("onRerun mode = %q, want continue (default)", gotMode)
	}
}

func TestRerunFailedNodeModeAndRunIDExpr(t *testing.T) {
	var gotRun, gotMode string
	w, store, q := newWorker(t, 30, 3, nil)
	w.onRerun = func(runID, mode string) error {
		gotRun = runID
		gotMode = mode
		return nil
	}
	if err := store.CreateRun("run-watcher", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	node := NodeJob{RunID: "run-watcher", WorkflowID: "wf", NodeID: "r", NodeName: "r",
		NodeType: "rerun-failed",
		Config:   map[string]any{"run_id": `"my-run-9"`, "mode": "restart"},
		Input:    map[string]any{"event": map[string]any{}, "steps": map[string]any{}},
	}
	if _, err := q.Enqueue(node); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, _ := q.ClaimOne("worker-1")
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if gotRun != "my-run-9" {
		t.Fatalf("onRerun run = %q, want my-run-9 (from run_id expr)", gotRun)
	}
	if gotMode != "restart" {
		t.Fatalf("onRerun mode = %q, want restart", gotMode)
	}
}

func TestGenericFailureMarksFailedAndSavesContinuation(t *testing.T) {
	// A non-secret node failure that exhausts attempts must dead-letter, mark
	// the run failed, and save its continuation so it can be re-run (ADR-0044).
	ext := extPath(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := honker.Open(dbPath, ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	q := store.Queue("nodes", 30, 1)
	w := New(store, q, "worker-1", Config{MaxAttempts: 1})
	if err := store.CreateRun("run-1", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	// A shell node whose command always fails (non-zero exit, no JSON).
	if _, err := q.Enqueue(NodeJob{
		RunID: "run-1", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", Command: shellCmd(`exit 1`),
		Input: map[string]any{"event": map[string]any{"trigger": "manual"}, "steps": map[string]any{}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, _ := q.ClaimOne("worker-1")
	_ = w.handle(context.Background(), job)

	if st, _ := store.RunStatus("run-1"); st != honker.RunFailed {
		t.Fatalf("status = %q, want failed (generic dead-letter)", st)
	}
	cont, err := store.GetFailedContinuation("run-1")
	if err != nil || cont == nil {
		t.Fatalf("failed continuation not saved: %v", err)
	}
	if cont.WorkflowID != "wf" {
		t.Fatalf("continuation workflow = %q, want wf", cont.WorkflowID)
	}
}

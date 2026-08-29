package runner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/worker"
)

// failedNodeJob returns a self-contained NodeJob representing a dead-lettered
// node, the shape saved as a failed continuation.
func failedNodeJob(runID string) worker.NodeJob {
	b := worker.NodeJob{RunID: runID, WorkflowID: "demo", NodeID: "b", NodeName: "b",
		NodeType: "shell", Command: []string{"/bin/sh", "-c", `printf '{"ok":true}'`}}
	return worker.NodeJob{RunID: runID, WorkflowID: "demo", NodeID: "a", NodeName: "a",
		NodeType: "shell", Command: []string{"/bin/sh", "-c", `printf '{"ok":true}'`},
		Input:      map[string]any{"event": map[string]any{"trigger": "manual"}, "steps": map[string]any{}},
		Downstream: []worker.NodeJob{b}}
}

func TestRerunContinueReenqueuesFailedNode(t *testing.T) {
	store := openStore(t)
	q := store.Queue("nodes", 30, 3)
	if err := store.CreateRun("run-1", "demo"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	// Simulate a dead-lettered run with a saved continuation.
	payload, _ := json.Marshal(failedNodeJob("run-1"))
	if err := store.WithTx(func(tx *honker.Tx) error {
		return tx.WriteFailedContinuation(honker.FailedContinuation{
			RunID: "run-1", WorkflowID: "demo",
			Event:   map[string]any{"trigger": "manual"},
			Payload: payload,
		})
	}); err != nil {
		t.Fatalf("write continuation: %v", err)
	}
	if err := store.SetRunStatus("run-1", honker.RunFailed); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	if err := Rerun(store, q, "run-1", "continue"); err != nil {
		t.Fatalf("Rerun continue: %v", err)
	}
	if st, _ := store.RunStatus("run-1"); st != honker.RunRunning {
		t.Fatalf("status = %q, want running", st)
	}
	// The failed node is re-enqueued (the run's head, node a).
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("failed node not re-enqueued: %v", err)
	}
	var got worker.NodeJob
	if err := job.UnmarshalPayload(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.NodeID != "a" {
		t.Fatalf("re-enqueued node = %q, want a", got.NodeID)
	}
}

func TestRerunRestartRebuildsFromTop(t *testing.T) {
	store := openStore(t)
	q := store.Queue("nodes", 30, 3)
	// Register the workflow the failed run belongs to.
	if err := store.RegisterWorkflow("demo", shellYAML); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := store.CreateRun("run-1", "demo"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	payload, _ := json.Marshal(failedNodeJob("run-1"))
	if err := store.WithTx(func(tx *honker.Tx) error {
		return tx.WriteFailedContinuation(honker.FailedContinuation{
			RunID: "run-1", WorkflowID: "demo",
			Event:   map[string]any{"trigger": "manual"},
			Payload: payload,
		})
	}); err != nil {
		t.Fatalf("write continuation: %v", err)
	}
	if err := store.SetRunStatus("run-1", honker.RunFailed); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	if err := Rerun(store, q, "run-1", "restart"); err != nil {
		t.Fatalf("Rerun restart: %v", err)
	}
	if st, _ := store.RunStatus("run-1"); st != honker.RunRunning {
		t.Fatalf("status = %q, want running", st)
	}
	// The head of the DAG is enqueued for a fresh run.
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("head not enqueued after restart: %v", err)
	}
}

func TestRerunDiscardDropsContinuation(t *testing.T) {
	store := openStore(t)
	q := store.Queue("nodes", 30, 3)
	if err := store.CreateRun("run-1", "demo"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	payload, _ := json.Marshal(failedNodeJob("run-1"))
	if err := store.WithTx(func(tx *honker.Tx) error {
		return tx.WriteFailedContinuation(honker.FailedContinuation{RunID: "run-1", WorkflowID: "demo", Payload: payload})
	}); err != nil {
		t.Fatalf("write continuation: %v", err)
	}
	if err := store.SetRunStatus("run-1", honker.RunFailed); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	if err := Rerun(store, q, "run-1", "discard"); err != nil {
		t.Fatalf("Rerun discard: %v", err)
	}
	cont, err := store.GetFailedContinuation("run-1")
	if err != nil || cont != nil {
		t.Fatalf("continuation not dropped: %v %v", cont, err)
	}
	if st, _ := store.RunStatus("run-1"); st != honker.RunFailed {
		t.Fatalf("status = %q, want failed (unchanged)", st)
	}
}

func TestRerunNoContinuationErrors(t *testing.T) {
	store := openStore(t)
	q := store.Queue("nodes", 30, 3)
	if err := store.CreateRun("run-1", "demo"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := Rerun(store, q, "run-1", "continue"); err == nil {
		t.Fatalf("Rerun on a run with no continuation should error")
	}
}

func TestRerunUnknownModeErrors(t *testing.T) {
	store := openStore(t)
	q := store.Queue("nodes", 30, 3)
	if err := store.CreateRun("run-1", "demo"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	payload, _ := json.Marshal(failedNodeJob("run-1"))
	if err := store.WithTx(func(tx *honker.Tx) error {
		return tx.WriteFailedContinuation(honker.FailedContinuation{RunID: "run-1", WorkflowID: "demo", Payload: payload})
	}); err != nil {
		t.Fatalf("write continuation: %v", err)
	}
	if err := Rerun(store, q, "run-1", "nonsense"); err == nil {
		t.Fatalf("Rerun with an unknown mode should error")
	}
}

var _ = context.Background

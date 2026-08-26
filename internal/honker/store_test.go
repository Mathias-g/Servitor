package honker

import (
	"testing"
)

// TestWorkflowRegistry exercises the durable workflow registry: register,
// get, list, enable/disable, and re-register.
func TestWorkflowRegistry(t *testing.T) {
	s := openStore(t)

	if _, err := s.GetWorkflow("none"); err != nil {
		t.Fatalf("GetWorkflow on missing: %v", err)
	}
	if wf, _ := s.GetWorkflow("none"); wf != nil {
		t.Fatalf("GetWorkflow on missing should return nil, got %+v", wf)
	}

	if err := s.RegisterWorkflow("w1", "name: w1"); err != nil {
		t.Fatalf("RegisterWorkflow: %v", err)
	}
	wf, err := s.GetWorkflow("w1")
	if err != nil || wf == nil {
		t.Fatalf("GetWorkflow after register: %v (wf=%v)", err, wf)
	}
	if wf.Name != "w1" || !wf.Enabled {
		t.Fatalf("GetWorkflow = %+v, want name w1, enabled by default", wf)
	}

	// A fresh registration is enabled by default; RegisterWorkflow replaces
	// but never flips `enabled`.
	if err := s.RegisterWorkflow("w1", "name: w1\nchanged: true"); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	wf, _ = s.GetWorkflow("w1")
	if !wf.Enabled {
		t.Fatalf("re-register must not reset enabled to false")
	}

	// Explicit enable/disable round-trips.
	if err := s.SetWorkflowEnabled("w1", true); err != nil {
		t.Fatalf("SetWorkflowEnabled true: %v", err)
	}
	wf, _ = s.GetWorkflow("w1")
	if !wf.Enabled {
		t.Fatalf("workflow should be enabled after SetWorkflowEnabled(true)")
	}
	if err := s.SetWorkflowEnabled("w1", false); err != nil {
		t.Fatalf("SetWorkflowEnabled false: %v", err)
	}
	wf, _ = s.GetWorkflow("w1")
	if wf.Enabled {
		t.Fatalf("workflow should be disabled after SetWorkflowEnabled(false)")
	}

	// Disabling an unregistered workflow errors.
	if err := s.SetWorkflowEnabled("missing", false); err == nil {
		t.Fatal("SetWorkflowEnabled on unregistered workflow should error")
	}

	list, err := s.ListWorkflows()
	if err != nil || len(list) != 1 || list[0].Name != "w1" {
		t.Fatalf("ListWorkflows = %+v, err %v; want one w1", list, err)
	}
}

// TestRunDepsHelpers exercises the fan-in dependency bookkeeping reads and the
// foreach override, which are separate from the worker's commit path.
func TestRunDepsHelpers(t *testing.T) {
	s := openStore(t)
	rd := NewRunDeps("run-1", map[string]int{"a": 0, "b": 1, "c": 2}, []string{"a", "b", "c"})
	if err := s.InitRunDeps(rd); err != nil {
		t.Fatalf("InitRunDeps: %v", err)
	}

	if n, err := s.RunDepsRemaining("run-1", "c"); err != nil || n != 2 {
		t.Fatalf("RunDepsRemaining c = %d, err %v; want 2", n, err)
	}

	// foreach override: set b's count to 5, read it back.
	if err := s.SetRunDepsRemaining("run-1", "b", 5); err != nil {
		t.Fatalf("SetRunDepsRemaining: %v", err)
	}
	if n, _ := s.RunDepsRemaining("run-1", "b"); n != 5 {
		t.Fatalf("RunDepsRemaining b after override = %d, want 5", n)
	}

	// A run with no tracked nodes is complete; one with pending work is not.
	if done, err := s.RunComplete("run-missing"); err != nil || !done {
		t.Fatalf("RunComplete on missing run = %v, err %v; want true", done, err)
	}
	if done, err := s.RunComplete("run-1"); err != nil || done {
		t.Fatalf("RunComplete on run with pending deps = %v, err %v; want false", done, err)
	}
}

// TestNodeResultStore exercises storing and reading a single node result and
// the count helper.
func TestNodeResultStore(t *testing.T) {
	s := openStore(t)
	if n, err := s.NodeResultCount(); err != nil || n != 0 {
		t.Fatalf("NodeResultCount empty = %d, err %v; want 0", n, err)
	}
	if raw, err := s.ResultJSON("run-1", "a"); err != nil || raw != "" {
		t.Fatalf("ResultJSON missing = %q, err %v; want empty", raw, err)
	}
	if v, err := s.Result("run-1", "a"); err != nil || v != nil {
		t.Fatalf("Result missing = %v, err %v; want nil", v, err)
	}

	atom := NodeAtom{RunID: "run-1", NodeID: "a", Result: map[string]any{"ok": true}}
	if err := s.CommitNodeAtom(atom); err != nil {
		t.Fatalf("CommitNodeAtom: %v", err)
	}
	if n, _ := s.NodeResultCount(); n != 1 {
		t.Fatalf("NodeResultCount = %d, want 1", n)
	}
	raw, err := s.ResultJSON("run-1", "a")
	if err != nil || raw == "" {
		t.Fatalf("ResultJSON after commit = %q, err %v; want non-empty", raw, err)
	}
	if v, err := s.Result("run-1", "a"); err != nil || v == nil {
		t.Fatalf("Result after commit = %v, err %v; want value", v, err)
	}
}

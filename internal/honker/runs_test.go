package honker

import "testing"

func TestRunLifecycle(t *testing.T) {
	s := openStore(t)

	if err := s.CreateRun("run-1", "demo"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	st, err := s.RunStatus("run-1")
	if err != nil {
		t.Fatalf("RunStatus: %v", err)
	}
	if st != RunRunning {
		t.Fatalf("status = %q, want running", st)
	}

	if err := s.SetRunStatus("run-1", RunCompleted); err != nil {
		t.Fatalf("SetRunStatus: %v", err)
	}
	r, err := s.GetRun("run-1")
	if err != nil || r == nil {
		t.Fatalf("GetRun: %v", err)
	}
	if r.Status != RunCompleted || r.WorkflowName != "demo" {
		t.Fatalf("run = %+v, want completed/demo", r)
	}

	// Unknown run -> empty status and nil run.
	if st, _ := s.RunStatus("nope"); st != "" {
		t.Fatalf("unknown run status = %q, want empty", st)
	}
	if r, _ := s.GetRun("nope"); r != nil {
		t.Fatalf("unknown run GetRun = %+v, want nil", r)
	}
}

func TestListRunsAndRunNodes(t *testing.T) {
	s := openStore(t)

	if err := s.CreateRun("run-1", "demo"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := s.CreateRun("run-2", "other"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	// Write a node result for run-1.
	if err := s.CommitNodeAtom(NodeAtom{RunID: "run-1", NodeID: "a", Result: map[string]any{"ok": true}}); err != nil {
		t.Fatalf("CommitNodeAtom: %v", err)
	}

	runs, err := s.ListRuns()
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}

	steps, err := s.RunNodes("run-1")
	if err != nil {
		t.Fatalf("RunNodes: %v", err)
	}
	if len(steps) != 1 || steps[0].NodeID != "a" {
		t.Fatalf("steps = %+v, want [a]", steps)
	}
	if steps, _ := s.RunNodes("run-2"); len(steps) != 0 {
		t.Fatalf("run-2 steps = %+v, want empty", steps)
	}
}

func TestCancelRun(t *testing.T) {
	s := openStore(t)
	if err := s.CreateRun("run-1", "demo"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	// Enqueue a pending job carrying this run id.
	q := s.Queue("nodes", 30, 3)
	if _, err := q.Enqueue(map[string]any{"RunID": "run-1", "NodeID": "a"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := s.CancelRun("run-1"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	if st, _ := s.RunStatus("run-1"); st != RunCancelled {
		t.Fatalf("status = %q, want cancelled", st)
	}
	// The pending job was dropped.
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatalf("pending job survived cancel (id %d)", job.ID)
	}
}

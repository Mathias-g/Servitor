package honker

import (
	"testing"
)

// TestInitRunDepsAndFanIn pins the dependency-counter fan-out (ADR-0023): a
// dependent node is enqueued only when its remaining dependency count reaches
// zero, and the decrement happens inside the atomic commit.
func TestInitRunDepsAndFanIn(t *testing.T) {
	s := openStore(t)
	q := s.Queue("nodes", 30, 3)

	// Run with nodes a, b, c. c depends on both a and b (fan-in); a and b
	// depend on nothing (initially ready).
	rd := NewRunDeps("run-1", map[string]int{"a": 0, "b": 0, "c": 2}, []string{"a", "b", "c"})
	if err := s.InitRunDeps(rd); err != nil {
		t.Fatalf("InitRunDeps: %v", err)
	}

	if n, _ := s.RunDepsRemaining("run-1", "c"); n != 2 {
		t.Fatalf("c remaining = %d, want 2", n)
	}

	// Node a completes. c's count drops to 1; c is not ready, so its job is
	// not enqueued.
	err := s.CommitNodeAtom(NodeAtom{
		RunID:      "run-1",
		NodeID:     "a",
		Result:     map[string]any{"a": true},
		Dependents: []string{"c"},
		Downstream: []Downstream{
			{Queue: q, Payload: map[string]any{"node": "c"}},
		},
	})
	if err != nil {
		t.Fatalf("CommitNodeAtom(a): %v", err)
	}
	if n, _ := s.RunDepsRemaining("run-1", "c"); n != 1 {
		t.Fatalf("c remaining after a = %d, want 1", n)
	}
	if job, _ := q.ClaimOne("worker-1"); job != nil {
		t.Fatalf("c enqueued before all deps satisfied")
	}

	// Node b completes. c's count drops to 0; c is ready and is enqueued.
	err = s.CommitNodeAtom(NodeAtom{
		RunID:      "run-1",
		NodeID:     "b",
		Result:     map[string]any{"b": true},
		Dependents: []string{"c"},
		Downstream: []Downstream{
			{Queue: q, Payload: map[string]any{"node": "c"}},
		},
	})
	if err != nil {
		t.Fatalf("CommitNodeAtom(b): %v", err)
	}
	if n, _ := s.RunDepsRemaining("run-1", "c"); n != 0 {
		t.Fatalf("c remaining after b = %d, want 0", n)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("c not enqueued after all deps satisfied: %v", err)
	}
	var p map[string]any
	if err := job.UnmarshalPayload(&p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p["node"] != "c" {
		t.Fatalf("payload = %v, want node c", p)
	}
}

// TestFanInRollback pins that a failed atom does not decrement dependents: the
// decrement commits only with the rest of the atom.
func TestFanInRollback(t *testing.T) {
	s := openStore(t)
	q := s.Queue("nodes", 30, 3)

	rd := NewRunDeps("run-1", map[string]int{"a": 0, "c": 1}, []string{"a", "c"})
	if err := s.InitRunDeps(rd); err != nil {
		t.Fatalf("InitRunDeps: %v", err)
	}

	// A failing atom (unmarshalable downstream payload) must not decrement c.
	err := s.CommitNodeAtom(NodeAtom{
		RunID:      "run-1",
		NodeID:     "a",
		Result:     map[string]any{"ok": true},
		Dependents: []string{"c"},
		Downstream: []Downstream{
			{Queue: q, Payload: map[string]any{"f": func() {}}},
		},
	})
	if err == nil {
		t.Fatal("expected CommitNodeAtom to fail")
	}
	if n, _ := s.RunDepsRemaining("run-1", "c"); n != 1 {
		t.Fatalf("c remaining after rollback = %d, want 1 (decrement rolled back)", n)
	}
}

package honker

import (
	"os"
	"path/filepath"
	"testing"
)

// extPath returns the path to the Honker extension, or "" if it is not
// configured. Honker-backed tests skip when it is unset, so plain `make check`
// needs no setup; CI sets HONKER_EXTENSION_PATH so the real tests run there.
func extPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("HONKER_EXTENSION_PATH")
	if p == "" {
		t.Skip("HONKER_EXTENSION_PATH not set; skipping Honker test")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("HONKER_EXTENSION_PATH %s not readable: %v", p, err)
	}
	return p
}

func openStore(t *testing.T) *Store {
	t.Helper()
	ext := extPath(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(dbPath, ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestQueueEnqueueClaimAck(t *testing.T) {
	s := openStore(t)
	q := s.Queue("jobs", 30, 3)

	id, err := q.Enqueue(map[string]any{"step": "a"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if id == 0 {
		t.Fatal("expected a job id")
	}

	job, err := q.ClaimOne("worker-1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if job == nil {
		t.Fatal("expected a claimable job")
	}
	var p map[string]any
	if err := job.UnmarshalPayload(&p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p["step"] != "a" {
		t.Fatalf("payload = %v, want step a", p)
	}
	acked, err := job.Ack()
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	if !acked {
		t.Fatal("ack should return true")
	}

	// The acked job must not be claimable again.
	again, err := q.ClaimOne("worker-1")
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if again != nil {
		t.Fatalf("acked job reclaimed (id %d)", again.ID)
	}
}

func TestWALMode(t *testing.T) {
	s := openStore(t)
	var mode string
	if err := s.db.Raw().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestCommitStepAtomIsAtomic(t *testing.T) {
	s := openStore(t)
	q := s.Queue("steps", 30, 3)

	// A failing atom: the downstream payload can't be JSON-marshaled, so the
	// enqueue fails after the result and dedupe rows are already written
	// inside the transaction. Everything must roll back together.
	err := s.CommitStepAtom(StepAtom{
		RunID:  "run-1",
		StepID: "a",
		Result: map[string]any{"ok": true},
		Dedupe: &DedupeRecord{WorkflowID: "wf", StepName: "a", Key: "k", Succeeded: true, Result: "x"},
		Downstream: []Downstream{
			{Queue: q, Payload: map[string]any{"step": "b", "f": func() {}}},
		},
	})
	if err == nil {
		t.Fatal("expected CommitStepAtom to fail on unmarshalable downstream payload")
	}

	// The dedupe record must not exist (rolled back).
	out, err := s.LookupDedupe("wf", "a", "k")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if out != nil {
		t.Fatalf("dedupe record persisted despite rollback: %+v", out)
	}

	// The downstream job must not be claimable (rolled back).
	job, err := q.ClaimOne("worker-1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if job != nil {
		t.Fatalf("downstream job persisted despite rollback (id %d)", job.ID)
	}
}

func TestCommitStepAtomWritesAllParts(t *testing.T) {
	s := openStore(t)
	q := s.Queue("steps", 30, 3)

	// Claim a job so we have a claim to ack.
	if _, err := q.Enqueue(map[string]any{"step": "a"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}

	err = s.CommitStepAtom(StepAtom{
		RunID:  "run-1",
		StepID: "a",
		Result: map[string]any{"ok": true},
		Dedupe: &DedupeRecord{WorkflowID: "wf", StepName: "a", Key: "k", Succeeded: true, Result: map[string]any{"ok": true}},
		Downstream: []Downstream{
			{Queue: q, Payload: map[string]any{"step": "b"}},
		},
		Job: job,
	})
	if err != nil {
		t.Fatalf("CommitStepAtom: %v", err)
	}

	// Dedupe record persisted and succeeded.
	out, err := s.LookupDedupe("wf", "a", "k")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if out == nil || !out.Succeeded {
		t.Fatalf("expected a succeeded dedupe record, got %+v", out)
	}

	// Downstream enqueued and claimable.
	down, err := q.ClaimOne("worker-1")
	if err != nil || down == nil {
		t.Fatalf("downstream not claimable: %v", err)
	}
	var dp map[string]any
	_ = down.UnmarshalPayload(&dp)
	if dp["step"] != "b" {
		t.Fatalf("downstream payload = %v, want step b", dp)
	}

	// The original job must be acked (not claimable again).
	again, err := q.ClaimOne("worker-1")
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if again != nil {
		t.Fatalf("acked job reclaimed (id %d)", again.ID)
	}
}

func TestSingerStateCommitsWithResultAtom(t *testing.T) {
	s := openStore(t)

	// No state recorded yet.
	if v, err := s.GetSingerState("wf", "tap"); err != nil || v != nil {
		t.Fatalf("initial state = %v (err %v), want nil", v, err)
	}

	// Commit a step result with its singer bookmark in one atom.
	err := s.CommitStepAtom(StepAtom{
		RunID: "run-1", StepID: "t",
		Result:      map[string]any{"records": []any{}},
		SingerState: &SingerState{WorkflowID: "wf", StepName: "tap", State: map[string]any{"bookmark": "x"}},
	})
	if err != nil {
		t.Fatalf("CommitStepAtom: %v", err)
	}

	got, err := s.GetSingerState("wf", "tap")
	if err != nil {
		t.Fatalf("GetSingerState: %v", err)
	}
	bm, ok := got.(map[string]any)
	if !ok || bm["bookmark"] != "x" {
		t.Fatalf("bookmark = %#v, want {bookmark:x}", got)
	}
}

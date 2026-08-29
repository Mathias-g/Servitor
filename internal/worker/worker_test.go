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
	"github.com/Mathias-g/Servitor/internal/secret"
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
	return newWorkerWithResolver(t, visS, maxAttempts, secret.ResolverFromMap(secrets))
}

func newWorkerWithResolver(t *testing.T, visS, maxAttempts int, resolver *secret.Resolver) (*Worker, *honker.Store, *honker.Queue) {
	t.Helper()
	ext := extPath(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := honker.Open(dbPath, ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	q := store.Queue("nodes", visS, maxAttempts)
	w := New(store, q, "worker-1", Config{Resolver: resolver})
	return w, store, q
}

// flakyProvider fails the first Resolve with failFirst, then succeeds, for
// testing transient source failures.
type flakyProvider struct {
	failFirst error
	calls     int
}

func (p *flakyProvider) Resolve(_ context.Context, _, name string) (string, error) {
	p.calls++
	if p.calls == 1 && p.failFirst != nil {
		return "", p.failFirst
	}
	return name + "-value", nil
}

// failProvider always returns err; it is a test helper for a provider whose
// secret is permanently stale or missing.
type failProvider struct{ err error }

func (p failProvider) Resolve(context.Context, string, string) (string, error) {
	return "", p.err
}

func shellCmd(script string) []string { return []string{"/bin/sh", "-c", script} }

// claimResultJSON returns the decoded node result for a run/node, decoding
// "" as nil.
func claimResultJSON(t *testing.T, store *honker.Store, runID, nodeID string) any {
	t.Helper()
	raw, err := store.ResultJSON(runID, nodeID)
	if err != nil {
		t.Fatalf("result %s/%s: %v", runID, nodeID, err)
	}
	if raw == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("decode result %s/%s: %v", runID, nodeID, err)
	}
	return v
}

func TestExecuteShellNodeCommitsFanOut(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)

	id, err := q.Enqueue(NodeJob{
		RunID:      "run-1",
		WorkflowID: "wf",
		NodeID:     "a",
		NodeName:   "a",
		NodeType:   "shell",
		Input:      map[string]any{"x": 1},
		Command:    shellCmd(`printf '{"ok":true}'`),
		Downstream: []NodeJob{{
			RunID: "run-1", WorkflowID: "wf", NodeID: "b", NodeName: "b",
			NodeType: "shell", Command: shellCmd(`printf '{"ok":true}'`),
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
		t.Fatal("no result written for node a")
	}

	// The parent claim was acked (id 1); the next claimable job is the
	// downstream ((id 2, node b)). If the parent were still claimable, FIFO
	// would return it first.
	down, err := q.ClaimOne("worker-1")
	if err != nil || down == nil {
		t.Fatalf("downstream not claimable: %v", err)
	}
	if down.ID == id {
		t.Fatalf("parent job %d was not acked; it was re-claimed", id)
	}
	var d NodeJob
	if err := down.UnmarshalPayload(&d); err != nil {
		t.Fatalf("downstream payload: %v", err)
	}
	if d.NodeID != "b" {
		t.Fatalf("downstream node = %q, want b", d.NodeID)
	}
}

func TestDedupeSkipReturnsPriorResult(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)

	// Seed a prior successful dedupe record.
	err := store.CommitNodeAtom(honker.NodeAtom{
		RunID: "run-1", NodeID: "a",
		Result: map[string]any{"ok": true, "from": "prior"},
		Dedupe: &honker.DedupeRecord{WorkflowID: "wf", NodeName: "a", Key: "k1", Succeeded: true, Result: map[string]any{"ok": true, "from": "prior"}},
	})
	if err != nil {
		t.Fatalf("seed dedupe: %v", err)
	}

	// A node with the same key; its subprocess would produce a different value.
	if _, err := q.Enqueue(NodeJob{
		RunID: "run-2", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", DedupeKey: "\"k1\"",
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
		t.Fatalf("result = %v, want prior result (node skipped)", res)
	}

	// Job acked despite the skip.
	if again, _ := q.ClaimOne("worker-1"); again != nil {
		t.Fatalf("job reclaimed after dedupe skip")
	}
}

func TestDedupeProceedsOnPriorFailure(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)

	err := store.CommitNodeAtom(honker.NodeAtom{
		RunID: "run-1", NodeID: "a",
		Result: map[string]any{"ok": false, "error": "boom"},
		Dedupe: &honker.DedupeRecord{WorkflowID: "wf", NodeName: "a", Key: "k1", Succeeded: false, Result: map[string]any{"ok": false}},
	})
	if err != nil {
		t.Fatalf("seed failed dedupe: %v", err)
	}

	if _, err := q.Enqueue(NodeJob{
		RunID: "run-2", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", DedupeKey: "\"k1\"",
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

func TestNodeFailureRetriesAndRecordsFailure(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)

	if _, err := q.Enqueue(NodeJob{
		RunID: "run-1", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", DedupeKey: "\"k1\"",
		Command: shellCmd(`echo boom >&2; exit 1`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err == nil {
		t.Fatal("expected handle to return an error for a failing node")
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
		t.Fatalf("failed node should be re-issued for retry, got %v", err)
	}
}

func TestEnvFilteringToDeclaredSecrets(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, map[string]string{"TOKEN": "abc", "LEAK": "bad"})

	// The script reports whether TOKEN is set and non-empty, and echoes LEAK's
	// fallback. It does not echo TOKEN's value back: node output redaction
	// (SPEC: Varlock) scrubs any granted secret value from stdout, so the exact
	// value can never be observed through the result. What this test pins is
	// that the declared secret reaches the subprocess env and the undeclared
	// one does not.
	script := `printf '{"TOKEN_SET":"%s","LEAK":"%s"}' "${TOKEN:+yes}" "${LEAK:-unset}"`
	if _, err := q.Enqueue(NodeJob{
		RunID: "run-1", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", Secrets: []string{"TOKEN"},
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
	if res["TOKEN_SET"] != "yes" {
		t.Fatalf("TOKEN_SET = %v, want yes (declared secret must reach the subprocess)", res["TOKEN_SET"])
	}
	if res["LEAK"] != "unset" {
		t.Fatalf("LEAK = %v, want unset (undeclared secret must not reach the subprocess)", res["LEAK"])
	}
}

func TestMissingDeclaredSecretFailsFast(t *testing.T) {
	// "NOPE" is declared (source "test") but the provider has no value, so it
	// is missing, not undeclared.
	reg := secret.NewRegistry()
	reg.Register("test", secret.MapProvider{})
	resolver := secret.NewResolver(reg, map[string]string{"NOPE": "test"})
	w, store, q := newWorkerWithResolver(t, 30, 3, resolver)
	if _, err := q.Enqueue(NodeJob{
		RunID: "run-1", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", Secrets: []string{"NOPE"},
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

	// A missing secret is not transient, so it fails fast (no retry): the result
	// carries the missing_secret code and the claim is dead-lettered, not
	// re-issued (SPEC: Secret invalidity and rotation).
	res := claimResultJSON(t, store, "run-1", "a").(map[string]any)
	if res["code"] != "missing_secret" {
		t.Fatalf("result code = %v, want missing_secret", res["code"])
	}
	if again, err := q.ClaimOne("worker-1"); err == nil && again != nil {
		t.Fatal("a missing secret must fail fast, not be re-issued for retry")
	}
}

func TestUnreachableSourceRetriesThenSucceeds(t *testing.T) {
	// A provider that returns ErrSourceUnreachable on the first resolve and
	// succeeds on the second, simulating a source that comes back.
	prov := &flakyProvider{failFirst: secret.ErrSourceUnreachable}
	reg := secret.NewRegistry()
	reg.Register("flaky", prov)
	resolver := secret.NewResolver(reg, map[string]string{"S": "flaky"})
	w, store, q := newWorkerWithResolver(t, 30, 3, resolver)

	if _, err := q.Enqueue(NodeJob{
		RunID: "run-1", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", Secrets: []string{"S"},
		Command: shellCmd(`printf '{"ok":true}'`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// First attempt: the source is unreachable, so it must be retried (with
	// backoff), not failed fast.
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err == nil {
		t.Fatal("expected an error while the source is unreachable")
	}
	// The retry is scheduled with a 1s backoff; wait past it so the claim is
	// re-issued, then run the second attempt where the source is back.
	time.Sleep(2200 * time.Millisecond)
	job2, err := q.ClaimOne("worker-1")
	if err != nil || job2 == nil {
		t.Fatalf("unreachable source must be re-issued for retry, got %v", err)
	}
	if err := w.handle(context.Background(), job2); err != nil {
		t.Fatalf("handle: %v", err)
	}
	res := claimResultJSON(t, store, "run-1", "a").(map[string]any)
	if res["ok"] != true {
		t.Fatalf("result = %v, want ok:true after the source came back", res)
	}
	if prov.calls != 2 {
		t.Fatalf("provider consulted %d times, want 2", prov.calls)
	}
}

func TestStaleSecretFailsWithAuthErrorWhenExhausted(t *testing.T) {
	// A provider whose value is stale: it must be retried with a fresh resolve,
	// and once the secret retry count is exhausted it fails with
	// secret_auth_failed (SPEC: Secret invalidity and rotation).
	reg := secret.NewRegistry()
	reg.Register("stale", failProvider{err: secret.ErrStale})
	resolver := secret.NewResolver(reg, map[string]string{"S": "stale"})
	w, store, q := newWorkerWithResolver(t, 30, 3, resolver)
	w.secretRetries = 1

	if _, err := q.Enqueue(NodeJob{
		RunID: "run-1", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", Secrets: []string{"S"},
		Command: shellCmd(`printf '{"ok":true}'`),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Attempt 1: stale, so retry (attempts 1 < secretRetries 1? no -> 1 >= 1, so
	// fail). With secretRetries=1 the first failure already fails.
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	_ = w.handle(context.Background(), job)
	res := claimResultJSON(t, store, "run-1", "a").(map[string]any)
	if res["code"] != "secret_auth_failed" {
		t.Fatalf("result code = %v, want secret_auth_failed", res["code"])
	}
}

func TestVisibilityTimeoutReclaimsUnackedClaim(t *testing.T) {
	// A short visibility timeout so a worker that "crashes" mid-job loses its
	// claim and the job is re-issued to another worker (SPEC: Execution model
	// step 9).
	_, _, q := newWorker(t, 1, 3, nil)

	if _, err := q.Enqueue(NodeJob{
		RunID: "run-1", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", Command: shellCmd(`printf '{"ok":true}'`),
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

	if _, err := q.Enqueue(NodeJob{
		RunID: "run-cancel", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", Command: shellCmd(`printf '{"ok":true}'`),
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

	// The node must not have run: no result written, and the job acked.
	if res := claimResultJSON(t, store, "run-cancel", "a"); res != nil {
		t.Fatalf("cancelled run wrote a result: %v", res)
	}
	if again, _ := q.ClaimOne("worker-1"); again != nil {
		t.Fatalf("cancelled job not acked (id %d)", again.ID)
	}
}

func TestRunMarkedCompletedAfterLastNode(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)
	if err := store.CreateRun("run-done", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	// A single-node run: the only node has no downstream, so completing it
	// marks the run completed.
	if _, err := q.Enqueue(NodeJob{
		RunID: "run-done", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", Command: shellCmd(`printf '{"ok":true}'`),
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

func TestEmailPollHandsEmailsToCallback(t *testing.T) {
	w, _, q := newWorker(t, 30, 3, map[string]string{"PASS": "pw"})
	var gotKind string
	var got []any
	w.onPoll = func(wf, kind string, items []any) { gotKind = kind; got = items }
	w.runner = stubRunner{out: []any{
		map[string]any{"from": "a@b.com", "to": []any{"x@y.com"}, "subject": "Hi", "body": "yo"},
	}}

	if _, err := q.Enqueue(NodeJob{
		RunID: "poll", WorkflowID: "wf", NodeID: "poll", NodeName: "poll",
		NodeType: "poll", Command: []string{"ignored"},
		Secrets: []string{"PASS"},
		Config:  map[string]any{"kind": "email"},
		Input:   map[string]any{"host": "imap.example.com", "username": "u", "secret": "PASS"},
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
	if gotKind != "email" {
		t.Fatalf("kind = %q, want email", gotKind)
	}
	if len(got) != 1 {
		t.Fatalf("callback got %d items, want 1", len(got))
	}
	item, _ := got[0].(map[string]any)
	if item["subject"] != "Hi" {
		t.Fatalf("item = %v, want subject Hi", got[0])
	}
}

func TestOnRunCompleteFiresWhenRunFinishes(t *testing.T) {
	var gotWorkflow, gotRun string
	ext := extPath(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := honker.Open(dbPath, ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	q := store.Queue("nodes", 30, 3)
	w := New(store, q, "worker-1", Config{
		OnRunComplete: func(workflowID, runID string) {
			gotWorkflow = workflowID
			gotRun = runID
		},
	})
	if err := store.CreateRun("run-1", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := q.Enqueue(NodeJob{
		RunID: "run-1", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", Command: shellCmd(`printf '{"ok":true}'`),
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
	if gotWorkflow != "wf" || gotRun != "run-1" {
		t.Fatalf("OnRunComplete = (%q, %q), want (wf, run-1)", gotWorkflow, gotRun)
	}
}

func TestOnRunCompleteNotFiredForIncompleteRun(t *testing.T) {
	called := false
	ext := extPath(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := honker.Open(dbPath, ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	q := store.Queue("nodes", 30, 3)
	w := New(store, q, "worker-1", Config{OnRunComplete: func(_, _ string) { called = true }})
	if err := store.CreateRun("run-1", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	// Two nodes: a -> b. Running a leaves b pending, so the run is not complete.
	b := NodeJob{RunID: "run-1", WorkflowID: "wf", NodeID: "b", NodeName: "b",
		NodeType: "shell", Command: shellCmd(`true`)}
	a := NodeJob{RunID: "run-1", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", Command: shellCmd(`printf '{"ok":true}'`), Downstream: []NodeJob{b}}
	if _, err := q.Enqueue(a); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	job, err := q.ClaimOne("worker-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := w.handle(context.Background(), job); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if called {
		t.Fatal("OnRunComplete fired before the run finished")
	}
}

func TestSingerTapPersistsBookmarkAcrossRuns(t *testing.T) {
	out := filepath.Join(t.TempDir(), "inv.json")
	w, store, q := newWorker(t, 30, 3, map[string]string{"OUT": out})

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
		if _, err := q.Enqueue(NodeJob{
			RunID: runID, WorkflowID: "wf", NodeID: "t", NodeName: "t",
			NodeType: "singer-tap",
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

	// Second run of the same workflow/node: the prior bookmark must have been
	// passed as a --state file (visible in $OUT).
	runTap("run-2")
	b, err := os.ReadFile(out)
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

func TestMCPNodeDispatchesAndMapsError(t *testing.T) {
	ext := extPath(t)
	store, err := honker.Open(filepath.Join(t.TempDir(), "test.db"), ext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	q := store.Queue("nodes", 30, 3)
	w := New(store, q, "worker-1", Config{MCP: stubMCP{
		result: mcp.CallResult{IsError: true, Content: "boom"},
	}})

	if _, err := q.Enqueue(NodeJob{
		RunID: "run-mcp", WorkflowID: "wf", NodeID: "m", NodeName: "m",
		NodeType: "mcp-call",
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

// stubRunner is a NodeRunner that returns a fixed result without spawning a
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
	if _, err := q.Enqueue(NodeJob{
		RunID: "run-1", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", Input: headInput,
		Command: []string{"ignored"},
		Downstream: []NodeJob{{
			RunID: "run-1", WorkflowID: "wf", NodeID: "b", NodeName: "b",
			NodeType: "shell", Command: []string{"ignored"},
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
	var d NodeJob
	if err := down.UnmarshalPayload(&d); err != nil {
		t.Fatalf("downstream payload: %v", err)
	}
	// The downstream's input must carry the event and node a's result under its name.
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
	err := store.CommitNodeAtom(honker.NodeAtom{
		RunID: "run-1", NodeID: "a",
		Result: map[string]any{"ok": true, "from": "prior"},
		Dedupe: &honker.DedupeRecord{WorkflowID: "wf", NodeName: "a", Key: "e-7", Succeeded: true, Result: map[string]any{"ok": true, "from": "prior"}},
	})
	if err != nil {
		t.Fatalf("seed dedupe: %v", err)
	}

	// The node's dedupe_key is the expression event.id, evaluated against input.
	if _, err := q.Enqueue(NodeJob{
		RunID: "run-2", WorkflowID: "wf", NodeID: "a", NodeName: "a",
		NodeType: "shell", DedupeKey: "event.id",
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

// switchStubRunner is a NodeRunner that answers a switch node with a fixed
// chosen branch and returns a fixed result for other nodes, without spawning
// subprocesses.
type switchStubRunner struct {
	chosen string
	ran    map[string]bool
}

func (s *switchStubRunner) Run(_ context.Context, req exec.Request) (exec.Result, error) {
	// Only the switch node returns the branch name; body nodes return a map.
	for _, c := range req.Command {
		if c == "__switch" {
			return exec.Result{Output: s.chosen}, nil
		}
	}
	return exec.Result{Output: map[string]any{"ok": true}}, nil
}

// TestSwitchRoutesChosenBranchAndSkipsOthers pins switch routing (ADR-0022):
// the chosen branch runs, the skipped branch is recorded skipped, and a rejoin
// node depending on both runs once all deps are satisfied (ADR-0023).
func TestSwitchRoutesChosenBranchAndSkipsOthers(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)
	stub := &switchStubRunner{chosen: "notify_finance", ran: map[string]bool{}}
	w.runner = stub

	// Build the switch workflow as a NodeJob tree: route -> [notify_finance,
	// log_and_done] -> record.
	record := NodeJob{RunID: "run-1", WorkflowID: "wf", NodeID: "record", NodeName: "record", NodeType: "shell", Command: []string{"ignored"}}
	nf := NodeJob{RunID: "run-1", WorkflowID: "wf", NodeID: "notify_finance", NodeName: "notify_finance", NodeType: "shell", Command: []string{"ignored"},
		Dependents: []string{"record"}, Downstream: []NodeJob{record}}
	ld := NodeJob{RunID: "run-1", WorkflowID: "wf", NodeID: "log_and_done", NodeName: "log_and_done", NodeType: "shell", Command: []string{"ignored"},
		Dependents: []string{"record"}, Downstream: []NodeJob{record}}
	route := NodeJob{RunID: "run-1", WorkflowID: "wf", NodeID: "route", NodeName: "route", NodeType: "switch",
		Config:     map[string]any{"cases": map[string]any{"high": "notify_finance", "low": "log_and_done"}},
		Command:    []string{"servitor", "__switch", "steps.check"},
		Dependents: []string{"notify_finance", "log_and_done"},
		Downstream: []NodeJob{nf, ld},
	}

	// Init run deps: route depends on nothing; nf, ld depend on route; record
	// depends on both nf and ld.
	if err := store.CreateRun("run-1", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := store.InitRunDeps(honker.NewRunDeps("run-1", map[string]int{
		"route": 0, "notify_finance": 1, "log_and_done": 1, "record": 2,
	}, []string{"route", "notify_finance", "log_and_done", "record"})); err != nil {
		t.Fatalf("InitRunDeps: %v", err)
	}

	if _, err := q.Enqueue(route); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Process jobs until the run completes.
	runLoop(t, w, q)

	if st, _ := store.RunStatus("run-1"); st != honker.RunCompleted {
		t.Fatalf("run status = %q, want completed", st)
	}
	// notify_finance ran; log_and_done skipped.
	nfRes := claimResultJSON(t, store, "run-1", "notify_finance").(map[string]any)
	if _, ok := nfRes["ok"]; !ok {
		t.Fatalf("notify_finance result = %v, want ran (not skipped)", nfRes)
	}
	ldRes := claimResultJSON(t, store, "run-1", "log_and_done").(map[string]any)
	if ldRes["skipped"] != true {
		t.Fatalf("log_and_done result = %v, want skipped", ldRes)
	}
	// record (rejoin) ran.
	recRes := claimResultJSON(t, store, "run-1", "record")
	if recRes == nil {
		t.Fatalf("record (rejoin) did not run")
	}
}

// runLoop claims and handles jobs until none remain for the worker.
func runLoop(t *testing.T, w *Worker, q *honker.Queue) {
	t.Helper()
	for {
		job, err := q.ClaimOne("worker-1")
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if job == nil {
			return
		}
		if err := w.handle(context.Background(), job); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}
}

// foreachStubRunner answers a foreach node with the list and each body
// iteration with a result derived from its `item` input.
type foreachStubRunner struct{}

func (foreachStubRunner) Run(_ context.Context, req exec.Request) (exec.Result, error) {
	in, _ := req.Input.(map[string]any)
	// A body iteration has an `item` key; return a result derived from it.
	if _, isBody := in["item"]; isBody {
		return exec.Result{Output: map[string]any{"item": in["item"]}}, nil
	}
	// A rejoin node has the collected array under the foreach node's name in its
	// input; return it so the test can verify the collect.
	if steps, ok := in["steps"].(map[string]any); ok {
		if fan, ok := steps["fan"]; ok {
			return exec.Result{Output: fan}, nil
		}
	}
	// The foreach node itself returns the iteration list.
	return exec.Result{Output: []any{"a", "b", "c"}}, nil
}

// TestForeachFansOutAndCollectsAtRejoin pins foreach (ADR-0024): the body runs
// once per element, results collect into an array under the foreach node's name
// at the rejoin, in input order.
func TestForeachFansOutAndCollectsAtRejoin(t *testing.T) {
	w, store, q := newWorker(t, 30, 3, nil)
	w.runner = foreachStubRunner{}

	// foreach fan -> body process_one (fanned N times) -> rejoin summarize.
	summarize := NodeJob{RunID: "run-1", WorkflowID: "wf", NodeID: "summarize", NodeName: "summarize",
		NodeType: "transform", Command: []string{"ignored"},
		CollectFrom: "process_one", CollectAs: "item", CollectCount: 3, CollectName: "fan"}
	processOne := NodeJob{RunID: "run-1", WorkflowID: "wf", NodeID: "process_one", NodeName: "process_one",
		NodeType: "shell", Command: []string{"ignored"},
		Dependents: []string{"summarize"}, Downstream: []NodeJob{summarize}}
	fan := NodeJob{RunID: "run-1", WorkflowID: "wf", NodeID: "fan", NodeName: "fan",
		NodeType: "foreach", Command: []string{"servitor", "__foreach", "steps.ids"},
		Body: &processOne, BodyAs: "item", Rejoins: []string{"summarize"},
		Downstream: []NodeJob{summarize}}

	if err := store.CreateRun("run-1", "wf"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := store.InitRunDeps(honker.NewRunDeps("run-1", map[string]int{
		"fan": 0, "process_one": 1, "summarize": 1,
	}, []string{"fan", "process_one", "summarize"})); err != nil {
		t.Fatalf("InitRunDeps: %v", err)
	}

	if _, err := q.Enqueue(fan); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	runLoop(t, w, q)

	if st, _ := store.RunStatus("run-1"); st != honker.RunCompleted {
		t.Fatalf("run status = %q, want completed", st)
	}
	// The rejoin's result should be an array of {item: a/b/c}.
	res := claimResultJSON(t, store, "run-1", "summarize")
	arr, ok := res.([]any)
	if !ok {
		t.Fatalf("summarize result = %#v, want array", res)
	}
	if len(arr) != 3 {
		t.Fatalf("collected array len = %d, want 3", len(arr))
	}
	want := []string{"a", "b", "c"}
	for i, v := range arr {
		m, _ := v.(map[string]any)
		if m["item"] != want[i] {
			t.Fatalf("collected[%d] = %v, want item %s (in input order)", i, v, want[i])
		}
	}
}

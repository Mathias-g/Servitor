package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/worker"
)

func daemonExtPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("HONKER_EXTENSION_PATH")
	if p == "" {
		t.Skip("HONKER_EXTENSION_PATH not set; skipping daemon runner integration test")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("HONKER_EXTENSION_PATH %s not readable: %v", p, err)
	}
	return p
}

// TestDaemonRunsQueuedJob boots the daemon with a store and a worker, enqueues
// a shell step through a second handle to the same file, and verifies the
// daemon's worker executes it and writes the result. This exercises the wiring
// in Run (worker loop + store ownership) end to end.
func TestDaemonRunsQueuedJob(t *testing.T) {
	ext := daemonExtPath(t)
	dbPath := filepath.Join(t.TempDir(), "d.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Addr:         "127.0.0.1:0",
			DBPath:       dbPath,
			ExtPath:      ext,
			Workers:      1,
			DrainTimeout: 2 * time.Second,
			Started:      func(a string) { started <- a },
		})
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not start")
	}

	// Second handle to the same file, used only to enqueue from the test.
	store, err := honker.Open(dbPath, ext)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	q := store.Queue("steps", 30, 3)
	if _, err := q.Enqueue(worker.StepJob{
		RunID: "r", WorkflowID: "wf", StepID: "a", StepName: "a",
		StepType: "shell", Command: []string{"/bin/sh", "-c", `printf '{"ok":true}'`},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		res, err := store.ResultJSON("r", "a")
		if err != nil {
			t.Fatalf("result: %v", err)
		}
		if res != "" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("daemon worker did not execute the queued step in time")
		case <-time.After(20 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon shutdown error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down")
	}
}

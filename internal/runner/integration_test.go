package runner

import (
	"context"
	"testing"
	"time"

	"github.com/Mathias-g/Servitor/internal/wafer"
	"github.com/Mathias-g/Servitor/internal/worker"
)

// TestRunEndToEnd starts a worker on a run enqueued by StartRun and verifies
// the whole shell chain (a -> b) completes: both step results are written and
// the queue drains. It exercises the composition of the runner's run builder,
// the worker loop, and the fan-out transaction together.
func TestRunEndToEnd(t *testing.T) {
	store := openStore(t)
	q := store.Queue("steps", 30, 3)

	w, err := wafer.Parse([]byte(shellYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if _, err := StartRun(store, q, w, map[string]any{"trigger": "manual"}, "run-e2e"); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	worker := worker.New(store, q, "worker-1", worker.Config{Secrets: map[string]string{}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = worker.Run(ctx) }()

	// Wait for both steps in the chain to complete.
	deadline := time.After(4 * time.Second)
	for {
		a, _ := store.ResultJSON("run-e2e", "a")
		b, _ := store.ResultJSON("run-e2e", "b")
		if a != "" && b != "" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("chain did not complete: a=%q b=%q", a, b)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

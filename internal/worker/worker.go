// Package worker implements the step-execution worker loop (SPEC: Execution
// model). A worker claims a job, checks the step's dedupe_key, runs the step
// as a subprocess with a filtered environment, and commits the fan-out
// transaction ({result, dedupe, downstream, claim_ack}) through
// honker.CommitStepAtom.
//
// The worker is deliberately single-threaded per instance; worker concurrency
// limits are an open SPEC question, and BSSN says one loop now. Honker's
// visibility timeout and attempt counting provide crash safety: a claim a
// worker dies holding expires and is re-issued, and a step that keeps failing
// is dead-lettered. Whether a re-run is side-effect-safe is the dedupe_key
// contract, enforced here.
package worker

import (
	"context"
	"fmt"
	"strings"

	"github.com/Mathias-g/Servitor/internal/exec"
	"github.com/Mathias-g/Servitor/internal/honker"
)

// StepJob is the payload of one queued step execution. It is fully
// self-contained: it carries the step's definition and its own downstream
// steps, so the worker needs no separate workflow registry to run it. The
// initial step(s) of a run are enqueued by the trigger path; each step's
// successors ride in its Downstream field and are enqueued on completion.
type StepJob struct {
	// RunID identifies the workflow run this step belongs to.
	RunID string
	// WorkflowID identifies the workflow the run was created from.
	WorkflowID string
	// StepID is the step's effective identifier (its name or list position).
	StepID string
	// StepName is the step's `name` field from the Wafer.
	StepName string
	// StepType is the step type (for example `shell`).
	StepType string
	// Config is the step's type-specific config.
	Config map[string]any
	// Input is the step's input: the trigger event plus prior step results.
	Input map[string]any
	// DedupeKey is the resolved value of the step's dedupe_key expression. It
	// is evaluated at enqueue time; the expression language is an open SPEC
	// question, so the caller supplies the resolved value. Empty means the
	// step has no dedupe contract.
	DedupeKey string
	// Command is the argv to run for this step.
	Command []string
	// Secrets are the names of the secrets this step declares. The subprocess
	// environment is filtered to exactly these.
	Secrets []string
	// Downstream are the steps to enqueue when this step completes.
	Downstream []StepJob
}

// StepRunner runs a step subprocess. It is an interface so tests can stub it;
// the production value is subprocessRunner.
type StepRunner interface {
	Run(ctx context.Context, req exec.Request) (exec.Result, error)
}

// subprocessRunner runs steps as real OS subprocesses (ADR-0008).
type subprocessRunner struct{}

func (subprocessRunner) Run(ctx context.Context, req exec.Request) (exec.Result, error) {
	return exec.Run(ctx, req)
}

// Config controls a Worker.
type Config struct {
	// Secrets are the runner's resolved secrets (name to value). Only the
	// secrets a step declares are passed to its subprocess. In later phases
	// this comes from varlock; for now the caller supplies the resolved map.
	Secrets map[string]string
	// Runner runs step subprocesses. Defaults to real subprocesses.
	Runner StepRunner
}

// Worker claims and executes jobs from a queue, committing each step's
// completion atomically through the honker store.
type Worker struct {
	store    *honker.Store
	queue    *honker.Queue
	workerID string
	secrets  map[string]string
	runner   StepRunner
}

// New builds a worker over the store's queue. workerID is used for Honker
// claim ownership and for the claim/ack contract.
func New(store *honker.Store, queue *honker.Queue, workerID string, cfg Config) *Worker {
	if cfg.Runner == nil {
		cfg.Runner = subprocessRunner{}
	}
	if cfg.Secrets == nil {
		cfg.Secrets = map[string]string{}
	}
	return &Worker{
		store:    store,
		queue:    queue,
		workerID: workerID,
		secrets:  cfg.Secrets,
		runner:   cfg.Runner,
	}
}

// Run is the blocking worker loop. It claims jobs as they become available,
// executes each, and stops when ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	waker := w.queue.ClaimWaker()
	defer waker.Close()
	for {
		job, err := waker.Next(ctx, w.workerID)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if job == nil {
			return nil
		}
		_ = w.handle(ctx, job)
	}
}

// handle processes one claimed job. It returns an error for diagnostics; step
// failures are recorded (failed result and dedupe record) and the claim
// retried inside, so the loop keeps going.
func (w *Worker) handle(ctx context.Context, claimed *honker.Job) error {
	var sj StepJob
	if err := claimed.UnmarshalPayload(&sj); err != nil {
		_, _ = claimed.Fail("invalid step job payload: " + err.Error())
		return fmt.Errorf("worker: decode job %d: %w", claimed.ID, err)
	}

	result, ran, err := w.runStep(ctx, sj)
	if err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: step %s (job %d): %w", sj.StepID, claimed.ID, err)
	}

	atom := honker.StepAtom{
		RunID:  sj.RunID,
		StepID: sj.StepID,
		Result: result,
		Job:    claimed,
	}
	if sj.DedupeKey != "" && ran {
		atom.Dedupe = &honker.DedupeRecord{
			WorkflowID: sj.WorkflowID,
			StepName:   sj.StepName,
			Key:        sj.DedupeKey,
			Succeeded:  true,
			Result:     result,
		}
	}
	for _, d := range sj.Downstream {
		atom.Downstream = append(atom.Downstream, honker.Downstream{Queue: w.queue, Payload: d})
	}
	if err := w.store.CommitStepAtom(atom); err != nil {
		return fmt.Errorf("worker: commit step %s (job %d): %w", sj.StepID, claimed.ID, err)
	}
	return nil
}

// runStep runs a single step to produce its result. It returns ran=false when
// the step is skipped by a prior successful dedupe record, in which case
// result is the prior result and no subprocess runs.
func (w *Worker) runStep(ctx context.Context, sj StepJob) (result any, ran bool, err error) {
	if sj.DedupeKey != "" {
		out, lerr := w.store.LookupDedupe(sj.WorkflowID, sj.StepName, sj.DedupeKey)
		if lerr != nil {
			return nil, false, lerr
		}
		// Skip on a prior success; proceed on a prior failure or a first run.
		if out != nil && out.Succeeded {
			return out.Result, false, nil
		}
	}

	if len(sj.Command) == 0 {
		return nil, true, fmt.Errorf("step type %q has no command to run", sj.StepType)
	}

	env, missing := exec.FilteredEnv(w.secrets, sj.Secrets)
	if len(missing) > 0 {
		return nil, true, fmt.Errorf("step declares secrets the runner does not have: %s", strings.Join(missing, ", "))
	}

	res, rerr := w.runner.Run(ctx, exec.Request{Command: sj.Command, Env: env, Input: sj.Input})
	if rerr != nil {
		return nil, true, rerr
	}
	return res.Output, true, nil
}

// recordFailure persists a step's failure: a failed result row and, when the
// step has a dedupe key, a failed dedupe record so a later retry proceeds
// rather than being skipped (SPEC: Idempotency). It then retries the claim,
// which Honker dead-letters once attempts reach the queue's max.
func (w *Worker) recordFailure(sj StepJob, claimed *honker.Job, cause error) {
	result := map[string]any{"ok": false, "error": cause.Error()}
	atom := honker.StepAtom{
		RunID:  sj.RunID,
		StepID: sj.StepID,
		Result: result,
		// No Job and no Downstream: the claim is not acked and successors do
		// not run, so the step is re-issued on retry/visibility timeout.
	}
	if sj.DedupeKey != "" {
		atom.Dedupe = &honker.DedupeRecord{
			WorkflowID: sj.WorkflowID,
			StepName:   sj.StepName,
			Key:        sj.DedupeKey,
			Succeeded:  false,
			Result:     result,
		}
	}
	_ = w.store.CommitStepAtom(atom)
	if claimed != nil {
		_, _ = claimed.Retry(0, cause.Error())
	}
}

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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mathias-g/Servitor/internal/exec"
	"github.com/Mathias-g/Servitor/internal/expression"
	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/mcp"
	"github.com/Mathias-g/Servitor/internal/singer"
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
	// DedupeKey is the step's dedupe_key JSONata expression (ADR-0020), evaluated
	// against the step's {event, steps} input at execution time to form the
	// dedupe key. Empty means the step has no dedupe contract.
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

// SingerRunner runs a singer tap or target as a subprocess. It is an interface
// so tests can stub it; the production value is subprocessSingerRunner.
type SingerRunner interface {
	RunTap(ctx context.Context, req singer.TapRequest) (singer.TapResult, error)
	RunTarget(ctx context.Context, req singer.TargetRequest) (singer.TargetResult, error)
}

// subprocessSingerRunner runs singer taps and targets as real OS subprocesses
// (ADR-0008, SPEC: Singer).
type subprocessSingerRunner struct{}

func (subprocessSingerRunner) RunTap(ctx context.Context, req singer.TapRequest) (singer.TapResult, error) {
	return singer.RunTap(ctx, req)
}

func (subprocessSingerRunner) RunTarget(ctx context.Context, req singer.TargetRequest) (singer.TargetResult, error) {
	return singer.RunTarget(ctx, req)
}

// MCPRunner runs an mcp-call step as a subprocess. It is an interface so tests
// can stub it; the production value is subprocessMCPRunner.
type MCPRunner interface {
	Call(ctx context.Context, req mcp.CallRequest) (mcp.CallResult, error)
}

// subprocessMCPRunner runs mcp-call steps as real OS subprocesses (ADR-0008,
// ADR-0015).
type subprocessMCPRunner struct{}

func (subprocessMCPRunner) Call(ctx context.Context, req mcp.CallRequest) (mcp.CallResult, error) {
	return mcp.Call(ctx, req)
}

// Config controls a Worker.
type Config struct {
	// Secrets are the runner's resolved secrets (name to value). Only the
	// secrets a step declares are passed to its subprocess. In later phases
	// this comes from varlock; for now the caller supplies the resolved map.
	Secrets map[string]string
	// Runner runs step subprocesses. Defaults to real subprocesses.
	Runner StepRunner
	// Singer runs singer tap and target subprocesses. Defaults to real
	// subprocesses.
	Singer SingerRunner
	// MCP runs mcp-call subprocesses. Defaults to real subprocesses.
	MCP MCPRunner
}

// Worker claims and executes jobs from a queue, committing each step's
// completion atomically through the honker store.
type Worker struct {
	store    *honker.Store
	queue    *honker.Queue
	workerID string
	secrets  map[string]string
	runner   StepRunner
	singer   SingerRunner
	mcp      MCPRunner
}

// New builds a worker over the store's queue. workerID is used for Honker
// claim ownership and for the claim/ack contract.
func New(store *honker.Store, queue *honker.Queue, workerID string, cfg Config) *Worker {
	if cfg.Runner == nil {
		cfg.Runner = subprocessRunner{}
	}
	if cfg.Singer == nil {
		cfg.Singer = subprocessSingerRunner{}
	}
	if cfg.MCP == nil {
		cfg.MCP = subprocessMCPRunner{}
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
		singer:   cfg.Singer,
		mcp:      cfg.MCP,
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

	// A cancelled (or already finished) run must not continue: ack the job
	// without running it or enqueueing its successors. This is what actually
	// stops a cancelled chain even if a step was already claimed.
	if status, _ := w.store.RunStatus(sj.RunID); status != "" && status != honker.RunRunning {
		_, _ = claimed.Ack()
		return nil
	}

	// Resolve the step's dedupe_key JSONata expression against its {event, steps}
	// input at execution time (ADR-0020, ADR-0021). The resolved key is used for
	// the dedupe lookup and, on completion, for the dedupe record.
	dedupeKey, derr := resolveDedupeKey(sj)
	if derr != nil {
		w.recordFailure(sj, claimed, derr)
		return fmt.Errorf("worker: step %s (job %d): %w", sj.StepID, claimed.ID, derr)
	}

	result, ran, state, err := w.runStep(ctx, sj, dedupeKey)
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
	if state != nil {
		atom.SingerState = state
	}
	if dedupeKey != "" && ran {
		atom.Dedupe = &honker.DedupeRecord{
			WorkflowID: sj.WorkflowID,
			StepName:   sj.StepName,
			Key:        dedupeKey,
			Succeeded:  true,
			Result:     result,
		}
	}
	for i := range sj.Downstream {
		d := sj.Downstream[i]
		// Thread the {event, steps} input forward: this step's result becomes
		// available to each successor under this step's name (ADR-0021).
		d.Input = threadInput(sj.Input, sj.StepName, result)
		atom.Downstream = append(atom.Downstream, honker.Downstream{Queue: w.queue, Payload: d})
	}
	if err := w.store.CommitStepAtom(atom); err != nil {
		return fmt.Errorf("worker: commit step %s (job %d): %w", sj.StepID, claimed.ID, err)
	}

	// The last step in a run's chain (no downstream) marks the run done.
	if len(sj.Downstream) == 0 {
		_ = w.store.SetRunStatus(sj.RunID, honker.RunCompleted)
	}
	return nil
}

// resolveDedupeKey evaluates a step's dedupe_key JSONata expression (ADR-0020)
// against its {event, steps} input (ADR-0021) and stringifies the result to form
// the dedupe key. It returns "" when the step has no dedupe_key. An expression
// that evaluates to nothing is treated as no key.
func resolveDedupeKey(sj StepJob) (string, error) {
	if sj.DedupeKey == "" {
		return "", nil
	}
	out, err := expression.Eval(sj.DedupeKey, sj.Input)
	if err != nil {
		return "", fmt.Errorf("dedupe_key expression: %w", err)
	}
	return dedupeString(out), nil
}

// dedupeString stringifies a JSONata result into a dedupe key. A string result
// is used as-is (matching the common `event.id` / `row-42` form); anything else
// is rendered as its JSON representation so the key is stable and unique.
func dedupeString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

// runStep runs a single step to produce its result. It returns ran=false when
// the step is skipped by a prior successful dedupe record, in which case
// result is the prior result and no subprocess runs. It returns state, non-nil
// only for a completed singer-tap step, whose new bookmark is committed with
// the step's result (SPEC: Execution model step 8).
func (w *Worker) runStep(ctx context.Context, sj StepJob, dedupeKey string) (result any, ran bool, state *honker.SingerState, err error) {
	if dedupeKey != "" {
		out, lerr := w.store.LookupDedupe(sj.WorkflowID, sj.StepName, dedupeKey)
		if lerr != nil {
			return nil, false, nil, lerr
		}
		// Skip on a prior success; proceed on a prior failure or a first run.
		if out != nil && out.Succeeded {
			return out.Result, false, nil, nil
		}
	}

	if sj.StepType == "singer-tap" || sj.StepType == "singer-target" {
		return w.runSingerStep(ctx, sj)
	}

	if sj.StepType == "mcp-call" {
		return w.runMCPStep(ctx, sj)
	}

	if len(sj.Command) == 0 {
		return nil, true, nil, fmt.Errorf("step type %q has no command to run", sj.StepType)
	}

	env, missing := exec.FilteredEnv(w.secrets, sj.Secrets)
	if len(missing) > 0 {
		return nil, true, nil, fmt.Errorf("step declares secrets the runner does not have: %s", strings.Join(missing, ", "))
	}

	res, rerr := w.runner.Run(ctx, exec.Request{Command: sj.Command, Env: env, Input: sj.Input})
	if rerr != nil {
		return nil, true, nil, rerr
	}
	return res.Output, true, nil, nil
}

// runSingerStep runs a singer-tap or singer-target step as a subprocess
// (SPEC: Singer). A tap is fed its config, selected streams, and prior bookmark
// on stdin and returns its records and next bookmark; the bookmark is returned
// as state so it commits with the step's result. A target is fed the records
// from the step's input.
func (w *Worker) runSingerStep(ctx context.Context, sj StepJob) (result any, ran bool, state *honker.SingerState, err error) {
	if len(sj.Command) == 0 {
		return nil, true, nil, fmt.Errorf("step type %q has no command to run", sj.StepType)
	}
	env, missing := exec.FilteredEnv(w.secrets, sj.Secrets)
	if len(missing) > 0 {
		return nil, true, nil, fmt.Errorf("step declares secrets the runner does not have: %s", strings.Join(missing, ", "))
	}

	switch sj.StepType {
	case "singer-tap":
		cfg, _ := sj.Config["config"].(map[string]any)
		prior, gerr := w.store.GetSingerState(sj.WorkflowID, sj.StepName)
		if gerr != nil {
			return nil, true, nil, gerr
		}
		res, rerr := w.singer.RunTap(ctx, singer.TapRequest{
			Command: sj.Command,
			Env:     env,
			Config:  cfg,
			Catalog: sj.Config["catalog"],
			State:   prior,
		})
		if rerr != nil {
			return nil, true, nil, rerr
		}
		return map[string]any{"records": res.Records, "streams": res.Streams, "state": res.State}, true,
			&honker.SingerState{WorkflowID: sj.WorkflowID, StepName: sj.StepName, State: res.State}, nil
	case "singer-target":
		targetCfg, _ := sj.Config["config"].(map[string]any)
		res, rerr := w.singer.RunTarget(ctx, singer.TargetRequest{
			Command: sj.Command,
			Env:     env,
			Config:  targetCfg,
			Records: recordsFromInput(sj.Input),
		})
		if rerr != nil {
			return nil, true, nil, rerr
		}
		return map[string]any{"consumed": res.Consumed, "output": res.Output}, true, nil, nil
	default:
		return nil, true, nil, fmt.Errorf("step type %q is not a singer step", sj.StepType)
	}
}

// runMCPStep runs an mcp-call step as a subprocess (SPEC: MCP integration,
// ADR-0015). It spawns the named server with a filtered secret env, invokes one
// tool, and maps an errored result onto Servitor's structured error format.
func (w *Worker) runMCPStep(ctx context.Context, sj StepJob) (result any, ran bool, state *honker.SingerState, err error) {
	if len(sj.Command) == 0 {
		return nil, true, nil, fmt.Errorf("step type %q has no command to run", sj.StepType)
	}
	env, missing := exec.FilteredEnv(w.secrets, sj.Secrets)
	if len(missing) > 0 {
		return nil, true, nil, fmt.Errorf("step declares secrets the runner does not have: %s", strings.Join(missing, ", "))
	}
	tool, _ := sj.Config["tool"].(string)
	if tool == "" {
		return nil, true, nil, fmt.Errorf("step type mcp-call requires a `tool` name")
	}
	input, _ := sj.Config["input"].(map[string]any)
	mode := mcp.ModeUnknown
	if m, ok := sj.Config["mode"].(string); ok {
		mode = mcp.Mode(m)
	}

	res, rerr := w.mcp.Call(ctx, mcp.CallRequest{
		Server: mcp.ServerRequest{
			Command: sj.Command,
			Env:     env,
			Mode:    mode,
		},
		Tool:  tool,
		Input: input,
	})
	if rerr != nil {
		return nil, true, nil, rerr
	}
	if res.IsError {
		se := mcp.AsStructuredError(tool, res)
		return map[string]any{"ok": false, "path": se.Path, "code": se.Code, "message": se.Message, "suggestion": se.Suggestion}, true, nil, nil
	}
	return map[string]any{"ok": true, "content": res.Content, "data": res.Data}, true, nil, nil
}

// threadInput builds a downstream step's input from a completing step's input
// and its result (ADR-0021). The input is an object with an `event` field (the
// durable trigger payload) and a `steps` field (prior step results keyed by step
// name). This step's result is added under this step's name; the event is passed
// through unchanged.
func threadInput(parentInput map[string]any, stepName string, result any) map[string]any {
	event := parentInput["event"]
	steps, _ := parentInput["steps"].(map[string]any)
	if steps == nil {
		steps = map[string]any{}
	}
	next := make(map[string]any, len(steps)+1)
	for k, v := range steps {
		next[k] = v
	}
	if stepName != "" {
		next[stepName] = result
	}
	return map[string]any{"event": event, "steps": next}
}

// recordsFromInput extracts singer records from a step's `{event, steps}` input
// (ADR-0021). A target is downstream of the tap that produced the records, so
// the records live under the tap step's result, which has the shape
// {records, streams, state}. In the linear chain a target has one
// records-producing predecessor, so scanning the `steps` map for the first
// result with a `records` array is unambiguous for now.
func recordsFromInput(input map[string]any) []singer.Record {
	var out []singer.Record
	if input == nil {
		return out
	}
	steps, _ := input["steps"].(map[string]any)
	for _, v := range steps {
		rm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		raw, ok := rm["records"].([]any)
		if !ok {
			continue
		}
		for _, r := range raw {
			rr, ok := r.(map[string]any)
			if !ok {
				continue
			}
			stream, _ := rr["stream"].(string)
			out = append(out, singer.Record{Stream: stream, Record: rr["record"]})
		}
	}
	return out
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
	// The dedupe key is resolved before the step runs; if it could not be
	// evaluated we have no key, so no failed dedupe record is written. A later
	// retry re-evaluates it.
	if sj.DedupeKey != "" {
		key, err := resolveDedupeKey(sj)
		if err == nil && key != "" {
			atom.Dedupe = &honker.DedupeRecord{
				WorkflowID: sj.WorkflowID,
				StepName:   sj.StepName,
				Key:        key,
				Succeeded:  false,
				Result:     result,
			}
		}
	}
	_ = w.store.CommitStepAtom(atom)
	if claimed != nil {
		_, _ = claimed.Retry(0, cause.Error())
	}
	// A failing final step (no downstream) marks the run failed.
	if len(sj.Downstream) == 0 {
		_ = w.store.SetRunStatus(sj.RunID, honker.RunFailed)
	}
}

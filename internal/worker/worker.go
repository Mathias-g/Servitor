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
	// Skip marks a job that is part of a non-chosen switch branch. It records
	// the step as skipped (ADR-0023) and cascades to its dependents without
	// executing anything.
	Skip bool
	// Dependents are the ids of the steps that depend on this one. When this
	// step completes, the worker decrements each dependent's count and enqueues
	// it (from Downstream, aligned by index) only when its count reaches zero
	// (ADR-0023). Empty means this step feeds nothing.
	Dependents []string
	// Downstream are the jobs for the dependents, aligned by index with
	// Dependents. The worker enqueues a job only for a dependent whose count
	// reached zero.
	Downstream []StepJob
	// Body, when set, marks a foreach scheduler step (ADR-0024): it carries the
	// body step template to fan out once per element.
	Body *StepJob
	// BodyAs is the loop variable name for a foreach body, exposed in each
	// iteration's input.
	BodyAs string
	// Rejoins are the step ids that depend on a foreach body and collect its
	// results. The foreach sets their dependency count to the iteration count.
	Rejoins []string
	// CollectFrom, CollectAs, and CollectCount mark a rejoin step that assembles
	// a foreach body's iteration results into an array under the foreach step's
	// name (ADR-0024). CollectFrom is the body step id, CollectAs is the loop
	// variable name, and CollectCount is the number of iterations. CollectName
	// is the foreach step's id, the key the array is placed under in `steps`.
	CollectFrom  string
	CollectAs    string
	CollectCount int
	CollectName  string
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

	// A skip-job (a non-chosen switch branch) records skipped and cascades
	// without executing anything (ADR-0023).
	if sj.Skip {
		return w.handleSkip(ctx, sj, claimed)
	}

	// A switch step resolves its branch and routes: chosen branch enqueued
	// normally, skipped branches enqueued as skip-jobs (ADR-0022).
	if sj.StepType == "switch" {
		return w.handleSwitch(ctx, sj, claimed)
	}

	// A foreach step resolves its list and fans out the body step once per
	// element, collecting results at the rejoin (ADR-0024).
	if sj.StepType == "foreach" {
		return w.handleForeach(ctx, sj, claimed)
	}

	return w.handleStep(ctx, sj, claimed)
}

// handleStep runs a normal step, commits its atom, and cascades to dependents.
func (w *Worker) handleStep(ctx context.Context, sj StepJob, claimed *honker.Job) error {
	// A rejoin step that collects a foreach body's results assembles the array
	// of iteration results into its input before running (ADR-0024).
	if sj.CollectFrom != "" {
		assembled, aerr := w.assembleForeachInput(sj)
		if aerr != nil {
			w.recordFailure(sj, claimed, aerr)
			return fmt.Errorf("worker: step %s (job %d): %w", sj.StepID, claimed.ID, aerr)
		}
		sj.Input = assembled
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
		RunID:      sj.RunID,
		StepID:     sj.StepID,
		Result:     result,
		Job:        claimed,
		Dependents: sj.Dependents,
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
	return w.checkRunComplete(ctx, sj)
}

// handleSkip records a non-chosen switch branch step as skipped and cascades:
// it decrements its dependents' counts and enqueues its own downstream as
// skip-jobs, so a skipped branch propagates to a rejoin without executing
// anything (ADR-0023).
func (w *Worker) handleSkip(ctx context.Context, sj StepJob, claimed *honker.Job) error {
	atom := honker.StepAtom{
		RunID:      sj.RunID,
		StepID:     sj.StepID,
		Result:     map[string]any{"skipped": true},
		Job:        claimed,
		Dependents: sj.Dependents,
	}
	for i := range sj.Downstream {
		d := sj.Downstream[i]
		d.Skip = true
		d.Input = threadInput(sj.Input, sj.StepName, map[string]any{"skipped": true})
		atom.Downstream = append(atom.Downstream, honker.Downstream{Queue: w.queue, Payload: d})
	}
	if err := w.store.CommitStepAtom(atom); err != nil {
		return fmt.Errorf("worker: commit skip %s (job %d): %w", sj.StepID, claimed.ID, err)
	}
	return w.checkRunComplete(ctx, sj)
}

// handleSwitch runs a switch step, resolves its chosen branch, and routes: the
// chosen branch's dependent is enqueued normally (if ready); each skipped
// branch's dependent is enqueued as a skip-job (ADR-0022, ADR-0023).
func (w *Worker) handleSwitch(ctx context.Context, sj StepJob, claimed *honker.Job) error {
	chosen, err := w.runSwitch(ctx, sj)
	if err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: switch %s (job %d): %w", sj.StepID, claimed.ID, err)
	}

	atom := honker.StepAtom{
		RunID:      sj.RunID,
		StepID:     sj.StepID,
		Result:     map[string]any{"branch": chosen},
		Job:        claimed,
		Dependents: sj.Dependents,
	}
	for i := range sj.Downstream {
		d := sj.Downstream[i]
		d.Input = threadInput(sj.Input, sj.StepName, map[string]any{"branch": chosen})
		if d.StepID == chosen {
			// Chosen branch: enqueue normally (the atom's readiness check gates
			// on the dependent's count).
			atom.Downstream = append(atom.Downstream, honker.Downstream{Queue: w.queue, Payload: d})
		} else {
			// Skipped branch: enqueue a skip-job that cascades without running.
			d.Skip = true
			atom.Downstream = append(atom.Downstream, honker.Downstream{Queue: w.queue, Payload: d})
		}
	}
	if err := w.store.CommitStepAtom(atom); err != nil {
		return fmt.Errorf("worker: commit switch %s (job %d): %w", sj.StepID, claimed.ID, err)
	}
	return w.checkRunComplete(ctx, sj)
}

// runSwitch runs the switch step as a subprocess (ADR-0008) and returns the
// chosen branch's target step name. It passes the step's input and cases/default
// config on stdin.
func (w *Worker) runSwitch(ctx context.Context, sj StepJob) (string, error) {
	if len(sj.Command) == 0 {
		return "", fmt.Errorf("step type switch has no command to run")
	}
	env, missing := exec.FilteredEnv(w.secrets, sj.Secrets)
	if len(missing) > 0 {
		return "", fmt.Errorf("switch declares secrets the runner does not have: %s", strings.Join(missing, ", "))
	}
	cases, _ := sj.Config["cases"].(map[string]any)
	defaultTarget, _ := sj.Config["default"].(string)
	res, rerr := w.runner.Run(ctx, exec.Request{
		Command: sj.Command,
		Env:     env,
		Input: map[string]any{
			"input":   sj.Input,
			"cases":   cases,
			"default": defaultTarget,
		},
	})
	if rerr != nil {
		return "", rerr
	}
	chosen, ok := res.Output.(string)
	if !ok || chosen == "" {
		return "", fmt.Errorf("switch %s returned no branch target", sj.StepID)
	}
	return chosen, nil
}

// checkRunComplete marks a run completed once its pending job count reaches
// zero (ADR-0023). It is called after a step (or skip) commits its atom. The
// pending count is adjusted in the same transaction as the commit, so by the
// time this runs, pending reflects all in-flight work for the run.
func (w *Worker) checkRunComplete(ctx context.Context, sj StepJob) error {
	pending, err := w.store.RunPending(sj.RunID)
	if err != nil {
		return err
	}
	if pending == 0 {
		return w.store.SetRunStatus(sj.RunID, honker.RunCompleted)
	}
	return nil
}

// handleForeach runs a foreach step: it resolves the list, sets each rejoin's
// dependency count to the iteration count N, and enqueues N body-step jobs, one
// per element (ADR-0024). Each body job carries the rejoins as its dependents
// so, when it completes, it decrements each rejoin; a rejoin is enqueued once
// its count reaches zero.
func (w *Worker) handleForeach(ctx context.Context, sj StepJob, claimed *honker.Job) error {
	items, err := w.runForeach(ctx, sj)
	if err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: foreach %s (job %d): %w", sj.StepID, claimed.ID, err)
	}
	n := len(items)
	if sj.Body == nil {
		return fmt.Errorf("worker: foreach %s has no body step", sj.StepID)
	}

	// Set each rejoin's count to N so it waits for all iterations.
	for _, rejoin := range sj.Rejoins {
		if err := w.store.SetRunDepsRemaining(sj.RunID, rejoin, n); err != nil {
			w.recordFailure(sj, claimed, err)
			return fmt.Errorf("worker: foreach %s set rejoin count: %w", sj.StepID, err)
		}
		// Mark the rejoin to collect the array under this foreach step's name.
		rj := w.findDownstreamJob(sj, rejoin)
		if rj != nil {
			rj.CollectFrom = sj.Body.StepID
			rj.CollectAs = sj.BodyAs
			rj.CollectCount = n
		}
	}

	// The foreach step's own result records the items it fanned out. The N body
	// jobs are enqueued as the atom's Downstream (with empty Dependents, so they
	// are all enqueued immediately) and therefore count toward the run's pending
	// in-flight work: the fan's ack removes one, each body adds one (ADR-0023).
	atom := honker.StepAtom{
		RunID:  sj.RunID,
		StepID: sj.StepID,
		Result: map[string]any{"items": items},
		Job:    claimed,
	}
	// Enqueue N body jobs, each with the rejoins as dependents. The body job's
	// own Config/Command are its template; only Input and Collect markers vary.
	// Each body job gets a distinct StepID (`<body>#<i>`) so its result is stored
	// separately and the rejoin can read all N in input order (ADR-0024).
	for i, item := range items {
		body := *sj.Body
		body.RunID = sj.RunID
		body.StepID = fmt.Sprintf("%s#%d", sj.Body.StepID, i)
		body.Input = foreachItemInput(sj.Input, sj.BodyAs, item)
		body.Dependents = sj.Rejoins
		body.Downstream = w.rejoinJobs(sj, body)
		body.CollectFrom = ""
		body.CollectAs = ""
		body.CollectCount = 0
		body.CollectName = ""
		atom.Downstream = append(atom.Downstream, honker.Downstream{Queue: w.queue, Payload: body})
	}
	if err := w.store.CommitStepAtom(atom); err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: commit foreach %s (job %d): %w", sj.StepID, claimed.ID, err)
	}
	return w.checkRunComplete(ctx, sj)
}

// runForeach runs the foreach step as a subprocess (ADR-0008) and returns the
// list of elements to iterate.
func (w *Worker) runForeach(ctx context.Context, sj StepJob) ([]any, error) {
	if len(sj.Command) == 0 {
		return nil, fmt.Errorf("step type foreach has no command to run")
	}
	env, missing := exec.FilteredEnv(w.secrets, sj.Secrets)
	if len(missing) > 0 {
		return nil, fmt.Errorf("foreach declares secrets the runner does not have: %s", strings.Join(missing, ", "))
	}
	res, rerr := w.runner.Run(ctx, exec.Request{Command: sj.Command, Env: env, Input: sj.Input})
	if rerr != nil {
		return nil, rerr
	}
	list, ok := res.Output.([]any)
	if !ok {
		return nil, fmt.Errorf("foreach %s returned a non-list", sj.StepID)
	}
	return list, nil
}

// foreachItemInput builds a body iteration's input: the foreach step's
// {event, steps} input plus the loop element under the loop-variable name
// (ADR-0024).
func foreachItemInput(parentInput map[string]any, as string, item any) map[string]any {
	event := parentInput["event"]
	steps, _ := parentInput["steps"].(map[string]any)
	if steps == nil {
		steps = map[string]any{}
	}
	return map[string]any{"event": event, "steps": steps, as: item}
}

// findDownstreamJob returns the job in sj's Downstream tree with the given id.
func (w *Worker) findDownstreamJob(sj StepJob, id string) *StepJob {
	for i := range sj.Downstream {
		d := &sj.Downstream[i]
		if d.StepID == id {
			return d
		}
	}
	return nil
}

// rejoinJobs returns the rejoin jobs for a body job, looked up from the
// foreach step's Downstream by rejoin id.
func (w *Worker) rejoinJobs(sj StepJob, body StepJob) []StepJob {
	var out []StepJob
	for _, rejoin := range sj.Rejoins {
		if rj := w.findDownstreamJob(sj, rejoin); rj != nil {
			out = append(out, *rj)
		}
	}
	return out
}

// assembleForeachInput reads the N iteration results for a rejoin's foreach
// body and places them, in input order, as an array under the foreach step's
// name in the rejoin's `steps` input (ADR-0024). Each iteration's result is
// stored under the distinct step id `<body>#<i>`.
func (w *Worker) assembleForeachInput(sj StepJob) (map[string]any, error) {
	steps, _ := sj.Input["steps"].(map[string]any)
	if steps == nil {
		steps = map[string]any{}
	}
	arr := make([]any, 0, sj.CollectCount)
	for i := 0; i < sj.CollectCount; i++ {
		id := fmt.Sprintf("%s#%d", sj.CollectFrom, i)
		v, err := w.store.Result(sj.RunID, id)
		if err != nil {
			return nil, fmt.Errorf("foreach collect %s iteration %d: %w", sj.CollectFrom, i, err)
		}
		arr = append(arr, v)
	}
	steps[sj.CollectName] = arr
	out := make(map[string]any, len(sj.Input)+1)
	for k, v := range sj.Input {
		out[k] = v
	}
	out["steps"] = steps
	return out, nil
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
	// The failing step is not acked (it retries), so the run's pending count is
	// unchanged and the run is neither completed nor failed yet. It resolves
	// when the step succeeds or is dead-lettered.
}

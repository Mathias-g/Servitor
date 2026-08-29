// Package worker implements the node-execution worker loop (SPEC: Execution
// model). A worker claims a job, checks the node's dedupe_key, runs the node
// as a subprocess with a filtered environment, and commits the fan-out
// transaction ({result, dedupe, downstream, claim_ack}) through
// honker.CommitNodeAtom.
//
// The worker is deliberately single-threaded per instance; worker concurrency
// limits are an open SPEC question, and BSSN says one loop now. Honker's
// visibility timeout and attempt counting provide crash safety: a claim a
// worker dies holding expires and is re-issued, and a node that keeps failing
// is dead-lettered. Whether a re-run is side-effect-safe is the dedupe_key
// contract, enforced here.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Mathias-g/Servitor/internal/exec"
	"github.com/Mathias-g/Servitor/internal/expression"
	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/mcp"
	"github.com/Mathias-g/Servitor/internal/secret"
	"github.com/Mathias-g/Servitor/internal/singer"
)

// NodeJob is the payload of one queued node execution. It is fully
// self-contained: it carries the node's definition and its own downstream
// nodes, so the worker needs no separate workflow registry to run it. The
// initial node(s) of a run are enqueued by the trigger path; each node's
// successors ride in its Downstream field and are enqueued on completion.
type NodeJob struct {
	// RunID identifies the workflow run this node belongs to.
	RunID string
	// WorkflowID identifies the workflow the run was created from.
	WorkflowID string
	// NodeID is the node's effective identifier (its name or list position).
	NodeID string
	// NodeName is the node's `name` field from the Wafer.
	NodeName string
	// NodeType is the node type (for example `shell`).
	NodeType string
	// Config is the node's type-specific config.
	Config map[string]any
	// Input is the node's input: the trigger event plus prior node results.
	Input map[string]any
	// DedupeKey is the node's dedupe_key JSONata expression (ADR-0020), evaluated
	// against the step's {event, steps} input at execution time to form the
	// dedupe key. Empty means the node has no dedupe contract.
	DedupeKey string
	// Command is the argv to run for this node.
	Command []string
	// Secrets are the names of the secrets this node declares. The subprocess
	// environment is filtered to exactly these.
	Secrets []string
	// Skip marks a job that is part of a non-chosen switch branch. It records
	// the node as skipped (ADR-0023) and cascades to its dependents without
	// executing anything.
	Skip bool
	// Dependents are the ids of the nodes that depend on this one. When this
	// node completes, the worker decrements each dependent's count and enqueues
	// it (from Downstream, aligned by index) only when its count reaches zero
	// (ADR-0023). Empty means this node feeds nothing.
	Dependents []string
	// Downstream are the jobs for the dependents, aligned by index with
	// Dependents. The worker enqueues a job only for a dependent whose count
	// reached zero.
	Downstream []NodeJob
	// Body, when set, marks a foreach scheduler node (ADR-0024): it carries the
	// body node template to fan out once per element.
	Body *NodeJob
	// BodyAs is the loop variable name for a foreach body, exposed in each
	// iteration's input.
	BodyAs string
	// Rejoins are the node ids that depend on a foreach body and collect its
	// results. The foreach sets their dependency count to the iteration count.
	Rejoins []string
	// CollectFrom, CollectAs, and CollectCount mark a rejoin node that assembles
	// a foreach body's iteration results into an array under the foreach node's
	// name (ADR-0024). CollectFrom is the body node id, CollectAs is the loop
	// variable name, and CollectCount is the number of iterations. CollectName
	// is the foreach node's id, the key the array is placed under in `steps`.
	CollectFrom  string
	CollectAs    string
	CollectCount int
	CollectName  string
}

// NodeRunner runs a node subprocess. It is an interface so tests can stub it;
// the production value is subprocessRunner.
type NodeRunner interface {
	Run(ctx context.Context, req exec.Request) (exec.Result, error)
}

// subprocessRunner runs nodes as real OS subprocesses (ADR-0008).
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

// MCPRunner runs an mcp-call node as a subprocess. It is an interface so tests
// can stub it; the production value is subprocessMCPRunner.
type MCPRunner interface {
	Call(ctx context.Context, req mcp.CallRequest) (mcp.CallResult, error)
}

// subprocessMCPRunner runs mcp-call nodes as real OS subprocesses (ADR-0008,
// ADR-0015).
type subprocessMCPRunner struct{}

func (subprocessMCPRunner) Call(ctx context.Context, req mcp.CallRequest) (mcp.CallResult, error) {
	return mcp.Call(ctx, req)
}

// Config controls a Worker.
type Config struct {
	// Resolver resolves a node's declared secrets per node, per subprocess
	// (SPEC: Secret resolution, ADR-0032, ADR-0033). It replaces the resolved
	// global secret map; a node's value dies with its subprocess.
	Resolver *secret.Resolver
	// SecretRetryCount bounds how many times a node is retried for a
	// secret-specific failure (a stale secret, or a source that is unreachable)
	// before it fails, independent of the queue's generic MaxAttempts
	// (SPEC: Secret invalidity and rotation). Zero means 3.
	SecretRetryCount int
	// Runner runs node subprocesses. Defaults to real subprocesses.
	Runner NodeRunner
	// Singer runs singer tap and target subprocesses. Defaults to real
	// subprocesses.
	Singer SingerRunner
	// MCP runs mcp-call subprocesses. Defaults to real subprocesses.
	MCP MCPRunner
	// OnRunComplete, if set, is called after a run transitions to completed
	// (pending reaches zero). It lets the caller fire downstream work, such as
	// the `completed` trigger (SPEC: `completed` trigger), without coupling the
	// worker to the runner or trigger packages.
	OnRunComplete func(workflowID, runID string)
	// OnPoll, if set, is called when a `poll` node returns new items (ADR-0027).
	// kind identifies the source (for example "email") so the caller can turn
	// each item into a run's event without the worker knowing any provider.
	OnPoll func(workflowID, kind string, items []any)
}

// Worker claims and executes jobs from a queue, committing each node's
// completion atomically through the honker store.
type Worker struct {
	store         *honker.Store
	queue         *honker.Queue
	workerID      string
	resolver      *secret.Resolver
	secretRetries int
	runner        NodeRunner
	singer        SingerRunner
	mcp           MCPRunner
	onDone        func(workflowID, runID string)
	onPoll        func(workflowID, kind string, items []any)
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
	if cfg.Resolver == nil {
		cfg.Resolver = secret.NewResolver(secret.DefaultRegistry(), nil)
	}
	secretRetries := cfg.SecretRetryCount
	if secretRetries == 0 {
		secretRetries = 3
	}
	return &Worker{
		store:         store,
		queue:         queue,
		workerID:      workerID,
		resolver:      cfg.Resolver,
		secretRetries: secretRetries,
		runner:        cfg.Runner,
		singer:        cfg.Singer,
		mcp:           cfg.MCP,
		onDone:        cfg.OnRunComplete,
		onPoll:        cfg.OnPoll,
	}
}

// nodeEnv resolves a node's declared secrets through the resolver and builds
// the filtered environment for its subprocess (per-node, per-subprocess
// delivery; ADR-0033). It returns the NAME=value pairs (PATH plus the resolved
// declared secrets) and an error if a secret is undeclared, its source is
// unreachable, or it is stale. A missing secret is returned as the second
// value so the caller can fail with the same "does not have" message as
// before.
func (w *Worker) nodeEnv(ctx context.Context, nodeName string, names []string) (env []string, missing []string, err error) {
	values, missing, err := w.resolver.Resolve(ctx, nodeName, names)
	if err != nil {
		return nil, missing, err
	}
	env, _ = exec.FilteredEnv(values, names)
	return env, missing, nil
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

// handle processes one claimed job. It returns an error for diagnostics; node
// failures are recorded (failed result and dedupe record) and the claim
// retried inside, so the loop keeps going.
func (w *Worker) handle(ctx context.Context, claimed *honker.Job) error {
	var sj NodeJob
	if err := claimed.UnmarshalPayload(&sj); err != nil {
		_, _ = claimed.Fail("invalid node job payload: " + err.Error())
		return fmt.Errorf("worker: decode job %d: %w", claimed.ID, err)
	}

	// A cancelled (or already finished) run must not continue: ack the job
	// without running it or enqueueing its successors. This is what actually
	// stops a cancelled chain even if a node was already claimed.
	if status, _ := w.store.RunStatus(sj.RunID); status != "" && status != honker.RunRunning {
		_, _ = claimed.Ack()
		return nil
	}

	// A skip-job (a non-chosen switch branch) records skipped and cascades
	// without executing anything (ADR-0023).
	if sj.Skip {
		return w.handleSkip(ctx, sj, claimed)
	}

	// A switch node resolves its branch and routes: chosen branch enqueued
	// normally, skipped branches enqueued as skip-jobs (ADR-0022).
	if sj.NodeType == "switch" {
		return w.handleSwitch(ctx, sj, claimed)
	}

	// A foreach node resolves its list and fans out the body node once per
	// element, collecting results at the rejoin (ADR-0024).
	if sj.NodeType == "foreach" {
		return w.handleForeach(ctx, sj, claimed)
	}

	// A `poll` node runs a fetcher subprocess and hands the returned items to a
	// callback so the caller can fan out one run per item (ADR-0027).
	if sj.NodeType == "poll" {
		return w.handlePoll(ctx, sj, claimed)
	}

	return w.handleNode(ctx, sj, claimed)
}

// handleNode runs a normal node, commits its atom, and cascades to dependents.
func (w *Worker) handleNode(ctx context.Context, sj NodeJob, claimed *honker.Job) error {
	// A rejoin node that collects a foreach body's results assembles the array
	// of iteration results into its input before running (ADR-0024).
	if sj.CollectFrom != "" {
		assembled, aerr := w.assembleForeachInput(sj)
		if aerr != nil {
			w.recordFailure(sj, claimed, aerr)
			return fmt.Errorf("worker: node %s (job %d): %w", sj.NodeID, claimed.ID, aerr)
		}
		sj.Input = assembled
	}

	// Resolve the step's dedupe_key JSONata expression against its {event, steps}
	// input at execution time (ADR-0020, ADR-0021). The resolved key is used for
	// the dedupe lookup and, on completion, for the dedupe record.
	dedupeKey, derr := resolveDedupeKey(sj)
	if derr != nil {
		w.recordFailure(sj, claimed, derr)
		return fmt.Errorf("worker: node %s (job %d): %w", sj.NodeID, claimed.ID, derr)
	}

	result, ran, state, err := w.runNode(ctx, sj, dedupeKey)
	if err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: node %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}

	atom := honker.NodeAtom{
		RunID:      sj.RunID,
		NodeID:     sj.NodeID,
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
			NodeName:   sj.NodeName,
			Key:        dedupeKey,
			Succeeded:  true,
			Result:     result,
		}
	}
	for i := range sj.Downstream {
		d := sj.Downstream[i]
		// Thread the {event, steps} input forward: this step's result becomes
		// available to each successor under this node's name (ADR-0021).
		d.Input = threadInput(sj.Input, sj.NodeName, result)
		atom.Downstream = append(atom.Downstream, honker.Downstream{Queue: w.queue, Payload: d})
	}
	if err := w.store.CommitNodeAtom(atom); err != nil {
		return fmt.Errorf("worker: commit node %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}
	return w.checkRunComplete(ctx, sj)
}

// handleSkip records a non-chosen switch branch node as skipped and cascades:
// it decrements its dependents' counts and enqueues its own downstream as
// skip-jobs, so a skipped branch propagates to a rejoin without executing
// anything (ADR-0023).
func (w *Worker) handleSkip(ctx context.Context, sj NodeJob, claimed *honker.Job) error {
	atom := honker.NodeAtom{
		RunID:      sj.RunID,
		NodeID:     sj.NodeID,
		Result:     map[string]any{"skipped": true},
		Job:        claimed,
		Dependents: sj.Dependents,
	}
	for i := range sj.Downstream {
		d := sj.Downstream[i]
		d.Skip = true
		d.Input = threadInput(sj.Input, sj.NodeName, map[string]any{"skipped": true})
		atom.Downstream = append(atom.Downstream, honker.Downstream{Queue: w.queue, Payload: d})
	}
	if err := w.store.CommitNodeAtom(atom); err != nil {
		return fmt.Errorf("worker: commit skip %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}
	return w.checkRunComplete(ctx, sj)
}

// handleSwitch runs a switch node, resolves its chosen branch, and routes: the
// chosen branch's dependent is enqueued normally (if ready); each skipped
// branch's dependent is enqueued as a skip-job (ADR-0022, ADR-0023).
func (w *Worker) handleSwitch(ctx context.Context, sj NodeJob, claimed *honker.Job) error {
	chosen, err := w.runSwitch(ctx, sj)
	if err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: switch %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}

	atom := honker.NodeAtom{
		RunID:      sj.RunID,
		NodeID:     sj.NodeID,
		Result:     map[string]any{"branch": chosen},
		Job:        claimed,
		Dependents: sj.Dependents,
	}
	for i := range sj.Downstream {
		d := sj.Downstream[i]
		d.Input = threadInput(sj.Input, sj.NodeName, map[string]any{"branch": chosen})
		if d.NodeID == chosen {
			// Chosen branch: enqueue normally (the atom's readiness check gates
			// on the dependent's count).
			atom.Downstream = append(atom.Downstream, honker.Downstream{Queue: w.queue, Payload: d})
		} else {
			// Skipped branch: enqueue a skip-job that cascades without running.
			d.Skip = true
			atom.Downstream = append(atom.Downstream, honker.Downstream{Queue: w.queue, Payload: d})
		}
	}
	if err := w.store.CommitNodeAtom(atom); err != nil {
		return fmt.Errorf("worker: commit switch %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}
	return w.checkRunComplete(ctx, sj)
}

// runSwitch runs the switch node as a subprocess (ADR-0008) and returns the
// chosen branch's target node name. It passes the node's input and cases/default
// config on stdin.
func (w *Worker) runSwitch(ctx context.Context, sj NodeJob) (string, error) {
	if len(sj.Command) == 0 {
		return "", fmt.Errorf("node type switch has no command to run")
	}
	env, missing, err := w.nodeEnv(ctx, sj.NodeName, sj.Secrets)
	if err != nil {
		return "", err
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("%w: %s", secret.ErrSecretMissing, strings.Join(missing, ", "))
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
		return "", fmt.Errorf("switch %s returned no branch target", sj.NodeID)
	}
	return chosen, nil
}

// checkRunComplete marks a run completed once its pending job count reaches
// zero (ADR-0023). It is called after a node (or skip) commits its atom. The
// pending count is adjusted in the same transaction as the commit, so by the
// time this runs, pending reflects all in-flight work for the run.
func (w *Worker) checkRunComplete(ctx context.Context, sj NodeJob) error {
	pending, err := w.store.RunPending(sj.RunID)
	if err != nil {
		return err
	}
	if pending == 0 {
		if err := w.store.SetRunStatus(sj.RunID, honker.RunCompleted); err != nil {
			return err
		}
		if w.onDone != nil {
			w.onDone(sj.WorkflowID, sj.RunID)
		}
	}
	return nil
}

// handleForeach runs a foreach node: it resolves the list, sets each rejoin's
// dependency count to the iteration count N, and enqueues N body-node jobs, one
// per element (ADR-0024). Each body job carries the rejoins as its dependents
// so, when it completes, it decrements each rejoin; a rejoin is enqueued once
// its count reaches zero.
func (w *Worker) handleForeach(ctx context.Context, sj NodeJob, claimed *honker.Job) error {
	items, err := w.runForeach(ctx, sj)
	if err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: foreach %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}
	n := len(items)
	if sj.Body == nil {
		return fmt.Errorf("worker: foreach %s has no body node", sj.NodeID)
	}

	// Set each rejoin's count to N so it waits for all iterations.
	for _, rejoin := range sj.Rejoins {
		if err := w.store.SetRunDepsRemaining(sj.RunID, rejoin, n); err != nil {
			w.recordFailure(sj, claimed, err)
			return fmt.Errorf("worker: foreach %s set rejoin count: %w", sj.NodeID, err)
		}
		// Mark the rejoin to collect the array under this foreach node's name.
		rj := w.findDownstreamJob(sj, rejoin)
		if rj != nil {
			rj.CollectFrom = sj.Body.NodeID
			rj.CollectAs = sj.BodyAs
			rj.CollectCount = n
		}
	}

	// The foreach node's own result records the items it fanned out. The N body
	// jobs are enqueued as the atom's Downstream (with empty Dependents, so they
	// are all enqueued immediately) and therefore count toward the run's pending
	// in-flight work: the fan's ack removes one, each body adds one (ADR-0023).
	atom := honker.NodeAtom{
		RunID:  sj.RunID,
		NodeID: sj.NodeID,
		Result: map[string]any{"items": items},
		Job:    claimed,
	}
	// Enqueue N body jobs, each with the rejoins as dependents. The body job's
	// own Config/Command are its template; only Input and Collect markers vary.
	// Each body job gets a distinct NodeID (`<body>#<i>`) so its result is stored
	// separately and the rejoin can read all N in input order (ADR-0024).
	for i, item := range items {
		body := *sj.Body
		body.RunID = sj.RunID
		body.NodeID = fmt.Sprintf("%s#%d", sj.Body.NodeID, i)
		body.Input = foreachItemInput(sj.Input, sj.BodyAs, item)
		body.Dependents = sj.Rejoins
		body.Downstream = w.rejoinJobs(sj, body)
		body.CollectFrom = ""
		body.CollectAs = ""
		body.CollectCount = 0
		body.CollectName = ""
		atom.Downstream = append(atom.Downstream, honker.Downstream{Queue: w.queue, Payload: body})
	}
	if err := w.store.CommitNodeAtom(atom); err != nil {
		w.recordFailure(sj, claimed, err)
		return fmt.Errorf("worker: commit foreach %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}
	return w.checkRunComplete(ctx, sj)
}

// handlePoll runs a `poll` node as a subprocess (ADR-0027) and hands the
// returned items to the OnPoll callback so the caller can fan out one run per
// item. The poll job carries its kind (for example "email") in Config, which
// the callback uses to build each run's event without the worker knowing any
// provider. The poll job is always acked: it runs on a schedule, so the next
// fire retries on failure rather than spamming retries on the claim.
func (w *Worker) handlePoll(ctx context.Context, sj NodeJob, claimed *honker.Job) error {
	if w.onPoll == nil {
		_, _ = claimed.Ack()
		return nil
	}
	if len(sj.Command) == 0 {
		_, _ = claimed.Ack()
		return fmt.Errorf("worker: poll %s (job %d): no command", sj.NodeID, claimed.ID)
	}
	env, missing, err := w.nodeEnv(ctx, sj.NodeName, sj.Secrets)
	if err != nil {
		_, _ = claimed.Ack()
		return fmt.Errorf("worker: poll %s (job %d): %w", sj.NodeID, claimed.ID, err)
	}
	if len(missing) > 0 {
		_, _ = claimed.Ack()
		return fmt.Errorf("worker: poll %s (job %d): %w: %s", sj.NodeID, claimed.ID, secret.ErrSecretMissing, strings.Join(missing, ", "))
	}
	res, rerr := w.runner.Run(ctx, exec.Request{Command: sj.Command, Env: env, Input: sj.Input})
	if rerr != nil {
		_, _ = claimed.Ack()
		return fmt.Errorf("worker: poll %s (job %d): %w", sj.NodeID, claimed.ID, rerr)
	}
	raw, ok := res.Output.([]any)
	if !ok {
		_, _ = claimed.Ack()
		return fmt.Errorf("worker: poll %s (job %d): helper returned a non-list", sj.NodeID, claimed.ID)
	}
	kind, _ := sj.Config["kind"].(string)
	w.onPoll(sj.WorkflowID, kind, raw)
	_, _ = claimed.Ack()
	return nil
}

// runForeach runs the foreach node as a subprocess (ADR-0008) and returns the
// list of elements to iterate.
func (w *Worker) runForeach(ctx context.Context, sj NodeJob) ([]any, error) {
	if len(sj.Command) == 0 {
		return nil, fmt.Errorf("node type foreach has no command to run")
	}
	env, missing, err := w.nodeEnv(ctx, sj.NodeName, sj.Secrets)
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s", secret.ErrSecretMissing, strings.Join(missing, ", "))
	}
	res, rerr := w.runner.Run(ctx, exec.Request{Command: sj.Command, Env: env, Input: sj.Input})
	if rerr != nil {
		return nil, rerr
	}
	list, ok := res.Output.([]any)
	if !ok {
		return nil, fmt.Errorf("foreach %s returned a non-list", sj.NodeID)
	}
	return list, nil
}

// foreachItemInput builds a body iteration's input: the foreach node's
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
func (w *Worker) findDownstreamJob(sj NodeJob, id string) *NodeJob {
	for i := range sj.Downstream {
		d := &sj.Downstream[i]
		if d.NodeID == id {
			return d
		}
	}
	return nil
}

// rejoinJobs returns the rejoin jobs for a body job, looked up from the
// foreach node's Downstream by rejoin id.
func (w *Worker) rejoinJobs(sj NodeJob, body NodeJob) []NodeJob {
	var out []NodeJob
	for _, rejoin := range sj.Rejoins {
		if rj := w.findDownstreamJob(sj, rejoin); rj != nil {
			out = append(out, *rj)
		}
	}
	return out
}

// assembleForeachInput reads the N iteration results for a rejoin's foreach
// body and places them, in input order, as an array under the foreach node's
// name in the rejoin's `steps` input (ADR-0024). Each iteration's result is
// stored under the distinct node id `<body>#<i>`.
func (w *Worker) assembleForeachInput(sj NodeJob) (map[string]any, error) {
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

// resolveDedupeKey evaluates a node's dedupe_key JSONata expression (ADR-0020)
// against its {event, steps} input (ADR-0021) and stringifies the result to form
// the dedupe key. It returns "" when the node has no dedupe_key. An expression
// that evaluates to nothing is treated as no key.
func resolveDedupeKey(sj NodeJob) (string, error) {
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

// runNode runs a single node to produce its result. It returns ran=false when
// the node is skipped by a prior successful dedupe record, in which case
// result is the prior result and no subprocess runs. It returns state, non-nil
// only for a completed singer-tap node, whose new bookmark is committed with
// the node's result (SPEC: Execution model step 8).
func (w *Worker) runNode(ctx context.Context, sj NodeJob, dedupeKey string) (result any, ran bool, state *honker.SingerState, err error) {
	if dedupeKey != "" {
		out, lerr := w.store.LookupDedupe(sj.WorkflowID, sj.NodeName, dedupeKey)
		if lerr != nil {
			return nil, false, nil, lerr
		}
		// Skip on a prior success; proceed on a prior failure or a first run.
		if out != nil && out.Succeeded {
			return out.Result, false, nil, nil
		}
	}

	if sj.NodeType == "singer-tap" || sj.NodeType == "singer-target" {
		return w.runSingerNode(ctx, sj)
	}

	if sj.NodeType == "mcp-call" {
		return w.runMCPNode(ctx, sj)
	}

	if len(sj.Command) == 0 {
		return nil, true, nil, fmt.Errorf("node type %q has no command to run", sj.NodeType)
	}

	env, missing, err := w.nodeEnv(ctx, sj.NodeName, sj.Secrets)
	if err != nil {
		return nil, true, nil, err
	}
	if len(missing) > 0 {
		return nil, true, nil, fmt.Errorf("%w: %s", secret.ErrSecretMissing, strings.Join(missing, ", "))
	}

	res, rerr := w.runner.Run(ctx, exec.Request{Command: sj.Command, Env: env, Input: sj.Input})
	if rerr != nil {
		return nil, true, nil, rerr
	}
	return res.Output, true, nil, nil
}

// runSingerNode runs a singer-tap or singer-target node as a subprocess
// (SPEC: Singer). A tap is fed its config, selected streams, and prior bookmark
// on stdin and returns its records and next bookmark; the bookmark is returned
// as state so it commits with the node's result. A target is fed the records
// from the node's input.
func (w *Worker) runSingerNode(ctx context.Context, sj NodeJob) (result any, ran bool, state *honker.SingerState, err error) {
	if len(sj.Command) == 0 {
		return nil, true, nil, fmt.Errorf("node type %q has no command to run", sj.NodeType)
	}
	env, missing, err := w.nodeEnv(ctx, sj.NodeName, sj.Secrets)
	if err != nil {
		return nil, true, nil, err
	}
	if len(missing) > 0 {
		return nil, true, nil, fmt.Errorf("%w: %s", secret.ErrSecretMissing, strings.Join(missing, ", "))
	}

	switch sj.NodeType {
	case "singer-tap":
		cfg, _ := sj.Config["config"].(map[string]any)
		prior, gerr := w.store.GetSingerState(sj.WorkflowID, sj.NodeName)
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
			&honker.SingerState{WorkflowID: sj.WorkflowID, NodeName: sj.NodeName, State: res.State}, nil
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
		return nil, true, nil, fmt.Errorf("node type %q is not a singer node", sj.NodeType)
	}
}

// runMCPNode runs an mcp-call node as a subprocess (SPEC: MCP integration,
// ADR-0015). It spawns the named server with a filtered secret env, invokes one
// tool, and maps an errored result onto Servitor's structured error format.
func (w *Worker) runMCPNode(ctx context.Context, sj NodeJob) (result any, ran bool, state *honker.SingerState, err error) {
	if len(sj.Command) == 0 {
		return nil, true, nil, fmt.Errorf("node type %q has no command to run", sj.NodeType)
	}
	env, missing, err := w.nodeEnv(ctx, sj.NodeName, sj.Secrets)
	if err != nil {
		return nil, true, nil, err
	}
	if len(missing) > 0 {
		return nil, true, nil, fmt.Errorf("%w: %s", secret.ErrSecretMissing, strings.Join(missing, ", "))
	}
	tool, _ := sj.Config["tool"].(string)
	if tool == "" {
		return nil, true, nil, fmt.Errorf("node type mcp-call requires a `tool` name")
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

// threadInput builds a downstream node's input from a completing node's input
// and its result (ADR-0021). The input is an object with an `event` field (the
// durable trigger payload) and a `steps` field (prior node results keyed by node
// name; the field is named `steps` for historical reasons, ADR-0021). This
// node's result is added under this node's name; the event is passed
// through unchanged.
func threadInput(parentInput map[string]any, nodeName string, result any) map[string]any {
	event := parentInput["event"]
	steps, _ := parentInput["steps"].(map[string]any)
	if steps == nil {
		steps = map[string]any{}
	}
	next := make(map[string]any, len(steps)+1)
	for k, v := range steps {
		next[k] = v
	}
	if nodeName != "" {
		next[nodeName] = result
	}
	return map[string]any{"event": event, "steps": next}
}

// recordsFromInput extracts singer records from a step's `{event, steps}` input
// (ADR-0021). A target is downstream of the tap that produced the records, so
// the records live under the tap node's result, which has the shape
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

// recordFailure persists a node's failure: a failed result row and, when the
// node has a dedupe key, a failed dedupe record so a later retry proceeds
// rather than being skipped (SPEC: Idempotency). It then decides what to do
// with the claim based on the failure kind (SPEC: Secret invalidity and
// rotation):
//
//   - A missing secret fails fast: no retry (a missing value is not transient),
//     the claim is dead-lettered with a `missing_secret` error, and the operator
//     adds the value and resumes.
//   - An unreachable source retries with exponential backoff, since the source
//     may come back; if it stays down past the secret retry count it fails with
//     a distinct error.
//   - A stale secret retries with a fresh resolve (each retry re-resolves), and
//     fails with `secret_auth_failed` once the secret retry count is reached.
//   - Any other node failure keeps the current behavior: retry immediately,
//     bounded by the queue's generic max attempts.
func (w *Worker) recordFailure(sj NodeJob, claimed *honker.Job, cause error) {
	code := ""
	switch {
	case errors.Is(cause, secret.ErrSecretMissing):
		code = "missing_secret"
	case errors.Is(cause, secret.ErrSourceUnreachable):
		code = "secret_source_unreachable"
	case errors.Is(cause, secret.ErrStale):
		code = "secret_auth_failed"
	}
	if code != "" {
		result := map[string]any{"ok": false, "code": code, "error": cause.Error()}
		w.commitFailed(sj, result)
		if claimed != nil {
			if code == "missing_secret" || claimed.Attempts >= int64(w.secretRetries) {
				_, _ = claimed.Fail(cause.Error())
				return
			}
			if code == "secret_source_unreachable" {
				_, _ = claimed.Retry(backoffDelay(claimed.Attempts), cause.Error())
				return
			}
			_, _ = claimed.Retry(0, cause.Error())
			return
		}
		return
	}

	result := map[string]any{"ok": false, "error": cause.Error()}
	w.commitFailed(sj, result)
	if claimed != nil {
		_, _ = claimed.Retry(0, cause.Error())
	}
	// The failing node is not acked (it retries), so the run's pending count is
	// unchanged and the run is neither completed nor failed yet. It resolves
	// when the node succeeds or is dead-lettered.
}

// commitFailed persists a failed result row and, when the node has a dedupe
// key, a failed dedupe record so a later retry proceeds rather than being
// skipped (SPEC: Idempotency).
func (w *Worker) commitFailed(sj NodeJob, result map[string]any) {
	atom := honker.NodeAtom{
		RunID:  sj.RunID,
		NodeID: sj.NodeID,
		Result: result,
		// No Job and no Downstream: the claim is not acked and successors do
		// not run, so the node is re-issued on retry/visibility timeout.
	}
	// The dedupe key is resolved before the node runs; if it could not be
	// evaluated we have no key, so no failed dedupe record is written. A later
	// retry re-evaluates it.
	if sj.DedupeKey != "" {
		key, err := resolveDedupeKey(sj)
		if err == nil && key != "" {
			atom.Dedupe = &honker.DedupeRecord{
				WorkflowID: sj.WorkflowID,
				NodeName:   sj.NodeName,
				Key:        key,
				Succeeded:  false,
				Result:     result,
			}
		}
	}
	_ = w.store.CommitNodeAtom(atom)
}

// backoffDelay returns an exponential backoff delay in seconds for the given
// attempt (1s, 2s, 4s, ...) capped at 30s, for a transiently unreachable
// secret source (SPEC: Secret invalidity and rotation).
func backoffDelay(attempt int64) int64 {
	delay := int64(1) << min(attempt, 5)
	if delay > 30 {
		return 30
	}
	return delay
}

// Package runner wires triggers to the worker by building a run's initial node
// job tree and registering cron triggers on the Honker scheduler (SPEC:
// Triggers, Execution model step 5).
//
// A run is built from a Wafer as a single topological-order chain of NodeJobs:
// each node's Downstream is the next node in run order. Because run order
// respects dependencies, a node always executes after the nodes it depends on,
// and no node is ever enqueued twice. This is deliberately sequential; fanning
// out independent branches in parallel is a later refinement (SPEC: Roadmap,
// worker concurrency).
package runner

import (
	"encoding/json"
	"fmt"

	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/registry"
	"github.com/Mathias-g/Servitor/internal/wafer"
	"github.com/Mathias-g/Servitor/internal/worker"
)

// commandFor maps a node to the argv the worker runs for it. Every node runs as
// a subprocess (ADR-0008); `transform` re-invokes the servitor binary's hidden
// `__transform` command so even pure-computation nodes stay out of the runner's
// process. It dispatches through the mechanism registry's Spawn (ADR-0045), so
// the runner names no node type in a switch.
func commandFor(s wafer.Node) ([]string, error) {
	return registry.CommandFor(s.Type, s.Config)
}

// removeStr removes all occurrences of s from the slice, preserving order.
func removeStr(xs []string, s string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// FromWafer builds the head NodeJob of a run from a validated Wafer and the
// trigger event that started it. The returned job's Downstream carries the rest
// of the workflow as a dependency DAG: each node's Dependents/Downstream lists
// the nodes that depend on it (ADR-0023). It returns an error when a node type
// has no handler yet or the DAG does not resolve.
func FromWafer(w *wafer.Wafer, event map[string]any) (*worker.NodeJob, error) {
	dag, issues := wafer.ResolveDAG(w)
	if len(issues) > 0 {
		return nil, fmt.Errorf("run: workflow %q does not resolve: %v", w.Name, issues)
	}
	if len(dag.Nodes) == 0 {
		return nil, nil
	}

	// Build one NodeJob per node, indexed by the node's position in the DAG
	// run order. Each job carries the nodes that depend on it so the worker can
	// fan out correctly (ADR-0023).
	jobs := make([]*worker.NodeJob, len(dag.Nodes))
	idxByName := map[string]int{}
	for i, d := range dag.Nodes {
		s := w.Nodes[d.Index]
		cmd, err := commandFor(s)
		if err != nil {
			return nil, err
		}
		jobs[i] = &worker.NodeJob{
			WorkflowID: w.Name,
			NodeID:     d.Name,
			NodeName:   s.Name,
			NodeType:   s.Type,
			Config:     s.Config,
			Command:    cmd,
			Secrets:    s.Secrets,
			DedupeKey:  s.DedupeKey,
		}
		idxByName[d.Name] = i
	}

	// Assign each node's dependents (the nodes that list it in their DependsOn).
	// Dependents are assigned fully first, so when a job is later copied into a
	// parent's Downstream it carries the complete dependents set (the value copy
	// would otherwise be stale).
	for _, d := range dag.Nodes {
		for _, dep := range d.DependsOn {
			if j, ok := idxByName[dep]; ok {
				jobs[j].Dependents = append(jobs[j].Dependents, d.Name)
			}
		}
	}
	// Handle foreach nodes (ADR-0024): a foreach fanned out its body node N
	// times, so the body is not a normal dependent of the foreach. Instead the
	// foreach carries the body template, the loop-variable name, and the nodes
	// that depend on the body (the rejoins) which collect its results.
	forEachBody := map[string]bool{}
	for _, d := range dag.Nodes {
		if d.Type != "foreach" {
			continue
		}
		fj := jobs[idxByName[d.Name]]
		bodyName, _ := fj.Config["body"].(string)
		bodyIdx, ok := idxByName[bodyName]
		if !ok {
			return nil, fmt.Errorf("run: foreach %q has unknown body %q", d.Name, bodyName)
		}
		// The body is fanned out, so remove it from the foreach's dependents.
		fj.Dependents = removeStr(fj.Dependents, bodyName)
		as, _ := fj.Config["as"].(string)
		if as == "" {
			as = "item"
		}
		fj.Body = jobs[bodyIdx]
		fj.BodyAs = as
		fj.Rejoins = jobs[bodyIdx].Dependents
		forEachBody[bodyName] = true
		// Mark the body's rejoins to collect; they keep the body in Dependents
		// so the static DAG count exists, but the foreach overrides it to N at
		// runtime (ADR-0024).
		for _, rejoin := range fj.Rejoins {
			rj := jobs[idxByName[rejoin]]
			rj.CollectFrom = bodyName
			rj.CollectAs = as
			rj.CollectName = d.Name
			// The foreach carries the rejoin jobs in its Downstream so the worker
			// can build body jobs that point to them (they are not reachable via
			// the body, which is fanned out).
			fj.Downstream = append(fj.Downstream, *rj)
		}
	}
	// Build each job's Downstream from its (now complete) dependents.
	for j := range jobs {
		for _, depID := range jobs[j].Dependents {
			k := idxByName[depID]
			jobs[j].Downstream = append(jobs[j].Downstream, *jobs[k])
		}
	}

	// Find the head node(s): those with no dependencies (DependsOn empty).
	head := jobs[0]
	for i, d := range dag.Nodes {
		if len(d.DependsOn) == 0 {
			head = jobs[i]
			break
		}
	}
	head.Input = map[string]any{"event": event, "steps": map[string]any{}}
	return head, nil
}

// Rerun re-runs a dead-lettered (failed) run by mode (ADR-0044). The run must
// have a saved failed continuation (a node dead-lettered and the run was marked
// failed):
//
//   - "continue": re-enqueue the saved failed node with its original input, so
//     only the failed node and its remaining successors run. Completed nodes'
//     results stay in node_results. The default, and the safe choice for the
//     secret-invalidity case.
//   - "restart": rebuild the run from the top for the same run id via StartRun,
//     resetting the run, its dependency counts, and its pending count. Redoes
//     completed side effects unless they are guarded by dedupe_key.
//   - "discard": drop the saved continuation, leaving the run failed (a
//     terminal "not resuming" state).
func Rerun(store *honker.Store, queue *honker.Queue, runID, mode string) error {
	cont, err := store.GetFailedContinuation(runID)
	if err != nil {
		return err
	}
	if cont == nil {
		return fmt.Errorf("rerun: run %s has no saved continuation (it did not fail, or it was already rerun or discarded)", runID)
	}
	switch mode {
	case "continue":
		return rerunContinue(store, queue, cont)
	case "restart":
		return rerunRestart(store, queue, cont)
	case "discard":
		return store.WithTx(func(tx *honker.Tx) error {
			return tx.DeleteFailedContinuation(runID)
		})
	default:
		return fmt.Errorf("rerun: unknown mode %q", mode)
	}
}

// rerunContinue re-enqueues a failed run's saved failed node, setting the run
// back to running with a fresh pending count, in one transaction (ADR-0044).
func rerunContinue(store *honker.Store, queue *honker.Queue, cont *honker.FailedContinuation) error {
	var job worker.NodeJob
	if err := json.Unmarshal(cont.Payload, &job); err != nil {
		return fmt.Errorf("rerun: continue %s: decode node: %w", cont.RunID, err)
	}
	return store.WithTx(func(tx *honker.Tx) error {
		if err := tx.SetRunStatusTx(cont.RunID, honker.RunRunning); err != nil {
			return err
		}
		// The one re-enqueued node is the only pending work.
		if err := tx.Exec(`UPDATE runs SET pending = ? WHERE run_id = ?`, 1, cont.RunID); err != nil {
			return err
		}
		return tx.Enqueue(queue, &job)
	})
}

// rerunRestart rebuilds a failed run from the top, for the same run id, using
// the workflow's current definition and the run's original event (ADR-0044).
func rerunRestart(store *honker.Store, queue *honker.Queue, cont *honker.FailedContinuation) error {
	wf, err := store.GetWorkflow(cont.WorkflowID)
	if err != nil {
		return fmt.Errorf("rerun: restart %s: %w", cont.RunID, err)
	}
	if wf == nil {
		return fmt.Errorf("rerun: restart %s: workflow %q is not registered", cont.RunID, cont.WorkflowID)
	}
	w, perr := wafer.Parse([]byte(wf.Wafer))
	if perr != nil {
		return fmt.Errorf("rerun: restart %s: parse workflow: %w", cont.RunID, perr)
	}
	// StartRun OR-REPLACEs the run row (pending=1, status=running), re-inits
	// the dependency counts, and enqueues the head for this run id.
	if _, err := StartRun(store, queue, w, cont.Event, cont.RunID); err != nil {
		return fmt.Errorf("rerun: restart %s: %w", cont.RunID, err)
	}
	return nil
}

// StartRun builds a run's head job, records the run, initializes the fan-in
// dependency counts, and enqueues the head. It assigns a fresh run id to every
// node in the run's DAG. It returns the run id and the head job.
func StartRun(store *honker.Store, queue *honker.Queue, w *wafer.Wafer, event map[string]any, runID string) (string, error) {
	head, err := FromWafer(w, event)
	if err != nil {
		return "", err
	}
	if head == nil {
		return "", nil
	}
	assignRunID(head, runID)
	if err := store.CreateRun(runID, w.Name); err != nil {
		return "", err
	}
	if err := initDeps(store, w, runID); err != nil {
		return "", err
	}
	if _, err := queue.Enqueue(head); err != nil {
		return "", fmt.Errorf("run: enqueue head node: %w", err)
	}
	return runID, nil
}

// initDeps initializes the run_deps dependency counts for a run from the Wafer
// (ADR-0023). Each node's count is the number of nodes it depends on.
func initDeps(store *honker.Store, w *wafer.Wafer, runID string) error {
	dag, issues := wafer.ResolveDAG(w)
	if len(issues) > 0 {
		return fmt.Errorf("run: workflow %q does not resolve: %v", w.Name, issues)
	}
	depCount := map[string]int{}
	order := []string{}
	for _, d := range dag.Nodes {
		depCount[d.Name] = len(d.DependsOn)
		order = append(order, d.Name)
	}
	return store.InitRunDeps(honker.NewRunDeps(runID, depCount, order))
}

// assignRunID sets the run id on every job in the run's DAG. Because a run can
// fan out (ADR-0023), it does not walk a linear next pointer; it traverses the
// full Downstream set from the head.
func assignRunID(head *worker.NodeJob, runID string) {
	seen := map[*worker.NodeJob]bool{}
	var walk func(j *worker.NodeJob)
	walk = func(j *worker.NodeJob) {
		if j == nil || seen[j] {
			return
		}
		seen[j] = true
		j.RunID = runID
		for i := range j.Downstream {
			walk(&j.Downstream[i])
		}
	}
	walk(head)
}

// CronTask registers one cron trigger for a workflow on the Honker scheduler.
// When the schedule fires, Honker enqueues the run's head node to the queue,
// starting a run. Registration is idempotent by name.
type CronTask struct {
	// Name uniquely identifies the scheduled task (for example
	// "<workflow>:cron-0").
	Name string
	// Schedule is the cron expression from the trigger config.
	Schedule string
	// RunID is the run id assigned when the schedule fires.
	RunID string
	// Event is the static trigger payload enqueued with each fire.
	Event map[string]any
}

// RegisterCron registers a cron trigger for a workflow. schedule comes from the
// trigger's `schedule` config field.
func RegisterCron(store *honker.Store, queue *honker.Queue, w *wafer.Wafer, task CronTask) error {
	head, err := FromWafer(w, task.Event)
	if err != nil {
		return err
	}
	if head == nil {
		return nil
	}
	assignRunID(head, task.RunID)
	if err := initDeps(store, w, task.RunID); err != nil {
		return err
	}
	err = store.RegisterScheduledTask(honker.ScheduledTask{
		Name:     task.Name,
		Queue:    queue.Name(),
		Schedule: task.Schedule,
		Payload:  head,
	})
	if err != nil {
		return fmt.Errorf("cron: register %s: %w", task.Name, err)
	}
	return nil
}

// PollTask is a registered recurring poll. When the schedule fires, Honker
// enqueues a poll job that runs a helper subprocess (ADR-0027) and returns a
// list of new items; the worker hands them to a callback so the caller can fan
// out one run per item. `email_received` is the first poll kind; a future
// polled source is a new kind and command.
type PollTask struct {
	// Name uniquely identifies the scheduled poll (for example
	// "<workflow>:email-0").
	Name string
	// Schedule is the cron expression from the trigger's `poll` config field.
	Schedule string
	// WorkflowID is the workflow the poll belongs to; new items fan out into
	// runs of it.
	WorkflowID string
	// Kind identifies the poll source (for example "email"). The callback uses
	// it to turn each item into a run's event.
	Kind string
	// Command is the helper subprocess to run (for example the servitor binary
	// with `__email_poll`).
	Command []string
	// Secrets are the secret names filtered into the subprocess env.
	Secrets []string
	// Config is the trigger's config, passed to the helper on stdin.
	Config map[string]any
}

// RegisterPoll registers a recurring poll. schedule comes from the trigger's
// `poll` config field. The poll runs as a subprocess (ADR-0008), so it never
// runs inside the runner's process.
func RegisterPoll(store *honker.Store, queue *honker.Queue, task PollTask) error {
	if len(task.Command) == 0 {
		return fmt.Errorf("poll: %s has no command", task.Name)
	}
	pollJob := &worker.NodeJob{
		WorkflowID: task.WorkflowID,
		NodeID:     "poll",
		NodeName:   "poll",
		NodeType:   "poll",
		Config:     map[string]any{"kind": task.Kind},
		Command:    task.Command,
		Secrets:    task.Secrets,
		Input:      task.Config,
	}
	err := store.RegisterScheduledTask(honker.ScheduledTask{
		Name:     task.Name,
		Queue:    queue.Name(),
		Schedule: task.Schedule,
		Payload:  pollJob,
	})
	if err != nil {
		return fmt.Errorf("poll: register %s: %w", task.Name, err)
	}
	return nil
}

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
	"fmt"
	"os"

	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/wafer"
	"github.com/Mathias-g/Servitor/internal/worker"
)

// commandFor maps a node to the argv the worker runs for it. Every node runs as
// a subprocess (ADR-0008); `transform` re-invokes the servitor binary's hidden
// `__transform` command so even pure-computation nodes stay out of the runner's
// process.
func commandFor(s wafer.Node) ([]string, error) {
	switch s.Type {
	case "shell":
		cmd, ok := s.Config["command"].(string)
		if !ok || cmd == "" {
			return nil, fmt.Errorf("node %q: shell requires a string command", nodeName(s))
		}
		return []string{"/bin/sh", "-c", cmd}, nil
	case "transform":
		expr, ok := s.Config["expression"].(string)
		if !ok || expr == "" {
			return nil, fmt.Errorf("node %q: transform requires a string expression", nodeName(s))
		}
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("node %q: locate servitor binary: %w", nodeName(s), err)
		}
		return []string{exe, "__transform", expr}, nil
	case "switch":
		expr, ok := s.Config["expression"].(string)
		if !ok || expr == "" {
			return nil, fmt.Errorf("node %q: switch requires a string expression", nodeName(s))
		}
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("node %q: locate servitor binary: %w", nodeName(s), err)
		}
		return []string{exe, "__switch", expr}, nil
	case "foreach":
		expr, ok := s.Config["over"].(string)
		if !ok || expr == "" {
			return nil, fmt.Errorf("node %q: foreach requires a string `over` expression", nodeName(s))
		}
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("node %q: locate servitor binary: %w", nodeName(s), err)
		}
		return []string{exe, "__foreach", expr}, nil
	case "singer-tap":
		tap, ok := s.Config["tap"].(string)
		if !ok || tap == "" {
			return nil, fmt.Errorf("node %q: singer-tap requires a `tap` name", nodeName(s))
		}
		return []string{tap}, nil
	case "singer-target":
		target, ok := s.Config["target"].(string)
		if !ok || target == "" {
			return nil, fmt.Errorf("node %q: singer-target requires a `target` name", nodeName(s))
		}
		return []string{target}, nil
	case "mcp-call":
		server, ok := s.Config["server"].(string)
		if !ok || server == "" {
			return nil, fmt.Errorf("node %q: mcp-call requires a `server` name", nodeName(s))
		}
		return []string{server}, nil
	default:
		return nil, fmt.Errorf("node %q: node type %q has no handler built yet (Phase 6 runs shell; the rest come later)", nodeName(s), s.Type)
	}
}

func nodeName(s wafer.Node) string {
	if s.Name != "" {
		return s.Name
	}
	return s.Type
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

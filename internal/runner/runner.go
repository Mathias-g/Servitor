// Package runner wires triggers to the worker by building a run's initial step
// job tree and registering cron triggers on the Honker scheduler (SPEC:
// Triggers, Execution model step 5).
//
// A run is built from a Wafer as a single topological-order chain of StepJobs:
// each step's Downstream is the next step in run order. Because run order
// respects dependencies, a step always executes after the steps it depends on,
// and no step is ever enqueued twice. This is deliberately sequential; fanning
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

// commandFor maps a step to the argv the worker runs for it. Every step runs as
// a subprocess (ADR-0008); `transform` re-invokes the servitor binary's hidden
// `__transform` command so even pure-computation steps stay out of the runner's
// process.
func commandFor(s wafer.Step) ([]string, error) {
	switch s.Type {
	case "shell":
		cmd, ok := s.Config["command"].(string)
		if !ok || cmd == "" {
			return nil, fmt.Errorf("step %q: shell requires a string command", stepName(s))
		}
		return []string{"/bin/sh", "-c", cmd}, nil
	case "transform":
		expr, ok := s.Config["expression"].(string)
		if !ok || expr == "" {
			return nil, fmt.Errorf("step %q: transform requires a string expression", stepName(s))
		}
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("step %q: locate servitor binary: %w", stepName(s), err)
		}
		return []string{exe, "__transform", expr}, nil
	case "switch":
		expr, ok := s.Config["expression"].(string)
		if !ok || expr == "" {
			return nil, fmt.Errorf("step %q: switch requires a string expression", stepName(s))
		}
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("step %q: locate servitor binary: %w", stepName(s), err)
		}
		return []string{exe, "__switch", expr}, nil
	case "singer-tap":
		tap, ok := s.Config["tap"].(string)
		if !ok || tap == "" {
			return nil, fmt.Errorf("step %q: singer-tap requires a `tap` name", stepName(s))
		}
		return []string{tap}, nil
	case "singer-target":
		target, ok := s.Config["target"].(string)
		if !ok || target == "" {
			return nil, fmt.Errorf("step %q: singer-target requires a `target` name", stepName(s))
		}
		return []string{target}, nil
	case "mcp-call":
		server, ok := s.Config["server"].(string)
		if !ok || server == "" {
			return nil, fmt.Errorf("step %q: mcp-call requires a `server` name", stepName(s))
		}
		return []string{server}, nil
	default:
		return nil, fmt.Errorf("step %q: step type %q has no handler built yet (Phase 6 runs shell; the rest come later)", stepName(s), s.Type)
	}
}

func stepName(s wafer.Step) string {
	if s.Name != "" {
		return s.Name
	}
	return s.Type
}

// FromWafer builds the head StepJob of a run from a validated Wafer and the
// trigger event that started it. The returned job's Downstream carries the rest
// of the workflow as a dependency DAG: each step's Dependents/Downstream lists
// the steps that depend on it (ADR-0023). It returns an error when a step type
// has no handler yet or the DAG does not resolve.
func FromWafer(w *wafer.Wafer, event map[string]any) (*worker.StepJob, error) {
	dag, issues := wafer.ResolveDAG(w)
	if len(issues) > 0 {
		return nil, fmt.Errorf("run: workflow %q does not resolve: %v", w.Name, issues)
	}
	if len(dag.Steps) == 0 {
		return nil, nil
	}

	// Build one StepJob per step, indexed by the step's position in the DAG
	// run order. Each job carries the steps that depend on it so the worker can
	// fan out correctly (ADR-0023).
	jobs := make([]*worker.StepJob, len(dag.Steps))
	idxByName := map[string]int{}
	for i, d := range dag.Steps {
		s := w.Steps[d.Index]
		cmd, err := commandFor(s)
		if err != nil {
			return nil, err
		}
		jobs[i] = &worker.StepJob{
			WorkflowID: w.Name,
			StepID:     d.Name,
			StepName:   s.Name,
			StepType:   s.Type,
			Config:     s.Config,
			Command:    cmd,
			Secrets:    s.Secrets,
			DedupeKey:  s.DedupeKey,
		}
		idxByName[d.Name] = i
	}

	// Assign each step's dependents (the steps that list it in their DependsOn).
	// Dependents are assigned fully first, so when a job is later copied into a
	// parent's Downstream it carries the complete dependents set (the value copy
	// would otherwise be stale).
	for _, d := range dag.Steps {
		for _, dep := range d.DependsOn {
			if j, ok := idxByName[dep]; ok {
				jobs[j].Dependents = append(jobs[j].Dependents, d.Name)
			}
		}
	}
	// Build each job's Downstream from its (now complete) dependents.
	for j := range jobs {
		for _, depID := range jobs[j].Dependents {
			k := idxByName[depID]
			jobs[j].Downstream = append(jobs[j].Downstream, *jobs[k])
		}
	}

	// Find the head step(s): those with no dependencies (DependsOn empty).
	head := jobs[0]
	for i, d := range dag.Steps {
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
// step in the run's DAG. It returns the run id and the head job.
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
		return "", fmt.Errorf("run: enqueue head step: %w", err)
	}
	return runID, nil
}

// initDeps initializes the run_deps dependency counts for a run from the Wafer
// (ADR-0023). Each step's count is the number of steps it depends on.
func initDeps(store *honker.Store, w *wafer.Wafer, runID string) error {
	dag, issues := wafer.ResolveDAG(w)
	if len(issues) > 0 {
		return fmt.Errorf("run: workflow %q does not resolve: %v", w.Name, issues)
	}
	depCount := map[string]int{}
	order := []string{}
	for _, d := range dag.Steps {
		depCount[d.Name] = len(d.DependsOn)
		order = append(order, d.Name)
	}
	return store.InitRunDeps(honker.NewRunDeps(runID, depCount, order))
}

// assignRunID sets the run id on every job in the run's DAG. Because a run can
// fan out (ADR-0023), it does not walk a linear next pointer; it traverses the
// full Downstream set from the head.
func assignRunID(head *worker.StepJob, runID string) {
	seen := map[*worker.StepJob]bool{}
	var walk func(j *worker.StepJob)
	walk = func(j *worker.StepJob) {
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
// When the schedule fires, Honker enqueues the run's head step to the queue,
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

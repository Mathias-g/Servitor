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

	"github.com/Mathias-g/Servitor/internal/honker"
	"github.com/Mathias-g/Servitor/internal/wafer"
	"github.com/Mathias-g/Servitor/internal/worker"
)

// commandFor maps a step to the argv the worker runs for it. In Phase 6 the
// `shell` step is the concrete subprocess primitive; the other handlers
// (transform, branch, foreach, integration helpers) dispatch through the same
// machinery in later phases.
func commandFor(s wafer.Step) ([]string, error) {
	switch s.Type {
	case "shell":
		cmd, ok := s.Config["command"].(string)
		if !ok || cmd == "" {
			return nil, fmt.Errorf("step %q: shell requires a string command", stepName(s))
		}
		return []string{"/bin/sh", "-c", cmd}, nil
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
// of the workflow as a chain. It returns an error when a step type has no
// handler yet or the DAG does not resolve.
func FromWafer(w *wafer.Wafer, event map[string]any) (*worker.StepJob, error) {
	dag, issues := wafer.ResolveDAG(w)
	if len(issues) > 0 {
		return nil, fmt.Errorf("run: workflow %q does not resolve: %v", w.Name, issues)
	}
	if len(dag.Steps) == 0 {
		return nil, nil
	}

	// Build one StepJob per step in run order, then link each to the next.
	jobs := make([]*worker.StepJob, len(dag.Steps))
	for i, d := range dag.Steps {
		s := w.Steps[d.Index]
		cmd, err := commandFor(s)
		if err != nil {
			return nil, err
		}
		jobs[i] = &worker.StepJob{
			RunID:      "", // set by the caller (StartRun)
			WorkflowID: w.Name,
			StepID:     d.Name,
			StepName:   s.Name,
			StepType:   s.Type,
			Config:     s.Config,
			Command:    cmd,
			Secrets:    s.Secrets,
			// Input and DedupeKey are filled by the trigger path: the event for
			// the head step, and the resolved dedupe value once the expression
			// language is settled (SPEC: open questions).
		}
	}
	for i := 0; i < len(jobs)-1; i++ {
		jobs[i].Downstream = []worker.StepJob{*jobs[i+1]}
	}
	head := jobs[0]
	head.Input = event
	return head, nil
}

// StartRun builds a run's head job, records the run, and enqueues the head
// onto the queue, assigning a fresh run id to every step in the chain. It
// returns the run id and the head job.
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
	if _, err := queue.Enqueue(head); err != nil {
		return "", fmt.Errorf("run: enqueue head step: %w", err)
	}
	return runID, nil
}

// assignRunID sets the run id on a step job and, recursively, on every job in
// its Downstream chain, so all of a run's steps land under the same run id.
func assignRunID(head *worker.StepJob, runID string) {
	for n := head; n != nil; n = next(n) {
		n.RunID = runID
	}
}

// next returns the single downstream of a chained job, or nil. Runs built by
// FromWafer are a linear chain (each step has at most one successor).
func next(j *worker.StepJob) *worker.StepJob {
	if len(j.Downstream) == 0 {
		return nil
	}
	return &j.Downstream[0]
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

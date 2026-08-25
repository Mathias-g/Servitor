package honker

import (
	"database/sql"
	"fmt"
)

// runDeps tracks, per run and step, how many of a step's dependencies are still
// unsatisfied. A step is ready to run only when its count reaches zero. This is
// the fan-in mechanism (ADR-0022, SPEC: Execution model): a switch step
// produces one branch, and a rejoin step runs only after all the branches it
// depends on have completed.
//
// The counts live in the `run_deps` table and are adjusted inside the same
// SQLite transaction as the completing step's result, dedupe record, and claim
// ack, so the SPEC's no-split rule (step 8) is preserved: a dependent is
// enqueued exactly when its last dependency completes, never before and never
// split from that completion.
//
// A linear chain (each step depends on at most the previous) is the degenerate
// case: every step has count 1, and each completion enqueues the next.
type RunDeps struct {
	// RunID is the run these counts belong to.
	RunID string
	// Remaining maps step id -> unsatisfied dependency count.
	Remaining map[string]int
	// Order preserves the run order of steps for deterministic enqueue.
	Order []string
}

// NewRunDeps builds the initial dependency counts for a run from a per-step
// list of dependency counts. depCount[step] is the number of steps step depends
// on. Steps with a zero count are initially ready.
func NewRunDeps(runID string, depCount map[string]int, order []string) *RunDeps {
	rd := &RunDeps{RunID: runID, Remaining: map[string]int{}, Order: order}
	for _, s := range order {
		rd.Remaining[s] = depCount[s]
	}
	return rd
}

// InitRunDeps writes the initial dependency counts for a run, replacing any
// prior counts (a run id is unique). Call it when a run is created, before any
// step is enqueued, so the fan-in bookkeeping is in place.
func (s *Store) InitRunDeps(rd *RunDeps) error {
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, step := range rd.Order {
		if err := tx.Exec(
			`INSERT OR REPLACE INTO run_deps (run_id, step_id, remaining) VALUES (?, ?, ?)`,
			rd.RunID, step, rd.Remaining[step],
		); err != nil {
			return fmt.Errorf("honker: init run deps %s/%s: %w", rd.RunID, step, err)
		}
	}
	return tx.Commit()
}

// decrementDependents decrements the remaining dependency count for each of the
// given dependents and returns the ids whose count reached zero (ready to
// enqueue). It runs inside the caller's transaction so the decrement and the
// dependent enqueue commit atomically with the completing step's result and
// ack (ADR-0023, SPEC: Execution model step 8).
func (t *Tx) decrementDependents(runID string, dependents []string) ([]string, error) {
	var ready []string
	for _, dep := range dependents {
		if _, err := t.tx.Exec(
			`UPDATE run_deps SET remaining = remaining - 1 WHERE run_id = ? AND step_id = ?`,
			runID, dep,
		); err != nil {
			return nil, fmt.Errorf("honker: decrement deps for %s: %w", dep, err)
		}
		var remaining int
		err := t.tx.QueryRow(
			`SELECT remaining FROM run_deps WHERE run_id = ? AND step_id = ?`,
			runID, dep,
		).Scan(&remaining)
		if err != nil {
			return nil, fmt.Errorf("honker: read remaining for %s: %w", dep, err)
		}
		if remaining == 0 {
			ready = append(ready, dep)
		}
	}
	return ready, nil
}

// RunDepsRemaining returns the remaining dependency count for a run's step, or
// (0, nil) when no row exists. It is how tests (and run inspection) observe the
// fan-in state.
func (s *Store) RunDepsRemaining(runID, stepID string) (int, error) {
	var n int
	err := s.db.Raw().QueryRow(
		`SELECT remaining FROM run_deps WHERE run_id = ? AND step_id = ?`,
		runID, stepID,
	).Scan(&n)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return n, nil
}

// RunComplete reports whether every step in the run has its dependencies
// satisfied (remaining == 0), which is the dependency-based run-completion
// signal (ADR-0023): a run is done when no step is left waiting on a dependency.
// It returns true when the run has no tracked steps.
func (s *Store) RunComplete(runID string) (bool, error) {
	var n int
	err := s.db.Raw().QueryRow(
		`SELECT COUNT(*) FROM run_deps WHERE run_id = ? AND remaining > 0`,
		runID,
	).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

package honker

import (
	"database/sql"
	"errors"
	"fmt"
)

// Run statuses. A run is created as running, becomes completed or failed when
// its last node finishes, cancelled when the operator stops it, and waiting
// when it is parked at a `wait` node (ADR-0041).
const (
	RunRunning   = "running"
	RunCompleted = "completed"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
	RunWaiting   = "waiting"
)

// Run is one workflow run, identified by its run id (SPEC: Control plane,
// `runs` / `run <id>`).
type Run struct {
	ID           string
	WorkflowName string
	Status       string
	CreatedAt    string
}

// NodeOutcome is one node's recorded result within a run.
type NodeOutcome struct {
	NodeID string
	Result string // JSON
}

// CreateRun records a new run as running. It is called when a run's head node
// is enqueued, so a run always has a row even before any node finishes. pending
// is initialized to 1 (the head node about to run) so the run's completion is
// tracked by in-flight jobs reaching zero (ADR-0023).
func (s *Store) CreateRun(id, workflowName string) error {
	if id == "" {
		return fmt.Errorf("honker: create run: empty run id")
	}
	_, err := s.db.Raw().Exec(
		`INSERT OR REPLACE INTO runs (run_id, workflow_name, status, pending) VALUES (?, ?, ?, ?)`,
		id, workflowName, RunRunning, 1,
	)
	if err != nil {
		return fmt.Errorf("honker: create run %s: %w", id, err)
	}
	return nil
}

// RunPending returns a run's pending job count, or (0, nil) when the run is not
// recorded. The run is complete when pending reaches zero.
func (s *Store) RunPending(id string) (int, error) {
	var n int
	err := s.db.Raw().QueryRow(`SELECT pending FROM runs WHERE run_id = ?`, id).Scan(&n)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return n, nil
}

// AdjustPending changes a run's pending job count by delta inside the given
// transaction. It is how the worker tracks in-flight work: a node's completion
// decrements pending by its own ack and increments it by the dependents it
// enqueues, in the same atomic commit as the result (ADR-0023).
func (t *Tx) AdjustPending(runID string, delta int) error {
	_, err := t.tx.Exec(
		`UPDATE runs SET pending = pending + ? WHERE run_id = ?`, delta, runID,
	)
	if err != nil {
		return fmt.Errorf("honker: adjust pending %s by %d: %w", runID, delta, err)
	}
	return nil
}

// SetRunStatus updates a run's status.
func (s *Store) SetRunStatus(id, status string) error {
	_, err := s.db.Raw().Exec(`UPDATE runs SET status = ? WHERE run_id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("honker: set run %s status %s: %w", id, status, err)
	}
	return nil
}

// RunStatus returns a run's status, or "" when the run is not recorded.
func (s *Store) RunStatus(id string) (string, error) {
	var st string
	err := s.db.Raw().QueryRow(`SELECT status FROM runs WHERE run_id = ?`, id).Scan(&st)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return st, nil
}

// GetRun returns a recorded run, or (nil, nil) when it is not recorded.
func (s *Store) GetRun(id string) (*Run, error) {
	var r Run
	err := s.db.Raw().QueryRow(
		`SELECT run_id, workflow_name, status, created_at FROM runs WHERE run_id = ?`, id,
	).Scan(&r.ID, &r.WorkflowName, &r.Status, &r.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// ListRuns returns all runs, newest first.
func (s *Store) ListRuns() ([]Run, error) {
	rows, err := s.db.Raw().Query(
		`SELECT run_id, workflow_name, status, created_at FROM runs ORDER BY created_at DESC, run_id DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.WorkflowName, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunNodes returns a run's node outcomes.
func (s *Store) RunNodes(id string) ([]NodeOutcome, error) {
	rows, err := s.db.Raw().Query(
		`SELECT node_id, result FROM node_results WHERE run_id = ?`, id,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []NodeOutcome
	for rows.Next() {
		var so NodeOutcome
		if err := rows.Scan(&so.NodeID, &so.Result); err != nil {
			return nil, err
		}
		out = append(out, so)
	}
	return out, rows.Err()
}

// CancelRun stops an in-flight run: it marks the run cancelled and drops any
// jobs still pending in the queue for that run, and drops a parked
// continuation so a run no one is ever going to resume can be cleaned up
// (ADR-0040). A node already claimed and running is stopped by the worker's
// cancel check rather than here.
func (s *Store) CancelRun(id string) error {
	if err := s.SetRunStatus(id, RunCancelled); err != nil {
		return err
	}
	// Drop pending jobs for this run so they are not claimed and run. The
	// worker's cancel check is the authoritative guard; this is cleanup.
	if _, err := s.db.Raw().Exec(
		`DELETE FROM _honker_live WHERE state = 'pending' AND json_extract(payload, '$.RunID') = ?`,
		id,
	); err != nil {
		return fmt.Errorf("honker: cancel run %s: %w", id, err)
	}
	// Drop a parked continuation so the run is fully cleaned up.
	if _, err := s.db.Raw().Exec(
		`DELETE FROM suspended_continuations WHERE run_id = ?`, id,
	); err != nil {
		return fmt.Errorf("honker: cancel run %s (drop continuation): %w", id, err)
	}
	return nil
}

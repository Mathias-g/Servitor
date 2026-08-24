package honker

import (
	"database/sql"
	"errors"
	"fmt"
)

// Run statuses. A run is created as running, becomes completed or failed when
// its last step finishes, and cancelled when the operator stops it.
const (
	RunRunning   = "running"
	RunCompleted = "completed"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
)

// Run is one workflow run, identified by its run id (SPEC: Control plane,
// `runs` / `run <id>`).
type Run struct {
	ID           string
	WorkflowName string
	Status       string
	CreatedAt    string
}

// StepOutcome is one step's recorded result within a run.
type StepOutcome struct {
	StepID string
	Result string // JSON
}

// CreateRun records a new run as running. It is called when a run's head step
// is enqueued, so a run always has a row even before any step finishes.
func (s *Store) CreateRun(id, workflowName string) error {
	if id == "" {
		return fmt.Errorf("honker: create run: empty run id")
	}
	_, err := s.db.Raw().Exec(
		`INSERT OR REPLACE INTO runs (run_id, workflow_name, status) VALUES (?, ?, ?)`,
		id, workflowName, RunRunning,
	)
	if err != nil {
		return fmt.Errorf("honker: create run %s: %w", id, err)
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

// RunSteps returns a run's step outcomes.
func (s *Store) RunSteps(id string) ([]StepOutcome, error) {
	rows, err := s.db.Raw().Query(
		`SELECT step_id, result FROM step_results WHERE run_id = ?`, id,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []StepOutcome
	for rows.Next() {
		var so StepOutcome
		if err := rows.Scan(&so.StepID, &so.Result); err != nil {
			return nil, err
		}
		out = append(out, so)
	}
	return out, rows.Err()
}

// CancelRun stops an in-flight run: it marks the run cancelled and drops any
// jobs still pending in the queue for that run. A step already claimed and
// running is stopped by the worker's cancel check rather than here.
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
	return nil
}

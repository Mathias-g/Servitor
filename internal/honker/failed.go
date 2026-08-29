package honker

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// FailedContinuation is a dead-lettered run's saved continuation (ADR-0044).
// When a node dead-letters (retries exhausted), the run is marked failed and
// the failed node's self-contained NodeJob is stored here so the run can later
// be re-run (continue/restart/discard) without restarting from the top.
// Event is the run's original trigger event, kept for the `restart` mode;
// Payload is the worker-serialized failed NodeJob, kept for the `continue`
// mode.
type FailedContinuation struct {
	// RunID is the failed run.
	RunID string
	// WorkflowID is the workflow the run was created from.
	WorkflowID string
	// Event is the run's original trigger event.
	Event map[string]any
	// Payload is the worker-serialized failed node's NodeJob (opaque to honker).
	Payload []byte
}

// WriteFailedContinuation stores a dead-lettered run's continuation inside this
// transaction.
func (t *Tx) WriteFailedContinuation(c FailedContinuation) error {
	eventJSON := "null"
	if c.Event != nil {
		b, err := json.Marshal(c.Event)
		if err != nil {
			return fmt.Errorf("honker: marshal failed event: %w", err)
		}
		eventJSON = string(b)
	}
	if _, err := t.tx.Exec(
		`INSERT OR REPLACE INTO failed_continuations (run_id, workflow_id, event, payload) VALUES (?, ?, ?, ?)`,
		c.RunID, c.WorkflowID, eventJSON, string(c.Payload),
	); err != nil {
		return fmt.Errorf("honker: write failed continuation %s: %w", c.RunID, err)
	}
	return nil
}

// GetFailedContinuation returns a dead-lettered run's saved continuation, or
// (nil, nil) when none is saved.
func (s *Store) GetFailedContinuation(runID string) (*FailedContinuation, error) {
	var c FailedContinuation
	var event string
	var payload string
	err := s.db.Raw().QueryRow(
		`SELECT run_id, workflow_id, event, payload FROM failed_continuations WHERE run_id = ?`,
		runID,
	).Scan(&c.RunID, &c.WorkflowID, &event, &payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(event), &c.Event)
	c.Payload = []byte(payload)
	return &c, nil
}

// DeleteFailedContinuation removes a dead-lettered run's saved continuation,
// inside this transaction.
func (t *Tx) DeleteFailedContinuation(runID string) error {
	if _, err := t.tx.Exec(
		`DELETE FROM failed_continuations WHERE run_id = ?`, runID,
	); err != nil {
		return fmt.Errorf("honker: delete failed continuation %s: %w", runID, err)
	}
	return nil
}

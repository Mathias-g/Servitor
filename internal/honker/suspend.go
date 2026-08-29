package honker

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Continuation is a parked run's stored continuation (ADR-0040). It records
// where a `wait` node parked the run so it can be resumed later, by a timer
// firing or a named signal arriving. Payload is opaque bytes the worker
// serialized (the wait node's downstream sub-DAG plus its input and identity);
// honker stores it without interpreting it, so honker does not depend on the
// worker package.
type Continuation struct {
	// RunID is the parked run.
	RunID string
	// WorkflowID is the workflow the run was created from.
	WorkflowID string
	// SignalName is the resolved effective signal name the run is parked on,
	// or "" when the wait has no signal source.
	SignalName string
	// RunAt is the absolute unix epoch the timer will resume the run, or 0
	// when the wait has no timer source.
	RunAt int64
	// Payload is the worker-serialized continuation (opaque).
	Payload []byte
}

// WriteContinuation stores a run's parked continuation inside this transaction.
func (t *Tx) WriteContinuation(c Continuation) error {
	if _, err := t.tx.Exec(
		`INSERT OR REPLACE INTO suspended_continuations (run_id, workflow_id, signal_name, run_at, payload) VALUES (?, ?, ?, ?, ?)`,
		c.RunID, c.WorkflowID, nullable(c.SignalName), nullableInt(c.RunAt), string(c.Payload),
	); err != nil {
		return fmt.Errorf("honker: write continuation %s: %w", c.RunID, err)
	}
	return nil
}

// DeleteContinuation removes a run's parked continuation inside this
// transaction. It is called when the run is resumed (or cancelled).
func (t *Tx) DeleteContinuation(runID string) error {
	if _, err := t.tx.Exec(
		`DELETE FROM suspended_continuations WHERE run_id = ?`, runID,
	); err != nil {
		return fmt.Errorf("honker: delete continuation %s: %w", runID, err)
	}
	return nil
}

// SetRunStatusTx sets a run's status inside this transaction.
func (t *Tx) SetRunStatusTx(runID, status string) error {
	if _, err := t.tx.Exec(
		`UPDATE runs SET status = ? WHERE run_id = ?`, status, runID,
	); err != nil {
		return fmt.Errorf("honker: set run %s status %s in tx: %w", runID, status, err)
	}
	return nil
}

// GetContinuation returns a run's parked continuation, or (nil, nil) when the
// run is not parked.
func (s *Store) GetContinuation(runID string) (*Continuation, error) {
	var c Continuation
	var sig sql.NullString
	var runAt sql.NullInt64
	var payload string
	err := s.db.Raw().QueryRow(
		`SELECT run_id, workflow_id, signal_name, run_at, payload FROM suspended_continuations WHERE run_id = ?`,
		runID,
	).Scan(&c.RunID, &c.WorkflowID, &sig, &runAt, &payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	c.SignalName = sig.String
	c.RunAt = runAt.Int64
	c.Payload = []byte(payload)
	return &c, nil
}

// ParkedRunsForSignal returns the run ids of parked (waiting) runs currently
// parked on the given effective signal name.
func (s *Store) ParkedRunsForSignal(name string) ([]string, error) {
	rows, err := s.db.Raw().Query(
		`SELECT c.run_id FROM suspended_continuations c
		 JOIN runs r ON r.run_id = c.run_id
		 WHERE c.signal_name = ? AND r.status = ?`,
		name, RunWaiting,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// BufferSignal persists a signal that arrived when no run was parked on its
// name, so a later `wait` park on that name can consume it rather than parking
// (ADR-0042). It mirrors the "signal arrives before the run parks" race rule.
func (s *Store) BufferSignal(name string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("honker: buffer signal: %w", err)
	}
	if _, err := s.db.Raw().Exec(
		`INSERT INTO buffered_signals (signal_name, payload) VALUES (?, ?)`,
		name, string(b),
	); err != nil {
		return fmt.Errorf("honker: buffer signal %s: %w", name, err)
	}
	return nil
}

// TakeBufferedSignal consumes the oldest buffered signal for a name inside this
// transaction, returning its payload and whether one was found. It is called by
// a `wait` park so a signal that arrived before the park resumes immediately.
func (t *Tx) TakeBufferedSignal(name string) (any, bool, error) {
	var payload string
	err := t.tx.QueryRow(
		`SELECT payload FROM buffered_signals WHERE signal_name = ? ORDER BY id ASC LIMIT 1`,
		name,
	).Scan(&payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if _, err := t.tx.Exec(
		`DELETE FROM buffered_signals WHERE signal_name = ? AND payload = ?`,
		name, payload,
	); err != nil {
		return nil, false, err
	}
	var v any
	_ = json.Unmarshal([]byte(payload), &v)
	return v, true, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

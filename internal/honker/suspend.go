package honker

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Continuation is a parked wait's stored continuation (ADR-0040). It records
// where a `wait` node parked the run so it can be resumed later, by a timer
// firing or a named signal arriving. A run can park more than one wait at a
// time (for example a `wait` inside a `foreach` body parks one continuation per
// iteration), so a continuation is keyed by (RunID, NodeID), where NodeID is
// the wait node's effective identifier. Payload is opaque bytes the worker
// serialized (the wait node's downstream sub-DAG plus its input and identity);
// honker stores it without interpreting it, so honker does not depend on the
// worker package.
type Continuation struct {
	// RunID is the parked run.
	RunID string
	// NodeID is the wait node's effective identifier (its name or list
	// position), distinguishing one parked wait from another in the same run.
	NodeID string
	// WorkflowID is the workflow the run was created from.
	WorkflowID string
	// SignalName is the resolved effective signal name the wait is parked on,
	// or "" when the wait has no signal source.
	SignalName string
	// RunAt is the absolute unix epoch the timer will resume the wait, or 0
	// when the wait has no timer source.
	RunAt int64
	// Payload is the worker-serialized continuation (opaque).
	Payload []byte
}

// WriteContinuation stores a parked wait's continuation inside this
// transaction. It is keyed by (RunID, NodeID); a repeat write for the same key
// replaces the prior one.
func (t *Tx) WriteContinuation(c Continuation) error {
	if _, err := t.tx.Exec(
		`INSERT OR REPLACE INTO suspended_continuations (run_id, node_id, workflow_id, signal_name, run_at, payload) VALUES (?, ?, ?, ?, ?, ?)`,
		c.RunID, c.NodeID, c.WorkflowID, nullable(c.SignalName), nullableInt(c.RunAt), string(c.Payload),
	); err != nil {
		return fmt.Errorf("honker: write continuation %s/%s: %w", c.RunID, c.NodeID, err)
	}
	return nil
}

// DeleteContinuation removes a parked wait's continuation inside this
// transaction. It is called when the wait is resumed (or the run cancelled).
func (t *Tx) DeleteContinuation(runID, nodeID string) error {
	if _, err := t.tx.Exec(
		`DELETE FROM suspended_continuations WHERE run_id = ? AND node_id = ?`, runID, nodeID,
	); err != nil {
		return fmt.Errorf("honker: delete continuation %s/%s: %w", runID, nodeID, err)
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

// ContinuationCount returns how many waits are currently parked for a run,
// inside this transaction. It is how the resume path decides whether the run is
// still waiting on other waits or can go back to running (the last parked wait's
// resume flips it).
func (t *Tx) ContinuationCount(runID string) (int, error) {
	var n int
	if err := t.tx.QueryRow(
		`SELECT COUNT(*) FROM suspended_continuations WHERE run_id = ?`, runID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("honker: count continuations %s: %w", runID, err)
	}
	return n, nil
}

// GetContinuation returns a parked wait's continuation, or (nil, nil) when the
// wait is not parked.
func (s *Store) GetContinuation(runID, nodeID string) (*Continuation, error) {
	var c Continuation
	var sig sql.NullString
	var runAt sql.NullInt64
	var payload string
	err := s.db.Raw().QueryRow(
		`SELECT run_id, node_id, workflow_id, signal_name, run_at, payload FROM suspended_continuations WHERE run_id = ? AND node_id = ?`,
		runID, nodeID,
	).Scan(&c.RunID, &c.NodeID, &c.WorkflowID, &sig, &runAt, &payload)
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

// ParkedInstance identifies a single parked wait: the run it belongs to and the
// wait node's effective identifier within that run.
type ParkedInstance struct {
	RunID  string
	NodeID string
}

// ParkedInstancesForSignal returns the parked (waiting) waits currently parked
// on the given effective signal name.
func (s *Store) ParkedInstancesForSignal(name string) ([]ParkedInstance, error) {
	rows, err := s.db.Raw().Query(
		`SELECT c.run_id, c.node_id FROM suspended_continuations c
		 JOIN runs r ON r.run_id = c.run_id
		 WHERE c.signal_name = ? AND r.status = ?`,
		name, RunWaiting,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ParkedInstance
	for rows.Next() {
		var p ParkedInstance
		if err := rows.Scan(&p.RunID, &p.NodeID); err != nil {
			return nil, err
		}
		out = append(out, p)
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

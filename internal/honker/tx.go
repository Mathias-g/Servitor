package honker

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	hg "github.com/russellromney/honker-go"
)

// Tx is a single SQLite transaction. It exists to compose the SPEC's
// transactional atom: a step's result, its dedupe record, its downstream
// enqueues, and its claim ack all commit (or all roll back) together
// (SPEC: Execution model step 8).
type Tx struct {
	tx *hg.Transaction
}

// Begin opens a transaction against the store's single write connection.
func (s *Store) Begin() (*Tx, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("honker: begin: %w", err)
	}
	return &Tx{tx: tx}, nil
}

// Exec runs a statement inside the transaction.
func (t *Tx) Exec(sql string, args ...any) error {
	_, err := t.tx.Exec(sql, args...)
	return err
}

// Enqueue adds a downstream job to a queue inside this transaction, so it
// commits (or rolls back) with the rest of the atom.
func (t *Tx) Enqueue(queue *Queue, payload any) error {
	_, err := queue.q.EnqueueTx(t.tx, payload, hg.EnqueueOptions{})
	return err
}

// Ack marks a claimed job as done inside this transaction. This is the claim
// ack half of the atom: it only commits if the rest of the transaction does.
func (t *Tx) Ack(job *Job) error {
	var n int64
	if err := t.tx.QueryRow(`SELECT honker_ack(?, ?)`, job.ID, job.WorkerID).Scan(&n); err != nil {
		return fmt.Errorf("honker: ack %d in tx: %w", job.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("honker: ack %d in tx: claim no longer valid (expired?)", job.ID)
	}
	return nil
}

// Commit commits the transaction.
func (t *Tx) Commit() error { return t.tx.Commit() }

// Rollback aborts the transaction.
func (t *Tx) Rollback() error { return t.tx.Rollback() }

// StepAtom is the set of writes that must commit as one unit when a step
// completes (SPEC: Execution model step 8).
type StepAtom struct {
	// RunID and StepID identify the completed step's result row.
	RunID  string
	StepID string
	// Result is the step's output, stored as JSON.
	Result any
	// Dedupe, when non-nil, records a dedupe_key outcome keyed by
	// (WorkflowID, StepName, Dedupe.Key).
	Dedupe *DedupeRecord
	// Dependents are the ids of the steps that depend on this one. Each is
	// decremented in the run_deps table inside the transaction; a dependent is
	// enqueued (from Downstream) only when its remaining count reaches zero
	// (ADR-0023). A dependent with no matching Downstream entry is still
	// decremented but not enqueued (for example a failed branch that must still
	// count as done).
	Dependents []string
	// Downstream are the jobs to enqueue for dependents whose count reached
	// zero, in the same transaction. Index i corresponds to the i-th entry of
	// Dependents (the enqueue is skipped if that dependent is not ready).
	Downstream []Downstream
	// Job is the claimed job to ack.
	Job *Job
	// SingerState, when non-nil, upserts the step's Singer bookmark in the same
	// transaction (SPEC: Execution model step 8). Keyed by (WorkflowID,
	// StepName); it is set for a completed singer-tap step.
	SingerState *SingerState
}

// DedupeRecord is a row in the step_dedupe table (SPEC: Idempotency).
type DedupeRecord struct {
	WorkflowID string
	StepName   string
	Key        string
	Succeeded  bool
	// Result is the prior result to return on a subsequent skip.
	Result any
}

// Downstream is one dependent's job to enqueue, paired by index with a
// Dependents entry in StepAtom. Payload is the job to enqueue; Queue is the
// queue it goes on.
type Downstream struct {
	Queue   *Queue
	Payload any
}

// CommitStepAtom writes the four parts of a step's completion in one SQLite
// transaction: the result, the dedupe record, the downstream enqueues, and the
// claim ack. If any part fails, everything rolls back. This is non-negotiable
// per the SPEC; splitting it produces silent failures.
func (s *Store) CommitStepAtom(atom StepAtom) error {
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Result.
	resultJSON, err := json.Marshal(atom.Result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	if err := tx.Exec(
		`INSERT OR REPLACE INTO step_results (run_id, step_id, result) VALUES (?, ?, ?)`,
		atom.RunID, atom.StepID, string(resultJSON),
	); err != nil {
		return fmt.Errorf("write result: %w", err)
	}

	// 2. Dedupe record.
	if atom.Dedupe != nil {
		dedupeJSON, err := json.Marshal(atom.Dedupe.Result)
		if err != nil {
			return fmt.Errorf("marshal dedupe result: %w", err)
		}
		succeeded := 0
		if atom.Dedupe.Succeeded {
			succeeded = 1
		}
		if err := tx.Exec(
			`INSERT OR REPLACE INTO step_dedupe (workflow_id, step_name, dedupe_key, succeeded, result)
			 VALUES (?, ?, ?, ?, ?)`,
			atom.Dedupe.WorkflowID, atom.Dedupe.StepName, atom.Dedupe.Key, succeeded, string(dedupeJSON),
		); err != nil {
			return fmt.Errorf("write dedupe record: %w", err)
		}
	}

	// 3. Dependency fan-out (ADR-0023): decrement each dependent's count and
	// enqueue only those whose count reaches zero, all in this transaction.
	ready := map[string]bool{}
	if len(atom.Dependents) > 0 {
		ids, err := tx.decrementDependents(atom.RunID, atom.Dependents)
		if err != nil {
			return err
		}
		for _, id := range ids {
			ready[id] = true
		}
	}
	enqueued := 0
	for i, d := range atom.Downstream {
		if len(atom.Dependents) > i && !ready[atom.Dependents[i]] {
			// This dependent's other dependencies are not yet satisfied; do not
			// enqueue it now.
			continue
		}
		if err := tx.Enqueue(d.Queue, d.Payload); err != nil {
			return fmt.Errorf("enqueue downstream: %w", err)
		}
		enqueued++
	}
	// Track in-flight jobs (ADR-0023): each ack of a claim removes one pending
	// job; each enqueued dependent adds one. The run completes when pending
	// reaches zero.
	if atom.Job != nil {
		enqueued--
	}
	if enqueued != 0 {
		if err := tx.AdjustPending(atom.RunID, enqueued); err != nil {
			return err
		}
	}

	// 3b. Singer bookmark. Written here, in the same transaction, so a tap's
	// result and its next bookmark commit (or roll back) together; a crash
	// between the two would re-emit records on the next run (SPEC: Idempotency).
	if atom.SingerState != nil {
		stateJSON, err := json.Marshal(atom.SingerState.State)
		if err != nil {
			return fmt.Errorf("marshal singer state: %w", err)
		}
		if err := tx.Exec(
			`INSERT OR REPLACE INTO singer_state (workflow_id, step_name, state) VALUES (?, ?, ?)`,
			atom.SingerState.WorkflowID, atom.SingerState.StepName, string(stateJSON),
		); err != nil {
			return fmt.Errorf("write singer state: %w", err)
		}
	}

	// 4. Claim ack.
	if atom.Job != nil {
		if err := tx.Ack(atom.Job); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// DedupeOutcome is the stored result for a dedupe key.
type DedupeOutcome struct {
	Succeeded bool
	Result    any
}

// LookupDedupe returns the stored outcome for a dedupe key, or (nil, nil) when
// the key has never been recorded. A prior successful run means the step
// should be skipped (its prior result returned); a prior failed run means it
// proceeds (SPEC: Idempotency).
func (s *Store) LookupDedupe(workflowID, stepName, key string) (*DedupeOutcome, error) {
	var succeeded int
	var result string
	err := s.db.Raw().QueryRow(
		`SELECT succeeded, result FROM step_dedupe WHERE workflow_id = ? AND step_name = ? AND dedupe_key = ?`,
		workflowID, stepName, key,
	).Scan(&succeeded, &result)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	out := &DedupeOutcome{Succeeded: succeeded != 0}
	if result != "" {
		var v any
		if uerr := json.Unmarshal([]byte(result), &v); uerr == nil {
			out.Result = v
		}
	}
	return out, nil
}

// ResultJSON returns the stored result JSON for a completed step, or "" when
// no result row exists. It is how a downstream step or the run inspector reads
// a prior step's output.
func (s *Store) ResultJSON(runID, stepID string) (string, error) {
	var r string
	err := s.db.Raw().QueryRow(
		`SELECT result FROM step_results WHERE run_id = ? AND step_id = ?`,
		runID, stepID,
	).Scan(&r)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return r, nil
}

// Result returns the decoded result for a completed step, or nil when no result
// row exists. It is how a rejoin step reads a foreach body's iteration results
// to assemble the collected array (ADR-0024).
func (s *Store) Result(runID, stepID string) (any, error) {
	raw, err := s.ResultJSON(runID, stepID)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// StepResultCount returns the number of step result rows. It is how tests (and
// later the run inspector) observe that runs have executed.
func (s *Store) StepResultCount() (int, error) {
	var n int
	if err := s.db.Raw().QueryRow(`SELECT COUNT(*) FROM step_results`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ensureSchema creates the runner's business tables on the store's single
// connection. It runs at Open time so the tables always exist.
func (s *Store) ensureSchema() error {
	for _, stmt := range schemaStmts {
		if _, err := s.db.Raw().Exec(stmt); err != nil {
			return fmt.Errorf("honker: ensure schema: %w", err)
		}
	}
	return nil
}

var schemaStmts = []string{
	`CREATE TABLE IF NOT EXISTS step_results (
		run_id  TEXT NOT NULL,
		step_id TEXT NOT NULL,
		result  TEXT NOT NULL,
		PRIMARY KEY (run_id, step_id)
	)`,
	`CREATE TABLE IF NOT EXISTS step_dedupe (
		workflow_id TEXT NOT NULL,
		step_name   TEXT NOT NULL,
		dedupe_key  TEXT NOT NULL,
		succeeded   INTEGER NOT NULL,
		result      TEXT,
		created_at  TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (workflow_id, step_name, dedupe_key)
	)`,
	`CREATE TABLE IF NOT EXISTS workflows (
		name       TEXT PRIMARY KEY,
		wafer      TEXT NOT NULL,
		enabled    INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS events (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		source      TEXT NOT NULL,
		payload     TEXT NOT NULL,
		received_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS runs (
		run_id        TEXT PRIMARY KEY,
		workflow_name TEXT NOT NULL,
		status        TEXT NOT NULL,
		pending       INTEGER NOT NULL DEFAULT 0,
		created_at    TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS singer_state (
		workflow_id TEXT NOT NULL,
		step_name   TEXT NOT NULL,
		state       TEXT NOT NULL,
		updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (workflow_id, step_name)
	)`,
	`CREATE TABLE IF NOT EXISTS run_deps (
		run_id   TEXT NOT NULL,
		step_id  TEXT NOT NULL,
		remaining INTEGER NOT NULL,
		PRIMARY KEY (run_id, step_id)
	)`,
}

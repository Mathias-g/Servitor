package honker

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	hg "github.com/russellromney/honker-go"
)

// Tx is a single SQLite transaction. It exists to compose the SPEC's
// transactional atom: a node's result, its dedupe record, its downstream
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

// EnqueueAt enqueues a one-shot job that is claimable at the given absolute
// unix epoch (ADR-0043). Honker's queue delays the job until runAt; the worker's
// claim loop sleeps until the soonest future RunAt and wakes then. It commits
// (or rolls back) with the rest of the transaction.
func (t *Tx) EnqueueAt(queue *Queue, payload any, runAt int64) error {
	_, err := queue.q.EnqueueTx(t.tx, payload, hg.EnqueueOptions{RunAt: &runAt})
	return err
}

// WithTx runs fn inside a transaction and commits it, rolling back if fn
// returns an error. It is how a caller composes a set of writes that must
// commit (or roll back) together, for example the park/resume of a run
// (SPEC: Execution model step 11).
func (s *Store) WithTx(fn func(tx *Tx) error) error {
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// WriteResult writes a node's result row inside this transaction.
func (t *Tx) WriteResult(runID, nodeID string, result any) error {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	return t.Exec(
		`INSERT OR REPLACE INTO node_results (run_id, node_id, result) VALUES (?, ?, ?)`,
		runID, nodeID, string(resultJSON),
	)
}

// FanOut performs the dependency fan-out inside this transaction (ADR-0023):
// it decrements each dependent's remaining count and enqueues only those whose
// count reached zero, aligning each downstream job by index with its dependent.
// It returns the number of jobs enqueued, which the caller uses to adjust the
// run's pending count. This is the same fan-out the completion atom uses,
// factored out so the resume path can share it.
func (t *Tx) FanOut(runID string, dependents []string, downstream []Downstream) (int, error) {
	ready := map[string]bool{}
	if len(dependents) > 0 {
		ids, err := t.decrementDependents(runID, dependents)
		if err != nil {
			return 0, err
		}
		for _, id := range ids {
			ready[id] = true
		}
	}
	enqueued := 0
	for i, d := range downstream {
		if len(dependents) > i && !ready[dependents[i]] {
			continue
		}
		if err := t.Enqueue(d.Queue, d.Payload); err != nil {
			return 0, fmt.Errorf("enqueue downstream: %w", err)
		}
		enqueued++
	}
	return enqueued, nil
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

// NodeAtom is the set of writes that must commit as one unit when a node
// completes (SPEC: Execution model step 8).
type NodeAtom struct {
	// RunID and NodeID identify the completed node's result row.
	RunID  string
	NodeID string
	// Result is the node's output, stored as JSON.
	Result any
	// Dedupe, when non-nil, records a dedupe_key outcome keyed by
	// (WorkflowID, NodeName, Dedupe.Key).
	Dedupe *DedupeRecord
	// Dependents are the ids of the nodes that depend on this one. Each is
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
	// SingerState, when non-nil, upserts the node's Singer bookmark in the same
	// transaction (SPEC: Execution model step 8). Keyed by (WorkflowID,
	// NodeName); it is set for a completed singer-tap node.
	SingerState *SingerState
}

// DedupeRecord is a row in the node_dedupe table (SPEC: Idempotency).
type DedupeRecord struct {
	WorkflowID string
	NodeName   string
	Key        string
	Succeeded  bool
	// Result is the prior result to return on a subsequent skip.
	Result any
}

// Downstream is one dependent's job to enqueue, paired by index with a
// Dependents entry in NodeAtom. Payload is the job to enqueue; Queue is the
// queue it goes on.
type Downstream struct {
	Queue   *Queue
	Payload any
}

// CommitNodeAtom writes the four parts of a node's completion in one SQLite
// transaction: the result, the dedupe record, the downstream enqueues, and the
// claim ack. If any part fails, everything rolls back. This is non-negotiable
// per the SPEC; splitting it produces silent failures.
func (s *Store) CommitNodeAtom(atom NodeAtom) error {
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
		`INSERT OR REPLACE INTO node_results (run_id, node_id, result) VALUES (?, ?, ?)`,
		atom.RunID, atom.NodeID, string(resultJSON),
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
			`INSERT OR REPLACE INTO node_dedupe (workflow_id, node_name, dedupe_key, succeeded, result)
			 VALUES (?, ?, ?, ?, ?)`,
			atom.Dedupe.WorkflowID, atom.Dedupe.NodeName, atom.Dedupe.Key, succeeded, string(dedupeJSON),
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
			`INSERT OR REPLACE INTO singer_state (workflow_id, node_name, state) VALUES (?, ?, ?)`,
			atom.SingerState.WorkflowID, atom.SingerState.NodeName, string(stateJSON),
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
// the key has never been recorded. A prior successful run means the node
// should be skipped (its prior result returned); a prior failed run means it
// proceeds (SPEC: Idempotency).
func (s *Store) LookupDedupe(workflowID, nodeName, key string) (*DedupeOutcome, error) {
	var succeeded int
	var result string
	err := s.db.Raw().QueryRow(
		`SELECT succeeded, result FROM node_dedupe WHERE workflow_id = ? AND node_name = ? AND dedupe_key = ?`,
		workflowID, nodeName, key,
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

// ResultJSON returns the stored result JSON for a completed node, or "" when
// no result row exists. It is how a downstream node or the run inspector reads
// a prior node's output.
func (s *Store) ResultJSON(runID, nodeID string) (string, error) {
	var r string
	err := s.db.Raw().QueryRow(
		`SELECT result FROM node_results WHERE run_id = ? AND node_id = ?`,
		runID, nodeID,
	).Scan(&r)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return r, nil
}

// Result returns the decoded result for a completed node, or nil when no result
// row exists. It is how a rejoin node reads a foreach body's iteration results
// to assemble the collected array (ADR-0024).
func (s *Store) Result(runID, nodeID string) (any, error) {
	raw, err := s.ResultJSON(runID, nodeID)
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

// NodeResultCount returns the number of node result rows. It is how tests (and
// later the run inspector) observe that runs have executed.
func (s *Store) NodeResultCount() (int, error) {
	var n int
	if err := s.db.Raw().QueryRow(`SELECT COUNT(*) FROM node_results`).Scan(&n); err != nil {
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
	`CREATE TABLE IF NOT EXISTS node_results (
		run_id  TEXT NOT NULL,
		node_id TEXT NOT NULL,
		result  TEXT NOT NULL,
		PRIMARY KEY (run_id, node_id)
	)`,
	`CREATE TABLE IF NOT EXISTS node_dedupe (
		workflow_id TEXT NOT NULL,
		node_name   TEXT NOT NULL,
		dedupe_key  TEXT NOT NULL,
		succeeded   INTEGER NOT NULL,
		result      TEXT,
		created_at  TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (workflow_id, node_name, dedupe_key)
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
		node_name   TEXT NOT NULL,
		state       TEXT NOT NULL,
		updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (workflow_id, node_name)
	)`,
	`CREATE TABLE IF NOT EXISTS run_deps (
		run_id   TEXT NOT NULL,
		node_id  TEXT NOT NULL,
		remaining INTEGER NOT NULL,
		PRIMARY KEY (run_id, node_id)
	)`,
	`CREATE TABLE IF NOT EXISTS suspended_continuations (
		run_id       TEXT PRIMARY KEY,
		workflow_id  TEXT NOT NULL,
		signal_name  TEXT,
		run_at       INTEGER,
		payload      TEXT NOT NULL,
		created_at   TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS buffered_signals (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		signal_name TEXT NOT NULL,
		payload     TEXT NOT NULL,
		created_at  TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
}

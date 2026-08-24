package honker

import (
	"database/sql"
	"errors"
	"fmt"
)

// Workflow is one registered workflow in the runner's store. The Wafer YAML
// is the artifact (SPEC: The Wafer); registration stores a faithful copy so
// the daemon can match events against its triggers and build runs, without a
// separate mutable source of truth.
type Workflow struct {
	// Name is the workflow's name, also the registry key.
	Name string
	// Wafer is the submitted Wafer, as YAML.
	Wafer string
	// Enabled is whether the workflow's triggers may fire runs.
	Enabled bool
}

// RegisterWorkflow inserts or replaces a workflow definition. Replace is used
// by both `submit` (first registration) and `update` (replace). Registration
// never changes `enabled`; enabling is a separate, explicit operation.
func (s *Store) RegisterWorkflow(name, wafer string) error {
	if name == "" {
		return fmt.Errorf("honker: register workflow: empty name")
	}
	_, err := s.db.Raw().Exec(
		`INSERT INTO workflows (name, wafer) VALUES (?, ?)
		 ON CONFLICT(name) DO UPDATE SET wafer = excluded.wafer`,
		name, wafer,
	)
	if err != nil {
		return fmt.Errorf("honker: register workflow %s: %w", name, err)
	}
	return nil
}

// SetWorkflowEnabled enables or disables a workflow's triggers. Disabling
// leaves the definition registered but stops it from firing.
func (s *Store) SetWorkflowEnabled(name string, enabled bool) error {
	n := 0
	if enabled {
		n = 1
	}
	res, err := s.db.Raw().Exec(`UPDATE workflows SET enabled = ? WHERE name = ?`, n, name)
	if err != nil {
		return fmt.Errorf("honker: set workflow %s enabled: %w", name, err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return fmt.Errorf("honker: workflow %s is not registered", name)
	}
	return nil
}

// GetWorkflow returns a registered workflow, or (nil, nil) when it is not
// registered.
func (s *Store) GetWorkflow(name string) (*Workflow, error) {
	var enabled int
	var wafer string
	err := s.db.Raw().QueryRow(
		`SELECT wafer, enabled FROM workflows WHERE name = ?`, name,
	).Scan(&wafer, &enabled)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &Workflow{Name: name, Wafer: wafer, Enabled: enabled != 0}, nil
}

// ListWorkflows returns all registered workflows.
func (s *Store) ListWorkflows() ([]Workflow, error) {
	rows, err := s.db.Raw().Query(`SELECT name, wafer, enabled FROM workflows`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Workflow
	for rows.Next() {
		var w Workflow
		var enabled int
		if err := rows.Scan(&w.Name, &w.Wafer, &enabled); err != nil {
			return nil, err
		}
		w.Enabled = enabled != 0
		out = append(out, w)
	}
	return out, rows.Err()
}

// AppendEvent persists a raw inbound event before any matching or verification
// happens (SPEC: Execution model step 2). source identifies the receiver path
// or trigger that produced it; payload is the raw JSON body. It returns the
// event's id.
func (s *Store) AppendEvent(source, payload string) (int64, error) {
	res, err := s.db.Raw().Exec(
		`INSERT INTO events (source, payload) VALUES (?, ?)`, source, payload,
	)
	if err != nil {
		return 0, fmt.Errorf("honker: append event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// EventCount returns the number of persisted events. It is how the trigger
// receiver's persistence (SPEC step 2) is asserted.
func (s *Store) EventCount() (int, error) {
	var n int
	if err := s.db.Raw().QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

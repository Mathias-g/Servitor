package honker

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// SingerState is a tap's bookmark (SPEC: Singer, State persistence). It is
// written as part of a completed tap step's transactional atom and read back
// before the next invocation of the same step.
type SingerState struct {
	// WorkflowID and StepName key the bookmark: the same tap step in the same
	// workflow reuses its own bookmark across runs.
	WorkflowID string
	StepName   string
	// State is the bookmark value (the tap's STATE message), as JSON.
	State any
}

// GetSingerState returns the stored bookmark for a tap step, or (nil, nil) when
// none has been recorded (the first invocation). It is how the worker feeds the
// prior state into the next tap run (SPEC: Singer, State persistence).
func (s *Store) GetSingerState(workflowID, stepName string) (any, error) {
	var raw string
	err := s.db.Raw().QueryRow(
		`SELECT state FROM singer_state WHERE workflow_id = ? AND step_name = ?`,
		workflowID, stepName,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var v any
	if uerr := json.Unmarshal([]byte(raw), &v); uerr != nil {
		return nil, uerr
	}
	return v, nil
}

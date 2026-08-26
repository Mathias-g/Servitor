package honker

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// SingerState is a tap's bookmark (SPEC: Singer, State persistence). It is
// written as part of a completed tap node's transactional atom and read back
// before the next invocation of the same node.
type SingerState struct {
	// WorkflowID and NodeName key the bookmark: the same tap node in the same
	// workflow reuses its own bookmark across runs.
	WorkflowID string
	NodeName   string
	// State is the bookmark value (the tap's STATE message), as JSON.
	State any
}

// GetSingerState returns the stored bookmark for a tap node, or (nil, nil) when
// none has been recorded (the first invocation). It is how the worker feeds the
// prior state into the next tap run (SPEC: Singer, State persistence).
func (s *Store) GetSingerState(workflowID, nodeName string) (any, error) {
	var raw string
	err := s.db.Raw().QueryRow(
		`SELECT state FROM singer_state WHERE workflow_id = ? AND node_name = ?`,
		workflowID, nodeName,
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

package wafer

// DryRunResult is the outcome of a dry-run: the structured validation result
// plus, when the Wafer is structurally valid, the resolved DAG the runner
// would execute. Nothing is run, contacted, or persisted (SPEC: Phase 4 /
// dry-run).
type DryRunResult struct {
	Result Result `json:"result"`
	// DAG is the resolved run order, or null when validation found blocking
	// errors.
	DAG *DAG `json:"dag"`
}

// DryRun validates YAML bytes and resolves the workflow's dependency DAG
// without executing, contacting, or persisting anything.
func DryRun(data []byte) DryRunResult {
	res := Validate(data)
	out := DryRunResult{Result: res}
	if !res.Valid() {
		return out
	}
	w, err := Parse(data)
	if err != nil {
		// Validate already reported parse problems; treat as not resolvable.
		return out
	}
	dag, issues := ResolveDAG(w)
	if len(issues) > 0 {
		res.Errors = append(res.Errors, issues...)
		out.Result = res
		return out
	}
	out.DAG = &dag
	return out
}

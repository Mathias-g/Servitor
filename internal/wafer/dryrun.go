package wafer

import (
	"os"
)

// DryRunResult is the outcome of a dry-run: the structured validation result
// plus, when the Wafer is structurally valid, the resolved DAG the runner
// would execute. Nothing is run, contacted, or persisted (SPEC: Phase 4 /
// dry-run).
type DryRunResult struct {
	Result Result `json:"result"`
	// Name is the workflow name, present when the Wafer parsed.
	Name string `json:"name,omitempty"`
	// Triggers are the workflow's triggers, present when the Wafer parsed.
	Triggers []Trigger `json:"triggers,omitempty"`
	// DAG is the resolved run order, or null when validation found blocking
	// errors.
	DAG *DAG `json:"dag"`
	// Secrets lists the distinct secret names the workflow's nodes declare,
	// redacted (names only, never values). A declared secret missing from the
	// environment is reported as a warning in Result (SPEC: dry-run confirms
	// secrets resolve).
	Secrets []string `json:"secrets,omitempty"`
}

// DryRun validates YAML bytes and resolves the workflow's dependency DAG
// without executing, contacting, or persisting anything. When valid it also
// collects the workflow's declared secret names and warns on any that are not
// present in the current environment.
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
	out.Name = w.Name
	out.Triggers = w.On
	dag, issues := ResolveDAG(w)
	if len(issues) > 0 {
		res.Errors = append(res.Errors, issues...)
		out.Result = res
		return out
	}
	out.DAG = &dag

	out.Secrets = declaredSecretNames(w)
	for _, s := range out.Secrets {
		if os.Getenv(s) == "" {
			res.Warnings = append(res.Warnings, Issue{
				Path:    "/nodes",
				Code:    codeMissingSecret,
				Message: "declared secret \"" + s + "\" is not present in the environment",
			})
		}
	}
	out.Result = res
	return out
}

// declaredSecretNames returns the distinct secret names declared across a
// workflow's nodes, in first-use order.
func declaredSecretNames(w *Wafer) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range w.Nodes {
		for _, name := range s.Secrets {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

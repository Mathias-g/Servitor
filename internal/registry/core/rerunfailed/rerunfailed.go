// Package rerunfailed registers the `rerun-failed` action mechanism: re-run a
// dead-lettered (failed) run (ADR-0044, ADR-0048). It is part of Servitor's
// spec, not a mechanism, so this package is never removed.
package rerunfailed

import "github.com/Mathias-g/Servitor/internal/registry"

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "rerun-failed",
	Desc:           "Re-run a dead-lettered (failed) run, continuing from the failed node, restarting from the top, or discarding it (ADR-0044).",
	Role:           registry.RoleAction,
	SideEffect:     false,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"run_id": {Type: "string", Desc: "A JSONata expression over the step's `{event, steps}` input resolving to the id of the failed run to re-run. Defaults to `event.from_run` (the run whose `failed` trigger started this workflow).", Examples: []any{"event.from_run"}},
		"mode":   {Type: "string", Desc: "How to re-run: `continue` (from the failed node), `restart` (from the top), or `discard` (drop the failed run). Default `continue`.", Examples: []any{"continue"}},
	},
	RunKind: registry.RunFlow,
}

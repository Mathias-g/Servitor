// Package core registers Servitor's own universal primitives and scheduling
// capabilities: the `core` mechanism group (ADR-0031, ADR-0045). These are
// part of Servitor's spec, not integrations, so this package is never removed.
package core

import (
	"fmt"
	"os"

	"github.com/Mathias-g/Servitor/internal/registry"
)

func init() {
	registry.Register(http)
	registry.Register(shell)
	registry.Register(transform)
	registry.Register(switchNode)
	registry.Register(foreachNode)
	registry.Register(waitNode)
	registry.Register(sendSignal)
	registry.Register(rerunFailed)
	registry.Register(cron)
	registry.Register(manual)
	registry.Register(completed)
	registry.Register(failed)
}

// selfExe returns the path to the running servitor binary, used to re-invoke it
// for the pure-computation subprocess commands (`__transform`, `__switch`,
// `__foreach`) so even those stay out of the runner's process (ADR-0008).
func selfExe() string {
	exe, err := os.Executable()
	if err != nil {
		return "servitor"
	}
	return exe
}

var http = &registry.Capability{
	Name:           "http",
	Desc:           "Make an HTTP request and capture the response.",
	Role:           registry.RoleAction,
	SideEffect:     true,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"url":     {Type: "string", Required: true, Desc: "The URL to request.", Examples: []any{"https://api.example.com/things"}},
		"method":  {Type: "string", Required: true, Desc: "HTTP method.", Examples: []any{"GET"}},
		"headers": {Type: "object", Desc: "Request headers as a map.", Examples: []any{map[string]any{"Content-Type": "application/json"}}},
		"body":    {Type: "any", Desc: "Request body.", Examples: []any{map[string]any{"query": "SELECT * FROM things"}}},
		"timeout": {Type: "integer", Desc: "Request timeout in seconds.", Examples: []any{30}},
	},
	RunKind: registry.RunPlain,
}

var shell = &registry.Capability{
	Name:           "shell",
	Desc:           "Execute a command on the runner host.",
	Role:           registry.RoleAction,
	SideEffect:     true,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"command": {Type: "string", Required: true, Desc: "The command to run.", Examples: []any{"echo hello"}},
	},
	RunKind: registry.RunPlain,
	Spawn: func(cfg map[string]any) ([]string, error) {
		cmd, ok := cfg["command"].(string)
		if !ok || cmd == "" {
			return nil, fmt.Errorf("shell requires a string command")
		}
		return []string{"/bin/sh", "-c", cmd}, nil
	},
}

var transform = &registry.Capability{
	Name:           "transform",
	Desc:           "Reshape, extract, or compute over previous nodes' JSON output.",
	Role:           registry.RoleAction,
	SideEffect:     false,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"expression": {Type: "string", Required: true, Desc: "A JSONata expression over the step's `{event, steps}` input (ADR-0020, ADR-0021).", Examples: []any{"$sum(steps.fetch.items[active=true].amount)"}},
	},
	RunKind: registry.RunPlain,
	Spawn: func(cfg map[string]any) ([]string, error) {
		expr, ok := cfg["expression"].(string)
		if !ok || expr == "" {
			return nil, fmt.Errorf("transform requires a string expression")
		}
		return []string{selfExe(), "__transform", expr}, nil
	},
}

var switchNode = &registry.Capability{
	Name:           "switch",
	Desc:           "Route to one named branch based on a value.",
	Role:           registry.RoleFlow,
	SideEffect:     false,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"expression": {Type: "string", Required: true, Desc: "A JSONata expression over the step's `{event, steps}` input producing the routing value (ADR-0020, ADR-0022).", Examples: []any{"steps.check"}},
		"cases":      {Type: "object", Required: true, Desc: "Map of value to the name of the top-level node to route to.", Examples: []any{map[string]any{"high": "notify_finance", "low": "log_and_done"}}},
		"default":    {Type: "string", Desc: "Name of the top-level node to route to when no `cases` key matches.", Examples: []any{"log_unknown"}},
	},
	RunKind: registry.RunFlow,
	Spawn: func(cfg map[string]any) ([]string, error) {
		expr, ok := cfg["expression"].(string)
		if !ok || expr == "" {
			return nil, fmt.Errorf("switch requires a string expression")
		}
		return []string{selfExe(), "__switch", expr}, nil
	},
}

var foreachNode = &registry.Capability{
	Name:           "foreach",
	Desc:           "Fan a node out over a list.",
	Role:           registry.RoleFlow,
	SideEffect:     false,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"over": {Type: "string", Required: true, Desc: "A JSONata expression over the step's `{event, steps}` input yielding the list to iterate (ADR-0020, ADR-0024).", Examples: []any{"steps.fetch_ids"}},
		"as":   {Type: "string", Desc: "Name for each element in the loop, exposed in each iteration's input. Defaults to `item`.", Examples: []any{"item"}},
		"body": {Type: "string", Required: true, Desc: "Name of the top-level node to run once per element (ADR-0024).", Examples: []any{"process_one"}},
	},
	RunKind: registry.RunFlow,
	Spawn: func(cfg map[string]any) ([]string, error) {
		expr, ok := cfg["over"].(string)
		if !ok || expr == "" {
			return nil, fmt.Errorf("foreach requires a string `over` expression")
		}
		return []string{selfExe(), "__foreach", expr}, nil
	},
}

var waitNode = &registry.Capability{
	Name:           "wait",
	Desc:           "Park the run and resume it later, via a timer or a named signal, resolving on whichever fires first (ADR-0041).",
	Role:           registry.RoleFlow,
	SideEffect:     false,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"signal": {Type: "string", Desc: "A JSONata expression over the step's `{event, steps}` input resolved at park time to the effective signal name (ADR-0042). A signal resumes the run with the payload; the result reports `source: \"signal\"`.", Examples: []any{"approval_gate.${event.order_id}"}},
		"timer":  {Type: "object", Desc: "A timer that resumes the run: `after` (a duration, for example `48h`) or `at` (an absolute time). The result reports `source: \"timer\"` (ADR-0043).", Examples: []any{map[string]any{"after": "48h"}}},
	},
	RunKind: registry.RunFlow,
}

var sendSignal = &registry.Capability{
	Name:           "send-signal",
	Desc:           "Wake a parked run in another workflow by named signal (ADR-0042).",
	Role:           registry.RoleAction,
	SideEffect:     false,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"signal":  {Type: "string", Required: true, Desc: "A JSONata expression over the step's `{event, steps}` input resolving to the effective signal name of the parked run to wake.", Examples: []any{"approval_gate.${event.order_id}"}},
		"payload": {Type: "any", Desc: "A JSONata expression for the payload delivered to the parked run's wait node result. Omit for no payload.", Examples: []any{map[string]any{"approved": true}}},
	},
	RunKind: registry.RunFlow,
}

var rerunFailed = &registry.Capability{
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

var cron = &registry.Capability{
	Name:           "cron",
	Desc:           "Run on the Honker scheduler.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryScheduled,
	MechanismGroup: registry.Core,
	Fields:         map[string]*registry.Field{"schedule": {Type: "string", Required: true, Desc: "Cron expression.", Examples: []any{"0 * * * *"}}},
}

var manual = &registry.Capability{
	Name:           "manual",
	Desc:           "Invoked via the CLI.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryManual,
	MechanismGroup: registry.Core,
	Fields:         map[string]*registry.Field{},
}

var completed = &registry.Capability{
	Name:           "completed",
	Desc:           "Fired by another workflow's completion.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryEvent,
	MechanismGroup: registry.Core,
	Fields:         map[string]*registry.Field{"workflow": {Type: "string", Required: true, Desc: "The workflow that fires this.", Examples: []any{"upstream-workflow"}}},
}

var failed = &registry.Capability{
	Name:           "failed",
	Desc:           "Fired by another workflow's failure.",
	Role:           registry.RoleTrigger,
	Delivery:       registry.DeliveryEvent,
	MechanismGroup: registry.Core,
	Fields:         map[string]*registry.Field{"workflow": {Type: "string", Required: true, Desc: "The workflow that fires this.", Examples: []any{"upstream-workflow"}}},
}

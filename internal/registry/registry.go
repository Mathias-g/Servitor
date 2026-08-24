// Package registry holds the set of step types and trigger types the runner
// knows about, with a stable JSON Schema per type. This is the per-server
// authoritative set that `servitor capabilities` reports (SPEC: How an agent
// discovers integrations). The field metadata here is the single source of
// truth: validation and the emitted JSON Schema both derive from it, so they
// cannot drift apart.
package registry

import (
	"sort"
)

// Field describes one config field of a step or trigger type.
type Field struct {
	// Type is one of: string, integer, number, boolean, object, array, any.
	Type string
	// Required marks a field the author must supply.
	Required bool
	// Desc is a plain-language description surfaced to agents.
	Desc string
	// Examples are sample values rendered by the schema-to-example generator
	// (Phase 3). They version alongside the field's type.
	Examples []any
}

// StepType describes one step type (for example `http` or `singer-tap`).
type StepType struct {
	// Name is the step type as it appears in a Wafer's `type:` field.
	Name string
	// Desc is a plain-language description.
	Desc string
	// SideEffect is true when the step performs externally-visible actions,
	// which is what the missing_dedupe_key warning keys off (SPEC: Idempotency).
	SideEffect bool
	// Fields is the step's config schema, keyed by field name.
	Fields map[string]*Field
}

// TriggerType describes one trigger type (for example `cron` or `http_webhook`).
type TriggerType struct {
	Name   string
	Desc   string
	Fields map[string]*Field
}

var stepTypes = []*StepType{
	{
		Name:       "http",
		Desc:       "Make an HTTP request and capture the response.",
		SideEffect: true,
		Fields: map[string]*Field{
			"url":     {Type: "string", Required: true, Desc: "The URL to request.", Examples: []any{"https://api.example.com/things"}},
			"method":  {Type: "string", Required: true, Desc: "HTTP method.", Examples: []any{"GET"}},
			"headers": {Type: "object", Desc: "Request headers as a map."},
			"body":    {Type: "any", Desc: "Request body."},
			"timeout": {Type: "integer", Desc: "Request timeout in seconds.", Examples: []any{30}},
		},
	},
	{
		Name:       "shell",
		Desc:       "Execute a command on the runner host.",
		SideEffect: true,
		Fields: map[string]*Field{
			"command": {Type: "string", Required: true, Desc: "The command to run.", Examples: []any{"echo hello"}},
		},
	},
	{
		Name:       "transform",
		Desc:       "Reshape, extract, or compute over previous steps' JSON output.",
		SideEffect: false,
		Fields: map[string]*Field{
			"expression": {Type: "string", Required: true, Desc: "An expression over the input.", Examples: []any{"input.items | map(select(.active))"}},
		},
	},
	{
		Name:       "branch",
		Desc:       "Conditionally route based on inputs.",
		SideEffect: false,
		Fields: map[string]*Field{
			"when": {Type: "string", Required: true, Desc: "A condition over the input.", Examples: []any{"input.status == 'ok'"}},
		},
	},
	{
		Name:       "foreach",
		Desc:       "Fan a step out over a list.",
		SideEffect: false,
		Fields: map[string]*Field{
			"over": {Type: "array", Required: true, Desc: "The list to iterate over.", Examples: []any{[]any{"a", "b", "c"}}},
			"as":   {Type: "string", Desc: "Name for each element in the loop.", Examples: []any{"item"}},
		},
	},
	{
		Name:       "singer-tap",
		Desc:       "Run a Singer tap and capture records and state.",
		SideEffect: true,
		Fields: map[string]*Field{
			"tap":     {Type: "string", Required: true, Desc: "The tap to run.", Examples: []any{"tap-stripe"}},
			"config":  {Type: "object", Desc: "Tap config."},
			"streams": {Type: "array", Desc: "Streams to sync.", Examples: []any{[]any{"customers"}}},
		},
	},
	{
		Name:       "singer-target",
		Desc:       "Run a Singer target consuming records.",
		SideEffect: true,
		Fields: map[string]*Field{
			"target": {Type: "string", Required: true, Desc: "The target to run.", Examples: []any{"target-grist"}},
			"config": {Type: "object", Desc: "Target config."},
		},
	},
}

var triggerTypes = []*TriggerType{
	{Name: "http_webhook", Desc: "Generic inbound HTTP receiver with configurable HMAC verification.", Fields: map[string]*Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/things"}}}},
	{Name: "standard_webhook", Desc: "Standard Webhooks-compliant receiver.", Fields: map[string]*Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/std"}}}},
	{Name: "grist_webhook", Desc: "Grist-specific receiver.", Fields: map[string]*Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/grist"}}}},
	{Name: "github_webhook", Desc: "GitHub-specific receiver.", Fields: map[string]*Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/github"}}}},
	{Name: "slack_event", Desc: "Slack events (messages, mentions).", Fields: map[string]*Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/slack"}}}},
	{Name: "atomic_event", Desc: "Atomic knowledge-base changes.", Fields: map[string]*Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/atomic"}}}},
	{Name: "email_received", Desc: "Inbound email parsed into a structured payload.", Fields: map[string]*Field{}},
	{Name: "cron", Desc: "Run on the Honker scheduler.", Fields: map[string]*Field{"schedule": {Type: "string", Required: true, Desc: "Cron expression.", Examples: []any{"0 * * * *"}}}},
	{Name: "manual", Desc: "Invoked via the CLI.", Fields: map[string]*Field{}},
	{Name: "internal", Desc: "Fired by another workflow's completion.", Fields: map[string]*Field{"workflow": {Type: "string", Required: true, Desc: "The workflow that fires this.", Examples: []any{"upstream-workflow"}}}},
}

// StepTypes returns the registered step types, sorted by name.
func StepTypes() []*StepType {
	return sortedStepTypes()
}

// TriggerTypes returns the registered trigger types, sorted by name.
func TriggerTypes() []*TriggerType {
	return sortedTriggerTypes()
}

// LookupStep returns the step type with the given name, or nil.
func LookupStep(name string) *StepType {
	for _, st := range stepTypes {
		if st.Name == name {
			return st
		}
	}
	return nil
}

// LookupTrigger returns the trigger type with the given name, or nil.
func LookupTrigger(name string) *TriggerType {
	for _, tt := range triggerTypes {
		if tt.Name == name {
			return tt
		}
	}
	return nil
}

func sortedStepTypes() []*StepType {
	out := make([]*StepType, len(stepTypes))
	copy(out, stepTypes)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedTriggerTypes() []*TriggerType {
	out := make([]*TriggerType, len(triggerTypes))
	copy(out, triggerTypes)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

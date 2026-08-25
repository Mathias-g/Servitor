// Package registry holds the set of step types and trigger types the runner
// knows about, with a stable JSON Schema per type. This is the per-server
// authoritative set that `servitor capabilities` reports (SPEC: How an agent
// discovers integrations). The field metadata here is the single source of
// truth: validation and the emitted JSON Schema both derive from it, so they
// cannot drift apart. Field `Examples` also feed the schema-to-example
// generator (SPEC: How an agent discovers integrations), so a new type should
// give every field a representative `Examples` value; otherwise its generated
// example is an empty skeleton and the agent has to guess.
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
	// Examples are sample values rendered by the schema-to-example generator.
	// They version alongside the field's type.
	Examples []any
}

// Group is the top-level mechanism a type belongs to. Mechanisms are how
// Servitor interacts with a service (ADR-0017): `core` (universal primitives
// and scheduling), `webhook` (inbound HTTP reception), `singer` (record
// streaming), `mcp` (tool invocation), `helper` (compiled-in wrappers), and
// `websocket` (inbound streaming, future). `capabilities` groups its output by
// this value, so a service reached by several mechanisms appears in several
// groups (SPEC: How an agent discovers integrations).
const (
	Core      = "core"
	Webhook   = "webhook"
	Helper    = "helper"
	Singer    = "singer"
	MCP       = "mcp"
	Websocket = "websocket"
)

// StepType describes one step type (for example `http` or `singer-tap`).
type StepType struct {
	// Name is the step type as it appears in a Wafer's `type:` field.
	Name string
	// Desc is a plain-language description.
	Desc string
	// SideEffect is true when the step performs externally-visible actions,
	// which is what the missing_dedupe_key warning keys off (SPEC: Idempotency).
	SideEffect bool
	// Group is the mechanism this step belongs to, or Core for Servitor's own
	// primitives (SPEC: What counts as an integration).
	Group string
	// Fields is the step's config schema, keyed by field name.
	Fields map[string]*Field
}

// TriggerType describes one trigger type (for example `cron` or `http_webhook`).
type TriggerType struct {
	Name string
	Desc string
	// Group is the mechanism this trigger belongs to, or Core for Servitor's
	// own triggers (SPEC: What counts as an integration).
	Group  string
	Fields map[string]*Field
}

var stepTypes = []*StepType{
	{
		Name:       "http",
		Desc:       "Make an HTTP request and capture the response.",
		SideEffect: true,
		Group:      Core,
		Fields: map[string]*Field{
			"url":     {Type: "string", Required: true, Desc: "The URL to request.", Examples: []any{"https://api.example.com/things"}},
			"method":  {Type: "string", Required: true, Desc: "HTTP method.", Examples: []any{"GET"}},
			"headers": {Type: "object", Desc: "Request headers as a map.", Examples: []any{map[string]any{"Content-Type": "application/json"}}},
			"body":    {Type: "any", Desc: "Request body.", Examples: []any{map[string]any{"query": "SELECT * FROM things"}}},
			"timeout": {Type: "integer", Desc: "Request timeout in seconds.", Examples: []any{30}},
		},
	},
	{
		Name:       "shell",
		Desc:       "Execute a command on the runner host.",
		SideEffect: true,
		Group:      Core,
		Fields: map[string]*Field{
			"command": {Type: "string", Required: true, Desc: "The command to run.", Examples: []any{"echo hello"}},
		},
	},
	{
		Name:       "transform",
		Desc:       "Reshape, extract, or compute over previous steps' JSON output.",
		SideEffect: false,
		Group:      Core,
		Fields: map[string]*Field{
			"expression": {Type: "string", Required: true, Desc: "A JSONata expression over the step's `{event, steps}` input (ADR-0020, ADR-0021).", Examples: []any{"$sum(steps.fetch.items[active=true].amount)"}},
		},
	},
	{
		Name:       "switch",
		Desc:       "Route to one named branch based on a value.",
		SideEffect: false,
		Group:      Core,
		Fields: map[string]*Field{
			"expression": {Type: "string", Required: true, Desc: "A JSONata expression over the step's `{event, steps}` input producing the routing value (ADR-0020, ADR-0022).", Examples: []any{"steps.check"}},
			"cases":      {Type: "object", Required: true, Desc: "Map of value to the name of the top-level step to route to.", Examples: []any{map[string]any{"high": "notify_finance", "low": "log_and_done"}}},
			"default":    {Type: "string", Desc: "Name of the top-level step to route to when no `cases` key matches.", Examples: []any{"log_unknown"}},
		},
	},
	{
		Name:       "foreach",
		Desc:       "Fan a step out over a list.",
		SideEffect: false,
		Group:      Core,
		Fields: map[string]*Field{
			"over": {Type: "string", Required: true, Desc: "A JSONata expression over the step's `{event, steps}` input yielding the list to iterate (ADR-0020, ADR-0024).", Examples: []any{"steps.fetch_ids"}},
			"as":   {Type: "string", Desc: "Name for each element in the loop, exposed in each iteration's input. Defaults to `item`.", Examples: []any{"item"}},
			"body": {Type: "string", Required: true, Desc: "Name of the top-level step to run once per element (ADR-0024).", Examples: []any{"process_one"}},
		},
	},
	{
		Name:       "singer-tap",
		Desc:       "Run a Singer tap and capture records and state.",
		SideEffect: true,
		Group:      Singer,
		Fields: map[string]*Field{
			"tap":     {Type: "string", Required: true, Desc: "The tap to run.", Examples: []any{"tap-stripe"}},
			"config":  {Type: "object", Desc: "Tap config."},
			"catalog": {Type: "array", Desc: "The selected streams to sync, copied from the tap's catalog in capabilities. Each entry is one stream with its schema and a `selected: true` metadata. Omit to sync all streams.", Examples: []any{[]any{map[string]any{"stream": "customers", "tap_stream_id": "customers", "schema": map[string]any{"type": "object"}, "metadata": []any{map[string]any{"breadcrumb": []any{}, "metadata": map[string]any{"selected": true}}}}}}},
		},
	},
	{
		Name:       "singer-target",
		Desc:       "Run a Singer target consuming records.",
		SideEffect: true,
		Group:      Singer,
		Fields: map[string]*Field{
			"target": {Type: "string", Required: true, Desc: "The target to run.", Examples: []any{"target-grist"}},
			"config": {Type: "object", Desc: "Target config."},
		},
	},
	{
		Name:       "mcp-call",
		Desc:       "Invoke one named tool on one named MCP server over stdio (ADR-0015).",
		SideEffect: true,
		Group:      MCP,
		Fields: map[string]*Field{
			"server": {Type: "string", Required: true, Desc: "The MCP server executable to run.", Examples: []any{"atomic-server"}},
			"tool":   {Type: "string", Required: true, Desc: "The named tool to invoke.", Examples: []any{"search"}},
			"input":  {Type: "object", Desc: "The tool arguments.", Examples: []any{map[string]any{"query": "meeting notes"}}},
			"mode":   {Type: "string", Desc: "The MCP protocol mode the server speaks, `classic` or `stateless`, copied from capabilities. Omit to probe once at run time.", Examples: []any{"stateless"}},
		},
	},
}

var triggerTypes = []*TriggerType{
	{Name: "http_webhook", Desc: "Generic inbound HTTP receiver with configurable HMAC verification.", Group: Webhook, Fields: map[string]*Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/things"}}, "secret": {Type: "string", Desc: "Secret name to verify the x-servitor-signature HMAC header with.", Examples: []any{"MY_WEBHOOK_SECRET"}}}},
	{Name: "standard_webhook", Desc: "Standard Webhooks-compliant receiver.", Group: Webhook, Fields: map[string]*Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/std"}}, "secret": {Type: "string", Desc: "Secret name to verify the Standard Webhooks signature with.", Examples: []any{"WEBHOOK_SECRET"}}}},
	{Name: "grist_webhook", Desc: "Grist-specific receiver.", Group: Webhook, Fields: map[string]*Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/grist"}}}},
	{Name: "github_webhook", Desc: "GitHub-specific receiver.", Group: Webhook, Fields: map[string]*Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/github"}}, "secret": {Type: "string", Desc: "Secret name to verify the X-Hub-Signature-256 header with.", Examples: []any{"GITHUB_WEBHOOK_SECRET"}}}},
	{Name: "slack_event", Desc: "Slack events (messages, mentions).", Group: Webhook, Fields: map[string]*Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/slack"}}, "secret": {Type: "string", Desc: "Secret name to verify the X-Slack-Signature header with.", Examples: []any{"SLACK_SIGNING_SECRET"}}}},
	{Name: "atomic_event", Desc: "Atomic knowledge-base changes.", Group: Webhook, Fields: map[string]*Field{"path": {Type: "string", Required: true, Desc: "Path to receive on.", Examples: []any{"/hooks/atomic"}}}},
	{Name: "email_received", Desc: "Inbound email parsed into a structured payload.", Group: Helper, Fields: map[string]*Field{}},
	{Name: "cron", Desc: "Run on the Honker scheduler.", Group: Core, Fields: map[string]*Field{"schedule": {Type: "string", Required: true, Desc: "Cron expression.", Examples: []any{"0 * * * *"}}}},
	{Name: "manual", Desc: "Invoked via the CLI.", Group: Core, Fields: map[string]*Field{}},
	{Name: "internal", Desc: "Fired by another workflow's completion.", Group: Core, Fields: map[string]*Field{"workflow": {Type: "string", Required: true, Desc: "The workflow that fires this.", Examples: []any{"upstream-workflow"}}}},
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

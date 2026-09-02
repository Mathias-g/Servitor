// Package registry holds the set of capabilities the runner knows about, with a
// stable JSON Schema per capability. This is the per-server authoritative set
// that `servitor capabilities` reports (SPEC: How an agent discovers
// capabilities and connectors). The field metadata here is the single source of truth:
// validation and the emitted JSON Schema both derive from it, so they cannot
// drift apart. Field `Examples` also feed the schema-to-example generator (SPEC:
// How an agent discovers capabilities and connectors), so a new capability should give every
// field a representative `Examples` value; otherwise its generated example is an
// empty skeleton and the agent has to guess.
//
// Every capability is one of three things: a trigger (starts a run, under `on:`),
// an action node (does work mid-run, under `nodes:`), or a flow node (routes or
// fans out mid-run, under `nodes:`). Triggers also carry a `Delivery` tag
// (instant/polling/scheduled/event) describing how the run is started, shown to
// agents the way Zapier labels its triggers; it is informational, not
// author-configurable. Action nodes carry `SideEffect`, which keys the
// missing_dedupe_key warning.
package registry

import (
	"fmt"
	"sort"
)

// Field describes one config field of a capability.
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

// Role is the category of thing a capability is, which determines where it may
// be used (under `on:` or `nodes:`) and how it is treated.
type Role string

const (
	// RoleTrigger means the capability starts a run, used under `on:`.
	RoleTrigger Role = "trigger"
	// RoleAction means the capability is an action node that does work
	// mid-run, used under `nodes:`.
	RoleAction Role = "action"
	// RoleFlow means the capability is a flow node that routes or fans out
	// mid-run (for example switch, foreach), used under `nodes:`.
	RoleFlow Role = "flow"
)

// Delivery describes how a trigger starts a run. It is informational
// (shown to agents like Zapier's trigger labels), not author-configurable: each
// capability is fixed as one delivery.
const (
	// DeliveryInstant means the run starts by a push (for example a webhook).
	DeliveryInstant = "instant"
	// DeliveryPolling means the run starts by polling on a schedule.
	DeliveryPolling = "polling"
	// DeliveryScheduled means the run starts on a schedule (for example cron).
	DeliveryScheduled = "scheduled"
	// DeliveryEvent means the run starts by an in-process event (for example
	// another workflow completing).
	DeliveryEvent = "event"
	// DeliveryManual means the run starts by explicit operator or agent action.
	DeliveryManual = "manual"
)

// MechanismGroup is the top-level category a type belongs to (ADR-0017): `core`
// (universal primitives and scheduling), `webhook` (inbound HTTP reception),
// `singer` (record streaming), `mcp` (tool invocation), `helper` (compiled-in
// wrappers), and `websocket` (inbound streaming, future). A mechanism group is
// a family of mechanisms; the individual types within it are the mechanisms.
// `capabilities` groups its output by this value, so a service reached by
// several mechanisms appears in several groups (SPEC: How an agent discovers
// capabilities and connectors).
const (
	Core      = "core"
	Webhook   = "webhook"
	Helper    = "helper"
	Singer    = "singer"
	MCP       = "mcp"
	Websocket = "websocket"
)

// RunKind tells the worker which execution harness runs a node of this type. It
// is set by a mechanism's package alongside its Spawn (ADR-0045); the worker
// dispatches on it instead of naming a node type in a switch.
type RunKind string

const (
	// RunPlain is the default: the node runs as a subprocess via the worker's
	// exec harness on its Spawn argv, and its JSON output is the result.
	RunPlain RunKind = "plain"
	// RunSinger means the node runs through the Singer harness (a tap or
	// target, with bookmark state), not a plain exec.
	RunSinger RunKind = "singer"
	// RunMCP means the node runs through the MCP harness (a tool call over
	// stdio), not a plain exec.
	RunMCP RunKind = "mcp"
	// RunFlow marks a control node whose execution the worker engine handles
	// directly (it routes or fans out); it has no plain subprocess result.
	RunFlow RunKind = "flow"
)

// Capability describes one capability (for example `http`, `email_received`, or
// `singer-tap`). A capability is either a trigger, an action node, or a flow
// node.
type Capability struct {
	// Name is the capability as it appears in a Wafer's `type:` field.
	Name string
	// Desc is a plain-language description.
	Desc string
	// Role is what this capability is: trigger, action, or flow. "" means
	// RoleAction (the default for a capability that does work mid-run).
	Role Role
	// SideEffect is true when the node performs externally-visible actions,
	// which is what the missing_dedupe_key warning keys off (SPEC: Idempotency).
	// It applies to action nodes.
	SideEffect bool
	// Delivery describes how a trigger starts a run (instant, polling,
	// scheduled, event, manual). It is informational, shown to agents, and
	// fixed per capability. Empty for node capabilities.
	Delivery string
	// MechanismGroup is the mechanism group this capability belongs to, or Core
	// for Servitor's own primitives.
	MechanismGroup string
	// Fields is the capability's config schema, keyed by field name.
	Fields map[string]*Field
	// Spawn builds the argv for a node of this type from its config, or nil for
	// a control node with no plain subprocess. A mechanism package sets it
	// alongside its metadata (ADR-0045); the runner dispatches through it rather
	// than a central switch.
	Spawn func(cfg map[string]any) ([]string, error)
	// RunKind tells the worker which execution harness runs a node of this type
	// (ADR-0045). It applies to node capabilities; empty means RunPlain.
	RunKind RunKind
}

// isTriggerRole reports whether the capability may be used as a trigger.
func (t *Capability) isTriggerRole() bool {
	return t.Role == RoleTrigger
}

// isNodeRole reports whether the capability may be used as a node (action or
// flow).
func (t *Capability) isNodeRole() bool {
	return t.Role == RoleAction || t.Role == RoleFlow || t.Role == ""
}

// types is the full set of registered capabilities. A single list, since every
// capability is one of trigger/action/flow; role separates trigger-usable from
// node-usable.
// (ADR-0028).
//
// The list is not written here. Each mechanism lives in its own package under
// the directory of its mechanism group and registers itself through Register
// (ADR-0045), so removing a mechanism's package removes it with no central
// reference left to edit.
var types = []*Capability{}

// Register adds a capability to the registry. It is idempotent by name: a
// mechanism that registers twice (for example a package imported through two
// paths) is added once. Mechanism packages call this from their init function
// (ADR-0045).
func Register(c *Capability) {
	if c == nil {
		return
	}
	if Lookup(c.Name) != nil {
		return
	}
	types = append(types, c)
}

// Types returns all registered capabilities, sorted by name.
func Types() []*Capability {
	return sortedTypes()
}

// Nodes returns the node-usable capabilities (role action or flow), sorted by
// name. This is the set valid under a Wafer's `nodes:` list.
func Nodes() []*Capability {
	var out []*Capability
	for _, t := range types {
		if t.isNodeRole() {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// TriggerTypes returns the trigger capabilities, sorted by name. This is the
// set valid under a Wafer's `on:` list.
func TriggerTypes() []*Capability {
	var out []*Capability
	for _, t := range types {
		if t.isTriggerRole() {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LookupNode returns the node-usable capability with the given name, or nil.
func LookupNode(name string) *Capability {
	for _, t := range types {
		if t.Name == name && t.isNodeRole() {
			return t
		}
	}
	return nil
}

// LookupTrigger returns the trigger capability with the given name, or nil.
func LookupTrigger(name string) *Capability {
	for _, t := range types {
		if t.Name == name && t.isTriggerRole() {
			return t
		}
	}
	return nil
}

// Lookup returns the capability with the given name regardless of role, or nil.
func Lookup(name string) *Capability {
	for _, t := range types {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// CommandFor builds the argv for a node of the given type from its config, by
// dispatching through the registered capability's Spawn (ADR-0045). It returns
// nil when the type is a control node with no plain subprocess, and an error
// when the type is unknown or its Spawn rejects the config. The runner calls
// this instead of naming node types in a switch.
func CommandFor(nodeType string, cfg map[string]any) ([]string, error) {
	c := Lookup(nodeType)
	if c == nil {
		return nil, fmt.Errorf("node type %q has no handler built yet", nodeType)
	}
	if c.Spawn == nil {
		return nil, nil
	}
	return c.Spawn(cfg)
}

// RunKindFor returns the execution harness a node of the given type runs with
// (ADR-0045). It returns RunPlain for an unknown or unset type, so the worker
// needs no switch over node type names; an unknown type is rejected earlier by
// validation and CommandFor.
func RunKindFor(nodeType string) RunKind {
	c := Lookup(nodeType)
	if c == nil {
		return RunPlain
	}
	if c.RunKind == "" {
		return RunPlain
	}
	return c.RunKind
}

func sortedTypes() []*Capability {
	out := make([]*Capability, len(types))
	copy(out, types)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

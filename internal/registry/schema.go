package registry

import "sort"

// jsonType maps a Field.Type to its JSON Schema "type" keyword.
func jsonType(t string) string {
	switch t {
	case "integer", "number":
		return t
	case "string", "boolean", "object", "array":
		return t
	default:
		return ""
	}
}

// JSONSchema returns the JSON Schema (draft 2020-12) for this capability's
// config. It is derived from the same field metadata validation uses, so the
// schema an agent reads cannot drift from what the validator enforces.
func (t *Capability) JSONSchema() map[string]any {
	return typeSchema(t.Name, t.Desc, t.Fields)
}

func typeSchema(name, desc string, fields map[string]*Field) map[string]any {
	properties := map[string]any{}
	required := []any{}
	for _, name := range sortedFieldNames(fields) {
		f := fields[name]
		p := map[string]any{}
		if t := jsonType(f.Type); t != "" {
			p["type"] = t
		}
		if f.Desc != "" {
			p["description"] = f.Desc
		}
		if len(f.Examples) > 0 {
			p["examples"] = f.Examples
		}
		properties[name] = p
		if f.Required {
			required = append(required, name)
		}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if name != "" {
		schema["title"] = name
	}
	if desc != "" {
		schema["description"] = desc
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func sortedFieldNames(fields map[string]*Field) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// WaferSchema returns the JSON Schema for the whole Wafer document, composed
// from the node and trigger capability schemas. Used by `servitor capabilities`
// to hand agents a schema they can validate Wafers against locally.
func WaferSchema() map[string]any {
	nodeNames := []any{}
	for _, st := range Nodes() {
		nodeNames = append(nodeNames, st.Name)
	}
	triggerNames := []any{}
	for _, tt := range TriggerTypes() {
		triggerNames = append(triggerNames, tt.Name)
	}

	node := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":       map[string]any{"type": "string", "enum": nodeNames, "description": "The node type."},
			"name":       map[string]any{"type": "string", "description": "A name for this node, for referencing from other nodes."},
			"dedupe_key": map[string]any{"type": "string", "description": "A key making the node run at most once per value (SPEC: Idempotency)."},
			"depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Node names this node depends on."},
			"secrets":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Secret names this node declares; only these are passed to its subprocess (SPEC: Varlock)."},
		},
		"required":             []any{"type"},
		"additionalProperties": true,
	}

	trigger := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type": map[string]any{"type": "string", "enum": triggerNames, "description": "The trigger type."},
		},
		"required":             []any{"type"},
		"additionalProperties": true,
	}

	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title":   "Wafer",
		"type":    "object",
		"properties": map[string]any{
			"name":     map[string]any{"type": "string", "description": "The workflow name."},
			"triggers": map[string]any{"type": "array", "items": trigger, "description": "Triggers that start the workflow."},
			"nodes":    map[string]any{"type": "array", "items": node, "minItems": 1, "description": "The nodes the workflow runs."},
		},
		"required":             []any{"name", "nodes"},
		"additionalProperties": false,
	}
}

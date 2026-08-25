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

// JSONSchema returns the JSON Schema (draft 2020-12) for this step type's
// config. It is derived from the same field metadata validation uses, so the
// schema an agent reads cannot drift from what the validator enforces.
func (t *StepType) JSONSchema() map[string]any {
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
// from the step and trigger type schemas. Used by `servitor capabilities` to
// hand agents a schema they can validate Wafers against locally.
func WaferSchema() map[string]any {
	stepNames := []any{}
	for _, st := range StepTypes() {
		stepNames = append(stepNames, st.Name)
	}
	triggerNames := []any{}
	for _, tt := range TriggerTypes() {
		triggerNames = append(triggerNames, tt.Name)
	}

	step := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":       map[string]any{"type": "string", "enum": stepNames, "description": "The step type."},
			"name":       map[string]any{"type": "string", "description": "A name for this step, for referencing from other steps."},
			"dedupe_key": map[string]any{"type": "string", "description": "A key making the step run at most once per value (SPEC: Idempotency)."},
			"depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Step names this step depends on."},
			"secrets":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Secret names this step declares; only these are passed to its subprocess (SPEC: Varlock)."},
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
			"name":  map[string]any{"type": "string", "description": "The workflow name."},
			"on":    map[string]any{"type": "array", "items": trigger, "description": "Triggers that start the workflow."},
			"steps": map[string]any{"type": "array", "items": step, "minItems": 1, "description": "The steps the workflow runs."},
		},
		"required":             []any{"name", "steps"},
		"additionalProperties": false,
	}
}

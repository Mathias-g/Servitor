package registry

// exampleForType builds a sample Wafer fragment for a step or trigger type:
// a struct skeleton taken from the schema (required fields first, nested
// objects and arrays from the type), with each property's value taken from its
// `examples` keyword when present. Because the example is derived from the same
// metadata as the schema, it cannot drift from it (SPEC: How an agent
// discovers integrations).
//
// For a step, the fragment is the step's `type` plus its config. For a trigger,
// the fragment is the trigger's `type` plus its config.
func exampleForType(name, kind string, fields map[string]*Field) map[string]any {
	out := map[string]any{"type": name}
	if kind == "step" {
		out["name"] = name + "-1"
	}
	for _, fname := range sortedFieldNames(fields) {
		f := fields[fname]
		out[fname] = sampleValue(f)
	}
	return out
}

// sampleValue returns a representative value for a field: the first example
// when present, otherwise a value matching the field's declared type.
func sampleValue(f *Field) any {
	if len(f.Examples) > 0 {
		return f.Examples[0]
	}
	switch f.Type {
	case "string":
		return ""
	case "integer":
		return 0
	case "number":
		return 0.0
	case "boolean":
		return false
	case "object":
		return map[string]any{}
	case "array":
		return []any{}
	}
	return nil
}

// StepExample returns an example Wafer fragment for the step type, keyed for
// use inside a `steps:` list.
func (st *StepType) StepExample() map[string]any {
	return exampleForType(st.Name, "step", st.Fields)
}

// TriggerExample returns an example Wafer fragment for the trigger type, keyed
// for use inside an `on:` list.
func (tt *TriggerType) TriggerExample() map[string]any {
	return exampleForType(tt.Name, "trigger", tt.Fields)
}

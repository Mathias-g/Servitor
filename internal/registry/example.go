package registry

// exampleForType builds a sample Wafer fragment for a capability: a struct
// skeleton taken from the schema (required fields first, nested objects and
// arrays from the type), with each property's value taken from its `examples`
// keyword when present. Because the example is derived from the same metadata
// as the schema, it cannot drift from it (SPEC: How an agent discovers
// capabilities and connectors).
//
// When used as a node, the fragment is the capability's `type` plus a `name`
// and its config, keyed for a `nodes:` list. When used as a trigger, the
// fragment is the capability's `type` plus its config, keyed for an `on:` list.
func exampleForType(name string, asNode bool, fields map[string]*Field) map[string]any {
	out := map[string]any{"type": name}
	if asNode {
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

// NodeExample returns an example Wafer fragment for the capability, keyed for
// use inside a `nodes:` list.
func (t *Capability) NodeExample() map[string]any {
	return exampleForType(t.Name, true, t.Fields)
}

// TriggerExample returns an example Wafer fragment for the capability, keyed for
// use inside an `on:` list.
func (t *Capability) TriggerExample() map[string]any {
	return exampleForType(t.Name, false, t.Fields)
}

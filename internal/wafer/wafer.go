// Package wafer is the Go representation of a Wafer, the workflow artifact
// (SPEC: The Wafer). A Wafer is a YAML file declaring triggers (`on:`) and
// steps (`steps:`). The YAML file is the artifact and the only place workflow
// state lives; this package parses it and validates it.
package wafer

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Wafer is a parsed workflow.
type Wafer struct {
	// Name is the workflow name (SPEC requires it).
	Name string
	// On are the triggers that start the workflow.
	On []Trigger
	// Steps are the steps the workflow runs, in dependency order.
	Steps []Step
}

// Trigger is one trigger config.
type Trigger struct {
	// Type is the trigger type (for example `cron` or `http_webhook`).
	Type string
	// Config holds the trigger's type-specific fields.
	Config map[string]any
}

// Step is one step config.
type Step struct {
	// Type is the step type (for example `http` or `transform`).
	Type string
	// Name is an optional name for referencing this step.
	Name string
	// DedupeKey is an expression making the step run at most once per value.
	DedupeKey string
	// DependsOn lists step names this step depends on.
	DependsOn []string
	// Config holds the step's type-specific fields.
	Config map[string]any
}

// Parse decodes a Wafer from YAML bytes into the typed model. It reports
// YAML/type errors, not semantic validation; use Validate for that.
func Parse(data []byte) (*Wafer, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse wafer: %w", err)
	}
	w, err := fromRaw(raw)
	if err != nil {
		return nil, err
	}
	return w, nil
}

func fromRaw(raw map[string]any) (*Wafer, error) {
	w := &Wafer{}
	if name, ok := raw["name"].(string); ok {
		w.Name = name
	}
	if on, ok := raw["on"].([]any); ok {
		for _, t := range on {
			m, ok := t.(map[string]any)
			if !ok {
				continue
			}
			tr := Trigger{}
			if typ, ok := m["type"].(string); ok {
				tr.Type = typ
			}
			tr.Config = copyMap(m, "type")
			w.On = append(w.On, tr)
		}
	}
	if steps, ok := raw["steps"].([]any); ok {
		for _, s := range steps {
			m, ok := s.(map[string]any)
			if !ok {
				continue
			}
			st := Step{}
			if typ, ok := m["type"].(string); ok {
				st.Type = typ
			}
			if name, ok := m["name"].(string); ok {
				st.Name = name
			}
			if dk, ok := m["dedupe_key"].(string); ok {
				st.DedupeKey = dk
			}
			if deps, ok := m["depends_on"].([]any); ok {
				for _, d := range deps {
					if s, ok := d.(string); ok {
						st.DependsOn = append(st.DependsOn, s)
					}
				}
			}
			st.Config = copyMap(m, "type", "name", "dedupe_key", "depends_on")
			w.Steps = append(w.Steps, st)
		}
	}
	return w, nil
}

func copyMap(m map[string]any, skip ...string) map[string]any {
	out := map[string]any{}
	skipSet := map[string]bool{}
	for _, s := range skip {
		skipSet[s] = true
	}
	for k, v := range m {
		if !skipSet[k] {
			out[k] = v
		}
	}
	return out
}

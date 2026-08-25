package wafer

import (
	"encoding/json"
	"testing"
)

func mustJSON(t *testing.T, res Result) string {
	t.Helper()
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return string(b)
}

func TestValidWafer(t *testing.T) {
	doc := []byte(`
name: my-workflow
on:
  - type: cron
    schedule: "0 * * * *"
steps:
  - type: http
    url: "https://api.example.com/things"
    method: GET
    dedupe_key: "input.event.id"
`)
	res := Validate(doc)
	if !res.Valid() {
		t.Fatalf("expected valid, got %s", mustJSON(t, res))
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %s", mustJSON(t, res))
	}
}

func TestMissingName(t *testing.T) {
	res := Validate([]byte("steps:\n  - type: http\n    url: x\n    method: GET\n"))
	if res.Valid() {
		t.Fatal("expected invalid")
	}
	found := false
	for _, e := range res.Errors {
		if e.Path == "/name" && e.Code == "missing_required_field" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing /name error, got %s", mustJSON(t, res))
	}
}

func TestUnknownStepTypeWithSuggestion(t *testing.T) {
	res := Validate([]byte(`
name: w
steps:
  - type: transformn
    expression: x
`))
	if res.Valid() {
		t.Fatal("expected invalid")
	}
	for _, e := range res.Errors {
		if e.Code == "unknown_step_type" {
			if e.Suggestion != "transform" {
				t.Fatalf("suggestion = %q, want transform", e.Suggestion)
			}
			if e.Path != "/steps/0/type" {
				t.Fatalf("path = %q, want /steps/0/type", e.Path)
			}
		}
	}
}

func TestMissingRequiredFieldInStep(t *testing.T) {
	// http requires url and method; omit method.
	res := Validate([]byte(`
name: w
steps:
  - type: http
    url: "https://example.com"
`))
	if res.Valid() {
		t.Fatal("expected invalid")
	}
	found := false
	for _, e := range res.Errors {
		if e.Path == "/steps/0/method" && e.Code == "missing_required_field" && e.Expected == "string" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing /steps/0/method error, got %s", mustJSON(t, res))
	}
}

func TestTypeMismatch(t *testing.T) {
	res := Validate([]byte(`
name: w
steps:
  - type: foreach
    over: [not, a, string]
    body: process_one
`))
	found := false
	for _, e := range res.Errors {
		if e.Code == "type_mismatch" && e.Path == "/steps/0/over" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected type_mismatch for /steps/0/over, got %s", mustJSON(t, res))
	}
}

func TestMissingDedupeWarning(t *testing.T) {
	res := Validate([]byte(`
name: w
steps:
  - type: shell
    command: "curl -X POST ..."
`))
	if len(res.Warnings) != 1 {
		t.Fatalf("expected 1 dedupe warning, got %s", mustJSON(t, res))
	}
	if res.Warnings[0].Code != "missing_dedupe_key" {
		t.Fatalf("warning code = %q, want missing_dedupe_key", res.Warnings[0].Code)
	}
}

func TestNoWarningWhenDedupePresent(t *testing.T) {
	res := Validate([]byte(`
name: w
steps:
  - type: shell
    command: "true"
    dedupe_key: "input.event.id"
`))
	if len(res.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %s", mustJSON(t, res))
	}
}

func TestMissingSteps(t *testing.T) {
	res := Validate([]byte("name: w\n"))
	found := false
	for _, e := range res.Errors {
		if e.Code == "missing_steps" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing_steps error, got %s", mustJSON(t, res))
	}
}

func TestMultipleErrorsAtOnce(t *testing.T) {
	// Three independent problems, all reported in one pass.
	res := Validate([]byte(`
name:
steps:
  - type: nosuch
  - type: http
`))
	if len(res.Errors) < 3 {
		t.Fatalf("expected multiple errors at once, got %s", mustJSON(t, res))
	}
}

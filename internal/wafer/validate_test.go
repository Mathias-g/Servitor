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
triggers:
  - type: cron
    schedule: "0 * * * *"
nodes:
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
	res := Validate([]byte("nodes:\n  - type: http\n    url: x\n    method: GET\n"))
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

func TestUnknownNodeTypeWithSuggestion(t *testing.T) {
	res := Validate([]byte(`
name: w
nodes:
  - type: transformn
    expression: x
`))
	if res.Valid() {
		t.Fatal("expected invalid")
	}
	for _, e := range res.Errors {
		if e.Code == "unknown_node_type" {
			if e.Suggestion != "transform" {
				t.Fatalf("suggestion = %q, want transform", e.Suggestion)
			}
			if e.Path != "/nodes/0/type" {
				t.Fatalf("path = %q, want /nodes/0/type", e.Path)
			}
		}
	}
}

func TestMissingRequiredFieldInNode(t *testing.T) {
	// http requires url and method; omit method.
	res := Validate([]byte(`
name: w
nodes:
  - type: http
    url: "https://example.com"
`))
	if res.Valid() {
		t.Fatal("expected invalid")
	}
	found := false
	for _, e := range res.Errors {
		if e.Path == "/nodes/0/method" && e.Code == "missing_required_field" && e.Expected == "string" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing /nodes/0/method error, got %s", mustJSON(t, res))
	}
}

func TestTypeMismatch(t *testing.T) {
	res := Validate([]byte(`
name: w
nodes:
  - type: foreach
    over: [not, a, string]
    body: process_one
`))
	found := false
	for _, e := range res.Errors {
		if e.Code == "type_mismatch" && e.Path == "/nodes/0/over" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected type_mismatch for /nodes/0/over, got %s", mustJSON(t, res))
	}
}

func TestMissingDedupeWarning(t *testing.T) {
	res := Validate([]byte(`
name: w
nodes:
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
nodes:
  - type: shell
    command: "true"
    dedupe_key: "input.event.id"
`))
	if len(res.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %s", mustJSON(t, res))
	}
}

func TestMissingNodes(t *testing.T) {
	res := Validate([]byte("name: w\n"))
	found := false
	for _, e := range res.Errors {
		if e.Code == "missing_nodes" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing_nodes error, got %s", mustJSON(t, res))
	}
}

func TestMultipleErrorsAtOnce(t *testing.T) {
	// Three independent problems, all reported in one pass.
	res := Validate([]byte(`
name:
nodes:
  - type: nosuch
  - type: http
`))
	if len(res.Errors) < 3 {
		t.Fatalf("expected multiple errors at once, got %s", mustJSON(t, res))
	}
}

func TestWaitNodeRequiresSignalOrTimer(t *testing.T) {
	// A wait with neither source must be rejected (ADR-0041).
	bad := `name: wf
nodes:
  - name: w
    type: wait
`
	res := Validate([]byte(bad))
	if res.Valid() {
		t.Fatalf("wait with neither source was accepted")
	}
	found := false
	for _, e := range res.Errors {
		if e.Code == "wait_requires_source" {
			found = true
		}
	}
	if !found {
		t.Fatalf("errors = %+v, want a wait_requires_source error", res.Errors)
	}

	// A wait with a timer is fine.
	good := `name: wf
nodes:
  - name: w
    type: wait
    timer:
      after: 48h
`
	if r := Validate([]byte(good)); !r.Valid() {
		t.Fatalf("wait with timer rejected: %+v", r.Errors)
	}
}

func TestOnFailureAndRerunModeValidation(t *testing.T) {
	// on_failure must be a valid rerun mode.
	bad := `name: wf
on_failure: bogus
nodes:
  - name: a
    type: shell
    command: "true"
`
	res := Validate([]byte(bad))
	if res.Valid() {
		t.Fatalf("invalid on_failure accepted")
	}

	// A rerun-failed node's mode must be valid.
	badNode := `name: wf
nodes:
  - name: r
    type: rerun-failed
    mode: nope
`
	if r := Validate([]byte(badNode)); r.Valid() {
		t.Fatalf("invalid rerun-failed mode accepted")
	}

	// Valid on_failure and rerun-failed are accepted.
	good := `name: wf
on_failure: restart
nodes:
  - name: r
    type: rerun-failed
    run_id: event.from_run
    mode: continue
`
	if r := Validate([]byte(good)); !r.Valid() {
		t.Fatalf("valid wafer rejected: %+v", r.Errors)
	}
}

package wafer

import (
	"strings"
	"testing"
)

func TestResolveDAGRunOrder(t *testing.T) {
	doc := []byte(`
name: w
steps:
  - type: transform
    name: b
    depends_on: [a]
    expression: x
  - type: transform
    name: a
    expression: x
  - type: transform
    name: c
    depends_on: [a, b]
    expression: x
`)
	w, err := Parse(doc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dag, issues := ResolveDAG(w)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	// a must come before b, and b before c.
	pos := map[string]int{}
	for i, s := range dag.Steps {
		pos[s.Name] = i
	}
	if pos["a"] > pos["b"] || pos["b"] > pos["c"] {
		t.Fatalf("run order violates dependencies: %+v", dag.Steps)
	}
	// c depends on a and b.
	for _, s := range dag.Steps {
		if s.Name == "c" {
			if len(s.DependsOn) != 2 {
				t.Fatalf("c DependsOn = %v, want [a b]", s.DependsOn)
			}
		}
	}
}

func TestResolveDAGUnknownReference(t *testing.T) {
	doc := []byte(`
name: w
steps:
  - type: transform
    name: a
    depends_on: [missing]
    expression: x
`)
	w, err := Parse(doc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, issues := ResolveDAG(w)
	if len(issues) != 1 || issues[0].Code != "unknown_step_reference" {
		t.Fatalf("expected unknown_step_reference, got %+v", issues)
	}
}

func TestResolveDAGCycle(t *testing.T) {
	doc := []byte(`
name: w
steps:
  - type: transform
    name: a
    depends_on: [b]
    expression: x
  - type: transform
    name: b
    depends_on: [a]
    expression: x
`)
	w, err := Parse(doc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, issues := ResolveDAG(w)
	if len(issues) != 1 || issues[0].Code != "circular_dependency" {
		t.Fatalf("expected circular_dependency, got %+v", issues)
	}
}

func TestDryRunProducesDAG(t *testing.T) {
	doc := []byte(`
name: w
steps:
  - type: http
    name: fetch
    url: x
    method: GET
    dedupe_key: k
  - type: transform
    name: t
    depends_on: [fetch]
    expression: x
`)
	out := DryRun(doc)
	if !out.Result.Valid() {
		t.Fatalf("expected valid, got %+v", out.Result.Errors)
	}
	if out.DAG == nil {
		t.Fatal("expected a resolved DAG")
	}
	if len(out.DAG.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(out.DAG.Steps))
	}
}

func TestDryRunNoDAGOnError(t *testing.T) {
	out := DryRun([]byte("name:\nsteps:\n  - type: nope\n"))
	if out.DAG != nil {
		t.Fatal("expected no DAG when validation fails")
	}
	if out.Result.Valid() {
		t.Fatal("expected invalid result")
	}
}

func TestDryRunReportsRedactedSecretsAndMissingWarnings(t *testing.T) {
	t.Setenv("PRESENT_SECRET", "value")
	t.Setenv("MISSING_SECRET", "")
	doc := []byte(`
name: w
steps:
  - type: shell
    name: a
    secrets: [PRESENT_SECRET, MISSING_SECRET]
    command: "echo hi"
`)
	out := DryRun(doc)
	if !out.Result.Valid() {
		t.Fatalf("expected valid, got %+v", out.Result.Errors)
	}
	if len(out.Secrets) != 2 || out.Secrets[0] != "PRESENT_SECRET" || out.Secrets[1] != "MISSING_SECRET" {
		t.Fatalf("secrets = %v, want [PRESENT_SECRET MISSING_SECRET]", out.Secrets)
	}
	// Only the missing one warns (there may also be a missing_dedupe_key
	// warning from the shell step).
	var missing []Issue
	for _, warn := range out.Result.Warnings {
		if warn.Code == "missing_secret" {
			missing = append(missing, warn)
		}
	}
	if len(missing) != 1 || !strings.Contains(missing[0].Message, "MISSING_SECRET") {
		t.Fatalf("missing_secret warnings = %+v, want one for MISSING_SECRET", missing)
	}
}

func TestDryRunNoSecretsNoWarnings(t *testing.T) {
	out := DryRun([]byte("name: w\nsteps:\n  - type: shell\n    name: a\n    command: echo hi\n"))
	if len(out.Secrets) != 0 {
		t.Fatalf("secrets = %v, want none", out.Secrets)
	}
	for _, warn := range out.Result.Warnings {
		if warn.Code == "missing_secret" {
			t.Fatalf("unexpected missing_secret warning: %+v", warn)
		}
	}
}

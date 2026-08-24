package wafer

import (
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

package expression

import (
	"testing"
)

func TestEvalSelection(t *testing.T) {
	out, err := Eval("Account.Order.Product.Price", map[string]any{
		"Account": map[string]any{
			"Order": []any{
				map[string]any{"Product": map[string]any{"Price": 34.45}},
				map[string]any{"Product": map[string]any{"Price": 21.67}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	want := []any{34.45, 21.67}
	got, ok := out.([]any)
	if !ok || len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", out, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", out, want)
		}
	}
}

func TestEvalAggregateFilter(t *testing.T) {
	out, err := Eval(`$sum(items[active=true].amount)`, map[string]any{
		"items": []any{
			map[string]any{"active": true, "amount": 10.0},
			map[string]any{"active": false, "amount": 100.0},
			map[string]any{"active": true, "amount": 5.0},
		},
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if out != 15.0 {
		t.Fatalf("got %#v, want 15", out)
	}
}

func TestEvalReshape(t *testing.T) {
	out, err := Eval(`{"name": person.name, "tags": person.tags}`, map[string]any{
		"person": map[string]any{"name": "ada", "tags": []any{"math", "code"}},
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	want := map[string]any{"name": "ada", "tags": []any{"math", "code"}}
	got, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %#v, want %#v", out, want)
	}
	if got["name"] != want["name"] {
		t.Fatalf("got %#v, want %#v", out, want)
	}
	tags, ok := got["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Fatalf("got %#v, want %#v", out, want)
	}
}

// TestEvalDedupeKey pins the subset transform uses for idempotency keys:
// extracting a scalar from the inputs and stringifying it.
func TestEvalDedupeKey(t *testing.T) {
	out, err := Eval("event.id", map[string]any{"event": map[string]any{"id": "row-42"}})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if out != "row-42" {
		t.Fatalf("got %#v, want row-42", out)
	}
}

func TestCompileError(t *testing.T) {
	if _, err := Compile(`["unterminated`); err == nil {
		t.Fatal("expected compile error for malformed expression")
	}
}

// TestNormalizeResult ensures a gnata result is a plain JSON round-trippable
// value, so swapping the backend never leaks a library-specific type to callers.
func TestNormalizeResult(t *testing.T) {
	got := map[string]any{"a": []any{1.0, 2.0}}
	raw, err := normalize(got)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if _, ok := raw.(map[string]any); !ok {
		t.Fatalf("normalize produced %T, want map[string]any", raw)
	}
}

// Package expression evaluates JSONata expressions over JSON data (ADR-0020).
// It is the single place Servitor talks to a JSONata implementation, so the
// backend can be swapped for any other JSONata-2.x-compatible library without
// touching callers or Wafers already written: a Wafer stores an expression as
// a string, never a library type, so the contract is the JSONata language, not
// the Go module.
//
// The backend is gnata (github.com/recolabs/gnata), a complete pure-Go JSONata
// 2.x implementation. Bounded evaluation is a first-class option on gnata
// (recursion depth, timeout, and a bound on sequence growth), which is what the
// SPEC requires for node expressions: a single node cannot loop or grow
// unboundedly. Evaluation is CPU-only and never touches the host, matching the
// subprocess-per-node isolation model (ADR-0008).
//
// Callers must not rely on gnata-specific behavior beyond the JSONata spec; the
// tests pin spec behavior so a backend swap that changes results is caught.
package expression

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/recolabs/gnata"
)

// Defaults for bounded evaluation, applied to every compiled expression.
const (
	// maxStack bounds expression recursion depth.
	maxStack = 64
	// maxSequence bounds how large any single sequence (range, $map, $filter,
	// wildcard, descendant) may grow during evaluation.
	maxSequence = 10000
	// evalTimeout bounds wall-clock evaluation time per node.
	evalTimeout = 2 * time.Second
)

// Compiler compiles an expression once and evaluates it many times. Callers
// that run the same expression repeatedly (for example a dedupe_key or a
// transform applied to many inputs) should compile once and reuse.
type Compiler struct {
	expr *gnata.Expression
}

// Compile parses a JSONata expression and applies Servitor's bounded-evaluation
// bounds. It returns an error if the expression does not parse.
func Compile(expr string) (*Compiler, error) {
	e, err := gnata.Compile(expr,
		gnata.WithStack(maxStack),
		gnata.WithSequence(maxSequence),
	)
	if err != nil {
		return nil, fmt.Errorf("expression: compile: %w", err)
	}
	return &Compiler{expr: e}, nil
}

// Eval evaluates the compiled expression against input and returns the JSON
// result. input is any Go value; the result is returned as a Go value that
// round-trips through JSON (map[string]any, []any, string, float64, bool, nil).
func (c *Compiler) Eval(input any) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), evalTimeout)
	defer cancel()
	out, err := c.evalBounded(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("expression: eval: %w", err)
	}
	return normalize(out)
}

// evalBounded runs the expression through the evaluation timeout. Timeout
// errors are surfaced so a pathological expression cannot hang a node.
func (c *Compiler) evalBounded(ctx context.Context, input any) (any, error) {
	return c.expr.Eval(ctx, input)
}

// Eval compiles an expression and evaluates it against input in one call. Use
// Compile when the same expression runs many times.
func Eval(expr string, input any) (any, error) {
	c, err := Compile(expr)
	if err != nil {
		return nil, err
	}
	return c.Eval(input)
}

// normalize coerces gnata's result into a JSON-clean Go value, so callers never
// see a gnata-specific type. gnata returns plain Go values for most inputs, but
// normalizing through JSON guarantees the shape regardless of backend.
func normalize(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("expression: normalize: %w", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("expression: normalize: %w", err)
	}
	return out, nil
}

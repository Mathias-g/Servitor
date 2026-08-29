package wafer

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Mathias-g/Servitor/internal/registry"
)

// Issue is one structured validation finding, shaped for agents (SPEC:
// Structured validation errors). Path is a JSON Pointer into the submitted
// YAML; Code is a stable identifier; Suggestion carries a best-guess fix when
// there is one.
type Issue struct {
	Path       string `json:"path"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Expected   string `json:"expected,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// Result is the full outcome of validating a Wafer: errors block it, warnings
// do not. Both are returned at once so an agent fixes in batches (SPEC:
// Structured validation errors).
type Result struct {
	Errors   []Issue `json:"errors"`
	Warnings []Issue `json:"warnings"`
}

// Valid reports whether validation found no blocking errors.
func (r Result) Valid() bool { return len(r.Errors) == 0 }

// stable codes (SPEC: Structured validation errors).
const (
	codeMissingRequired = "missing_required_field"
	codeTypeMismatch    = "type_mismatch"
	codeUnknownNode     = "unknown_node_type"
	codeUnknownTrigger  = "unknown_trigger_type"
	codeMissingName     = "missing_name"
	codeMissingNodes    = "missing_nodes"
	codeMissingDedupe   = "missing_dedupe_key"
	codeMissingSecret   = "missing_secret"
)

// Validate decodes and validates YAML bytes, returning the structured result.
func Validate(data []byte) Result {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Result{Errors: []Issue{{
			Path:    "",
			Code:    "invalid_yaml",
			Message: fmt.Sprintf("invalid YAML: %v", err),
		}}, Warnings: []Issue{}}
	}
	return validateValue(raw)
}

func validateValue(raw any) Result {
	res := Result{
		Errors:   []Issue{},
		Warnings: []Issue{},
	}

	root, ok := raw.(map[string]any)
	if !ok {
		res.Errors = append(res.Errors, Issue{
			Path:     "",
			Code:     codeTypeMismatch,
			Message:  "wafer must be a YAML object",
			Expected: "object",
		})
		return res
	}

	// name (required).
	switch v := root["name"].(type) {
	case nil:
		res.Errors = append(res.Errors, Issue{Path: "/name", Code: codeMissingRequired, Message: "field 'name' is required", Expected: "string"})
	case string:
		if strings.TrimSpace(v) == "" {
			res.Errors = append(res.Errors, Issue{Path: "/name", Code: codeMissingName, Message: "field 'name' must not be empty", Expected: "non-empty string"})
		}
	default:
		res.Errors = append(res.Errors, Issue{Path: "/name", Code: codeTypeMismatch, Message: "field 'name' must be a string", Expected: "string"})
	}

	// trigger (optional list of triggers).
	if trigs, present := root["triggers"]; present {
		res.validateTriggers(trigs)
	}

	// nodes (required, non-empty).
	nodes, ok := root["nodes"].([]any)
	if !ok {
		res.Errors = append(res.Errors, Issue{Path: "/nodes", Code: codeMissingNodes, Message: "field 'nodes' is required and must be a list", Expected: "array"})
		return res
	}
	if len(nodes) == 0 {
		res.Errors = append(res.Errors, Issue{Path: "/nodes", Code: codeMissingNodes, Message: "field 'nodes' must contain at least one node", Expected: "non-empty array"})
		return res
	}
	for i, s := range nodes {
		res.validateNode(ptr("/nodes", strconv.Itoa(i)), s)
	}

	return res
}

func (res *Result) validateTriggers(on any) {
	list, ok := on.([]any)
	if !ok {
		res.Errors = append(res.Errors, Issue{Path: "/on", Code: codeTypeMismatch, Message: "field 'on' must be a list of triggers", Expected: "array"})
		return
	}
	for i, t := range list {
		p := ptr("/on", strconv.Itoa(i))
		m, ok := t.(map[string]any)
		if !ok {
			res.Errors = append(res.Errors, Issue{Path: p, Code: codeTypeMismatch, Message: "trigger must be an object", Expected: "object"})
			continue
		}
		typ, ok := m["type"].(string)
		if !ok {
			res.Errors = append(res.Errors, Issue{Path: ptr(p, "type"), Code: codeMissingRequired, Message: "trigger must declare a 'type'", Expected: "string"})
			continue
		}
		tt := registry.LookupTrigger(typ)
		if tt == nil {
			res.Errors = append(res.Errors, Issue{Path: ptr(p, "type"), Code: codeUnknownTrigger, Message: fmt.Sprintf("unknown trigger type %q", typ), Suggestion: nearestTrigger(typ)})
			continue
		}
		validateConfig(res, tt.Name, tt.Fields, p, m, "trigger")
	}
}

func (res *Result) validateNode(p string, s any) {
	m, ok := s.(map[string]any)
	if !ok {
		res.Errors = append(res.Errors, Issue{Path: p, Code: codeTypeMismatch, Message: "node must be an object", Expected: "object"})
		return
	}
	typ, ok := m["type"].(string)
	if !ok {
		res.Errors = append(res.Errors, Issue{Path: ptr(p, "type"), Code: codeMissingRequired, Message: "node must declare a 'type'", Expected: "string"})
		return
	}
	st := registry.LookupNode(typ)
	if st == nil {
		res.Errors = append(res.Errors, Issue{Path: ptr(p, "type"), Code: codeUnknownNode, Message: fmt.Sprintf("unknown node type %q", typ), Suggestion: nearestNode(typ)})
		return
	}
	validateConfig(res, st.Name, st.Fields, p, m, "node")

	// A `wait` node with neither a signal nor a timer would park forever
	// (ADR-0041).
	if st.Name == "wait" {
		_, hasSignal := m["signal"]
		_, hasTimer := m["timer"]
		if !hasSignal && !hasTimer {
			res.Errors = append(res.Errors, Issue{
				Path:     p,
				Code:     "wait_requires_source",
				Message:  "a wait node needs at least one of `signal` or `timer`, else it would park forever",
				Expected: "signal or timer",
			})
		}
	}

	if st.SideEffect {
		_, hasKey := m["dedupe_key"]
		if !hasKey {
			res.Warnings = append(res.Warnings, Issue{
				Path:    p,
				Code:    codeMissingDedupe,
				Message: fmt.Sprintf("Node %q performs an external side effect and has no dedupe_key; this node may run more than once on retry", st.Name),
			})
		}
	}
}

// validateConfig checks a node/trigger's type-specific fields against its
// schema, reporting missing-required and type-mismatch issues. objPath is the
// JSON pointer to the config object itself.
func validateConfig(res *Result, typeName string, fields map[string]*registry.Field, objPath string, m map[string]any, kind string) {
	for name, f := range fields {
		v, present := m[name]
		if !present {
			if f.Required {
				res.Errors = append(res.Errors, Issue{
					Path:     ptr(objPath, name),
					Code:     codeMissingRequired,
					Message:  fmt.Sprintf("field %q is required for %s type %q", name, kind, typeName),
					Expected: f.Type,
				})
			}
			continue
		}
		if !typeMatches(f.Type, v) {
			res.Errors = append(res.Errors, Issue{
				Path:     ptr(objPath, name),
				Code:     codeTypeMismatch,
				Message:  fmt.Sprintf("field %q expects %s, got %s", name, f.Type, yamlKind(v)),
				Expected: f.Type,
			})
		}
	}
}

func typeMatches(t string, v any) bool {
	switch t {
	case "string":
		_, ok := v.(string)
		return ok
	case "integer":
		_, ok := v.(int)
		return ok
	case "number":
		switch v.(type) {
		case int, float64:
			return true
		}
		return false
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "any":
		return true
	}
	return true
}

func yamlKind(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case int:
		return "integer"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case nil:
		return "null"
	}
	return "unknown"
}

// nearestNode returns the closest registered node name to an unknown one,
// for a suggestion (for example "slak" -> "slack").
func nearestNode(unknown string) string {
	return nearest(unknown, func() []string {
		names := []string{}
		for _, st := range registry.Nodes() {
			names = append(names, st.Name)
		}
		return names
	}())
}

func nearestTrigger(unknown string) string {
	return nearest(unknown, func() []string {
		names := []string{}
		for _, tt := range registry.TriggerTypes() {
			names = append(names, tt.Name)
		}
		return names
	}())
}

func nearest(unknown string, candidates []string) string {
	best := ""
	bestDist := -1
	for _, c := range candidates {
		if d := editDistance(unknown, c); bestDist < 0 || d < bestDist {
			bestDist = d
			best = c
		}
	}
	if bestDist <= 2 {
		return best
	}
	return ""
}

func editDistance(a, b string) int {
	da := []rune(a)
	db := []rune(b)
	prev := make([]int, len(db)+1)
	cur := make([]int, len(db)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(da); i++ {
		cur[0] = i
		for j := 1; j <= len(db); j++ {
			cost := 1
			if da[i-1] == db[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(db)]
}

func min(vals ...int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// ptr appends a path segment to a JSON Pointer, escaping `~` and `/`.
func ptr(base, segment string) string {
	seg := strings.ReplaceAll(segment, "~", "~0")
	seg = strings.ReplaceAll(seg, "/", "~1")
	if base == "" {
		return "/" + seg
	}
	return base + "/" + seg
}

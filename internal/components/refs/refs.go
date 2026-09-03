// Package refs resolves `$NAME` secret references in template strings against
// a filtered environment (ADR-0033). A node's config fields (for example an
// `http` node's headers or an mcp-http server's headers) may carry a reference
// like "Bearer $SEARCH_TOKEN"; the value is resolved per use from the
// subprocess's filtered env, so a node sees exactly the secrets it declared and
// nothing else. The same substitution serves any config field that references a
// secret name by value, so it is a shared component rather than part of one
// mechanism.
package refs

import (
	"fmt"
	"strings"
)

// ResolveHeaders substitutes `$NAME` secret references in each header template
// with the value of NAME from env, the filtered secret env of the node's
// subprocess. A reference to a name not present in env is an error, so a header
// that names a secret the node did not declare fails fast (SPEC: Secret
// resolution, ADR-0033).
func ResolveHeaders(headers map[string]string, env []string) (map[string]string, error) {
	if len(headers) == 0 {
		return headers, nil
	}
	vals := map[string]string{}
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok {
			vals[name] = value
		}
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		resolved, err := Substitute(v, vals)
		if err != nil {
			return nil, fmt.Errorf("header %s: %w", k, err)
		}
		out[k] = resolved
	}
	return out, nil
}

// ReferencedSecrets returns the distinct `$NAME` secret references across the
// templates, in the order they first appear. Callers use it to know which
// secrets to resolve before evaluating a template.
func ReferencedSecrets(templates map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range templates {
		EachRef(v, func(name string) {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		})
	}
	return out
}

// EachRef walks template and calls fn with each `$NAME` reference found.
func EachRef(template string, fn func(name string)) {
	rest := template
	for {
		idx := strings.Index(rest, "$")
		if idx < 0 {
			return
		}
		after := rest[idx+1:]
		j := 0
		for j < len(after) {
			ch := after[j]
			if ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
				j++
			} else {
				break
			}
		}
		if j == 0 {
			rest = after
			continue
		}
		fn(after[:j])
		rest = after[j:]
	}
}

// Substitute replaces each `$NAME` token in template with the value of NAME in
// vals. A `$` not followed by an identifier is kept as a literal. A reference
// to a missing name is an error.
func Substitute(template string, vals map[string]string) (string, error) {
	var sb strings.Builder
	rest := template
	for {
		idx := strings.Index(rest, "$")
		if idx < 0 {
			sb.WriteString(rest)
			return sb.String(), nil
		}
		sb.WriteString(rest[:idx])
		after := rest[idx+1:]
		j := 0
		for j < len(after) {
			ch := after[j]
			if ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
				j++
			} else {
				break
			}
		}
		if j == 0 {
			sb.WriteString("$")
			rest = after
			continue
		}
		name := after[:j]
		val, ok := vals[name]
		if !ok {
			return "", fmt.Errorf("references undeclared secret %q", name)
		}
		sb.WriteString(val)
		rest = after[j:]
	}
}

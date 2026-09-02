// Package shell registers the `shell` action mechanism: execute a command on
// the runner host (ADR-0048). It is part of Servitor's spec, not a
// mechanism, so this package is never removed.
package shell

import (
	"fmt"

	"github.com/Mathias-g/Servitor/internal/registry"
)

func init() {
	registry.Register(capability)
}

var capability = &registry.Capability{
	Name:           "shell",
	Desc:           "Execute a command on the runner host.",
	Role:           registry.RoleAction,
	SideEffect:     true,
	MechanismGroup: registry.Core,
	Fields: map[string]*registry.Field{
		"command": {Type: "string", Required: true, Desc: "The command to run.", Examples: []any{"echo hello"}},
	},
	RunKind: registry.RunPlain,
	Spawn: func(cfg map[string]any) ([]string, error) {
		cmd, ok := cfg["command"].(string)
		if !ok || cmd == "" {
			return nil, fmt.Errorf("shell requires a string command")
		}
		return []string{"/bin/sh", "-c", cmd}, nil
	},
}

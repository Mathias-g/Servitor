// Package exec runs a node as a subprocess (ADR-0008). It is the isolation
// boundary: nothing runs inside the runner's process. A node receives its
// inputs on stdin as JSON, writes its result as structured JSON to stdout, and
// exits. The subprocess environment is deliberately filtered: it contains only
// the secrets the node declared, plus the PATH the command needs to run. This
// is why there is no "not a sandbox" surface (SPEC: Node execution).
package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Request is one subprocess node run.
type Request struct {
	// Command is the argv to run, for example ["/bin/sh", "-c", "..."]. It
	// must be non-empty.
	Command []string
	// Env is the final environment for the subprocess, as NAME=value pairs. The
	// caller builds it with FilteredEnv so it contains only what the node may
	// see.
	Env []string
	// Input is written to the subprocess's stdin, serialized as JSON.
	Input any
}

// Result is the outcome of a subprocess node run.
type Result struct {
	// Output is the node's structured JSON result, parsed from stdout.
	Output any
	// Stderr is the captured stderr, for diagnostics when the run fails.
	Stderr string
}

// Run executes the request and parses stdout as structured JSON. It returns an
// error if the command cannot start, exits non-zero, or writes output that is
// not valid JSON. stdout and stderr are captured in memory; a node's output is
// bounded by the contract (structured JSON, not a stream).
func Run(ctx context.Context, req Request) (Result, error) {
	if len(req.Command) == 0 {
		return Result{}, fmt.Errorf("exec: empty command")
	}
	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...)
	cmd.Env = req.Env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if req.Input != nil {
		in, err := json.Marshal(req.Input)
		if err != nil {
			return Result{}, fmt.Errorf("exec: marshal input: %w", err)
		}
		cmd.Stdin = bytes.NewReader(in)
	}

	if err := cmd.Run(); err != nil {
		return Result{Stderr: redactEnv(req.Env, stderr.String())}, fmt.Errorf("exec: node failed: %w: %s", err, redactEnv(req.Env, stderr.String()))
	}

	out := redactEnv(req.Env, string(bytes.TrimSpace(stdout.Bytes())))
	if len(out) == 0 {
		return Result{Stderr: redactEnv(req.Env, stderr.String())}, fmt.Errorf("exec: node produced no JSON output on stdout: %s", redactEnv(req.Env, stderr.String()))
	}
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return Result{Stderr: redactEnv(req.Env, stderr.String())}, fmt.Errorf("exec: node stdout is not valid JSON: %w: %s", err, redactEnv(req.Env, stderr.String()))
	}
	return Result{Output: v, Stderr: redactEnv(req.Env, stderr.String())}, nil
}

// redactEnv replaces any secret value present in env with a placeholder, so a
// node's captured output does not carry the secrets it was granted back into
// the runner's persisted state or logs (SPEC: Varlock). It uses only the
// values in env (which FilteredEnv limited to PATH plus the node's declared
// secrets), so it never touches anything the node could not already see. PATH
// itself is not a secret and is left alone.
func redactEnv(env []string, b string) string {
	out := b
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "PATH" || value == "" {
			continue
		}
		out = strings.ReplaceAll(out, value, "<redacted:"+name+">")
	}
	return out
}

// FilteredEnv builds the environment a node's subprocess may see: a minimal
// base set (PATH, so the command can find its executables) plus exactly the
// secrets the node declared. Any other value the runner holds is kept out, so
// a node sees nothing it did not declare (SPEC: Node execution, Varlock).
//
// It returns the env as NAME=value pairs, and reports whether every declared
// secret name was resolvable. Declared secrets the runner does not have are
// skipped from the env; the caller decides whether a missing secret is fatal.
func FilteredEnv(secrets map[string]string, declared []string) (env []string, missing []string) {
	for k, v := range baseEnv() {
		env = append(env, k+"="+v)
	}
	for _, name := range declared {
		val, ok := secrets[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		env = append(env, name+"="+val)
	}
	return env, missing
}

// baseEnv is the smallest set of standard variables a subprocess needs to run
// a command: just PATH, inherited from the runner so executables in custom
// locations still resolve. It is deliberately not the parent's full
// environment; a node sees nothing beyond PATH and the secrets it declared.
func baseEnv() map[string]string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	return map[string]string{"PATH": path}
}

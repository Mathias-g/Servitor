// Package exec runs a step as a subprocess (ADR-0008). It is the isolation
// boundary: nothing runs inside the runner's process. A step receives its
// inputs on stdin as JSON, writes its result as structured JSON to stdout, and
// exits. The subprocess environment is deliberately filtered: it contains only
// the secrets the step declared, plus the PATH the command needs to run. This
// is why there is no "not a sandbox" surface (SPEC: Step execution).
package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// Request is one subprocess step run.
type Request struct {
	// Command is the argv to run, for example ["/bin/sh", "-c", "..."]. It
	// must be non-empty.
	Command []string
	// Env is the final environment for the subprocess, as NAME=value pairs. The
	// caller builds it with FilteredEnv so it contains only what the step may
	// see.
	Env []string
	// Input is written to the subprocess's stdin, serialized as JSON.
	Input any
}

// Result is the outcome of a subprocess step run.
type Result struct {
	// Output is the step's structured JSON result, parsed from stdout.
	Output any
	// Stderr is the captured stderr, for diagnostics when the run fails.
	Stderr string
}

// Run executes the request and parses stdout as structured JSON. It returns an
// error if the command cannot start, exits non-zero, or writes output that is
// not valid JSON. stdout and stderr are captured in memory; a step's output is
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
		return Result{Stderr: stderr.String()}, fmt.Errorf("exec: step failed: %w: %s", err, stderr.String())
	}

	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		return Result{Stderr: stderr.String()}, fmt.Errorf("exec: step produced no JSON output on stdout: %s", stderr.String())
	}
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		return Result{Stderr: stderr.String()}, fmt.Errorf("exec: step stdout is not valid JSON: %w: %s", err, stderr.String())
	}
	return Result{Output: v, Stderr: stderr.String()}, nil
}

// FilteredEnv builds the environment a step's subprocess may see: a minimal
// base set (PATH, so the command can find its executables) plus exactly the
// secrets the step declared. Any other value the runner holds is kept out, so
// a step sees nothing it did not declare (SPEC: Step execution, Varlock).
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
// environment; a step sees nothing beyond PATH and the secrets it declared.
func baseEnv() map[string]string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	return map[string]string{"PATH": path}
}

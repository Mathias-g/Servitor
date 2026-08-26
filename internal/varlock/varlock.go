// Package varlock integrates Servitor's secrets with the varlock CLI
// (SPEC: Varlock). The runner is meant to boot under `varlock run`, which
// resolves and injects the configured secrets into the process environment.
//
// The self-healing launch makes that the default: when the daemon is started
// without the varlock sentinel set, it re-executes itself under `varlock run`
// so it always boots with secrets resolved. Resolved secrets are then exposed
// to the daemon as the process environment, and per-step filtering (exec
// package) decides which of them a subprocess may actually see.
package varlock

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// sentinelEnv is set by `varlock run` in the child process. Its presence marks
// a process already under varlock, so the self-heal does not loop.
const sentinelEnv = "__VARLOCK_RUN"

// Under reports whether the current process was launched under `varlock run`
// (the sentinel env var is present).
func Under() bool {
	return os.Getenv(sentinelEnv) != ""
}

// Available reports whether the varlock binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("varlock")
	return err == nil
}

// SelfHeal re-executes the current program under `varlock run` when it was not
// already launched under varlock, so the runner always boots with secrets
// resolved. It reports false when the process is already under varlock or the
// varlock binary is not on PATH, in which case the caller runs normally
// without secret resolution, and true when it has handed off to varlock (the
// caller should stop).
//
// The handoff is a true exec (syscall.Exec): this process's image is replaced
// by varlock, which then spawns the runner as its child. There is no lingering
// wrapper process, so the operator sees a clean `manager -> varlock -> runner`
// tree and signals sent to the process they launched reach varlock, which
// forwards them to the runner. varlock is invoked with `--inject vars` so the
// full `__VARLOCK_ENV` secret graph is not carried in the daemon's environment.
func SelfHeal() (bool, error) {
	if Under() {
		return false, nil
	}
	varlockBin, err := exec.LookPath("varlock")
	if err != nil {
		return false, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("varlock: locate self: %w", err)
	}
	argv := append([]string{"varlock", "run", "--inject", "vars", "--", exe}, os.Args[1:]...)
	// syscall.Exec does not return on success: the current process image is
	// replaced by varlock. If it returns, the exec failed and we report it.
	if err := syscall.Exec(varlockBin, argv, os.Environ()); err != nil {
		return true, fmt.Errorf("varlock: re-exec: %w", err)
	}
	return true, nil
}

// ResolvedSecrets returns the current process environment as a name-to-value
// map. Under varlock this is the resolved secret set (plus the runner's own
// environment); the per-step filtering in the exec package exposes only the
// names a step declares, so exposing the whole map is safe.
func ResolvedSecrets() map[string]string {
	return environMap()
}

// Package selfexe returns the path to the running servitor binary, used by the
// pure-computation mechanism packages (transform, switch, foreach) and the
// flow-node expression evaluator (wait, send-signal, rerun-failed) to re-invoke
// it for their hidden subprocess commands (ADR-0008, ADR-0046, ADR-0048). It is
// a shared component: reusable, mechanism-agnostic, and imported by more than
// one consumer, so it lives apart from the mechanisms that use it.
package selfexe

import "os"

// override, when non-empty, is the binary path Path returns instead of the
// running executable. Tests that boot a real daemon in-process set it to a
// built servitor binary, because the running executable is the test binary,
// which does not serve the hidden subprocess commands.
var override string

// SetPath sets the binary path Path returns. It is a test seam: it lets an
// integration test that runs a daemon in its own process point the hidden
// subprocess commands at a real built servitor binary.
func SetPath(p string) { override = p }

// Path returns the path to the running servitor binary. The subprocess commands
// (`__transform`, `__switch`, `__foreach`, `__eval`) are served by this binary,
// so a mechanism re-invokes it to run even pure computation out of the runner's
// process (ADR-0008). It falls back to "servitor" on PATH when the executable
// path cannot be determined.
func Path() string {
	if override != "" {
		return override
	}
	exe, err := os.Executable()
	if err != nil {
		return "servitor"
	}
	return exe
}

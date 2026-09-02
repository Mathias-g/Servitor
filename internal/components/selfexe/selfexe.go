// Package selfexe returns the path to the running servitor binary, used by the
// pure-computation mechanism packages (transform, switch, foreach) to re-invoke
// it for their hidden subprocess commands (ADR-0008, ADR-0046, ADR-0048). It is
// a shared component: reusable, mechanism-agnostic, and imported by more than
// one consumer, so it lives apart from the mechanisms that use it.
package selfexe

import "os"

// Path returns the path to the running servitor binary. The subprocess commands
// (`__transform`, `__switch`, `__foreach`) are served by this binary, so a
// mechanism re-invokes it to run even pure computation out of the runner's
// process (ADR-0008). It falls back to "servitor" on PATH when the executable
// path cannot be determined.
func Path() string {
	exe, err := os.Executable()
	if err != nil {
		return "servitor"
	}
	return exe
}

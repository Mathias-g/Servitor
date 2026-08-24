package cli

import (
	"fmt"
	"io"
)

// Run dispatches the servitor CLI. args excludes the program name.
// It returns an error that main prints and exits non-zero on.
func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	switch args[0] {
	case "version", "--version", "-v":
		_, _ = fmt.Fprintln(stdout, appVersion())
		return nil
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	}

	// All other commands are operations on the runner daemon. The daemon and
	// its control protocol are not implemented yet; until then, reject clearly.
	return fmt.Errorf("command %q is not implemented yet (the runner daemon and control plane are not built)", args[0])
}

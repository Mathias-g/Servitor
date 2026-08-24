package cli

import (
	"fmt"
	"io"

	"github.com/Mathias-g/Servitor/internal/app"
)

func appVersion() string {
	return fmt.Sprintf("servitor %s", app.Version)
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `servitor - workflow automation for the agentic stack

Usage:
  servitor <command> [arguments]

Commands:
  version          print the version
  help             show this help
  run              boot the runner daemon (under varlock)
  capabilities     write step/trigger/secret/tap schemas to files
  dry-run <wafer>  validate and resolve without executing
  submit <wafer>   validate and register a workflow
  update <wafer>   replace a workflow's definition
  enable <name>    register a workflow's triggers
  disable <name>   unregister without deleting
  trigger <name>   manual run with optional inputs
  runs             list run history
  run <id>         inspect one run
  cancel <id>      stop an in-flight run
  stop             drain and shut the daemon down

Flags:
  --addr ADDR  loopback address of the daemon (default 127.0.0.1:7365)

The daemon binds 127.0.0.1 only and the control plane is loopback-gated
(ADR-0009). 'run', 'stop', and 'dry-run' are implemented in this phase; the
rest are not built yet.
`)
}

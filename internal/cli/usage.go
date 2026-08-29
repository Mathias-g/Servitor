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
  run              boot the runner daemon
  capabilities     write node/trigger/secret/tap schemas to files
  dry-run <wafer>  validate and resolve without executing (--json for structured)
  submit <wafer>   validate and register a workflow
  update <wafer>   replace a workflow's definition
  enable <name>    register a workflow's triggers
  disable <name>   unregister without deleting
  trigger <name>   manual run with optional inputs
  runs             list run history
  run <id>         inspect one run
  cancel <id>      stop an in-flight run
  resume <name>    resume a parked (waiting) run by named signal (ADR-0042)
  stop             drain and shut the daemon down
  mcp add/list/remove  manage declared MCP servers (ADR-0018)
  tap add/list/remove  manage declared Singer taps (ADR-0018)
  target add/list/remove  manage declared Singer targets (ADR-0018)
  secret add/list/remove  manage declared secrets (ADR-0035)
  secret seal <name>      seal a value (stdin) into the on-box store (onbox source)

Flags:
  --addr ADDR  loopback address of the daemon (default 127.0.0.1:7365)
  --db PATH    SQLite file the daemon owns (via Honker)
  HONKER_EXTENSION_PATH  path to the Honker extension .so (ADR-0011)

The declared integration commands (mcp/tap/target/secret) edit a local
servitor.integrations.yaml and need no daemon; the actual software install is
delegated to the ecosystem's package managers (npx, pipx, uv, Meltano), and
secret values are delivered to the declared source, never stored here.

The daemon binds 127.0.0.1 only and the control plane is loopback-gated
(ADR-0009). All commands in the set above are implemented.
`)
}

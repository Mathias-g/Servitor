package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Mathias-g/Servitor/internal/capabilities"
	"github.com/Mathias-g/Servitor/internal/daemon"
	"github.com/Mathias-g/Servitor/internal/protocol"
	"github.com/Mathias-g/Servitor/internal/wafer"
)

// Exit codes are the CLI's signal to scripts and to the pipeline (SPEC:
// Control plane). 0 ok, 1 operation failed, 2 usage error, 3 daemon not running.
const (
	exitOK       = 0
	exitFailure  = 1
	exitUsage    = 2
	exitNoDaemon = 3
)

// Run dispatches the servitor CLI. args excludes the program name. It returns
// the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return exitOK
	}

	switch args[0] {
	case "version", "--version", "-v":
		_, _ = fmt.Fprintln(stdout, appVersion())
		return exitOK
	case "help", "--help", "-h":
		printUsage(stdout)
		return exitOK
	case "run":
		return cmdRun(args[1:], stdout, stderr)
	case "stop":
		return cmdStop(args[1:], stdout, stderr)
	case "dry-run":
		return cmdDryRun(args[1:], stdout, stderr)
	case "capabilities":
		return cmdCapabilities(args[1:], stdout, stderr)
	}

	// The remaining commands are daemon operations scheduled for later phases.
	_, _ = fmt.Fprintf(stderr, "servitor: command %q is not implemented yet (this phase builds run, stop, dry-run, and capabilities)\n", args[0])
	return exitFailure
}

// parseAddr parses the shared --addr flag shared by run and stop.
func parseAddr(args []string, name string, stderr io.Writer) (string, int) {
	addr := protocol.DefaultAddr
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&addr, "addr", protocol.DefaultAddr, "loopback address of the daemon")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return "", exitOK
		}
		return "", exitUsage
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: %s: unexpected argument %q\n", name, fs.Arg(0))
		return "", exitUsage
	}
	return addr, exitOK
}

func cmdRun(args []string, stdout, stderr io.Writer) int {
	addr := protocol.DefaultAddr
	dbPath := ""
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&addr, "addr", protocol.DefaultAddr, "loopback address of the daemon")
	fs.StringVar(&dbPath, "db", "", "SQLite file the daemon owns (via Honker)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: run: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	cfg := daemon.Config{
		Addr:    addr,
		DBPath:  dbPath,
		ExtPath: os.Getenv("HONKER_EXT_PATH"),
		Started: func(a string) {
			_, _ = fmt.Fprintf(stdout, "servitor: daemon listening on %s (loopback only, ADR-0009)\n", a)
		},
	}
	if err := daemon.Run(context.Background(), cfg); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintln(stdout, "servitor: daemon stopped")
	return exitOK
}

func cmdStop(args []string, stdout, stderr io.Writer) int {
	addr, code := parseAddr(args, "stop", stderr)
	if code != exitOK {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c := protocol.NewClient(addr)

	// Confirm a daemon is up first, so "daemon not running" is its own exit code.
	if err := c.Health(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: daemon not running at %s\n", addr)
		return exitNoDaemon
	}

	if err := c.Stop(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: stop requested; daemon draining and shutting down\n")
	return exitOK
}

// cmdDryRun validates a Wafer and resolves its dependency DAG without
// executing, contacting, or persisting anything (SPEC: dry-run). By default it
// prints a readable plan (steps in run order with their dependencies); --json
// prints the structured result instead. It exits non-zero if there are blocking
// errors.
func cmdDryRun(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	fs := flag.NewFlagSet("dry-run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&jsonOut, "json", false, "print the structured result as JSON instead of a readable plan")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor dry-run [--json] <wafer>\n")
		return exitUsage
	}
	path := fs.Arg(0)

	data, err := os.ReadFile(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: dry-run: %v\n", err)
		return exitFailure
	}

	res := wafer.DryRun(data)

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			_, _ = fmt.Fprintf(stderr, "servitor: dry-run: %v\n", err)
			return exitFailure
		}
	} else {
		renderDryRunPlan(stdout, res)
	}

	if !res.Result.Valid() {
		_, _ = fmt.Fprintf(stderr, "servitor: dry-run: %d error(s)\n", len(res.Result.Errors))
		return exitFailure
	}
	_, _ = fmt.Fprintf(stderr, "servitor: dry-run: valid\n")
	return exitOK
}

// renderDryRunPlan prints a readable plan: the workflow name and triggers, then
// the steps in run order with their dependencies.
func renderDryRunPlan(w io.Writer, res wafer.DryRunResult) {
	if !res.Result.Valid() {
		_, _ = fmt.Fprintf(w, "workflow: (invalid)\n")
		return
	}
	if res.Name != "" {
		_, _ = fmt.Fprintf(w, "workflow: %s\n", res.Name)
	}
	if len(res.Triggers) > 0 {
		_, _ = fmt.Fprintf(w, "triggers:\n")
		for _, t := range res.Triggers {
			_, _ = fmt.Fprintf(w, "  - %s\n", t.Type)
		}
	}
	if res.DAG == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "\nplan (%d step(s), in run order):\n", len(res.DAG.Steps))
	for i, s := range res.DAG.Steps {
		deps := "start"
		if len(s.DependsOn) > 0 {
			deps = "after " + strings.Join(s.DependsOn, ", ")
		}
		_, _ = fmt.Fprintf(w, "  %d. %s\t%s\t[%s]\n", i+1, s.Name, s.Type, deps)
	}
}

// cmdCapabilities writes the per-server capability set to a directory the agent
// reads on demand (SPEC: How an agent discovers integrations). A pipeline can
// commit the directory so remote agents read it from the repo (ADR-0009).
func cmdCapabilities(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor capabilities [dir]\n")
		return exitUsage
	}
	dir := ""
	if len(args) == 1 {
		dir = args[0]
	}

	if err := capabilities.Write(dir); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: %v\n", err)
		return exitFailure
	}
	if dir == "" {
		dir = capabilities.DefaultDir
	}
	_, _ = fmt.Fprintf(stdout, "servitor: wrote capabilities to %s\n", dir)
	return exitOK
}
